# Local Context: late CLI Tool System

## 1. Tool Interface Contract

**File:** `internal/common/interfaces.go` (lines 6-15) + `internal/common/tools.go` (lines 6-30)

Every tool implements the `common.Tool` interface:

```go
type Tool interface {
    Name() string
    Description() string
    Parameters() json.RawMessage // JSON Schema for the LLM
    Execute(ctx context.Context, args json.RawMessage) (string, error)
    RequiresConfirmation(args json.RawMessage) bool
    CallString(args json.RawMessage) string
}
```

**Key contract points:**

- `Execute` returns a result **string** on success or an **error** on unrecoverable failure. The caller converts errors to strings as `"Error executing tool %s: %v"` — tools should not format errors this way themselves.
- `RequiresConfirmation` is advisory: the actual confirmation gate for ShellTool is enforced **fail-closed** in `Execute()` itself (via `common.ToolApprovalKey` context value) and also in `executor.ExecuteToolCalls()` when no middlewares are present.
- `Parameters` returns a JSON Schema object defining the tool's input schema. This is sent directly to the LLM as a tool definition.
- `CallString` returns a concise human-readable summary shown in the TUI while the tool executes.

**Type aliases** in `internal/tool/tool.go`:

```go
type Tool = common.Tool
type Registry = common.ToolRegistry
func NewRegistry() *Registry { return common.NewToolRegistry() }
```

**ToolRegistry** (`internal/common/tools.go`, lines 32-63):

```go
type ToolRegistry struct { tools map[string]Tool }

func NewToolRegistry() *ToolRegistry
func (r *ToolRegistry) Register(t Tool)       // stores with t.Name() as key
func (r *ToolRegistry) Get(name string) Tool  // returns nil if not found
func (r *ToolRegistry) All() []Tool            // returns sorted by Name()
```

Tool names **must be unique** — registration uses `Name()` as the map key.

---

## 2. Registration Flow

### Entry point: `cmd/late/main.go` (lines 170-229)

The registration sequence is:

1. **MCP tools** registered first: `mcpClient.GetTools()` iterates MCP adapters, checks `enabledTools` (namespaced name first, then fallback to bare name), registers each into `sess.Registry`.

2. **Built-in tools** registered via `executor.RegisterTools(sess.Registry, enabledTools, isPlanning)`:
   - For planning agents (`isPlanning=true`): only `read_file`, `bash`, and `write_implementation_plan`
   - For coding agents (`isPlanning=false`): also `write_file`, `target_edit`
   - Skills: `activate_skill` tool is registered if any skills are discovered

3. **Spawn subagent tool** registered last: `sess.Registry.Register(tool.SpawnSubagentTool{Runner: ...})`

### `executor.RegisterTools()` (`internal/executor/executor.go`, lines 115-158)

```go
func RegisterTools(reg *tool.Registry, enabledTools map[string]bool, isPlanning bool) {
    if enabledTools == nil { enabledTools = make(map[string]bool) }
    if enabledTools["read_file"] { reg.Register(tool.NewReadFileTool()) }
    if enabledTools["bash"] { reg.Register(&tool.ShellTool{}) }
    if isPlanning {
        reg.Register(tool.WriteImplementationPlanTool{})
    } else {
        if enabledTools["write_file"] { reg.Register(tool.WriteFileTool{}) }
        if enabledTools["target_edit"] { reg.Register(tool.NewTargetEditTool()) }
    }
    // Skills discovery and activate_skill registration...
}
```

**`enabledTools` gating:** Each tool is registered only if `enabledTools[name]` is `true`. The map comes from `config.json`'s `enabled_tools` field, with flag overrides (e.g., `--enable-bash=false` sets `enabledTools["bash"] = false`).

**Planning vs Coding split:** Planning agents get read-only tools plus `write_implementation_plan`. Coding agents get the full set including writers. The `SpawnSubagentTool` (registered separately) uses `agent_type: "coder"` to trigger coding-mode registration for subagents.

### Tool execution chain (`internal/executor/executor.go`, lines 48-103)

