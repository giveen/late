package config

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Config represents the application configuration.
type Config struct {
	EnabledTools map[string]bool `json:"enabled_tools"`
}

func LoadConfig() (*Config, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return nil, err
	}

	lateConfigDir := filepath.Join(configDir, "late")
	configPath := filepath.Join(lateConfigDir, "config.json")

	content, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			// Pre-populate with a default config that enables everything
			defaultConfig := Config{
				EnabledTools: map[string]bool{
					"get_weather":    true,
					"read_file":      true,
					"update_file":    true,
					"write_file":     true,
					"list_dir":       true,
					"mkdir":          true,
					"grep_search":    true,
					"bash":           true,
					"ask":            true,
					"spawn_subagent": true,
					"target_edit":    true,
				},
			}
			defaultData, _ := json.MarshalIndent(defaultConfig, "", "  ")

			// Ensure directory exists
			if err := os.MkdirAll(lateConfigDir, 0755); err != nil {
				return &Config{}, nil // Fallback to empty config if we can't create dir
			}

			if err := os.WriteFile(configPath, defaultData, 0644); err != nil {
				return &Config{}, nil // Fallback to empty config if we can't write file
			}

			return &defaultConfig, nil
		}
		return nil, err
	}

	var cfg Config
	if err := json.Unmarshal(content, &cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}
