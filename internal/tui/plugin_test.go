package tui

import (
	"testing"
)

// TestPluginCommands_Empty verifies that when no commands are set, the getter returns nil.
func TestPluginCommands_Empty(t *testing.T) {
	m := &Model{}

	cmds := m.PluginCommands
	if cmds != nil {
		t.Errorf("expected nil, got %v", cmds)
	}
}

// TestPluginCommands_SetAndGet verifies setting commands and retrieving them.
func TestPluginCommands_SetAndGet(t *testing.T) {
	m := &Model{}
	expected := []string{"/query", "/graph", "/analyze"}

	m.SetPluginCommands(expected)
	cmds := m.ListedPluginCommands()

	if len(cmds) != len(expected) {
		t.Fatalf("expected %d commands, got %d", len(expected), len(cmds))
	}
	for i := range expected {
		if cmds[i] != expected[i] {
			t.Errorf("command[%d]: expected %q, got %q", i, expected[i], cmds[i])
		}
	}
}

// TestPluginCommands_DefensiveCopy verifies that the getter returns a copy,
// so mutating the returned slice does not affect the model's internal state.
func TestPluginCommands_DefensiveCopy(t *testing.T) {
	m := &Model{}
	original := []string{"/cmd1", "/cmd2"}
	m.SetPluginCommands(original)

	// Get a copy and mutate it
	got := m.PluginCommands()
	got[0] = "/mutated"

	// Internal state should be unchanged
	internal := m.PluginCommands()
	if internal[0] != "/cmd1" {
		t.Errorf("expected internal state to be unchanged, got %q", internal[0])
	}
}

// TestSetPluginCommands_Nil sets nil and verifies the getter returns nil.
func TestSetPluginCommands_Nil(t *testing.T) {
	m := &Model{}
	m.SetPluginCommands(nil)
	if cmds := m.PluginCommands(); cmds != nil {
		t.Errorf("expected nil after setting nil, got %v", cmds)
	}
}

// TestSetPluginCommands_EmptySlice sets an empty slice and verifies the getter returns nil.
func TestSetPluginCommands_EmptySlice(t *testing.T) {
	m := &Model{}
	m.SetPluginCommands([]string{})
	if cmds := m.PluginCommands(); cmds != nil {
		t.Errorf("expected nil after setting empty slice, got %v", cmds)
	}
}

// TestSetPluginCommands_Replace verifies that setting new commands replaces the old ones.
func TestSetPluginCommands_Replace(t *testing.T) {
	m := &Model{}
	m.SetPluginCommands([]string{"/old"})
	m.SetPluginCommands([]string{"/new"})

	cmds := m.PluginCommands()
	if len(cmds) != 1 || cmds[0] != "/new" {
		t.Errorf("expected [\"/new\"], got %v", cmds)
	}
}

// TestIsPluginCmd_ExactMatch verifies isPluginCmd matches exact command strings.
func TestIsPluginCmd_ExactMatch(t *testing.T) {
	cmds := []string{"/query", "/graph-rag", "/analyze"}
	cases := []struct {
		input    string
		expected bool
	}{
		{"/query", true},
		{"/graph-rag", true},
		{"/analyze", true},
		{"/query ", false},        // trailing space
		{"/unknown", false},
		{"/Query", false},         // case-sensitive
		{"query", false},          // missing leading slash
		{"", false},
	}

	for _, tc := range cases {
		result := isPluginCmd(tc.input, cmds)
		if result != tc.expected {
			t.Errorf("isPluginCmd(%q, cmds) = %v, expected %v", tc.input, result, tc.expected)
		}
	}
}

// TestIsPluginCmd_EmptyList verifies isPluginCmd with an empty command list.
func TestIsPluginCmd_EmptyList(t *testing.T) {
	if isPluginCmd("/anything", nil) {
		t.Error("expected false for nil command list")
	}
	if isPluginCmd("/anything", []string{}) {
		t.Error("expected false for empty command list")
	}
}

// TestIsPluginCmd_WhitespaceTrim verifies that isPluginCmd trims whitespace from input.
func TestIsPluginCmd_WhitespaceTrim(t *testing.T) {
	cmds := []string{"/clear"}
	if !isPluginCmd("  /clear  ", cmds) {
		t.Error("expected true — isPluginCmd should trim whitespace")
	}
}

// TestAvailableCommands_ContainsBuiltins verifies that the built-in command set
// includes the expected slash commands.
func TestAvailableCommands_ContainsBuiltins(t *testing.T) {
	expected := []string{"/clear", "/compose", "/help", "/log", "/quit", "/rewind"}

	for _, exp := range expected {
		found := false
		for _, cmd := range AvailableCommands {
			if cmd == exp {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("AvailableCommands should contain %q", exp)
		}
	}

	if len(AvailableCommands) < 6 {
		t.Errorf("expected at least 6 built-in commands, got %d", len(AvailableCommands))
	}
}

// TestPluginCommands_IsolatedModels verifies that two different Model instances
// have independent PluginCommands slices.
func TestPluginCommands_IsolatedModels(t *testing.T) {
	m1 := &Model{}
	m2 := &Model{}

	m1.SetPluginCommands([]string{"/m1-cmd"})
	m2.SetPluginCommands([]string{"/m2-cmd"})

	if c := m1.PluginCommands(); len(c) != 1 || c[0] != "/m1-cmd" {
		t.Errorf("expected m1 to have [/m1-cmd], got %v", c)
	}
	if c := m2.PluginCommands(); len(c) != 1 || c[0] != "/m2-cmd" {
		t.Errorf("expected m2 to have [/m2-cmd], got %v", c)
	}
}