```go
func ExecuteToolCalls(ctx context.Context, sess *session.Session, toolCalls []client.ToolCall, middlewares []common.ToolMiddleware) error {
    baseRunner := func(ctx context.Context, tc client.ToolCall) (string, error) {
        t := sess.Registry.Get(tc.Function.Name)
        if t == nil { return "Error: tool '...' not found", nil }
        return sess.ExecuteTool(ctx, tc)
    }
    // Wrap with middlewares (reverse order so first middleware is outermost)
    runner := baseRunner
    for i := len(middlewares) - 1; i >= 0; i-- {
        runner = middlewares[i](common.ToolRunner(runner))
    }
    // ...
}
```

**Fail-closed for no middlewares:** If `len(middlewares) == 0`, shell commands are blocked entirely — the code checks `if _, ok := t.(*tool.ShellTool); ok` and returns a hard error.

---

## 3. Config Default Values and Adding a New Tool

### `internal/config/config.go`

**`defaultConfig()`** (line 55):

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

When no `config.json` exists, the system creates one with these defaults.

**To add a new tool to the enabled set:**

1. Add the tool's name to the `defaultConfig().EnabledTools` map
2. Add an `enabledTools["my_tool"]` gate in `executor.RegisterTools()`
3. Register the tool instance there (with or without the gate at your discretion)
4. The `enabled_tools` config section auto-populates on first run from defaults

---

## 4. Existing Tool Patterns

### Pattern A: I/O Tool (ReadFileTool / WriteFileTool / TargetEditTool)

**ReadFileTool** (`internal/tool/implementations.go`, lines 23-103):

- Struct holds state (`LastReads map[string]ReadState`)
- Constructor `NewReadFileTool()` initializes state
- `RequiresConfirmation` returns `false` (read-only)
- `CallString` trims CWD prefix from path for display
- `Execute` reads file, handles partial range, truncates at `maxReadFileChars` (32768)
- Line-numbered output format: `"N | content\n"`

**WriteFileTool** (`internal/tool/implementations.go`, lines 105-156):

- Empty struct (stateless)
- `RequiresConfirmation` returns `true` if path is outside CWD (`!IsSafePath(path)`)
- Error if content is empty
- Writes with `0644` permissions

**TargetEditTool** (`internal/tool/targetEdit.go`):

- Search-and-replace editing: reads file, normalizes line endings, checks uniqueness of search block, replaces and writes back
- `RequiresConfirmation` = `true` if file outside CWD
- Line-ending normalization with `detectLineEnding()`, `normalizeToUnix()`, `restoreLineEnding()`

### Pattern B: Minimal Tool (SpawnSubagentTool)

**SpawnSubagentTool** (`internal/tool/subagent.go`):

- Uses a function field `Runner SubagentRunner` to inject the actual implementation
- `RequiresConfirmation` returns `false` always
- `Parameters` uses inline `json.RawMessage` literal
- Runtime nil-guard: `if t.Runner == nil { return "", fmt.Errorf(...) }`

### Pattern C: ShellTool with Security

**ShellTool** (`internal/tool/implementations.go`, lines 157-446):

- `Execute` path: validate command → enforce approval (`ToolApprovalKey` context check) → validate/expand CWD → execute via `newShellCommand()` → compress via sqz if available → detect binary → truncate by chars (32768) then lines (1024) → format exit code
- `RequiresConfirmation` delegates to AST analyzer: blocked commands return early; whitelisted commands (grep, find, ls, etc.) auto-approve; others prompt
- **Fail-closed in Execute:** checks `ctx.Value(common.ToolApprovalKey)` — if not present/false, returns error
- **Fail-closed in executor:** when no middlewares present, ShellTool is blocked entirely

### Pattern D: Skill Tools (ScriptTool / ActivateSkillTool)

**ScriptTool** (`internal/tool/skill_tool.go`):

- Dynamically named: `skill_{skillname}_{scriptname}`
- Parameter-free: only accepts `{"args": [...]}`
- `RequiresConfirmation` always `true`
- Extension-based runner selection (`.py` → python3, `.js` → node, default → executable)

**ActivateSkillTool**:

