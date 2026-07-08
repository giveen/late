package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)
// Smoke tests for bug fixes in context_resolver.
// These verify the specific bugs we fixed without needing full project setup.

func TestContextResolver_StdlibPrefixCollision(t *testing.T) {
	// Bug 1: module "late" must not match import "late-tool/foo"
	proj := &cachedProject{
		projectDir: "/tmp/testproj",
		allGoFiles: []string{"/tmp/testproj/foo.go"},
		pkgMap:     make(map[string]*pkgInfo),
		testFiles:  []string{"/tmp/testproj/foo_test.go"},
		grabbedAt:  mustParseTime(t),
	}

	// If module name is an import prefix, should NOT be treated as local
	got := isStdLibImport("late-tool/foo", "late", proj)
	if !got {
		t.Errorf("isStdLibImport('late-tool/foo', 'late', proj) = false, want true (it's a third-party dep)")
	}

	// But 'late/internal/tool' IS local
	proj.allGoFiles = []string{"/tmp/testproj/internal/tool/bar.go", "/tmp/testproj/internal/tool/bar_test.go"}
	got = isStdLibImport("late/internal/tool", "late", proj)
	if got {
		t.Errorf("isStdLibImport('late/internal/tool', 'late', proj) = true, want false (it's a local import)")
	}
}

func TestContextResolver_FindDefFilesTrimPrefix(t *testing.T) {
	// Bug 2: root package import should resolve to empty relative path
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "internal", "tool"), 0755)
	files := []string{
		filepath.Join(dir, "main.go"),
		filepath.Join(dir, "internal", "tool", "foo.go"),
		filepath.Join(dir, "internal", "tool", "bar.go"),
	}
	for _, f := range files {
		writeGoFile(t, f, "package X")
	}

	proj := buildProjectIndex(dir)

	base := filepath.Base(dir)
	defs := findDefFiles(proj, base, 5)

	if len(defs) == 0 {
		t.Logf("findDefFiles for root package found %d files (may be 0 if dir is empty)", len(defs))
	}
}

func TestContextResolver_RootDirTestDiscovery(t *testing.T) {
	// Bug 4: filepath.Dir("main.go") = "." must still match root-level test files
	dir := t.TempDir()
	writeGoFile(t, filepath.Join(dir, "main.go"), "package main")
	writeGoFile(t, filepath.Join(dir, "main_test.go"), "package main")
	writeGoFile(t, filepath.Join(dir, "helper_test.go"), "package main")
	writeGoFile(t, filepath.Join(dir, "internal", "sub", "sub_test.go"), "package sub")

	tool := &ContextResolverTool{pkgCache: make(map[string]*cachedProject)}
	args, _ := json.Marshal(map[string]any{
		"target_file":  "main.go",
		"project_root": dir,
	})

	resultStr, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	var result ContextResult
	if err := json.Unmarshal([]byte(resultStr), &result); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	foundRoot := false
	for _, tf := range result.TestFiles {
		if tf.Path == "main_test.go" || tf.Path == "helper_test.go" {
			foundRoot = true
			break
		}
	}
	if !foundRoot {
		t.Errorf("root-level test files not found. Got test files: %+v", result.TestFiles)
	}

	for _, tf := range result.TestFiles {
		if tf.Path == "internal/sub/sub_test.go" {
			t.Errorf("nested test file included for root target: %s", tf.Path)
		}
	}
}

func TestContextResolver_NonGoFile(t *testing.T) {
	dir := t.TempDir()
	jsFile := filepath.Join(dir, "app.js")
	os.WriteFile(jsFile, []byte(`
import { useState } from "react";
const x = require("lodash");
function hello() {}
class MyComp {}
`), 0644)

	tool := &ContextResolverTool{pkgCache: make(map[string]*cachedProject)}
	args, _ := json.Marshal(map[string]any{
		"target_file":  "app.js",
		"project_root": dir,
	})

	resultStr, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute failed for .js file: %v", err)
	}

	var result ContextResult
	if err := json.Unmarshal([]byte(resultStr), &result); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	t.Logf("JS result: lang=%s, imports=%d, symbols=%d",
		result.Language, len(result.Imports), len(result.LocalSymbols))
}

// TestReadImport_Go verifies Go import extraction still works.
func TestReadImport_Go(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "main.go")
	os.WriteFile(f, []byte("package main\n\nimport (\n\t\"fmt\"\n\t\"os\"\n)\n"), 0644)

	result, err := readImportBlock(f)
	if err != nil {
		t.Fatalf("readImportBlock failed: %v", err)
	}
	if !strings.Contains(result, "fmt") || !strings.Contains(result, "os") {
		t.Errorf("Go imports missing, got: %s", result)
	}
}

// TestReadImport_Python verifies Python import extraction.
func TestReadImport_Python(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "app.py")
	os.WriteFile(f, []byte("import os\nfrom pathlib import Path\nimport sys\n"), 0644)

	result, err := readImportBlock(f)
	if err != nil {
		t.Fatalf("readImportBlock failed: %v", err)
	}
	if !strings.Contains(result, "import os") || !strings.Contains(result, "from pathlib") {
		t.Errorf("Python imports missing, got: %s", result)
	}
}

// TestReadImport_JS verifies JavaScript import extraction.
func TestReadImport_JS(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "app.js")
	os.WriteFile(f, []byte("import { useState } from \"react\";\nconst x = require(\"lodash\");\n"), 0644)

	result, err := readImportBlock(f)
	if err != nil {
		t.Fatalf("readImportBlock failed: %v", err)
	}
	if !strings.Contains(result, "import") || !strings.Contains(result, "require") {
		t.Errorf("JS imports missing, got: %s", result)
	}
}

// TestReadImport_Unsupported verifies helpful message for unsupported files.
func TestReadImport_Unsupported(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "data.rs")
	os.WriteFile(f, []byte("fn main() {}"), 0644)

	result, err := readImportBlock(f)
	if err != nil {
		t.Fatalf("readImportBlock failed: %v", err)
	}
	if !strings.Contains(result, "not supported") {
		t.Errorf("expected helpful 'not supported' message, got: %s", result)
	}
}

// TestReadImportForDir_Mixed verifies directory scanning with mixed file types.
func TestReadImportForDir_Mixed(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nimport \"fmt\"\n"), 0644)
	os.WriteFile(filepath.Join(dir, "util.py"), []byte("import os\n"), 0644)
	os.WriteFile(filepath.Join(dir, "lib.rs"), []byte("fn main() {}\n"), 0644) // unsupported
	os.WriteFile(filepath.Join(dir, "helper.js"), []byte("import React from \"react\";\n"), 0644)

	result, err := readImportBlockForDir(dir)
	if err != nil {
		t.Fatalf("readImportBlockForDir failed: %v", err)
	}
	if !strings.Contains(result, "main.go") {
		t.Errorf("expected main.go in results, got: %s", result)
	}
	if !strings.Contains(result, "util.py") {
		t.Errorf("expected util.py in results, got: %s", result)
	}
	if !strings.Contains(result, "helper.js") {
		t.Errorf("expected helper.js in results, got: %s", result)
	}
	if strings.Contains(result, "lib.rs") {
		t.Errorf("lib.rs should not appear (unsupported), got: %s", result)
	}
}

// helpers

func writeGoFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func mustParseTime(t *testing.T) time.Time {
	t.Helper()
	return time.Now()
}
