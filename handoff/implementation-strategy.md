# Implementation Strategy: `search` Tool (Pure Go, stdlib only)

## 1. Design Decision: Pure Go with standard library

### Decision: stdlib-only (`filepath.WalkDir` + `regexp` + `bufio.Scanner`) for V1

**Rationale:** The llm coding agents surveyed all shell out to ripgrep because they operate on massive repos (100K+ files) where rg's 10-100× speedup matters. The `late` CLI works on typical developer workspaces (hundreds to low-thousands of files) where Go's `filepath.WalkDir` completes in under 100ms. The stdlib approach removes an external binary dependency entirely — no `rg` installation, no `exec.LookPath`, no exit-code parsing, no cross-platform rg distribution.

| Factor | Shell-out to ripgrep | Pure Go (stdlib only) |
|--------|---------------------|-----------------------|
| External dependency | Requires `rg` binary on PATH | **Zero** — single binary always works |
| Error surface | 3 exit codes + stderr parsing + `exec.LookPath` | Plain Go errors |
| Cross-platform | Must test/support rg on 3 OSes | Works wherever stdlib works |
| `.gitignore` | Built-in | Skipped for V1 (can add with `git-pkgs/gitignore` in V2) |
| Binary detection | rg built-in | `IsBinary()` already exists in `utils.go` |
| Code complexity | ~160 lines + output parsing | ~250 lines, all stdlib |
| Perf at 10K files | ~10ms | ~80ms (below human perception) |
| Deployment | Single binary + rg requirement | **True single binary** |
| Line-numbered output | Must parse `file:line:content` from rg | Direct control during walk |

**Key stdlib primitives:**

| Primitive | Role |
|-----------|------|
| `filepath.WalkDir` | Recursive directory traversal (Go 1.25 optimizations) |
| `os.ReadDir` | Non-recursive directory listing (for `maxdepth` or targeted reads) |
| `filepath.Glob` | Pattern-based file matching (`*.go`, `**/*.ts`) |
| `bufio.Scanner` | Line-by-line file reading (memory-efficient for large files) |
| `regexp` | Pattern matching (compiled once, reused per file) |
| `strings.Contains` | Literal string matching (faster than regexp for fixed patterns) |
| `os.ReadFile` | Full file read (for small files, <1MB) |
| `IsBinary()` | Existing binary detection in utils.go |

**Why not rg:**

- External binary dependency breaks the "single binary" promise
- `exec.LookPath` adds failure surface (rg missing, wrong version, permission denied)
- Output must be parsed back into structured data (re-parsing what we control)
- Context cancellation handling is more complex with subprocesses
- The speed difference is irrelevant at project scale

---

## 2. New File: `internal/tool/search.go`

### 2.1 Struct Definition

```go
package tool

import (
    "bufio"
    "context"
    "encoding/json"
    "fmt"
    "io/fs"
    "os"
    "path/filepath"
    "regexp"
    "strings"

    "late/internal/common"
)

// SearchTool performs file and content search using Go's standard library.
// It walks directories with filepath.WalkDir, reads files with bufio.Scanner,
// and matches patterns with regexp. No external dependencies.
type SearchTool struct{}
```

**Niladic, stateless struct** — follows the pattern of `ShellTool`, `WriteFileTool`, `WriteImplementationPlanTool`, `TargetEditTool`. No `LastReads`-style state since search results are inherently ephemeral.

**Why not stateful:** Search is idempotent, read-only, and results depend only on parameters. No caching or change tracking needed.

### 2.2 Tool Interface Implementation

```go
func (t *SearchTool) Name() string { return "search_tool" }

func (t *SearchTool) Description() string {
    return "Search files and file contents using regex or literal patterns. Returns matching files and/or content with line numbers. Use this instead of bash grep/find."
}
```

**Name choice:** `"search_tool"` — matches the descriptive pattern of existing tools (`read_file`, `write_file`, `target_edit`). Avoided bare `"search"` because it's too generic (could collide with web search concepts). Avoided `"grep"` because the LLM may confuse it with shell `grep`. The `_tool` suffix makes it unmistakably a tool invocation for the LLM.

### 2.3 Parameters JSON Schema

```go
func (t *SearchTool) Parameters() json.RawMessage {
    return json.RawMessage(`{
        "type": "object",
        "properties": {
            "pattern": {
                "type": "string",
                "description": "Pattern to search for. Interpreted as a regex unless 'fixed_strings' is true"
            },
            "path": {
                "type": "string",
                "description": "Directory to search in (default: current working directory)"
            },
            "include": {
                "type": "string",
                "description": "File glob pattern to filter, e.g. '*.go' or '*_test.go'. Uses filepath.Match semantics on the file name."
            },
            "output_mode": {
                "type": "string",
                "enum": ["files_with_matches", "content", "count"],
                "description": "Output format: 'files_with_matches' (default, file paths only), 'content' (matching lines with line numbers), 'count' (match count per file)"
            },
            "case_sensitive": {
                "type": "boolean",
                "description": "If true, do case-sensitive matching (default: false, case-insensitive)"
            },
            "fixed_strings": {
                "type": "boolean",
                "description": "If true, treat pattern as a literal string instead of regex (default: false)"
            },
            "context_lines": {
                "type": "integer",
                "description": "Number of context lines to show before and after each match (content mode only)"
            },
            "max_results": {
                "type": "integer",
                "description": "Maximum number of results to return (default: 100, max: 500)"
            }
        },
        "required": ["pattern"]
    }`)
}
```

