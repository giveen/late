package plugin

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// LateManifest represents the "late" field inside a plugin's package.json.
type LateManifest struct {
	Skills   []string            `json:"skills,omitempty"`   // relative paths to skill directories
	MCP      *LateMCPManifest    `json:"mcp,omitempty"`      // MCP server definitions
	Commands LateCommands        `json:"commands,omitempty"` // slash command names (see LateCommands for back-compat)
	Themes   []string            `json:"themes,omitempty"`   // relative paths to theme JSON files
	Hooks    *LateHooksManifest  `json:"hooks,omitempty"`    // hook script definitions
	Tools    []LateToolManifest  `json:"tools,omitempty"`    // inline agent-callable tools (no MCP needed)
}

// LateCommands is a backward-compatible adapter for the "commands" field.
// Plugins written before command handlers existed declare commands as a
// flat array of strings; plugins written after can declare objects with
// a per-command "handler" script path. This type accepts both shapes:
//
//	"commands": ["/weather", "/git"]
//
//	"commands": [{"name": "/weather", "handler": "scripts/weather.sh"}]
type LateCommands []LateCommandManifest

// UnmarshalJSON accepts either an array of strings or an array of objects.
// On parse failure for both shapes the error is returned verbatim.
func (lc *LateCommands) UnmarshalJSON(data []byte) error {
	var stringForms []string
	if err := json.Unmarshal(data, &stringForms); err == nil {
		out := make(LateCommands, 0, len(stringForms))
		for _, s := range stringForms {
			out = append(out, LateCommandManifest{Name: s})
		}
		*lc = out
		return nil
	}
	var objForms []LateCommandManifest
	if err := json.Unmarshal(data, &objForms); err != nil {
		return err
	}
	*lc = objForms
	return nil
}

// MarshalJSON encodes the late commands back to a string array when no
// command has a handler, so round-tripping through DefaultManifest stays
// readable. Otherwise emits the object form so handlers survive.
func (lc LateCommands) MarshalJSON() ([]byte, error) {
	hasHandler := false
	for _, c := range lc {
		if c.Handler != "" {
			hasHandler = true
			break
		}
	}
	if !hasHandler {
		names := make([]string, len(lc))
		for i, c := range lc {
			names[i] = c.Name
		}
		return json.Marshal(names)
	}
	return json.Marshal([]LateCommandManifest(lc))
}

// LateCommandManifest describes a single plugin slash command. The Name
// is required; Handler is optional. When Handler is set, the TUI runs
// the script with the trailing args (JSON-encoded) on stdin and shows
// the stdout as the chat response. When Handler is empty, the command
// falls back to the legacy "dispatch as a plain prompt" behavior.
type LateCommandManifest struct {
	Name    string `json:"name"`              // slash command name, with or without leading "/"
	Handler string `json:"handler,omitempty"` // optional relative path to a handler script
}

// LateToolManifest declares a single agent-callable tool inline within
// the manifest, removing the need for an MCP server wrapper. Scripts
// receive the tool arguments JSON on stdin and must return the result
// on stdout.
type LateToolManifest struct {
	Name        string          `json:"name"`                  // tool name, will be namespaced as "<plugin>:<name>"
	Description string          `json:"description"`           // shown to the model in the tool list
	Script      string          `json:"script"`                // relative path to the executable script
	Parameters  json.RawMessage `json:"parameters"`            // JSON Schema fragment describing arguments
}

// LateMCPManifest holds MCP server definitions declared by a plugin.
type LateMCPManifest struct {
	Servers map[string]MCPServerConfig `json:"servers"`
}

// MCPServerConfig mirrors the MCP server config structure from mcp_config.json.
type MCPServerConfig struct {
	Command       string            `json:"command,omitempty"`
	Args          []string          `json:"args,omitempty"`
	Env           map[string]string `json:"env,omitempty"`
	URL           string            `json:"url,omitempty"`
	TransportType string            `json:"transportType,omitempty"`
	Disabled      bool              `json:"disabled,omitempty"`
}

