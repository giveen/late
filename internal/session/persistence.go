package session

import (
	"encoding/json"
	"fmt"
	"late/internal/client"
	"os"
	"path/filepath"
	"strings"
)

// SaveHistory atomically saves the chat history to the specified path.
func SaveHistory(path string, history []client.ChatMessage) error {
	if path == "" {
		return nil // Skip saving if no path provided
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	data, err := json.MarshalIndent(history, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal history: %w", err)
	}

	// Write to a temporary file first
	tmpFile, err := os.CreateTemp(dir, "history-*.json.tmp")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	defer os.Remove(tmpFile.Name()) // Clean up if something goes wrong before rename

	if _, err := tmpFile.Write(data); err != nil {
		tmpFile.Close()
		return fmt.Errorf("failed to write to temp file: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("failed to close temp file: %w", err)
	}

	// Atomic rename
	if err := os.Rename(tmpFile.Name(), path); err != nil {
		return fmt.Errorf("failed to rename temp file: %w", err)
	}

	return nil
}

// cachedResultPrefix is the prefix of a tool result that was cached to disk
// instead of stored inline in the history JSON.
// Format: "[result cached: <hash> — <N> chars on disk]"
const cachedResultPrefix = "[result cached: "

// restoreCachedResults scans history for tool-result cache stubs and replaces
// them with the original content from the cache directory. This is the inverse
// of prepareHistoryForPersistence — it restores the full tool output that was
// written to a cache file at save time.
func restoreCachedResults(history []client.ChatMessage) []client.ChatMessage {
	cacheDir, err := toolResultCacheDir()
	if err != nil {
		// No cache dir — nothing to restore
		return history
	}

	result := make([]client.ChatMessage, len(history))
	copy(result, history)

	for i, msg := range result {
		if msg.Role != "tool" {
			continue
		}
		text := msg.Content.Text
		if !strings.HasPrefix(text, cachedResultPrefix) {
			continue
		}

		// Extract hash: "[result cached: <hash> — N chars on disk]"
		// Find the space after the hash
		rest := text[len(cachedResultPrefix):]
		spaceIdx := strings.IndexByte(rest, ' ')
		if spaceIdx < 0 {
			continue
		}
		hash := rest[:spaceIdx]
		if hash == "" {
			continue
		}

		cachePath := filepath.Join(cacheDir, hash)
		cachedContent, err := os.ReadFile(cachePath)
		if err != nil {
			// Cache file missing — leave the stub in place
			continue
		}

		result[i].Content.Text = string(cachedContent)
	}

	return result
}

// LoadHistory loads the chat history from the specified path.
func LoadHistory(path string) ([]client.ChatMessage, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return []client.ChatMessage{}, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read history file: %w", err)
	}

	var history []client.ChatMessage
	if err := json.Unmarshal(data, &history); err != nil {
		return nil, fmt.Errorf("failed to unmarshal history: %w", err)
	}

	// Restore any tool results that were cached to disk on save
	history = restoreCachedResults(history)

	return history, nil
}
