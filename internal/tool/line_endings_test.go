package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTargetEditTool_WindowsLineEndings(t *testing.T) {
	// Setup temp dir
	tmpDir, err := os.MkdirTemp("", "test_target_edit_repro_*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	file1 := "windows.txt"
	// File has Windows line endings (\r\n)
	content := "line 1\r\nline 2\r\nline 3\r\n"
	filePath := filepath.Join(tmpDir, file1)
	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	tool := TargetEditTool{}
	ctx := context.Background()

	t.Run("succeeds with unix search block on windows file", func(t *testing.T) {
		// Search block uses Unix line endings (\n)
		// Usually LLMs will provide search blocks with \n
		args := json.RawMessage(`{
			"file": "` + filePath + `",
			"search": "line 1\nline 2",
			"replace": "line 1\nupdated line 2"
		}`)
		res, err := tool.Execute(ctx, args)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(res, "Successfully applied") {
			t.Errorf("expected success message, got: %s", res)
		}

		data, _ := os.ReadFile(filePath)
		// Verify content is updated AND line endings are preserved as \r\n
		expected := "line 1\r\nupdated line 2\r\nline 3\r\n"
		if string(data) != expected {
			t.Errorf("expected %q, got %q", expected, string(data))
		}
	})

	t.Run("normalizes mixed line endings to unix", func(t *testing.T) {
		mixedFile := filepath.Join(tmpDir, "mixed.txt")
		// Mixed line endings: \n and \r\n
		content := "line 1\nline 2\r\nline 3\n"
		if err := os.WriteFile(mixedFile, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}

		args := json.RawMessage(`{
			"file": "` + mixedFile + `",
			"search": "line 2",
			"replace": "updated line 2"
		}`)
		_, err := tool.Execute(ctx, args)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		data, _ := os.ReadFile(mixedFile)
		// Expected: all normalized to \n
		expected := "line 1\nupdated line 2\nline 3\n"
		if string(data) != expected {
			t.Errorf("expected %q, got %q", expected, string(data))
		}
	})
}