- Reads skill scripts directory and registers each as a `ScriptTool`
- Injects skill instructions into response
- `RequiresConfirmation` returns `false`

### Pattern E: MCP Tools (ToolAdapter)

**ToolAdapter** (`internal/mcp/client.go`):

- Adapts `*mcp.Tool` to `tool.Tool`
- Namespaced naming: `"{server}:{tool}"` to prevent collisions
- `RequiresConfirmation` always `true`
- Output truncated at 32768 Unicode characters (by rune count, not byte)
- `BareName()` method for backward compat with pre-namespace configs

---

## 5. Security Model

### Path Safety: `IsSafePath()` (`internal/tool/permissions.go`, lines 100-157)

```go
func IsSafePath(path string) bool
```

- Relative paths without `..` are always safe
- Absolute paths (or `..`-containing paths) are resolved to absolute, symlink-resolved, and checked for prefix-match against CWD
- Symlink escape prevention: resolves symlinks by walking up from path until an existing directory is found, applying `filepath.EvalSymlinks`

### Write confirmation logic

| Tool | RequiresConfirmation |
|------|---------------------|
| ReadFileTool | `false` (always) |
| WriteFileTool | `!IsSafePath(path)` |
| TargetEditTool | `!IsSafePath(path)` |
| ShellTool | Delegates to AST analyzer |
| SpawnSubagentTool | `false` (always) |
| WriteImplementationPlanTool | `false` (always) |
| ScriptTool | `true` (always) |
| ActivateSkillTool | `false` (always) |
| MCP ToolAdapter | `true` (always) |

### Shell command approval chain

1. **AST analyzer** (`ast_bridge.go` → `ast` package): parses command, decides if blocked/needs-confirmation/auto-approve
2. **Whitelisted commands** (grep, find, ls, cat, head, tail, wc, echo, pwd, whoami, date, file) and their flags auto-approve
3. **`mkdir`/`New-Item` for new paths** auto-approve by AST (unsupervised mode exception: `HasRiskOnly(ir, ast.ReasonNewPath)`)
4. **Blocked:** commands with shell metacharacters (`$(...)`, backticks, process substitution, redirection), cd commands
5. **Fail-closed:** any parse error → `NeedsConfirmation: true`
6. **Final gate:** `Execute()` checks `ToolApprovalKey` context value

### Allow-list system (`internal/tool/permissions.go`)

Three scopes:

- **Session** (30min TTL): `SaveSessionAllowedCommand()` / `SaveSessionAllowedTool()`
- **Project-local** (30d TTL): `.late/allowed_commands.json` / `.late/allowed_tools.json`
- **Global** (30d TTL): `~/.config/late/allowed_commands.json` / `allowed_tools.json`

---

## 6. Output Limits and Truncation

Constants in `internal/tool/implementations.go`:

```go
const maxReadFileChars = 32768   // ReadFileTool output (chars)
const maxBashOutputChars = 32768 // ShellTool output (chars)
const maxBashOutputLines = 1024  // ShellTool output (lines, applied after char limit)
```

**ReadFileTool truncation (line ~85):** checks `sb.Len()+len(lineStr) > maxReadFileChars` during output building. Appends `"... (output truncated)"`.

**ShellTool truncation (lines ~330-350):** truncates by characters first (UTF-8 safe since `len()` on Go strings is byte count), then splits by lines and truncates to 1024 lines. Appends `"... (output truncated)"`.

**MCP ToolAdapter truncation (`mcp/client.go`):** slices by rune (Unicode-safe) at 32768 chars, appends `"[... truncated, output exceeded limit ...]"`.

**Binary detection (`internal/tool/utils.go`, `IsBinary()`):** checks first 8KB for null bytes.

**sqz compression (`internal/tool/utils.go`):** optional, disabled by default in tests (`init_test.go`), enabled via `--enable-sqz` flag or `SetSqzEnabled()`.

---

## 7. TUI Confirmation Middleware Flow

**`TUIConfirmMiddleware`** (`internal/tui/interactions.go`, lines 77-170):