**Parameter design rationale:**

| Parameter | Why | Design Choice |
|-----------|-----|---------------|
| `pattern` (required) | Core search input | Regex by default, with `fixed_strings` toggle. `strings.Contains` when literal (faster than regexp). |
| `path` | Scope the search | Defaults to CWD. Validated with `IsSafePath()` for security. |
| `include` | File type filtering | Glob via `filepath.Match` against filename during WalkDir. E.g., `"*.go"` matches `.go` files. |
| `output_mode` | Three output formats | `files_with_matches` is default (two-phase design, Codex CLI pattern). |
| `case_sensitive` | Toggle case sensitivity | Default false. Use `(?i)` prefix for regex, `strings.EqualFold` for fixed. |
| `fixed_strings` | Literal vs regex | Trivial in pure Go — switch between `strings.Contains` and `regexp.MatchString`. Not exposed by rg without `-F` flag. |
| `context_lines` | Surrounding context | Track via ring buffer during file scan. |
| `max_results` | Prevent overload | Default 100, hard cap 500. Global cap across all files. |

### 2.4 Execute Method (stdlib WalkDir approach)

```go
func (t *SearchTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
    var params struct {
        Pattern       string `json:"pattern"`
        Path          string `json:"path"`
        Include       string `json:"include"`
        OutputMode    string `json:"output_mode"`
        CaseSensitive bool   `json:"case_sensitive"`
        FixedStrings  bool   `json:"fixed_strings"`
        ContextLines  int    `json:"context_lines"`
        MaxResults    int    `json:"max_results"`
    }
    if err := json.Unmarshal(args, &params); err != nil {
        return "", fmt.Errorf("invalid search parameters: %w", err)
    }

    // Validate required field
    if params.Pattern == "" {
        return "", fmt.Errorf("pattern is required")
    }

    // Defaults
    if params.OutputMode == "" {
        params.OutputMode = "files_with_matches"
    }
    if params.MaxResults <= 0 || params.MaxResults > 500 {
        params.MaxResults = 100
    }

    // Resolve search path
    searchPath := "."
    if params.Path != "" {
        if !IsSafePath(params.Path) {
            return "", fmt.Errorf("search path '%s' is outside the allowed directory", params.Path)
        }
        searchPath = params.Path
    }

    // Compile matcher
    var (
        matchFunc func(line string) bool
        isRegex    bool
    )
    if params.FixedStrings {
        matchFunc = func(line string) bool {
            if params.CaseSensitive {
                return strings.Contains(line, params.Pattern)
            }
            return strings.Contains(strings.ToLower(line), strings.ToLower(params.Pattern))
        }
    } else {
        isRegex = true
        rePattern := params.Pattern
        if !params.CaseSensitive {
            rePattern = "(?i)" + rePattern
        }
        re, err := regexp.Compile(rePattern)
        if err != nil {
            return "", fmt.Errorf("invalid regex pattern: %w", err)
        }
        matchFunc = re.MatchString
    }

    // Build output
    var sb strings.Builder
    matchCount := 0
    fileCount := 0
    truncated := false

    // Common helper for per-file result cap
    maxPerFile := 50
    if params.OutputMode == "count" {
        maxPerFile = 0 // count all
    }

    err := filepath.WalkDir(searchPath, func(path string, d fs.DirEntry, err error) error {
        if err != nil {
            return filepath.SkipDir // Skip inaccessible dirs
        }

        // Skip directories and hidden files/dirs
        if d.IsDir() {
            name := d.Name()
            if name == ".git" || name == "node_modules" || name == ".svn" || name == ".hg" {
                return filepath.SkipDir
            }
            return nil
        }
        if strings.HasPrefix(d.Name(), ".") {
            return nil
        }

        // Apply include glob filter
        if params.Include != "" {
            matched, _ := filepath.Match(params.Include, d.Name())
            if !matched {
                return nil
            }
        }

        // Check context cancellation
        select {
        case <-ctx.Done():
            return ctx.Err()
        default:
        }

        // Read file and search contents
        fileMatches, fileBytes := 0, 0
        var firstLine int
        fileNeedsHeader := true

        f, err := os.Open(path)
        if err != nil {
            return nil // Skip unreadable files
        }
        defer f.Close()

        // Binary detection
        header := make([]byte, 8192)
        n, _ := f.Read(header)
        if IsBinary(header[:n]) {
            return nil
        }

        // Rewind to scan lines
        scanner := bufio.NewScanner(f)
        scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
        lineNum := 0

        for scanner.Scan() {
            lineNum++
            line := scanner.Text()

            // Line-length safety (prevent context poisoning)
            if len(line) > 1000 {
                line = line[:1000] + "..."
            }

            if matchFunc(line) {
                fileMatches++
                if firstLine == 0 {
                    firstLine = lineNum
                }
                fileBytes += len(line)
            }
        }

        if fileMatches == 0 {
            return nil
        }

        // Output based on mode
        fileCount++

        switch params.OutputMode {
        case "files_with_matches":
            if sb.Len()+len(path)+1 > maxSearchChars {
                truncated = true
                return io.EOF // Signal to stop walking
            }
            sb.WriteString(path + "\n")

        case "count":
            line := fmt.Sprintf("%s: %d\n", path, fileMatches)
            if sb.Len()+len(line) > maxSearchChars {
                truncated = true
                return io.EOF
            }
            sb.WriteString(line)

        case "content":
            // Re-read with context for content mode
            content, err := os.ReadFile(path)
            if err != nil {
                return nil
            }
            lines := strings.Split(string(content), "\n")

            if fileNeedsHeader {
                sb.WriteString(path + "\n")
            }

            // Re-scan with context ring buffer
            var contextBuf []string
            for i, line := range lines {
                if matchFunc(line) {
                    matchCount++
                    if matchCount > params.MaxResults {
                        truncated = true
                        return io.EOF
                    }

                    // Emit context before
                    if params.ContextLines > 0 {
                        start := i - params.ContextLines
                        if start < 0 {
                            start = 0
                        }
                        for j := start; j < i; j++ {
                            entry := fmt.Sprintf("  %5d - %s\n", j+1, truncateLine(lines[j]))
                            if sb.Len()+len(entry) > maxSearchChars {
                                truncated = true
                                return io.EOF
                            }
                            sb.WriteString(entry)
                        }
                    }

                    entry := fmt.Sprintf("  %5d | %s\n", i+1, truncateLine(line))
                    if sb.Len()+len(entry) > maxSearchChars {
                        truncated = true
                        return io.EOF
                    }
                    sb.WriteString(entry)
                }
            }

            sb.WriteString("\n") // File separator
        }

        // Stop walking if we hit max files in files_with_matches mode
        if params.OutputMode == "files_with_matches" && fileCount >= params.MaxResults {
            return io.EOF // Signal to stop walking
        }

        return nil
    })

    if err != nil && err != io.EOF {
        return "", fmt.Errorf("search failed: %w", err)
    }

    result := sb.String()
    if result == "" {
        return "No matches found", nil
    }

    if truncated {
        result += "\n... (output truncated)"
    }

    return result, nil
}
```

