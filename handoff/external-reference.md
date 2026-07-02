# External Reference: Native Search/Grep Tools in Coding Agents

> **Purpose:** Structured reference for how existing coding agents implement native file-content search/grep tools. Each entry covers parameters, output modes, truncation, sorting, context handling, and design philosophy.

---

## Table of Contents

1. [Claude Code (TypeScript)](#1-claude-code-typescript)
2. [Codex CLI (Rust)](#2-codex-cli-rust)
3. [OpenCode (TypeScript/Bun)](#3-opencode-typescriptbun)
4. [Gemini CLI (TypeScript)](#4-gemini-cli-typescript)
5. [Cursor CLI (TypeScript)](#5-cursor-cli-typescript)
6. [Roo Code (VS Code)](#6-roo-code-vs-code)
7. [Pi (TypeScript)](#7-pi-typescript)
8. [Go Libraries for Pure-Go Search](#8-go-libraries-for-pure-go-search)
9. [Go Shell-Out Approach](#9-go-shell-out-approach)
10. [Comparison Table](#10-comparison-table)
11. [Key Design Patterns](#11-key-design-patterns)

---

## 1. Claude Code (TypeScript)

**Source:** Extracted from minified 12MB JS bundle; documented at [Search Tools - Claude Code](https://sanbuphy-claude-code-source-code.mintlify.app/reference/tools/search-tools) and [antonoly/claude-code-anymodel](https://github.com/antonoly/claude-code-anymodel/blob/main/tools/GrepTool/GrepTool.ts). Comprehensive analysis at [How coding agents search code](https://wasnotwas.com/writing/grep-across-agents/).

### Parameters

| Parameter | Type | Description |
|-----------|------|-------------|
| `pattern` | string (required) | Regex pattern to search |
| `path` | string (optional) | Directory to search (default: cwd) |
| `glob` / `include` | string (optional) | File glob pattern filter |
| `type` | string (optional) | rg `--type` filter (e.g. `"go"`, `"py"`) — more efficient than glob |
| `output_mode` | enum (optional) | `files_with_matches` (default), `content`, or `count` |
| `context` (aliases: `-C`, `c`) | number (optional) | Lines before+after each match |
| `-A` / `-B` (aliases: `a`, `b`) | number (optional) | Lines after / before separately |
| `case_sensitive` | boolean | Respect case (default: false → `-i`) |
| `multiline` | boolean | `-U --multiline-dotall` for cross-line patterns |
| `head_limit` | number (optional) | Max result entries (`\| head -N`) |
| `offset` | number (optional) | Pagination offset (`\| tail -n +N`) |
| `isConcurrencySafe` | boolean | Always true — safe for parallel execution |

### Output Modes

1. **`files_with_matches`** (default) — file paths only via `rg -l`
2. **`content`** — matching lines (with line numbers, context if requested)
3. **`count`** — match counts per file via `rg -c`

### Ripgrep Invocation (reverse-engineered)

```text
rg --hidden --max-columns 500 [--glob !.git --glob !.svn ...]
   [-U --multiline-dotall] [-i] [-l | -c] [-n] [-C N] [--regexp <pattern>] <path>
```

- **`--hidden` is always set** — searches hidden files by default. Most tools skip them.
- **`--max-columns 500`** — enforced at ripgrep level, not in post-processing.
- VCS dirs (`.git`, `.svn`, `.hg`, `.bzr`, `.jj`) are explicitly excluded via `--glob !<dir>`.

### Truncation

- **20,000 character** maximum result size.
- `head_limit` and `offset` provide pagination through large result sets.
- `--max-columns 500` truncates long lines at the rg level.

### Design Notes

- Default `files_with_matches` assumes a **two-phase workflow**: grep narrows the candidate set, then `read` loads specific files.
- `type` parameter maps to `rg --type`, which is faster and more accurate than glob patterns for standard file types.
- Input aliases (`c` → `-C`, `a` → `-A`, `include` → `glob`, `regex` → `pattern`) help natural language map correctly.
- Ships as part of a 12MB minified bundle; source not directly browsable.

---

## 2. Codex CLI (Rust)

**Source:** [openai/codex](https://github.com/openai/codex), specifically [`codex-rs/core/src/tools/handlers/grep_files.rs`](https://github.com/openai/codex/blob/9950b5e265dbf94ae8b605c8ceee714875637e9d/codex-rs/core/src/tools/handlers/grep_files.rs). Analysis at [How coding agents search code](https://wasnotwas.com/writing/grep-across-agents/).

### Parameters

| Parameter | Type | Description |
|-----------|------|-------------|
| `pattern` | string (required) | Regex pattern |
| `include` | string (optional) | Glob pattern for file filtering |
| `path` | string (optional) | Directory path |
| `limit` | number (optional) | Max file results (default: 100, max: 2000) |

### Output

- **`files_with_matches` only** — returns file paths, no match content, no line numbers, no context.
- Sorted by file modification time (most recent first).

### Ripgrep Invocation

```text
rg --files-with-matches --sortr=modified --regexp <pattern> <path>
```

### Design Philosophy

- **Deliberately two-phase:** grep narrows the candidate set; `read_file` loads actual content.
- `read_file` has an indentation-aware mode: takes an anchor line + `max_levels`, expands outward through the indentation tree. Pairs surgically with the surgical file finder.
- Sorting by `mtime` means files edited most recently appear first — highly relevant in active coding sessions.
- Minimal parameter surface keeps the model focused.

### Truncation

- `limit` defaults to 100 files, max 2000.
- No content truncation needed since no content is returned.

### Side Channel

Codex also has a BM25-based side search for fuzzy/semantic file discovery (`file-search` module using the `nucleo` crate).

---

## 3. OpenCode (TypeScript/Bun)

**Source:** [sst/opencode](https://github.com/sst/opencode/blob/c7b35342/packages/opencode/src/tool/grep.ts), also [anomalyco/opencode](https://github.com/anomalyco/opencode/blob/2a33addd/packages/opencode/src/tool/grep.ts). Ripgrep wrapper at [`packages/opencode/src/file/ripgrep.ts`](https://github.com/sst/opencode/blob/c7b35342/packages/opencode/src/file/ripgrep.ts).

### Parameters

| Parameter | Type | Description |
|-----------|------|-------------|
| `pattern` | string (required) | Regex pattern |
| `path` | string (optional) | Directory to search |
| `include` | string (optional) | Glob pattern (e.g. `"*.js"`, `"*.{ts,tsx}"`) |

### Output

- Returns **match content** (not just filenames), with line numbers.
- Format: `filepath|line:content` (pipe `|` separator for unambiguous parsing without JSON).
- Sort order: results grouped by file, files sorted by **mtime** (post-stat).

### Ripgrep Invocation

```text
rg -nH --field-match-separator=| <pattern> [--glob <include>] <path>
```

### Internal Detail

A separate `Ripgrep.search()` method exists and uses `--json` for full structured output, but the **grep tool exposed to the model** uses plain text output — a deliberate simplification.

### Truncation

- **100-match cap** hard limit.
- **2000-character line truncation** — lines longer than 2000 chars are cut.
- No context lines.

### Design Notes

- Deliberately minimal — intentionally less featureful than the internal API.
- OpenCode's fork [KiloCode](https://github.com/Kilo-Org/kilocode) uses an identical grep tool (byte-for-byte copy) but adds worktree isolation for multi-agent parallelism.

---

## 4. Gemini CLI (TypeScript)

**Source:** [google-gemini/gemini-cli](https://github.com/google-gemini/gemini-cli), [`packages/core/src/tools/grep.ts`](https://github.com/google-gemini/gemini-cli/blob/e8bc7bea447936d8cef6e9a7ed7138379ca89892/packages/core/src/tools/grep.ts) and [`packages/core/src/tools/grep-utils.ts`](https://github.com/google-gemini/gemini-cli/blob/e8bc7bea447936d8cef6e9a7ed7138379ca89892/packages/core/src/tools/grep-utils.ts). Docs at [geminicli.com](https://geminicli.com/docs/tools/file-system/).

### Parameters

| Parameter | Type | Description |
|-----------|------|-------------|
| `pattern` | string (required) | Regex pattern |
| `path` | string (optional) | Directory path |
| `include_pattern` | string (optional) | Glob file filter |
| `exclude_pattern` | string (optional) | Glob exclusion |
| `context` | number (optional) | Lines before+after each match |
| `before` | number (optional) | Lines before |
| `after` | number (optional) | Lines after |
| `case_sensitive` | boolean (optional) | Respect case |
| `fixed_strings` | boolean (optional) | Literal string mode |
| `no_ignore` | boolean (optional) | Ignore `.gitignore` |
| `names_only` | boolean (optional) | File paths only |
| `max_matches_per_file` | number (optional) | Per-file cap |
| `total_max_matches` | number (optional) | Total cap |

### Three-Tier Fallback Strategy

1. **ripgrep** (preferred) — if not installed, downloads at runtime via `@joshua.litt/get-ripgrep`.
2. **System `grep` binary** — fallback if rg unavailable.
3. **Pure JavaScript fallback** — readline-based implementation if even `grep` is missing.

This is the **only agent** that treats rg as a preference rather than a hard dependency.

### Auto-Enrichment

When ripgrep returns a **small** number of matches **and** the model didn't request explicit context:

- **1 match** → automatically re-reads file with **50 lines** of context.
- **2–3 matches** → automatically re-reads file with **15 lines** of context.
- **>3 matches** → no auto-enrichment.

This optimization reduces SWEBench turn count by **~10%** (noted in source comment).

### Implementation

- Uses `rg --json` throughout, streaming and parsing structured output line-by-line.
- Groups matches by file, sorts by line number.
- Lines truncated to `MAX_LINE_LENGTH_TEXT_FILE` with `... [truncated]` suffix.
- Context lines distinguished from actual matches via separator character (`:` vs `-`).

### Design Notes

- The **most configurable** grep tool in the field — exposes the most parameters to the model.
- Parameter names use `snake_case` (e.g. `include_pattern`, `names_only`), unusual among agents that mostly use camelCase.
- Uses `AbortSignal` for timeout control.

---

## 5. Cursor CLI (TypeScript)

**Source:** Minified webpacked `index.js` in Cursor's bundled Node application. Analysis at [How coding agents search code](https://wasnotwas.com/writing/grep-across-agents/).

### Parameters

| Parameter | Type | Description |
|-----------|------|-------------|
| `pattern` | string (required) | Regex pattern |
| `path` | string (optional) | Directory path |
| `output_mode` | enum (optional) | `content` **(default)**, `files_with_matches`, or `count` |
| `glob` | string (optional) | File glob pattern (uses `--iglob`) |
| `type` | string (optional) | `rg --type` filter |
| `context` | number (optional) | Lines before+after |
| `context_before` | number (optional) | Lines before |
| `context_after` | number (optional) | Lines after |
| `case_insensitive` | boolean (optional) | Case insensitive (default: true?) |
| `multiline` | boolean (optional) | `--multiline --multiline-dotall` |
| `sort` | enum (optional) | Sort field (default: `"modified"`) |
| `sort_ascending` | boolean (optional) | Sort direction |

### Ripgrep Invocation

```text
rg --line-number --with-filename --no-heading -0
   --max-columns 1000 --max-columns-preview
   [--ignore-case | --case-sensitive]
   [--type <type>] [--iglob <glob>]
   [--multiline --multiline-dotall]
   [--sortr modified | --sort <field>]
   --no-config --color=never --hidden
   --regexp <pattern>
   [--context-ignore <*.cursorignore>]
```

- **`--hidden` always enabled** — searches hidden files by default.
- **`--no-config`** — ensures deterministic behavior (ignores host `~/.ripgreprc`).
- **`--color=never`** — prevents ANSI escape codes in output.

### `.cursorignore` Is a First-Class Boundary

- Cursor pushes `.cursorignore` patterns into rg via `--cursor-ignore` (custom flag?).
- Also post-filters results through its own ignore service.
- **Two-layer enforcement**: rg-level + agent-level. More serious than most implementations.

### Three-Layer Truncation

| Layer | Value | Mechanism |
|-------|-------|-----------|
| Timeout | **25 seconds** | `MAIN_TIMEOUT_MS` |
| Hard line cutoff | **10,000 lines** | `HARD_MAX_OUTPUT_LINES` — kills rg process |
| Client return budget | **2,000 lines** | `CLIENT_LIMIT_LINES` |
| Buffer cap | **8 MB** | stdout/stderr combined |

Counts output lines while streaming stdout; kills rg once it crosses the limit.

### Structured Content Parsing (without `--json`)

- Uses null-delimited output (`-0`) and parses results into per-file match groups.
- Distinguishes actual matches from context lines using separator character:

  ```text
  line:content   → actual match
  line-content   → context line
  ```

  Parsed with regex: `/^(\d+)([:-])(.*)$/`

### Bundled rg Binary

- Prefers colocated `rg` binary bundled with the application.
- Falls back to system PATH lookup.
- No download-on-demand — Gemini CLI's approach is the exception.

### Indexed Grep Hook (Architecture, Not Active)

The executor supports an `executeIndexedGrep` provider hook, but the local CLI provider leaves it `undefined`. The abstraction exists for future richer indexed retrieval (vector/BM25), but the shipped build still uses plain ripgrep.

### Design Notes

- **Content-first by default** (`output_mode: "content"`) — assumes the model wants to inspect matches immediately, not narrow first.
- Claude Code defaults to `files_with_matches`; Cursor defaults to `content`. This single design choice changes the interaction rhythm fundamentally.
- Most complete process-control implementation (timeout + line caps + buffer cap + bundled binary + deterministic config).

---

## 6. Roo Code (VS Code)

**Source:** [RooVetGit/Roo-Code](https://github.com/RooVetGit/Roo-Code), [`src/core/tools/SearchFilesTool.ts`](https://github.com/RooVetGit/Roo-Code/blob/0e56afc76413a3539bedcab1631e2c01ebc76875/src/core/tools/SearchFilesTool.ts) and [`src/services/ripgrep/`](https://github.com/RooVetGit/Roo-Code/tree/0e56afc76413a3539bedcab1631e2c01ebc76875/src/services/ripgrep). Fork of [Cline](https://github.com/cline/cline). Docs at [roocode.com](https://docs.roocode.com/advanced-usage/available-tools/search-files).

### Ripgrep Invocation

```text
rg --json -e <regex> --glob <pattern> --context 1 --no-messages <path>
```

### Key Characteristics

| Aspect | Value |
|--------|-------|
| Context lines | **1 line before and after** — hardcoded, model has no control |
| Match cap | **300 results** |
| Per-line cap | **500 characters** per line |
| Output format | Pipe-bar style with `padStart(3, " ")` line numbers |
| Group separator | `----` between match groups |
| Contiguous merging | Adjacent matches within 1 line → single block |
| Ignore file | `.rooignore` via `RooIgnoreController` |
| rg binary | VS Code's built-in ripgrep (privileged extension position) |

### Output Format Example

```
# src/utils/parser.ts
  42 | function parseInput(raw: string) {
  43 | const result = parseJSON(raw);  // match
  44 | return result;
----
```

### Design Notes

- Takes advantage of VS Code's privileged position — uses the editor's built-in rg binary.
- `.rooignore` provides user-controlled access boundaries, similar to Cursor's `.cursorignore`.
- Contiguous block merging prevents redundant separators when matches are close together.
- Fixed context line count (1) is the least flexible of any agent surveyed.

---

## 7. Pi (TypeScript)

**Source:** [badlogic/pi-mono](https://github.com/badlogic/pi-mono/blob/a74b18ca5aac4155fcf5b6e4bb529c5d9a98fa91/packages/coding-agent/src/core/tools/grep.ts).

### Parameters

```typescript
const grepSchema = Type.Object({
  pattern: string,            // required — regex or literal
  path:      string?,          // directory to search (default: cwd)
  glob:      string?,          // file glob pattern
  ignoreCase: boolean?,        // case-insensitive (default: false)
  literal:   boolean?,         // treat pattern as literal string
  context:   number?,          // lines before+after each match (default: 0)
  limit:     number?,          // max matches (default: 100)
});
```

## GrepOperations Interface (Plugin Architecture)

The cleanest plugin architecture in the field:

```typescript
export interface GrepOperations {
  isDirectory: (absolutePath: string) => Promise<boolean> | boolean;
  readFile: (absolutePath: string) => Promise<string> | string;
}
```

- Default implementation uses local `fs` (`fsStat`, `fsReadFile`).
- Override to delegate search to **remote systems** (SSH, containers) without reimplementing the tool.
- `GrepToolOptions` accepts custom `operations`.

No other agent abstracts this interface.

### Dual Truncation

Pi applies **two independent caps**:

1. **100-match count limit** (`DEFAULT_LIMIT`) — stops rg early by killing the process.
2. **50 KB size limit** (`DEFAULT_MAX_BYTES`) — truncates output to 50KB.

Three separate notices inform the model which limit(s) fired:

```
[Truncated: 100 matches limit, 50KB limit, some lines truncated]
```

### Asymmetric Truncation (Notable)

- When truncating **bash output**: keeps the **end** (errors are at the bottom).
- When truncating **file content**: keeps the **beginning** (structure and declarations at the top).
- Explicit about this distinction, unlike other agents.

### Ripgrep Invocation

```text
rg --json --line-number --color=never --hidden
   [--ignore-case] [--fixed-strings] [--glob <glob>]
   -- <pattern> <path>
```

- Uses `--json` mode throughout.
- `--hidden` always set.
- Context lines use `filepath-lineNumber- content` (dash separator), actual matches use `:`.

### Tool Set Segmentation

- **Read-only mode:** `[read, grep, find, ls]`
- **Coding mode:** `[read, bash, edit, write]` — grep is excluded because if you can run bash, you don't need a wrapped grep. Similar logic to OpenHands, but implemented as explicit tool set segmentation.

### Design Notes

- Uses `typebox` for schema (not Zod, unlike most other agents).
- Has `ensureTool("rg")` calls ripgrep with a download-on-demand mechanism.
- Per-file line caching via `fileCache` Map to avoid re-reading files.
- Self-adjusting limit feedback: `"Use limit=N for more, or refine pattern"`.

---

## 8. Go Libraries for Pure-Go Search

### 8a. boyter/gocodewalker

**Source:** [github.com/boyter/gocodewalker](https://github.com/boyter/gocodewalker) — 95 stars, Go.

A library for walking code directories in Go that respects `.gitignore` and `.ignore` files, including nested ones.

**Key features:**

- Default: respects both `.gitignore` and `.ignore` files (configurable).
- Nested `.gitignore` support — accurate per-directory ignore rules.
- Configurable file skipping by regex, extension, or general match.
- Uses `readdir` for directory listing.
- Binary detection to skip non-text files.
- Filter pipeline architecture for composable exclusions.
- Parallel worker support for concurrent walking.

**Use case:** Building a pure-Go grep tool without shelling out to ripgrep. Walk the file tree respecting ignores, then search each file.

### 8b. git-pkgs/gitignore

**Source:** [github.com/git-pkgs/gitignore](https://github.com/git-pkgs/gitignore) — Go library.

A Go library for matching paths against gitignore rules.

**Key features:**

- **Wildmatch engine:** Two-pointer backtracking implementation modeled on git's own `wildmatch.c` — same algorithm, same behavior.
- **Tested against git's wildmatch test suite** — git-level compatibility.
- Bracket expressions with ranges, negation, backslash escapes, and all 12 POSIX character classes (`[:alnum:]`, `[:alpha:]`, etc.).
- Proper `**` glob support (recursive wildcard).
- `NewFromDirectory()` for loading patterns from nested `.gitignore` files.
- Pattern sources: `.gitignore`, `.git/info/exclude`, global excludes file.

**Why it matters:** Most Go gitignore parsers compile patterns to regexes or use `filepath.Match`, which diverges from git's behavior. This library matches git's own wildmatch algorithm directly.

### 8c. charlievieth/fastwalk

**Source:** [github.com/charlievieth/fastwalk](https://github.com/charlievieth/fastwalk) — used by fzf (64K GitHub stars).

A fast parallel directory traversal library for Go.

**Performance (vs `filepath.WalkDir`):**

| Platform | Speedup | Memory | Allocations |
|----------|---------|--------|-------------|
| macOS    | ~2.5×   | -50%   | -25%        |
| Linux    | ~4×     | -50%   | -25%        |
| Windows  | ~6×     | -50%   | -25%        |

Also ~4–5× faster than `godirwalk` across all platforms.

**Key features:**

- Multiple goroutines stat the filesystem concurrently.
- Callback-based (`WalkDirFunc`), same API as `filepath.WalkDir`.
- Configurable concurrency.
- Safe symbolic link traversal.
- Based on `golang.org/x/tools/internal/fastwalk` (originally from `goimports`).

**Why it matters:** For pure-Go grep, file tree walking is the bottleneck. `fastwalk` is the fastest option available and battle-tested in fzf.

---

## 9. Go Shell-Out Approach

**Source:** [ghuntley/how-to-build-a-coding-agent](https://github.com/ghuntley/how-to-build-a-coding-agent) — workshop repository, [`code_search_tool.go`](https://github.com/ghuntley/how-to-build-a-coding-agent/blob/trunk/code_search_tool.go). Analysis at [ghuntley.com/agent/](https://ghuntley.com/agent/).

### CodeSearchInput

```go
type CodeSearchInput struct {
    Pattern       string `json:"pattern"`
    Path          string `json:"path,omitempty"`
    FileType      string `json:"file_type,omitempty"`
    CaseSensitive bool   `json:"case_sensitive,omitempty"`
}
```

### Ripgrep via `os/exec`

```go
func CodeSearch(input json.RawMessage) (string, error) {
    // ... unmarshal input ...
    args := []string{"rg", "--line-number", "--with-filename", "--color=never"}
    if !codeSearchInput.CaseSensitive {
        args = append(args, "--ignore-case")
    }
    if codeSearchInput.FileType != "" {
        args = append(args, "--type", codeSearchInput.FileType)
    }
    args = append(args, codeSearchInput.Pattern)
    if codeSearchInput.Path != "" {
        args = append(args, codeSearchInput.Path)
    } else {
        args = append(args, ".")
    }

    cmd := exec.Command(args[0], args[1:]...)
    output, err := cmd.Output()

    // Graceful handling of exit code 1 (no matches)
    if err != nil {
        if exitError, ok := err.(*exec.ExitError); ok && exitError.ExitCode() == 1 {
            return "No matches found", nil
        }
        return "", fmt.Errorf("search failed: %w", err)
    }
    // ...
}
```

### Key Pattern: Graceful Exit Code 1 Handling

ripgrep exits with code 1 when no matches are found. This is **not an error** for the agent — it should return `"No matches found"` rather than propagating the exit code as a failure.

```go
if exitError, ok := err.(*exec.ExitError); ok && exitError.ExitCode() == 1 {
    return "No matches found", nil
}
```

### Truncation

Simple line-based truncation — caps at 50 lines:

```go
if len(lines) > 50 {
    result = strings.Join(lines[:50], "\n") +
        fmt.Sprintf("\n... (showing first 50 of %d matches)", len(lines))
}
```

### Design Notes

- Minimal viable implementation: `os/exec.Command("rg", args...)` with `cmd.Output()`.
- No `--json` mode — uses plain text output.
- No context lines.
- No mtime sorting.
- The workshop is deliberately educational — patterns here establish the baseline that more sophisticated agents build on.

---

## 10. Comparison Table

### Search Behavior

| Tool | Output Mode (Default) | Context Lines | Auto-Enrich | Multiline | Hidden Files | Sort |
|------|----------------------|---------------|-------------|-----------|--------------|------|
| **Claude Code** | files/lines/count (files) | Configurable | ✗ | ✓ | ✓ (default) | None |
| **Codex CLI** | filenames only | None | ✗ | ✗ | ✗ | mtime |
| **OpenCode** | lines | None | ✗ | ✗ | ✗ | mtime (post-stat) |
| **Gemini CLI** | lines+ctx | Configurable | ✓ 50/15 lines | ✗ | ✗ | None |
| **Cursor CLI** | lines/files/count (lines) | Configurable | ✗ | ✓ | ✓ (default) | mtime (default) |
| **Roo Code** | lines+ctx | 1 line (fixed) | ✗ | ✗ | ✗ | None |
| **Pi** | lines | Configurable | ✗ | ✗ | ✓ (default) | None |
| **ghuntley/Go** | lines | None | ✗ | ✗ | ✗ | None |

### Implementation Details

| Tool | rg Mode | rg Availability | Match Cap | Size Cap | Context | Pagination |
|------|---------|----------------|-----------|----------|---------|------------|
| **Claude Code** | Both | Bundled | 20K chars | 20K chars | Configurable | head_limit + offset |
| **Codex CLI** | `--files-with-matches` | System | 100/2000 files | N/A | None | limit param |
| **OpenCode** | Text | System | 100 matches | 2000 chars/line | None | No |
| **Gemini CLI** | `--json` | 3-tier fallback | Configurable | Line truncation | Configurable + auto | total_max_matches |
| **Cursor CLI** | Structured text | Bundled | 2K/10K lines | 8MB buffer | Configurable | No (kills process) |
| **Roo Code** | `--json` | VS Code built-in | 300 matches | 500 chars/line | 1 line (fixed) | No |
| **Pi** | `--json` | System (download) | 100 matches | 50KB | Configurable | limit param |
| **ghuntley/Go** | Text | System | 50 lines (ad-hoc) | None | None | No |

### Special Features

| Tool | Unique Feature |
|------|---------------|
| **Claude Code** | `type` parameter for language-specific search, input aliases, `--hidden` always on |
| **Codex CLI** | mtime-sorted two-phase design, paired indentation-aware `read_file` |
| **OpenCode** | Pipe-delimited plain text format, internal `--json` not exposed to model |
| **Gemini CLI** | Auto-enrichment (reduces SWEBench turns ~10%), three-tier fallback, most params |
| **Cursor CLI** | Three-layer truncation with process kill, `.cursorignore` two-layer enforcement, bundled rg |
| **Roo Code** | Contiguous block merging, `.rooignore`, VS Code's privileged rg access |
| **Pi** | `GrepOperations` plugin interface, dual truncation, asymmetric truncation, tool set segmentation |
| **ghuntley/Go** | Exit code 1 handled as `"No matches found"` not error, pure Go shell-out pattern |

---

## 11. Key Design Patterns

### Pattern 1: Two-Phase vs Content-First

- **Two-phase** (Codex, Claude Code default): Return filenames first, let model read specific files. Assumes search is a narrowing step.
- **Content-first** (Cursor default, OpenCode, Gemini CLI): Return matching lines immediately. Assumes first results should be immediately interpretable.
- Neither is "right" — both change the interaction rhythm.

### Pattern 2: Truncation Strategy

| Strategy | Used By |
|----------|---------|
| Match count cap only | OpenCode (100), Codex (100/2000), Roo Code (300) |
| Size cap only | Claude Code (20K chars) |
| Dual cap (count + size) | Pi (100 matches + 50KB) |
| Multi-layer (timeout + lines + buffer) | Cursor CLI (25s/10K/2K/8MB) |
| Line-based ad-hoc | ghuntley (50 lines) |
| Configurable caps | Gemini CLI |

### Pattern 3: Context Enrichment

- **None** — most agents return exactly what the model asked for.
- **Fixed** — Roo Code (1 line fixed, hardcoded).
- **Configurable** — Claude Code, Gemini CLI, Cursor CLI, Pi.
- **Auto-enrich** — Gemini CLI only (50 lines for 1 match, 15 for 2-3 matches). Reduces SWEBench turns ~10%.

### Pattern 4: rg Availability

| Approach | Examples |
|----------|----------|
| **System rg assumed** | Codex CLI, OpenCode, Pi (Pi downloads if missing) |
| **Bundled binary** | Claude Code, Cursor CLI |
| **IDE built-in** | Roo Code (VS Code) |
| **Three-tier fallback** | Gemini CLI (rg → system grep → JS) |

### Pattern 5: Ignore File Handling

- **Default rg behavior** — most agents let rg handle `.gitignore` naturally.
- **`.cursorignore` as first-class boundary** — Cursor (two-layer enforcement).
- **`.rooignore`** — Roo Code (`RooIgnoreController`).
- **`--hidden` always on** — Claude Code, Cursor CLI, Pi (searches hidden files by default).
- **Explicit VCS exclusion** — Claude Code (`--glob !.git`, etc.).

### Pattern 6: Plugin/Abstraction Architecture

| Approach | Example | Details |
|----------|---------|---------|
| **Operations interface** | Pi | `GrepOperations { isDirectory, readFile }` — local/SSH/container |
| **Provider hook** | Cursor CLI | `executeIndexedGrep` hook (not active in shipped build) |
| **No abstraction** | Most agents | Hardcoded local filesystem calls |

### Pattern 7: rg Output Format

| Format | Used By | Pros |
|--------|---------|------|
| `--files-with-matches` | Codex CLI | Minimal, fast |
| Plain text with pipe separator | OpenCode | Unambiguous without JSON overhead |
| `--json` structured | Gemini CLI, Pi, Roo Code | Parseable, typed, streamable |
| Null-delimited structured text | Cursor CLI | Good balance of parseability and speed |
| Both plain text and json | Claude Code | Mode-dependent |

---

## Sources

### Kept

| Source | Why It Matters |
|--------|---------------|
| [How coding agents search code](https://wasnotwas.com/writing/grep-across-agents/) | Comprehensive analysis of 9 agents' grep implementations with source-level detail |
| [antonoly/claude-code-anymodel GrepTool.ts](https://github.com/antonoly/claude-code-anymodel/blob/main/tools/GrepTool/GrepTool.ts) | Extracted Claude Code grep source with parameter mapping |
| [Search Tools - Claude Code docs](https://sanbuphy-claude-code-source-code.mintlify.app/reference/tools/search-tools) | Official Claude Code search tool documentation |
| [openai/codex grep_files.rs](https://github.com/openai/codex/blob/9950b5e265dbf94ae8b605c8ceee714875637e9d/codex-rs/core/src/tools/handlers/grep_files.rs) | Codex CLI source — filenames-only design |
| [sst/opencode grep.ts](https://github.com/sst/opencode/blob/c7b35342/packages/opencode/src/tool/grep.ts) | OpenCode grep tool source |
| [google-gemini/gemini-cli grep.ts & grep-utils.ts](https://github.com/google-gemini/gemini-cli/blob/e8bc7bea447936d8cef6e9a7ed7138379ca89892/packages/core/src/tools/grep.ts) | Gemini CLI grep with auto-enrichment and three-tier fallback |
| [Gemini CLI file system tools docs](https://geminicli.com/docs/tools/file-system/) | Official Gemini CLI tool parameter docs |
| [earendil-works/pi-mono grep.ts](https://github.com/earendil-works/pi-mono/blob/main/packages/coding-agent/src/core/tools/grep.ts) | Pi grep source — GrepOperations interface, dual truncation |
| [Roo Code SearchFilesTool.ts](https://github.com/RooVetGit/Roo-Code/blob/0e56afc76413a3539bedcab1631e2c01ebc76875/src/core/tools/SearchFilesTool.ts) | Roo Code grep source |
| [Roo Code search_files docs](https://docs.roocode.com/advanced-usage/available-tools/search-files) | Official Roo Code tool docs |
| [boyter/gocodewalker](https://github.com/boyter/gocodewalker) | Go library for gitignore-aware file walking |
| [git-pkgs/gitignore](https://github.com/git-pkgs/gitignore) | Go library with wildmatch-based gitignore parsing tested against git's test suite |
| [charlievieth/fastwalk](https://github.com/charlievieth/fastwalk) | Fast parallel directory traversal for Go (2.5-6× faster) |
| [ghuntley/how-to-build-a-coding-agent code_search_tool.go](https://github.com/ghuntley/how-to-build-a-coding-agent/blob/trunk/code_search_tool.go) | Go shell-out pattern with exit code 1 handling |
| [ghuntley.com/agent/](https://ghuntley.com/agent/) | Workshop article explaining agent tool primitives |
| [Claude Code Grep/Glob fact report](https://gist.github.com/acmerfight/e870baa4c51cd85881a242bfff597998) | Details on native builds and Grep/Glob tool availability |

### Dropped

| Source | Why Dropped |
|--------|-------------|
| [continuedev/continue grepSearch.ts](https://github.com/continuedev/continue/blob/d220a2e3702994bc1a6e0a4daed84da67cb1277e/core/tools/implementations/grepSearch.ts) | Continue's implementation — not in task scope (task covers Claude Code, Codex CLI, OpenCode, Gemini CLI, Cursor CLI, Roo Code, Pi) |
| [Cursor forum rg hang reports](https://forum.cursor.com/t/agent-shell-hangs-on-rg-invocations-without-an-explicit-path-stdin-not-a-tty/160984) | Bug report, not implementation reference |
| [Code search for AI agents: three tools](https://zzet.org/gortex/grep-replacement-for-ai-agents/) | Opinion piece on search architecture, not specific to agent grep implementations |
| [Why Coding Agents Still Use grep](https://yage.ai/share/why-coding-agents-still-use-grep-en-20260327.html) | General analysis, not source-level implementation details |
| [Roo-Code PR #1824](https://github.com/RooVetGit/Roo-Code/pull/1824) | Context-mention search, not grep tool |
| [OpenCode PR #7501](https://github.com/anomalyco/opencode/pull/7501) | Symlink follow, minor parameter change |
| [Codex PR #9939](https://github.com/openai/codex/pull/9939) | File search perf improvement, not grep tool |
| [pi-search extension](https://github.com/buddingnewinsights/pi-search/) | Third-party extension, not core grep tool |
| [AFT plugin for Pi](https://github.com/cortexkit/aft) | Third-party replacement, not stock Pi grep |

---

## Gaps

1. **Claude Code source is obfuscated** — the 12MB minified bundle makes exact parameter extraction difficult. The `antonoly/claude-code-anymodel` repo provides a reverse-engineered view but may not be fully accurate.
2. **Cursor CLI source is minified** — implementation details extracted from webpacked JS may miss edge cases.
3. **No cross-agent benchmark data** — no comparative performance data (latency, token cost, accuracy) across agents for identical search tasks.
4. **Roo Code's contiguous block merging** — exact merge distance threshold not confirmed from source.
5. **Cursor's `--cursor-ignore` flag** — unclear whether this is a custom rg flag or handled via rg's `--ignore-file`.
6. **All agents surveyed use ripgrep** — no agents surveyed use a pure-Go or pure-JS search engine for production; the Go libraries section documents building blocks, not a deployed system.

### Suggested Next Steps

- Run a benchmark suite comparing truncation behavior, mtime sorting quality, and context enrichment effectiveness across the agents listed.
- Investigate whether Cursor's `--cursor-ignore` is custom patched rg or handled via standard rg flags.
- Profile auto-enrichment effectiveness (Gemini CLI claims ~10% SWEBench turn reduction; validate independently).
- Consider AST-based search (ast-grep, tree-sitter) and graph search (LSP) as complementary modalities — grep alone handles lexical search but not structural or semantic queries.

---

## Acceptance Report

```acceptance-report
{
  "criteriaSatisfied": [
    {
      "id": "criterion-1",
      "status": "satisfied",
      "evidence": "Wrote comprehensive external reference document to /mnt/storage/Projects/late/handoff/external-reference.md covering all 9 specified topics (Claude Code, Codex CLI, OpenCode, Gemini CLI, Cursor CLI, Roo Code, Pi, Go libraries, Go shell-out) with sections for each, a comparison table, design patterns analysis, source citations, and gap analysis."
    }
  ],
  "changedFiles": [
    "/mnt/storage/Projects/late/handoff/external-reference.md"
  ],
  "testsAddedOrUpdated": [],
  "commandsRun": [
    {
      "command": "web_search (8 queries across 4 batches)",
      "result": "passed",
      "summary": "Researched all 9 tool categories across 4 search batches covering Claude Code, Codex CLI, OpenCode, Gemini CLI, Cursor CLI, Roo Code, Pi, Go libraries, and Go shell-out approach"
    },
    {
      "command": "fetch_content on 5 URLs",
      "result": "passed",
      "summary": "Fetched full content from wasnotwas article, Pi grep source, ghuntley code_search_tool.go, and Gemini CLI grep-utils.ts for comprehensive detail extraction"
    }
  ],
  "validationOutput": [
    {
      "check": "All 9 topics covered",
      "result": "passed",
      "detail": "Document has sections for Claude Code (§1), Codex CLI (§2), OpenCode (§3), Gemini CLI (§4), Cursor CLI (§5), Roo Code (§6), Pi (§7), Go libraries (§8a-c), Go shell-out (§9)"
    },
    {
      "check": "Comparison table present",
      "result": "passed",
      "detail": "Two comparison tables in §10 covering search behavior and implementation details across all agents"
    },
    {
      "check": "Sources cited",
      "result": "passed",
      "detail": "Kept sources table (16 entries) and dropped sources table (9 entries) with explanations"
    },
    {
      "check": "Gap analysis present",
      "result": "passed",
      "detail": "§Gaps lists 6 identified gaps plus 4 suggested next steps"
    }
  ],
  "residualRisks": [
    "Claude Code and Cursor CLI sources are minified/obfuscated — some parameters or behavior may differ from documented extraction",
    "Roo Code contiguous block merging threshold not confirmed from direct source inspection",
    "Cursor's --cursor-ignore implementation unclear (custom rg flag or standard rg --ignore-file)"
  ],
  "noStagedFiles": true,
  "notes": "Research conducted via web searches and direct source fetches. The wasnotwas.com article provided the most comprehensive cross-agent comparison. Key sources were fetched for Pi, Gemini CLI, and ghuntley to extract exact code. Document written to /mnt/storage/Projects/late/handoff/external-reference.md as specified."
}
```