1. Bypasses if `SkipConfirmationKey` is set in context (except Windows bash)
2. Checks if tool is in global/project-local allow-list → auto-approve
3. Checks `t.RequiresConfirmation()` → auto-approve if false
4. For ShellTool, checks if command is blocked (cd, etc.) → hard error
5. Sends `ConfirmRequestMsg` to TUI (channels `ResultCh`/`ErrCh`)
6. Handles responses:
   - `y` → approve once
   - `s`/`S` → approve for session (30min)
   - `p`/`P` → approve for project (30d)
   - `g`/`G` → approve globally (30d)
   - `n` → cancel
7. Sets `ToolApprovalKey = true` in context for approved executions

Middleware wiring in `main.go` (line ~206):

```go
rootAgent.SetMiddlewares([]common.ToolMiddleware{
    tui.TUIConfirmMiddleware(p, sess.Registry),
})
```

---

## 8. Subagent Tool Inheritance

**`NewSubagentOrchestrator`** (`internal/agent/agent.go`, lines 18-95):

```go
// Inherit all tools from parent (including MCP tools)
if parent != nil && parent.Registry() != nil {
    for _, t := range parent.Registry().All() {
        name := t.Name()
        if name == "spawn_subagent" || name == "write_implementation_plan" {
            continue
        }
        sess.Registry.Register(t)
    }
}
// Always ensure coder subagents have the full toolset
if agentType == "coder" {
    executor.RegisterTools(sess.Registry, enabledTools, false)
}
```

Key points:

- **Inherits all parent tools** by iterating `parent.Registry().All()` — this includes MCP tools
- **Skips** `spawn_subagent` (prevent recursion) and `write_implementation_plan` (planning-only)
- **Coder subagents** get fresh `RegisterTools(..., false)` for full coding set
- If `messenger` is available, subagent gets its own confirmation middleware instance
- Context files are read and injected as initial user message
- Subagents use `session.New(..., ephemeral=true)` — no persistent history

---

## 9. Testing Patterns

### Test setup (`internal/tool/init_test.go`)

```go
func init() {
    isSqzAvailable = func() bool { return false }
}
```

Sqz is globally disabled for all tests to prevent interference.

### Approved context helper

```go
func approvedContext() context.Context {
    return context.WithValue(context.Background(), common.ToolApprovalKey, true)
}
```

Used by all ShellTool tests to bypass the fail-closed approval gate.

### Test patterns observed (`internal/tool/implementations_test.go`)

1. **Temp directory setup:** `t.TempDir()` for isolated filesystem tests
2. **Cross-platform guards:** `if runtime.GOOS == "windows" { t.Skip(...) }`
3. **JSON params as raw literals:** `json.RawMessage('{"command": "echo hello"}')`
4. **CallString tests:** structured table-driven with expected output
5. **RequiresConfirmation tests:** extensive map of cases covering whitelisted, non-whitelisted, bypass attempts
6. **Output limit tests:** verify truncation with `maxReadFileChars` and `maxBashOutputChars`
7. **Negative tests:** binary output detection, unsafe CWD rejection

---

## 10. AST Analyzer and Command Whitelisting

### `ast_bridge.go` — Whitelisted commands

**Unix whitelist** (auto-approve without prompt):

```
cat, date, echo, file, find, grep, head, ls, pwd, tail, wc, whoami
```

Each has a map of allowed flags (e.g., `grep: {-i, -v, -l, -n, -r, -R, -E, -F, -w, -x, -c}`).

**Windows whitelist:** `cat, date, dir, echo, gc, gci, get-childitem, get-content, get-date, get-location, ls, measure-object, pwd, select-string, sls, type, whoami, write-host, write-output`

**`astAnalyzer.Analyze()`** (`ast_bridge.go`, lines 99-127):

- Parses command with `ast.NewParser(platform, cwd).Parse(command)`
- Runs `policy.Decide(ir)` for blocked/needs-confirmation verdict
- Unsupervised mode exception: if only risk is `ReasonNewPath` (mkdir with new path), auto-approve

### `analyzer.go` — Interfaces

```go
type CommandAnalysis struct {
    IsBlocked         bool
    BlockReason       error
    NeedsConfirmation bool
}

type CommandAnalyzer interface {
    Analyze(command string) CommandAnalysis
}
```

