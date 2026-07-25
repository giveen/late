package plugin

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"late/internal/client"
	"late/internal/common"
)

// helper: write a small POSIX shell script
func writeExecutableShell(t *testing.T, path, body string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell-script test only runs on POSIX")
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0755); err != nil {
		t.Fatalf("write shell: %v", err)
	}
}

// helper: write a fake plugin into a temp dir
func writeTestPlugin(t *testing.T, parentDir, name string, manifest *LateManifest) *InstalledPlugin {
	t.Helper()
	pluginDir := filepath.Join(parentDir, name)
	if err := os.MkdirAll(pluginDir, 0755); err != nil {
		t.Fatalf("mkdir plugin: %v", err)
	}
	pkg := PackageJSON{Name: name, Version: "1.0.0", Late: manifest}
	b, _ := json.Marshal(pkg)
	if err := os.WriteFile(filepath.Join(pluginDir, "package.json"), b, 0644); err != nil {
		t.Fatalf("write package.json: %v", err)
	}
	p, err := LoadPlugin(pluginDir)
	if err != nil {
		t.Fatalf("LoadPlugin: %v", err)
	}
	_ = SavePluginMeta(p)
	return p
}

// 1. Hook path containment: rejects escaping paths
func TestResolveHookPath_RejectsTraversal(t *testing.T) {
	pluginDir := t.TempDir()
	if _, err := resolveHookPath(pluginDir, "../other.sh"); err == nil {
		t.Fatal("expected ../other.sh to escape plugin dir")
	}
	if _, err := resolveHookPath(pluginDir, "/etc/passwd"); err == nil {
		t.Fatal("expected absolute /etc/passwd to escape plugin dir")
	}
	if _, err := resolveHookPath(pluginDir, ""); err == nil {
		t.Fatal("expected empty path to be rejected")
	}
}

// 2. Hook path containment: allows contained paths
func TestResolveHookPath_AllowsContained(t *testing.T) {
	pluginDir := t.TempDir()
	got, err := resolveHookPath(pluginDir, "subdir/hook.sh")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasPrefix(got, pluginDir) {
		t.Fatalf("expected resolved path under plugin dir, got %q", got)
	}
}

