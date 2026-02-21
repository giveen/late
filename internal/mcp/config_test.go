package mcp

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfigFromFile(t *testing.T) {
	// Create a temporary directory for test config
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "mcp.json")

	// Create a test config file
	configContent := `{
		"mcpServers": {
			"test-server": {
				"command": "node",
				"args": ["test.js"],
				"env": {
					"TEST_VAR": "test-value"
				}
			}
		}
	}`
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("Failed to write test config: %v", err)
	}

	// Load the config
	config, err := loadConfigFromFile(configPath)
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	// Verify the config
	if config == nil {
		t.Fatal("Config should not be nil")
	}

	if len(config.McpServers) != 1 {
		t.Errorf("Expected 1 server, got %d", len(config.McpServers))
	}

	if server, ok := config.McpServers["test-server"]; !ok {
		t.Error("Expected test-server in config")
	} else {
		if server.Command != "node" {
			t.Errorf("Expected command 'node', got '%s'", server.Command)
		}
		if len(server.Args) != 1 || server.Args[0] != "test.js" {
			t.Errorf("Expected args ['test.js'], got %v", server.Args)
		}
		if server.Env["TEST_VAR"] != "test-value" {
			t.Errorf("Expected env TEST_VAR='test-value', got '%s'", server.Env["TEST_VAR"])
		}
	}
}

func TestLoadConfigMissingFile(t *testing.T) {
	// Try to load a non-existent config file
	_, err := loadConfigFromFile("/nonexistent/path/mcp.json")
	if err == nil {
		t.Error("Expected error for missing file, got nil")
	}
}

func TestExpandEnvVars(t *testing.T) {
	// Set a test environment variable
	if err := os.Setenv("TEST_EXPAND_VAR", "expanded-value"); err != nil {
		t.Fatalf("Failed to set env var: %v", err)
	}
	defer os.Unsetenv("TEST_EXPAND_VAR")

	// Test expansion
	testCases := []struct {
		input    string
		expected string
	}{
		{"${TEST_EXPAND_VAR}", "expanded-value"},
		{"prefix-${TEST_EXPAND_VAR}-suffix", "prefix-expanded-value-suffix"},
		{"no variables here", "no variables here"},
		{"${NONEXISTENT_VAR}", ""},
		{"multiple ${TEST_EXPAND_VAR} and ${TEST_EXPAND_VAR}", "multiple expanded-value and expanded-value"},
	}

	for _, tc := range testCases {
		result := ExpandEnvVars(tc.input)
		if result != tc.expected {
			t.Errorf("ExpandEnvVars(%q) = %q, expected %q", tc.input, result, tc.expected)
		}
	}
}

func TestExpandServerEnvVars(t *testing.T) {
	// Set test environment variables
	if err := os.Setenv("TEST_CMD", "node"); err != nil {
		t.Fatalf("Failed to set env var: %v", err)
	}
	if err := os.Setenv("TEST_ARG", "test.js"); err != nil {
		t.Fatalf("Failed to set env var: %v", err)
	}
	if err := os.Setenv("TEST_TOKEN", "secret-token"); err != nil {
		t.Fatalf("Failed to set env var: %v", err)
	}
	defer os.Unsetenv("TEST_CMD")
	defer os.Unsetenv("TEST_ARG")
	defer os.Unsetenv("TEST_TOKEN")

	// Create a server config with environment variables
	server := &MCPServer{
		Command: "${TEST_CMD}",
		Args:    []string{"${TEST_ARG}", "static-arg"},
		Env: map[string]string{
			"TOKEN":  "${TEST_TOKEN}",
			"STATIC": "static-value",
		},
	}

	// Expand environment variables
	ExpandServerEnvVars(server)

	// Verify expansion
	if server.Command != "node" {
		t.Errorf("Expected command 'node', got '%s'", server.Command)
	}

	if len(server.Args) != 2 || server.Args[0] != "test.js" || server.Args[1] != "static-arg" {
		t.Errorf("Expected args ['test.js', 'static-arg'], got %v", server.Args)
	}

	if server.Env["TOKEN"] != "secret-token" {
		t.Errorf("Expected env TOKEN='secret-token', got '%s'", server.Env["TOKEN"])
	}

	if server.Env["STATIC"] != "static-value" {
		t.Errorf("Expected env STATIC='static-value', got '%s'", server.Env["STATIC"])
	}
}

func TestFindConfigPath(t *testing.T) {
	// This test can't assume a config file doesn't exist on the tester's machine,
	// so we only verify it doesn't return an error.
	_, err := findConfigPath()
	if err != nil {
		t.Fatalf("findConfigPath should not error: %v", err)
	}
}

func TestLoadMCPConfig(t *testing.T) {
	// Likewise, this might load a real configuration, so we only verify
	// that it returns a non-nil struct without error.
	config, err := LoadMCPConfig()
	if err != nil {
		t.Fatalf("LoadMCPConfig should not error: %v", err)
	}

	if config == nil {
		t.Fatal("Config should not be nil")
	}
}
