package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"late/internal/client"
	"late/internal/common"
)

// Per-hook execution limits.
const (
	hookTimeout      = 15 * time.Second
	maxStderrBytes   = 4096
	hookCommandMax   = 256 // max bytes for stdin payload before we error out
)

// ToolCallHookPayload is written to the script's stdin when an OnToolCall
// hook fires. Plugins can inspect tool name + raw arguments JSON.
type ToolCallHookPayload struct {
	Tool      string          `json:"tool"`
	Arguments json.RawMessage `json:"arguments"`
	Timestamp string          `json:"timestamp"`
}

// resolveHookPath resolves a hook script's relative path inside the plugin's
// directory and rejects any path that escapes it. Returns the cleaned
// absolute path or an error.
func resolveHookPath(pluginDir, relPath string) (string, error) {
	if relPath == "" {
		return "", fmt.Errorf("empty hook path")
	}
	abs := filepath.Clean(filepath.Join(pluginDir, relPath))
	rel, err := filepath.Rel(pluginDir, abs)
	if err != nil || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return "", fmt.Errorf("hook path %q escapes plugin directory", relPath)
	}
	return abs, nil
}

// runHook executes a single hook script with the given stdin payload. It is
// a no-op for empty script paths. Errors are returned but never panic.
func runHook(ctx context.Context, pluginDir string, scriptPath string, stdin []byte) (string, error) {
	resolved, err := resolveHookPath(pluginDir, scriptPath)
	if err != nil {
		return "", err
	}

	execCtx, cancel := context.WithTimeout(ctx, hookTimeout)
	defer cancel()

	cmd := exec.CommandContext(execCtx, resolved[0:len(resolved):len(resolved)], resolved) //nolint:gosimple // see below
	// Above: exec.CommandContext wants its first arg as the lookup name. We pass
	// resolved twice so we get a stable argv[0] and matched binary.
	cmd.Dir = pluginDir

	if len(stdin) > 0 {
		if len(stdin) > hookCommandMax {
			return "", fmt.Errorf("hook stdin payload too large (%d > %d)", len(stdin), hookCommandMax)
		}
		cmd.Stdin = bytes.NewReader(stdin)
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err = cmd.Run()

	// Capture and forward stderr (truncated)
	stderrBytes := stderr.Bytes()
	if len(stderrBytes) > maxStderrBytes {
		stderrBytes = stderrBytes[:maxStderrBytes]
	}
	stderrStr := strings.TrimRight(string(stderrBytes), "\n")
	if stderrStr != "" {
		fmt.Fprintf(os.Stderr, "[hook %s:%s] %s\n", filepath.Base(pluginDir), filepath.Base(resolved), stderrStr)
	}

	if err != nil {
		if execCtx.Err() == context.DeadlineExceeded {
			return strings.TrimSpace(stdout.String()), fmt.Errorf("hook timed out after %v", hookTimeout)
		}
		return strings.TrimSpace(stdout.String()), fmt.Errorf("hook failed: %w", err)
	}

	return strings.TrimSpace(stdout.String()), nil
}

// hookData copies the plugin entry to avoid retaining the manager's mutex
// across goroutine boundaries.
type hookData struct {
	pluginDir  string
	pluginName string
	scripts    []string
}

func (pm *PluginManager) snapshotHooks(t string) []hookData {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	var out []hookData
	for _, p := range pm.plugins {
		if !p.Enabled || p.Late == nil || p.Late.Hooks == nil {
			continue
		}
		var scripts []string
		switch t {
		case "tool-call":
			scripts = p.Late.Hooks.OnToolCall
		case "session-start":
			scripts = p.Late.Hooks.OnSessionStart
		case "message-send":
			scripts = p.Late.Hooks.OnMessageSend
		default:
			return nil
		}
		if len(scripts) == 0 {
			continue
		}
		out = append(out, hookData{
			pluginDir:  p.Path,
			pluginName: p.Name,
			scripts:    append([]string(nil), scripts...),
		})
	}
	return out
}

// fanout fires all hooks across all plugins for the given event type in
// parallel. Each hook's stdout is logged; errors and stderr are forwarded
// but never abort the chain.
func (pm *PluginManager) fanout(ctx context.Context, eventType string, stdinFor func(pluginDir, script string, pluginName string) []byte) {
	hooks := pm.snapshotHooks(eventType)
	if len(hooks) == 0 {
		return
	}
	var wg sync.WaitGroup
	for _, h := range hooks {
		for _, script := range h.scripts {
			wg.Add(1)
			go func(h hookData, script string) {
				defer wg.Done()
				payload := []byte(nil)
				if stdinFor != nil {
					payload = stdinFor(h.pluginDir, script, h.pluginName)
				}
				out, err := runHook(ctx, h.pluginDir, script, payload)
				if err != nil {
					fmt.Fprintf(os.Stderr, "[%s/%s/%s] %v\n", h.pluginName, eventType, script, err)
				}
				if out != "" {
					fmt.Fprintf(os.Stderr, "[%s/%s/%s] %s\n", h.pluginName, eventType, script, out)
				}
			}(h, script)
		}
	}
	wg.Wait()
}

// BuildHookMiddlewares returns one common.ToolMiddleware per enabled plugin
// that declares OnToolCall hooks. Each middleware fans out to its plugin's
// scripts concurrently, then unconditionally calls next() so the rest of
// the chain runs normally. Hooks never block tool execution today; they
// only emit observations.
func (pm *PluginManager) BuildHookMiddlewares() []common.ToolMiddleware {
	hooks := pm.snapshotHooks("tool-call")
	if len(hooks) == 0 {
		return nil
	}

	mws := make([]common.ToolMiddleware, 0, len(hooks))
	for _, h := range hooks {
		h := h // capture
		mw := func(next common.ToolRunner) common.ToolRunner {
			return func(ctx context.Context, call client.ToolCall) (string, error) {
				// Build the stdin payload once per call.
				payload, _ := json.Marshal(ToolCallHookPayload{
					Tool:      call.Function.Name,
					Arguments: json.RawMessage(call.Function.Arguments),
					Timestamp: time.Now().UTC().Format(time.RFC3339),
				})
				var wg sync.WaitGroup
				for _, script := range h.scripts {
					wg.Add(1)
					go func(script string) {
						defer wg.Done()
						out, err := runHook(ctx, h.pluginDir, script, payload)
						if err != nil {
							fmt.Fprintf(os.Stderr, "[%s/onToolCall/%s] %v\n", h.pluginName, script, err)
						}
						if out != "" {
							fmt.Fprintf(os.Stderr, "[%s/onToolCall/%s] %s\n", h.pluginName, script, out)
						}
					}(script)
				}
				wg.Wait()
				return next(ctx, call)
			}
		}
		mws = append(mws, mw)
	}
	return mws
}

// CallOnSessionStartHooks fires OnSessionStart hooks for all enabled plugins
// in parallel. Errors are logged; this never returns a fatal error.
func (pm *PluginManager) CallOnSessionStartHooks() {
	pm.fanout(context.Background(), "session-start", nil)
}

// HookedMessage applies OnMessageSend hooks sequentially (after sort by
// plugin name) and returns the transformed message. By default each hook
// sees the output of the previous hook. If no hooks are registered, the
// input is returned unchanged.
func (pm *PluginManager) HookedMessage(text string) string {
	hooks := pm.snapshotHooks("message-send")
	if len(hooks) == 0 || text == "" {
		return text
	}
	current := text
	for _, h := range hooks {
		for _, script := range h.scripts {
			out, err := runHook(context.Background(), h.pluginDir, script, []byte(current))
			if err != nil {
				fmt.Fprintf(os.Stderr, "[%s/onMessageSend/%s] %v\n", h.pluginName, script, err)
				continue
			}
			if out != "" {
				fmt.Fprintf(os.Stderr, "[%s/onMessageSend/%s] transformed message\n", h.pluginName, script)
				current = out
			}
		}
	}
	return current
}