---

## 11. Registration Checklist (for adding a new tool)

1. **Implement `common.Tool` interface** in `internal/tool/`
2. **Add constructor** function (e.g., `NewMyTool()`) if stateful
3. **Decide `RequiresConfirmation`** logic:
   - `false` for read-only tools
   - `!IsSafePath(path)` for write tools
   - `true` for all MCP tools
4. **Add to `executor.RegisterTools()`** in `internal/executor/executor.go`
5. **Add `enabledTools` gate** if the tool should be configurable (recommended)
6. **Add default `true`** in `config.defaultConfig().EnabledTools`
7. **If planning-only:** add inside `if isPlanning { ... }` block
8. **If subagent-safe:** no changes needed unless exclusion from subagents is wanted
9. **Add tests** following patterns in `implementations_test.go`
10. **If MCP-sourced:** adapt through `ToolAdapter` — no code changes needed, just server config

---

## File Reference Summary

| File | Key Contents |
|------|-------------|
| `internal/common/interfaces.go` | `Tool` interface, `Orchestrator`, events, context keys |
| `internal/common/tools.go` | `ToolRegistry` (Register/Get/All), `ToolRunner`, `ToolMiddleware` |
| `internal/tool/tool.go` | Type aliases: `Tool`, `Registry`, `NewRegistry()` |
| `internal/tool/implementations.go` | ReadFileTool, WriteFileTool, ShellTool, WriteImplementationPlanTool — all concrete implementations |
| `internal/tool/targetEdit.go` | TargetEditTool — search-and-replace editor |
| `internal/tool/subagent.go` | SpawnSubagentTool with `SubagentRunner` dependency injection |
| `internal/tool/skill_tool.go` | ScriptTool (dynamic), ActivateSkillTool (dynamic registration from fs) |
| `internal/tool/utils.go` | `getToolParam`, `truncate`, `detectLineEnding`, `normalizeToUnix`, `restoreLineEnding`, `IsBinary`, sqz compression |
| `internal/tool/permissions.go` | `IsSafePath`, canonicalizePath, isNewPath, allow-list (Load/Save Allowed/Tools/Commands), session/project/global scopes, TTL decay |
| `internal/tool/analyzer.go` | `CommandAnalysis`, `CommandAnalyzer` interface |
| `internal/tool/ast_bridge.go` | `astAnalyzer` wrapper, whitelisted command maps (Unix + Windows), unsupervised mkdir exception |
| `internal/tool/init_test.go` | Test setup: disables sqz globally |
| `internal/tool/implementations_test.go` | Tests for ReadFileTool (partial, truncation), ShellTool (execute, cwd, args, truncation, unsafe cwd, callString, approval, confirmation, binary, large output) |
| `internal/executor/executor.go` | `ExecuteToolCalls()` with middleware chain, `RegisterTools()` with enabledTools/isPlanning gating, `RunLoop()`, `ConsumeStream()` |
| `internal/config/config.go` | `Config` struct, `defaultConfig()`, `LoadConfig()`, tool enablement, OpenAI/subagent resolution |
| `internal/tui/interactions.go` | `TUIConfirmMiddleware` (full approval flow), `TUIInputProvider`, `PromptRequestMsg`, `ConfirmRequestMsg` |
| `internal/mcp/client.go` | `ToolAdapter`, namespaced naming, MCP Connect/GetTools/Close |
| `internal/mcp/config.go` | `MCPConfig`, `MCPServer`, config file discovery (project > user), env var expansion |
| `internal/agent/agent.go` | `NewSubagentOrchestrator`: tool inheritance from parent, coder toolset, context files |
| `internal/orchestrator/base.go` | `BaseOrchestrator`: orchestration loop, event system, children/parent hierarchy, middleware wiring |
| `cmd/late/main.go` | Tool registration entry: MCP → builtins → spawn_subagent, enabledTools from config + flags, middleware setup |
| `internal/assets/prompts/instruction-coding.md` | Coder subagent system prompt: prefer native tools over bash, stop on ambiguity, report changes |
