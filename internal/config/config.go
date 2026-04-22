package config

import (
	"encoding/json"
	"fmt"
	"late/internal/pathutil"
	"os"
	"path/filepath"
	"runtime"
)

const (
	configDirPerm  os.FileMode = 0o700
	configFilePerm os.FileMode = 0o600
)

// Config represents the application configuration.
type Config struct {
	EnabledTools  map[string]bool `json:"enabled_tools"`
	OpenAIBaseURL string          `json:"openai_base_url,omitempty"`
	OpenAIAPIKey  string          `json:"openai_api_key,omitempty"`
	OpenAIModel   string          `json:"openai_model,omitempty"`
}

func defaultConfig() Config {
	return Config{
		EnabledTools: map[string]bool{
			"read_file":      true,
			"write_file":     true,
			"target_edit":    true,
			"spawn_subagent": true,
			"bash":           true,
		},
	}
}

func LoadConfig() (*Config, error) {
	lateConfigDir, err := pathutil.LateConfigDir()
	if err != nil {
		return nil, err
	}
	configPath := filepath.Join(lateConfigDir, "config.json")

	content, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			// Pre-populate with a default config that enables everything
			fallback := defaultConfig()
			defaultData, _ := json.MarshalIndent(fallback, "", "  ")

			// Ensure directory exists
			if err := os.MkdirAll(lateConfigDir, configDirPerm); err != nil {
				return &fallback, fmt.Errorf("failed to create config directory: %w", err)
			}

			if err := os.WriteFile(configPath, defaultData, configFilePerm); err != nil {
				return &fallback, fmt.Errorf("failed to write default config: %w", err)
			}

			if err := ensureSecureConfigPermissions(lateConfigDir, configPath); err != nil {
				return &fallback, err
			}

			return &fallback, nil
		}

		fallback := defaultConfig()
		return &fallback, err
	}

	permErr := ensureSecureConfigPermissions(lateConfigDir, configPath)

	var cfg Config
	if err := json.Unmarshal(content, &cfg); err != nil {
		fallback := defaultConfig()
		return &fallback, err
	}

	if cfg.EnabledTools == nil {
		cfg.EnabledTools = defaultConfig().EnabledTools
	}

	if permErr != nil {
		return &cfg, permErr
	}

	return &cfg, nil
}

func ensureSecureConfigPermissions(configDir, configPath string) error {
	if runtime.GOOS == "windows" {
		return nil
	}

	if err := tightenPermission(configDir, configDirPerm); err != nil {
		return fmt.Errorf("failed to set config directory permissions: %w", err)
	}

	if err := tightenPermission(configPath, configFilePerm); err != nil {
		return fmt.Errorf("failed to set config file permissions: %w", err)
	}

	return nil
}

func tightenPermission(path string, required os.FileMode) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}

	if info.Mode().Perm() == required {
		return nil
	}

	return os.Chmod(path, required)
}