// LateHooksManifest defines hook scripts a plugin provides.
//
// Hook contract:
//   - onToolCall receives the ToolCall as JSON on stdin. The hook may:
//     1. Return JSON (any valid JSON object/string) to mutate the call's
//        "arguments" field before next() runs (Gate via mutate).
//     2. Return exactly the string "blocked" to veto the tool execution.
//        The next() in the chain is skipped and late returns an error
//        result to the agent.
//     3. Return empty / non-JSON to pass through unchanged.
//   - onToolResult receives {"tool": "...", "result": "..."} via stdin.
//     Read-only observation hook; the return value is currently logged
//     but not used to mutate anything.
//   - onSessionStart, onTurnStart, onTurnEnd fire before/after their
//     respective lifecycle moments. They receive an empty JSON object
//     on stdin. Errors and stderr are forwarded to the user's TUI.
//   - onMessageSend and onInput form a sequential transform pipeline;
//     each hook sees the previous hook's stdout. Smoke (no stdout) is
//     treated as a no-op so a hook can be a no-op for some inputs.
type LateHooksManifest struct {
	OnToolCall     []string `json:"onToolCall,omitempty"`     // relative paths to scripts
	OnToolResult   []string `json:"onToolResult,omitempty"`   // relative paths to scripts
	OnSessionStart []string `json:"onSessionStart,omitempty"` // relative paths to scripts
	OnTurnStart    []string `json:"onTurnStart,omitempty"`    // relative paths to scripts
	OnTurnEnd      []string `json:"onTurnEnd,omitempty"`      // relative paths to scripts
	OnMessageSend  []string `json:"onMessageSend,omitempty"`  // relative paths to scripts
	OnInput        []string `json:"onInput,omitempty"`        // relative paths to scripts
}

// PackageJSON represents the minimal package.json fields we care about.
type PackageJSON struct {
	Name     string       `json:"name"`
	Version  string       `json:"version"`
	Description string    `json:"description,omitempty"`
	Late     *LateManifest `json:"late,omitempty"`
}

// InstalledPlugin represents an installed plugin with its manifest and metadata.
type InstalledPlugin struct {
	Name        string       `json:"name"`        // plugin name (from package.json)
	Version     string       `json:"version"`     // plugin version
	Description string       `json:"description,omitempty"`
	Path        string       `json:"path"`        // absolute path to the plugin directory
	SourceType  string       `json:"source_type"` // "npm", "git", "local", "marketplace"
	Source      string       `json:"source,omitempty"` // original install string passed by the user (pkg, URL, path, or marketplace name); empty for symlinked local plugins
	Enabled     bool         `json:"enabled"`
	Late        *LateManifest `json:"late"`        // the late extension manifest
}

// Source holds the resolved absolute paths for each surface after registration.
type SurfaceSources struct {
	Skills    []string // resolved absolute paths to skill dirs
	MCPServers map[string]MCPServerConfig
	Themes    []string // resolved absolute paths to theme JSON files
	Commands  []string
}

// ResolveSurfaces resolves relative paths from the manifest into absolute paths
// rooted at the plugin's directory. Returns a SurfaceSources struct.
func (p *InstalledPlugin) ResolveSurfaces() *SurfaceSources {
	src := &SurfaceSources{
		MCPServers: make(map[string]MCPServerConfig),
	}

	if p.Late == nil {
		return src
	}

	for _, rel := range p.Late.Skills {
		abs := filepath.Join(p.Path, rel)
		abs = filepath.Clean(abs)
		src.Skills = append(src.Skills, abs)
	}

	if p.Late.MCP != nil {
		for name, srv := range p.Late.MCP.Servers {
			// Prefix server name with plugin name to avoid collisions
			namespaced := p.Name + ":" + name
			srv.Args = resolveArgs(p.Path, srv.Args)
			src.MCPServers[namespaced] = srv
		}
	}

	for _, rel := range p.Late.Themes {
		abs := filepath.Join(p.Path, rel)
		abs = filepath.Clean(abs)
		src.Themes = append(src.Themes, abs)
	}

	src.Commands = make([]string, 0, len(p.Late.Commands))
	for _, c := range p.Late.Commands {
		if c.Name != "" {
			src.Commands = append(src.Commands, c.Name)
		}
	}

	return src
}

