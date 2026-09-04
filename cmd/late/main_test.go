package main

import (
	"testing"
)

// TestPluginInlineTool_RequiresConfirmation guards the documented contract
// that plugin inline tools (arbitrary scripts) go through the normal user
// confirmation flow. plugin-example.md: "user confirmation still prompts
// the user"; plugin-sdk.md: "plugin tools respect user confirmation".
func TestPluginInlineTool_RequiresConfirmation(t *testing.T) {
	tool := pluginInlineTool{name: "example:lookup"}
	if !tool.RequiresConfirmation(nil) {
		t.Error("plugin inline tool must require confirmation before running its script")
	}
}

// TestToolEnabled_LegacyColonKeyFallback guards migration compatibility: a
// config written before tool names were namespaced as "server__tool" may
// still disable a tool by its old "server:tool" key. toolEnabled must
// reconstruct and check that legacy form before falling back further to
// the bare tool name.
func TestToolEnabled_LegacyColonKeyFallback(t *testing.T) {
	enabledTools := map[string]bool{"myserver:mytool": false}

	if toolEnabled(enabledTools, "myserver__mytool") {
		t.Error("expected the namespaced name to resolve via the legacy colon key and report disabled")
	}

	// A namespaced-key entry still takes priority over the legacy form.
	enabledTools["other__tool"] = true
	enabledTools["other:tool"] = false
	if !toolEnabled(enabledTools, "other__tool") {
		t.Error("expected the exact namespaced key to win over the legacy colon key")
	}

	// Unrelated tools still default to enabled.
	if !toolEnabled(enabledTools, "unrelated__tool") {
		t.Error("expected an unconfigured tool to default to enabled")
	}
}