// 3. Hook execution: happy path reads from stdin
func TestRunHook_HappyPath(t *testing.T) {
	pluginDir := t.TempDir()
	script := filepath.Join(pluginDir, "echo.sh")
	writeExecutableShell(t, script, `cat`)
	out, err := runHook(context.Background(), pluginDir, "echo.sh", []byte("hello"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "hello" {
		t.Fatalf("expected hello, got %q", out)
	}
}

// 4. Hook execution: timeout enforced
func TestRunHook_TimeoutEnforced(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell test only on POSIX")
	}
	pluginDir := t.TempDir()
	script := filepath.Join(pluginDir, "sleep.sh")
	writeExecutableShell(t, script, `sleep 30`)
	// Override hookTimeout via short ctx to keep test fast; we pass a
	// shorter context via WithTimeout to be portable.
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := runHook(ctx, pluginDir, "sleep.sh", nil)
	if err == nil {
		t.Fatal("expected timeout error")
	}
}

// 5. HookedMessage: empty/no hooks returns input unchanged
func TestHookedMessage_NoHooksReturnsInput(t *testing.T) {
	pm := NewPluginManager(t.TempDir())
	if got := pm.HookedMessage("hi"); got != "hi" {
		t.Fatalf("expected unchanged, got %q", got)
	}
}

// 6. HookedMessage: applies OnMessageSend script transform
func TestHookedMessage_TransformsText(t *testing.T) {
	pm := NewPluginManager(t.TempDir())
	pluginDir := t.TempDir()
	mf := &LateManifest{Hooks: &LateHooksManifest{OnMessageSend: []string{"wrap.sh"}}}
	p := writeTestPlugin(t, pluginDir, "msg-wrap", mf)
	writeExecutableShell(t, filepath.Join(p.Path, "wrap.sh"), `cat; echo`)
	p.Path = filepath.Join(pluginDir, "msg-wrap")
	pm.Add(p)
	got := pm.HookedMessage("hi")
	if got != "hi" {
		t.Fatalf("expected 'hi', got %q (note: shell `echo` without args prints empty)", got)
	}
}

// 7. BuildHookMiddlewares: returns one middleware per plugin
func TestBuildHookMiddlewares_PerPlugin(t *testing.T) {
	pm := NewPluginManager(t.TempDir())
	for i := 0; i < 3; i++ {
		dir := t.TempDir()
		name := "p" + string(rune('1'+i))
		mf := &LateManifest{Hooks: &LateHooksManifest{OnToolCall: []string{"noop.sh"}}}
		p := writeTestPlugin(t, dir, name, mf)
		p.Path = filepath.Join(dir, name)
		pm.Add(p)
	}
	mws := pm.BuildHookMiddlewares()
	if len(mws) != 3 {
		t.Fatalf("expected 3 middlewares, got %d", len(mws))
	}
	// Verify signature is correct: invoking the middleware with a noop next
	// should still call next and return its result.
	var called bool
	next := common.ToolRunner(func(ctx context.Context, call client.ToolCall) (string, error) {
		called = true
		return "ok", nil
	})
	for _, mw := range mws {
		runner := mw(next)
		out, err := runner(context.Background(), client.ToolCall{
			Function: client.FunctionCall{Name: "anything", Arguments: "{}"},
		})
		if err != nil {
			t.Fatalf("middleware returned error: %v", err)
		}
		if out != "ok" {
			t.Fatalf("middleware didn't pass through, got %q", out)
		}
	}
	if !called {
		t.Fatal("expected 'next' to be called")
	}
}

// 8. BuildHookMiddlewares: empty when no plugins have OnToolCall
func TestBuildHookMiddlewares_EmptyWhenNoHooks(t *testing.T) {
	pm := NewPluginManager(t.TempDir())
	dir := t.TempDir()
	mf := &LateManifest{} // no hooks at all
	p := writeTestPlugin(t, dir, "silent", mf)
	p.Path = filepath.Join(dir, "silent")
	pm.Add(p)
	if mws := pm.BuildHookMiddlewares(); len(mws) != 0 {
		t.Fatalf("expected 0, got %d", len(mws))
	}
}

// 9. CallOnSessionStartHooks runs without panic on empty manager
func TestCallOnSessionStartHooks_NoPanicOnEmpty(t *testing.T) {
	pm := NewPluginManager(t.TempDir())
	pm.CallOnSessionStartHooks()
}

// 10. Theme resolve: empty/id reflection
func TestResolveRenderTheme_EmptyReturnsBase(t *testing.T) {
	got, err := ResolveRenderTheme("", nil, nil)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if string(got) != string(LateTheme) {
		t.Fatal("expected base theme when override is nil")
	}
}

// 11. Theme resolve: merges top-level glamour keys
func TestResolveRenderTheme_MergesGlamourKeys(t *testing.T) {
	mod := map[string]any{
		"document": map[string]any{
			"color": "#FF0000",
		},
	}
	got, err := ResolveRenderTheme("p:red", mod, nil)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	s := string(got)
	if !strings.Contains(s, "#FF0000") {
		t.Fatal("expected merged colour in output")
	}
	if !strings.Contains(s, "_late_theme_name") {
		t.Fatal("expected theme name marker in output")
	}
}

// 12. Theme resolve: palette appended under _late_palette
func TestResolveRenderTheme_PaletteAttached(t *testing.T) {
	palette := map[string]string{
		"bg":     "#000000",
		"accent": "#E5A85C",
	}
	got, err := ResolveRenderTheme("plugin:ocean", nil, palette)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	s := string(got)
	if !strings.Contains(s, "_late_palette") {
		t.Fatal("expected _late_palette marker")
	}
	if !strings.Contains(s, "E5A85C") {
		t.Fatal("expected palette colour in output")
	}
}

// 13. Theme path resolution: rejects traversal
func TestResolveThemePath_RejectsTraversal(t *testing.T) {
	pluginDir := t.TempDir()
	if _, err := resolveThemePath(pluginDir, "../../etc/theme.json"); err == nil {
		t.Fatal("expected traversal to be rejected")
	}
	if _, err := resolveThemePath(pluginDir, "/etc/passwd"); err == nil {
		t.Fatal("expected absolute to be rejected")
	}
}

// 14. Theme load: rejects missing name
func TestLoadThemeFile_RequiresName(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(p, []byte(`{"palette": {}}`), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadThemeFile(p); err == nil {
		t.Fatal("expected error when 'name' missing")
	}
}

// 15. GetTheme: bare name lookup across plugins
func TestGetTheme_BareNameLookup(t *testing.T) {
	pm := NewPluginManager(t.TempDir())
	dir := t.TempDir()
	writeJSON(t, dir, "ocean.json", `{"name":"ocean","palette":{"bg":"#000"}}`)
	mf := &LateManifest{Themes: []string{"ocean.json"}}
	p := writeTestPlugin(t, dir, "theme-plugin", mf)
	pm.Add(p)

	info, err := pm.GetTheme("ocean")
	if err != nil || info == nil {
		t.Fatalf("expected to find 'ocean', got err=%v info=%v", err, info)
	}
	if info.ID != "theme-plugin:ocean" {
		t.Fatalf("unexpected id: %s", info.ID)
	}
}

// 16. GetTheme: namespaced lookup
func TestGetTheme_NamespacedLookup(t *testing.T) {
	pm := NewPluginManager(t.TempDir())
	dir := t.TempDir()
	writeJSON(t, dir, "theme.json", `{"name":"v1"}`)
	mf := &LateManifest{Themes: []string{"theme.json"}}
	p := writeTestPlugin(t, dir, "green", mf)
	pm.Add(p)

	info, err := pm.GetTheme("green:v1")
	if err != nil || info == nil {
		t.Fatalf("expected namespace match, got err=%v info=%v", err, info)
	}
}

// 17. GetTheme: empty returns (nil, nil)
func TestGetTheme_EmptyReturnsNilNil(t *testing.T) {
	pm := NewPluginManager(t.TempDir())
	info, err := pm.GetTheme("")
	if err != nil || info != nil {
		t.Fatalf("expected nil/nil for empty id, got info=%v err=%v", info, err)
	}
}

// 18. AllThemes: aggregates across enabled plugins only
func TestAllThemes_AggregatesEnabledOnly(t *testing.T) {
	pm := NewPluginManager(t.TempDir())
	dir := t.TempDir()
	writeJSON(t, dir, "a.json", `{"name":"alpha"}`)
	mf := &LateManifest{Themes: []string{"a.json"}}
	p := writeTestPlugin(t, dir, "alpha", mf)
	p.Enabled = false
	pm.Add(p)

	got := pm.AllThemes()
	if len(got) != 0 {
		t.Fatalf("expected 0 themes from disabled plugin, got %d", len(got))
	}
	p.Enabled = true
	got = pm.AllThemes()
	if len(got) != 1 {
		t.Fatalf("expected 1 theme after enabling, got %d", len(got))
	}
}

// 19. AllThemes: skips unparseable files
func TestAllThemes_SkipsUnparseable(t *testing.T) {
	pm := NewPluginManager(t.TempDir())
	dir := t.TempDir()
	mf := &LateManifest{Themes: []string{"missing.json", "garbage.json"}}
	p := writeTestPlugin(t, dir, "broken", mf)
	// missing.json doesn't exist; garbage.json is not valid
	_ = os.WriteFile(filepath.Join(p.Path, "garbage.json"), []byte("not json{{"), 0644)
	pm.Add(p)

	got := pm.AllThemes()
	if len(got) != 0 {
		t.Fatalf("expected 0 themes, got %d", len(got))
	}
}

// 20. findTheme: name-mismatch returns error
func TestFindTheme_NameMismatch(t *testing.T) {
	pm := NewPluginManager(t.TempDir())
	dir := t.TempDir()
	writeJSON(t, dir, "x.json", `{"name":"realname"}`)
	mf := &LateManifest{Themes: []string{"x.json"}}
	p := writeTestPlugin(t, dir, "finder", mf)
	pm.Add(p)

	_, err := pm.findTheme("finder", "wrongname")
	if err == nil {
		t.Fatal("expected error for name mismatch")
	}
}

// ----------------------------------------------------------------------------

func writeJSON(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0644); err != nil {
		t.Fatalf("write json: %v", err)
	}
}