// resolveArgs resolves relative paths in args to absolute paths rooted at pluginDir.
func resolveArgs(pluginDir string, args []string) []string {
	resolved := make([]string, len(args))
	for i, arg := range args {
		if strings.HasPrefix(arg, "./") || strings.HasPrefix(arg, "../") {
			resolved[i] = filepath.Join(pluginDir, arg)
		} else {
			resolved[i] = arg
		}
	}
	return resolved
}

// LoadPlugin loads a plugin from the specified directory by reading its package.json.
func LoadPlugin(dir string) (*InstalledPlugin, error) {
	pkgPath := filepath.Join(dir, "package.json")
	data, err := os.ReadFile(pkgPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", pkgPath, err)
	}

	var pkg PackageJSON
	if err := json.Unmarshal(data, &pkg); err != nil {
		return nil, fmt.Errorf("failed to parse %s: %w", pkgPath, err)
	}

	if pkg.Name == "" {
		return nil, fmt.Errorf("plugin at %s is missing 'name' in package.json", dir)
	}

	baseName := filepath.Base(dir)
	if pkg.Name != baseName && !strings.HasPrefix(pkg.Name, "@") {
		// Allow scoped npm packages (@scope/name) to map to directory name
		// but log a warning if a non-scoped name doesn't match.
	}

	plugin := &InstalledPlugin{
		Name:        pkg.Name,
		Version:     pkg.Version,
		Description: pkg.Description,
		Path:        dir,
		SourceType:  "unknown",
		Enabled:     true,
		Late:        pkg.Late,
	}

	if plugin.Late == nil {
		plugin.Late = &LateManifest{}
	}

	// Detect source type from directory contents
	if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
		plugin.SourceType = "git"
	} else if isSymlink(dir) {
		plugin.SourceType = "local"
	} else {
		plugin.SourceType = "npm"
	}

	return plugin, nil
}

// isSymlink checks if a path is a symbolic link.
func isSymlink(path string) bool {
	info, err := os.Lstat(path)
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeSymlink != 0
}

// SavePluginMeta persists a minimal metadata file for the plugin. After
// writing, force the file's mtime to "now" so the PollingWatcher's snapshot
// always detects the change even on filesystems that coalesce rapid writes.
func SavePluginMeta(plugin *InstalledPlugin) error {
	metaPath := filepath.Join(plugin.Path, ".late-plugin.json")
	data, err := json.MarshalIndent(plugin, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal plugin metadata: %w", err)
	}
	if err := os.WriteFile(metaPath, data, 0644); err != nil {
		return err
	}
	now := time.Now()
	_ = os.Chtimes(metaPath, now, now)
	return nil
}

// LoadPluginMeta loads the metadata from a plugin directory.
func LoadPluginMeta(dir string) (*InstalledPlugin, error) {
	metaPath := filepath.Join(dir, ".late-plugin.json")
	data, err := os.ReadFile(metaPath)
	if err != nil {
		if os.IsNotExist(err) {
			return LoadPlugin(dir)
		}
		return nil, fmt.Errorf("failed to read %s: %w", metaPath, err)
	}

	var plugin InstalledPlugin
	if err := json.Unmarshal(data, &plugin); err != nil {
		return nil, fmt.Errorf("failed to parse %s: %w", metaPath, err)
	}

	// Ensure the Path field is up to date
	plugin.Path = dir

	return &plugin, nil
}