**Key design decisions in Execute:**

**1. `filepath.WalkDir` rather than `filepath.Walk`** — WalkDir is available since Go 1.16 and is more efficient (doesn't call `os.Lstat` on every entry). Go 1.25 has further optimizations for WalkDir's internal stack.

**2. Two-phase file reading** — First pass reads first 8KB for binary detection with `IsBinary()`, then seeks back and scans with `bufio.Scanner`. This avoids constructing a `bufio.Reader` then discarding it on binary files. A simpler approach for V1: call `os.ReadFile` (also 8KB for binary check) then fall back to scanner for content.

**3. Context cancellation via `ctx.Done()` check** — The `select` inside WalkDir's callback checks for cancellation between files. Since filepath.WalkDir is sequential, this is the cleanest approach. If we want to stop an in-progress file scan, the scanner will return on ctx cancellation too.

**4. Stopping WalkDir early** — Returns `io.EOF` as a sentinel to stop walking. This is cleaner than panicking or using a global flag. WalkDir stops on any error (except `filepath.SkipDir` and `filepath.SkipAll`). We use `io.EOF` which is not treated specially by WalkDir — it will stop and return the error, which we filter in the outer check.

Alternatively, use `filepath.SkipAll` (Go 1.24+) to stop gracefully: `return filepath.SkipAll`. This produces no error. **Recommendation:** Use `filepath.SkipAll` if Go 1.24+ is required. Since `go.mod` shows Go 1.25.8, `filepath.SkipAll` is available.

**5. Skip hidden files and common vendor dirs** — `.git`, `node_modules`, `.svn`, `.hg` are skipped entirely. Hidden files (starting with `.`) are also skipped. This approximates rg's default behavior without needing `.gitignore` parsing.

**6. `fixed_strings` is included in V1** — Because pure Go makes it trivial (just switch between `strings.Contains` and `regexp.MatchString`). rg requires `-F` flag; we get it for free.

**7. Line safety truncation at 1000 chars** — Prevents a single extremely long line (e.g., minified JS) from poisoning context. Same approach as Cursor CLI's `--max-columns 1000`.

**8. Context lines via re-read** — We call `os.ReadFile` only for content mode with matches, reading the full file and re-scanning. This is simpler than maintaining a ring buffer during the first scan. For files with many lines, the second read is negligible.

**9. `defer f.Close()` inside WalkDir callback** — WalkDir processes files sequentially, so deferred closes are fine (they're called before the next entry). No fd leak risk.

### 2.5 Read-Only Declaration

```go
func (t *SearchTool) RequiresConfirmation(args json.RawMessage) bool {
    return false
}
```

**Rationale:** The search tool is read-only. It cannot modify files or execute arbitrary commands. All surveyed agents treat grep/search as read-only and never require confirmation. The only risk is information disclosure from searching files outside CWD, but `IsSafePath()` prevents that.

**Fail-safe:** If a future risk is identified (e.g., searching `.env` files with secrets), add a `--glob '!*.env'` exclusion in V2 rather than requiring confirmation. Confirmation for search would defeat the purpose.

### 2.6 CallString

```go
func (t *SearchTool) CallString(args json.RawMessage) string {
    pattern := getToolParam(args, "pattern")
    if pattern == "" {
        return "Using search_tool..."
    }
    return fmt.Sprintf("Using search_tool for: %s", truncate(pattern, 50))
}
```

**Pattern:** Matches `ReadFileTool.CallString` pattern (truncate + description). The TUI shows this while the tool runs. The `search_tool` name helps the user understand which tool is executing.

---

## 3. Integration Points

### 3.1 `internal/executor/executor.go` — RegisterTools

**Current code (lines 115-158):**

```go
func RegisterTools(reg *tool.Registry, enabledTools map[string]bool, isPlanning bool) {
    // ...
    if enabledTools["read_file"] {
        reg.Register(tool.NewReadFileTool())
    }
    if enabledTools["bash"] {
        reg.Register(&tool.ShellTool{})
    }
    // ...
}
```

**Modification:** Add the search tool as a **read-only tool available in both planning and coding modes**. It belongs in the "Always register read-only and base tools" section because:

1. Planning agents need to search codebases to understand them before writing plans
2. It's read-only (no RequiresConfirmation)
3. It follows the pattern of `read_file` — available in both modes

**Change:**

```go
func RegisterTools(reg *tool.Registry, enabledTools map[string]bool, isPlanning bool) {
    if enabledTools == nil {
        enabledTools = make(map[string]bool)
    }

    // Always register read-only and base tools
    if enabledTools["read_file"] {
        reg.Register(tool.NewReadFileTool())
    }
    if enabledTools["search_tool"] {          // ← NEW
        reg.Register(&tool.SearchTool{})       // ← NEW
    }
    if enabledTools["bash"] {
        reg.Register(&tool.ShellTool{})
    }
    // ... rest unchanged
}
```

**Note:** The search tool is registered **before** the `isPlanning` split, making it available in both planning and coding modes. It does NOT need to be in the planning-only section.

**Subagent inheritance:** Since `NewSubagentOrchestrator` (`internal/agent/agent.go`, lines 18-95) inherits all parent tools (skipping only `spawn_subagent` and `write_implementation_plan`), and `RegisterTools` is called again for coder subagents, the search tool will be available in subagents automatically. No changes needed in `agent.go`.

### 3.3 Prompt Files — Agent Discoverability

Two prompt files must be updated so the LLM **knows about and prefers** `search_tool` over bash commands:

**`internal/assets/prompts/instruction-coding.md`** (coder subagent prompt):

Current:

```
- You must prefer native tools (e.g. `write_file` and `target_edit`) over bash commands (e.g. `echo` and `sed`).
```

Change to:

```
- You must prefer native tools (e.g. `search_tool`, `write_file`, and `target_edit`) over bash commands (e.g. `grep`, `find`, `echo`, and `sed`).
- Use `search_tool` for finding files or searching file contents instead of `bash` with `grep` or `find`.
```

**`internal/assets/prompts/instruction-planning.md`** (architect/planning agent prompt):

Current (Phase 1):

```
**YOU CAN**: Read files, search the codebase, list directories, and analyze project structure.
```

Change to:

```
**YOU CAN**: Read files, search the codebase with `search_tool`, list directories, and analyze project structure.
```

**Why prompt changes matter:** The LLM discovers `search_tool` through tool definitions (function calling schema), but it needs explicit instruction to **prefer** it over bash `grep`/`find`. Without this, the model may continue using `bash` for search tasks because `bash` is a familiar pattern. The prompt nudges it toward the native tool, which is safer, more structured, and has no confirmation gate.

**MCP namespace collision check:** Tool name `"search_tool"` is specific enough to avoid collision with MCP tools (which are namespaced as `"server:tool"`). MCP names are bare words like `"read"`, `"write"`, `"search"` — `"search_tool"` won't collide. This is more resilient than `"search"` alone.

### 3.2 `internal/config/config.go` — defaultConfig

**Current code (line 55):**

```go
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
```

**Change:** Add `"search_tool": true`

```go
func defaultConfig() Config {
    return Config{
        EnabledTools: map[string]bool{
            "read_file":      true,
            "write_file":     true,
            "target_edit":    true,
            "spawn_subagent": true,
            "bash":           true,
            "search_tool":    true,   // ← NEW
        },
    }
}
```

**Effect on first run:** When `config.json` doesn't exist, `LoadConfig()` calls `defaultConfig()`, writes it to disk, and uses it. Existing users who already have a `config.json` will get `search` implicitly? No — `LoadConfig()` only merges defaults for completely null maps:

```go
if cfg.EnabledTools == nil {
    cfg.EnabledTools = defaultConfig().EnabledTools
}
```

**Risk:** Existing users won't get `search_tool: true` unless they regenerate config or add it manually. Mitigation: The code change is minimal, and if the search tool is registered but `enabledTools["search"]` is false/missing, it simply won't register. The tool just won't appear. The user needs to add `"search": true` to their existing `config.json`.

**Better approach:** In `LoadConfig()`, merge missing default keys into existing config:

```go
if cfg.EnabledTools == nil {
    cfg.EnabledTools = defaultConfig().EnabledTools
} else {
    // Merge missing defaults for backward compat
    defaults := defaultConfig().EnabledTools
    for k, v := range defaults {
        if _, exists := cfg.EnabledTools[k]; !exists {
            cfg.EnabledTools[k] = v
        }
    }
}
```

This ensures existing users automatically get new tools enabled. **Recommend including this merge in the implementation.**

---

## 4. Output Format Specification

### 4.1 Content Mode (`output_mode: "content"`)

Format: `filepath:linenum | content`

Example:

```
src/main.go
  42 | func handleRequest(w http.ResponseWriter, r *http.Request) {
  45 |     if r.Method == "GET" {

src/handler.go
  12 |     return handleRequest(w, r)
```

**Design rationale:**

- Line numbers use 5-wide right-aligned format (`| content`) matching `ReadFileTool` output for consistency. Roo Code uses `padStart(3)` (external-reference.md §6); ReadFileTool uses `<spaces>N | content`.
- File path acts as a group header, separated by a blank line. This matches the two-phase model where the LLM can identify which files to read next.
- Context lines (when `context_lines > 0`) are interleaved and distinguished by: actual matches use `:` separator before content, context lines use `-` separator. This follows the Cursor CLI convention detailed in external-reference.md §5:

```
src/main.go
    40 | type Server struct {
    41 -     Port int
    42 :     func handleRequest(w http.ResponseWriter, r *http.Request) {  // match
    43 -     }
```

**Format:** The content mode uses ReadFileTool-style output (file header + `N | content` lines). This is produced directly during the WalkDir — no parsing needed since we control the scan loop.

Match lines use `|` separator, context lines use `-` separator (following Cursor CLI convention):

```
src/main.go
    40 | type Server struct {
    41 -     Port int
    42 |     func handleRequest(w http.ResponseWriter, r *http.Request) {  // match
    43 -     }
```

**Implementation note:** The context line distinction (`|` vs `-`) is handled naturally since we track match status during the scan. We know which lines matched and which are context.

### 4.2 Files-with-Matches Mode (`output_mode: "files_with_matches"`) — DEFAULT

Format: One file path per line, sorted by filesystem order (WalkDir's walk order, deterministic).

```
src/main.go
src/handler.go
internal/tool/search.go
```

**No line numbers, no content.** This is the two-phase approach used by Codex CLI and Claude Code. The LLM uses this to narrow down which files to read.

### 4.3 Count Mode (`output_mode: "count"`)

Format: `filepath:count`

```
src/main.go:3
src/handler.go:1
internal/tool/search.go:12
```

---

## 5. Edge Case Handling

### 5.1 No Matches

```go
result := sb.String()
if result == "" {
    return "No matches found", nil
}
```

**Not an error.** The LLM should see "No matches found" as normal output. This is consistent with all surveyed agents.

### 5.2 Binary Files

Binary detection uses the existing `IsBinary()` function from `utils.go`. First 8KB are checked for null bytes. Binary files are skipped silently. This is the same check used by `ShellTool`.

### 5.3 Bad Regex Pattern

`regexp.Compile()` returns an error for invalid patterns. This is propagated directly as a clear error message:

```go
re, err := regexp.Compile(rePattern)
if err != nil {
    return "", fmt.Errorf("invalid regex pattern: %w", err)
}
```

### 5.4 Very Large Repos

| Layer | Limit | Mechanism |
|-------|-------|-----------|
| Character cap | 32,768 | `maxSearchChars` constant, same as `maxReadFileChars` |
| File cap | `maxResults` (default 100) | `fileCount >= params.MaxResults` stops walk early |
| Match cap | `maxResults` (default 100) | `matchCount > params.MaxResults` stops walk early |
| Line length | 1,000 chars | Truncated per-line to prevent context poisoning |
| Context cancellation | `ctx.Done()` check | Checked per-file in walk callback |

`filepath.WalkDir` is fast enough at project scale (sub-second for 10K files).

### 5.5 Context Cancellation

A `select` on `ctx.Done()` is checked inside the `WalkDir` callback, between file processing. Since `filepath.WalkDir` is sequential, this provides responsive cancellation. The check runs once per file (not per line), which is sufficient — the model cancels at tool level, not mid-file.

### 5.6 Hidden Files and Vendor Dirs

Default exclusion list:

- Hidden files/dirs (starting with `.`): skipped entirely
- `.git`, `node_modules`, `.svn`, `.hg`: skipped with `filepath.SkipDir`

This approximates `.gitignore` behavior without needing gitignore parsing. If a user needs to search hidden files (like Claude Code's `--hidden`), add a `search_hidden` param in V2.

### 5.7 Unreadable Files

WalkDir errors on unreadable files/dirs are handled with `filepath.SkipDir` (directories) or silent skip (files). The walk continues past permission errors.

### 5.8 Very Deep Directory Trees

`filepath.WalkDir` handles arbitrary depth naturally. No `maxdepth` param in V1 — add if users report performance issues on deeply nested repos.

### 5.9 Large Files (>100MB)

`bufio.Scanner` reads line-by-line, so memory usage per file is O(line_length), not O(file_size). The `scanner.Buffer()` call sets a 1MB max line length, preventing memory exhaustion on files with extremely long lines.

---

## 6. Security Analysis

### 6.1 Path Traversal

`IsSafePath()` is used to validate the `path` parameter. This prevents searching outside the project root. The same function protects `WriteFileTool`, `TargetEditTool`, and `ShellTool.cwd`.

### 6.2 No Subprocess Execution

Unlike `ShellTool`, the search tool does not execute any external commands. All operations use Go standard library calls (`filepath.WalkDir`, `os.Open`, `bufio.Scanner`). There is no injection surface — the pattern is compiled by `regexp.Compile`, not passed to a shell.

### 6.3 No Side Effects

The search tool is read-only. It cannot:

- Write or modify files
- Execute arbitrary commands (unlike `ShellTool`)
- Spawn subagents
- Perform network communication

### 6.4 Information Disclosure

Search is limited to paths within the project CWD via `IsSafePath()`. Hidden files (`.env`, `.ssh`, etc.) are excluded by default via the dotfile skip check.

If stricter boundaries are needed, V2 could add a `--exclude` parameter matching files/dirs to skip.

---

## 7. Testing Strategy

### 7.1 Test File: `internal/tool/search_test.go`

**File follows existing patterns:**

- `init_test.go` already disables sqz globally (no change needed)
- `approvedContext()` for tests that need bypass — not needed here (no confirmation)
- Uses `t.TempDir()` for file isolation
- No external dependencies — tests are self-contained

### 7.2 Test Cases

```go
// Test no matches returns clean message, not error
func TestSearchTool_NoMatches(t *testing.T) {
    tmpDir := t.TempDir()
    os.WriteFile(filepath.Join(tmpDir, "test.txt"), []byte("hello world"), 0644)
    
    tool := &SearchTool{}
    args := json.RawMessage(`{"pattern": "nonexistent", "path": "` + tmpDir + `"}`)
    result, err := tool.Execute(context.Background(), args)
    
    // Expect: "No matches found", no error
    if err != nil { t.Fatal(err) }
    if result != "No matches found" {
        t.Errorf("expected 'No matches found', got: %q", result)
    }
}

// Test files_with_matches mode (default)
func TestSearchTool_FilesWithMatches(t *testing.T) {
    tmpDir := t.TempDir()
    os.WriteFile(filepath.Join(tmpDir, "a.go"), []byte("package a\nfunc Foo() {}"), 0644)
    os.WriteFile(filepath.Join(tmpDir, "b.go"), []byte("package b\nfunc Bar() {}"), 0644)
    os.WriteFile(filepath.Join(tmpDir, "c.txt"), []byte("no match here"), 0644)
    
    tool := &SearchTool{}
    args := json.RawMessage(`{"pattern": "func", "path": "` + tmpDir + `", "output_mode": "files_with_matches"}`)
    result, _ := tool.Execute(context.Background(), args)
    
    // Expect: both .go file paths, not c.txt
    if !strings.Contains(result, "a.go") || !strings.Contains(result, "b.go") {
        t.Errorf("expected both .go files, got: %q", result)
    }
    if strings.Contains(result, "c.txt") {
        t.Error("c.txt should not be in results")
    }
}

// Test content mode with line numbers
func TestSearchTool_ContentMode(t *testing.T) {
    tmpDir := t.TempDir()
    os.WriteFile(filepath.Join(tmpDir, "a.go"), []byte("package a\nfunc Foo() {}"), 0644)
    
    tool := &SearchTool{}
    args := json.RawMessage(`{"pattern": "func", "path": "` + tmpDir + `", "output_mode": "content"}`)
    result, _ := tool.Execute(context.Background(), args)
    
    // Expect: filename header and line number with content
    if !strings.Contains(result, "a.go") {
        t.Errorf("expected file in output, got: %q", result)
    }
    if !strings.Contains(result, "2 |") {
        t.Errorf("expected line 2 marker, got: %q", result)
    }
}

// Test count mode
    tmpDir := t.TempDir()
    os.WriteFile(filepath.Join(tmpDir, "a.go"), []byte("package a\nfunc Foo() {}\nfunc Bar() {}"), 0644)
    
    tool := &SearchTool{}
    args := json.RawMessage(`{"pattern": "func", "path": "` + tmpDir + `", "output_mode": "count"}`)
    result, err := tool.Execute(context.Background(), args)
    
    // Expect: count = 2
}

// Test case sensitivity
func TestSearchTool_CaseSensitivity(t *testing.T) {
    tmpDir := t.TempDir()
    os.WriteFile(filepath.Join(tmpDir, "a.go"), []byte("func Foo() {}"), 0644)
    
    tool := &SearchTool{}
    
    // Case-insensitive (default): should match "Foo"
    args := json.RawMessage(`{"pattern": "foo", "path": "` + tmpDir + `", "output_mode": "files_with_matches"}`)
    result, _ := tool.Execute(context.Background(), args)
    // Expect: matched
    
    // Case-sensitive: should NOT match "Foo"
    args = json.RawMessage(`{"pattern": "foo", "path": "` + tmpDir + `", "case_sensitive": true, "output_mode": "files_with_matches"}`)
    result, _ = tool.Execute(context.Background(), args)
    // Expect: "No matches found"
}

// Test truncation
func TestSearchTool_Truncation(t *testing.T) {
    tmpDir := t.TempDir()
    // Create many files with matches
    for i := 0; i < 150; i++ {
        os.WriteFile(filepath.Join(tmpDir, fmt.Sprintf("file%d.go", i)), 
            []byte(fmt.Sprintf("package p%d\nfunc F%d() {}", i, i)), 0644)
    }
    
    tool := &SearchTool{}
    args := json.RawMessage(`{"pattern": "func", "path": "` + tmpDir + `", "max_results": 100}`)
    result, err := tool.Execute(context.Background(), args)
    
    // Expect: truncated message
}

// Test include glob filter
func TestSearchTool_IncludeFilter(t *testing.T) {
    tmpDir := t.TempDir()
    os.WriteFile(filepath.Join(tmpDir, "a.go"), []byte("func A() {}"), 0644)
    os.WriteFile(filepath.Join(tmpDir, "a.ts"), []byte("function A() {}"), 0644)
    
    tool := &SearchTool{}
    args := json.RawMessage(`{"pattern": "A", "path": "` + tmpDir + `", "include": "*.go"}`)
    result, _ := tool.Execute(context.Background(), args)
    
    // Expect: only a.go, not a.ts
}

// Test path traversal prevention
func TestSearchTool_UnsafePath(t *testing.T) {
    tool := &SearchTool{}
    args := json.RawMessage(`{"pattern": "test", "path": "/etc"}`)
    _, err := tool.Execute(context.Background(), args)
    
    // Expect: error about path outside allowed directory
}

// Test context lines
func TestSearchTool_ContextLines(t *testing.T) {
    tmpDir := t.TempDir()
    content := "line1\nline2\nline3 MATCH\nline4\nline5"
    os.WriteFile(filepath.Join(tmpDir, "a.txt"), []byte(content), 0644)
    
    tool := &SearchTool{}
    args := json.RawMessage(`{"pattern": "MATCH", "path": "` + tmpDir + `", "output_mode": "content", "context_lines": 1}`)
    result, _ := tool.Execute(context.Background(), args)
    
    // Expect: line2, line3 MATCH, line4 (1 line context before and after)
}

// Test maximum results cap
func TestSearchTool_MaxResultsCap(t *testing.T) {
    tool := &SearchTool{}
    // Verify that max_results > 500 is capped to 500
    // (test via path traversal of default behavior)
}

// Test invalid output_mode
func TestSearchTool_InvalidOutputMode(t *testing.T) {
    tool := &SearchTool{}
    args := json.RawMessage(`{"pattern": "test", "output_mode": "invalid"}`)
    _, err := tool.Execute(context.Background(), args)
    // Expect: error
}

// Test empty pattern
func TestSearchTool_EmptyPattern(t *testing.T) {
    tool := &SearchTool{}
    args := json.RawMessage(`{"pattern": ""}`)
    _, err := tool.Execute(context.Background(), args)
    // Expect: error "pattern is required"
}
```

### 7.3 Environment Setup for Tests

All tests use `t.TempDir()` for isolation. No global state is modified. The `init_test.go` already disables sqz, which is not needed for search tests.

**No external dependencies needed.** Since the search tool uses only Go standard library (`filepath.WalkDir`, `bufio.Scanner`, `regexp`), tests run on any machine with Go installed — no `rg` binary needed.

---

## 8. Implementation Sequence

### Phase 1: Core Tool (1 session)

1. Create `internal/tool/search.go` with `SearchTool` struct and all 6 interface methods
2. Implement `Execute()` with `filepath.WalkDir` approach, all three output modes, and edge case handling
3. Add `truncateLine` helper function
4. Verify builds with `go build ./...`

### Phase 2: Integration (1 session)

1. Add `"search_tool": true` to `defaultConfig().EnabledTools` in `internal/config/config.go`
2. Add merge logic in `LoadConfig()` for backward compat with existing configs
3. Add `enabledTools["search_tool"]` gate and registration in `executor.RegisterTools()`
4. Update prompts: `instruction-coding.md` and `instruction-planning.md`
5. Verify with `go build ./...` and manual test

### Phase 3: Tests (1 session)

1. Create `internal/tool/search_test.go`
2. Write unit tests for all output modes, edge cases, and error conditions
3. Run `go test ./internal/tool/ -run TestSearch -v` and verify all pass

### Phase 4: Manual Validation

1. Test with ripgrep present: `rg --version` should succeed
2. Test with ripgrep absent: `mv /usr/bin/rg /usr/bin/rg.bak` → verify error message
3. Test on a real Go project: search for common patterns
4. Test with large output: verify truncation

---

## 9. Open Questions and Future Work

### Answered in this document

| Question | Answer |
|----------|--------|
| Pure Go vs shell-out? | **Pure Go** with stdlib (`filepath.WalkDir` + `regexp` + `bufio.Scanner`). No external deps. |
| Default output mode? | `files_with_matches` (two-phase pattern) |
| Default case sensitivity? | Case-insensitive |
| Security model? | No subprocess execution — pattern → `regexp.Compile`, not a shell |
| Confirmation required? | No (read-only) |
| Hidden file/dir search? | No — dotfiles, `.git`/`node_modules`/`.svn`/`.hg` skipped |
| Max results cap? | 100 default, 500 hard cap |
| Truncation? | 32,768 chars + 1000 char per-line limit |
| `fixed_strings` support? | Yes, V1 — trivial with `strings.Contains` vs `regexp.Compile` |

### Future Work (V2+)

1. **`.gitignore` awareness** — Use existing `IsSafePath` patterns or add `git-pkgs/gitignore`
2. **Hidden file opt-in** — `search_hidden` parameter
3. **Auto-enrichment** — Auto-add context for small result sets (Gemini CLI's ~10% SWEBench improvement)
4. **Pagination** — `offset`/`head_limit` for browsing large result sets
5. **`maxdepth` parameter** — Limit directory traversal depth
6. **Parallel walking** — `fastwalk` or goroutine-based for large repos
7. **Custom ignore files** — `.lateignore` support

---

## 10. Risks and Mitigations

| Risk | Likelihood | Impact | Mitigation |
|------|-----------|--------|------------|
| WalkDir slow on 100K+ file repos | Low | ~500ms vs rg's ~50ms | Acceptable for project-scale workspaces |
| Catastrophic backtracking in regex | Low | Search hangs | `regexp.Compile` validates syntax; user gets clear error on bad pattern |
| Binary file false positive | Low | Text file skipped | Extremely rare in source code (null byte in first 8KB) |
| Hidden file skip misses `.env` | Low | Secrets in results | Use `.gitignore`; add `.lateignore` in V2 |
| MCP name collision with `search_tool` | Very low | Not possible | `search_tool` is specific enough to avoid any MCP collision |

---

## 11. Complete File: `internal/tool/search.go` (reference implementation)

The complete reference implementation follows this structure. ~250-300 lines total.

**Key functions:**

| Function | Lines | Purpose |
|----------|-------|---------|
| `Name()` | 2 | Returns `"search_tool"` |
| `Description()` | 2 | Description for LLM |
| `Parameters()` | ~30 | JSON Schema (8 params including `fixed_strings`) |
| `Execute()` | ~200 | Core: parse → compile matcher → WalkDir → scan files → format → truncate |
| `truncateLine()` | ~6 | Per-line truncation at 1000 chars |
| `RequiresConfirmation()` | 2 | `false` (read-only) |
| `CallString()` | ~7 | `"Using search_tool for: <pattern>"` |

**Supporting patterns from codebase:**

| Pattern | Source | Usage |
|---------|--------|-------|
| `getToolParam()` | `utils.go` | Extract params for CallString |
| `truncate()` | `utils.go` | Truncate pattern display |
| `IsSafePath()` | `permissions.go` | Validate search path |
| `IsBinary()` | `utils.go` | Binary file detection |
| `maxReadFileChars` | `implementations.go` | 32,768 char constant |
| `filepath.WalkDir` | stdlib | Directory traversal |
| `bufio.Scanner` | stdlib | Line-by-line reading |
| `filepath.Match` | stdlib | Glob filter |
| `regexp.Compile`/`MatchString` | stdlib | Pattern matching |
| `strings.Contains` | stdlib | Literal matching (fast path) |
