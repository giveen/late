package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadFileTool_PartialRead(t *testing.T) {
	// constant setup
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "test.txt")
	content := "line1\nline2\nline3\nline4\nline5\n"
	err := os.WriteFile(filePath, []byte(content), 0644)
	if err != nil {
		t.Fatal(err)
	}

	tool := NewReadFileTool()

	// Test case: Read lines 2-4
	args := json.RawMessage(`{"path": "` + filePath + `", "start_line": 2, "end_line": 4}`)
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}

	expected := "2 | line2\n3 | line3\n4 | line4\n"
	if result != expected {
		t.Errorf("Expected:\n%q\nGot:\n%q", expected, result)
	}

	// Test case: Invalid range
	args = json.RawMessage(`{"path": "` + filePath + `", "start_line": 4, "end_line": 2}`)
	result, err = tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "Invalid range") {
		t.Errorf("Expected invalid range error, got: %q", result)
	}
}

func TestReadFileTool_NoCaching(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "test.txt")
	content := "unchanged content"
	os.WriteFile(filePath, []byte(content), 0644)

	tool := NewReadFileTool()
	args := json.RawMessage(`{"path": "` + filePath + `"}`)

	// First read
	res1, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res1, "unchanged content") {
		t.Error("First read failed")
	}

	// Second read (should RETURN CONTENT now, not unchanged message)
	res2, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	// It should contain the content again
	if !strings.Contains(res2, "unchanged content") {
		t.Errorf("Expected content to be returned again, got: %q", res2)
	}
	if strings.Contains(res2, "File has not changed") {
		t.Error("Should not return unchanged message")
	}

	// Modify file
	os.WriteFile(filePath, []byte("new content"), 0644)

	// Third read (should return new content)
	res3, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res3, "new content") {
		t.Errorf("Expected new content, got: %q", res3)
	}
}
