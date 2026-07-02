# Final Handoff Plan: PR #83 Maintainer Review Fixes

## 1. Summary

**What:** Implement 5 targeted changes across 3 files to address maintainer @mlhher's PR #83 review comments.

**Why:** Maintainer requested removal/rewriting of over-recommendation language that promotes `search_tool` over bash at the prompt and tool description level. The maintainer's philosophy is to **trust the bash gate** as the sole enforcement mechanism and avoid biasing the model through prompting.

**PR #83 context:** PR by @giveen added a native Go `search_tool`, a bash gate (blocks grep/rg/find), prompt updates recommending search_tool, and tool description changes. The maintainer approved the bash gate enforcement but rejected the prompt-level preference signaling.

**Git HEAD:** `597e08c` — "feat: add search_tool improvements — bash gate, grep-mapped params, better descriptions, prompts, perf fix"

**Maintainer philosophy distilled:**

- Trust the bash gate — the model should learn from refusal
- Don't over-prompt — avoid search_tool preference in tool descriptions or bold PREFERRED lines
- Keep prompt changes minimal — a single YOU MUST directive in the planning prompt is enough
- Let the model pivot naturally — no advance warnings about gate behavior needed
- Abstract implementation details — say "the tool" not "the bash gate"

---

## 2. What Each Fix Should Do

### Fix 1 — Delete PREFERRED line from instruction-coding.md

| Before | After |
|--------|-------|
| Line 14: `- **PREFERRED**: Use \`search_tool\` for finding files or searching file contents instead of \`bash\` with \`grep\`, \`find\`, or \`rg\`. \`search_tool\` returns structured {path, line, content} matches, respects permission gates, and applies per-tool output caps for efficient results.` | **(line removed entirely)** |

**Source:** Maintainer comment 1 — "I'd still try to leave it out if possible... I am not a fan of forcing such specific behaviour through prompting."

### Fix 2 — Reword bash gate notice in instruction-coding.md

| Before | After |
|--------|-------|
| `- If you use \`bash\` for a search command (\`grep\`, \`rg\`, \`find\`), the bash gate will refuse the command and remind you to use \`search_tool\`.` | `- If you use bash for a search command (e.g. grep, rg, find), the tool will refuse your command and remind you to use the search_tool instead.` |

**Source:** Maintainer comment 2 — "Preferably use something like: If you use bash for a search command (e.g. grep, rg, find), the tool will refuse your command and remind you to use the search_tool instead."

### Fix 3 — Remove parenthetical from instruction-planning.md

| Before | After |
|--------|-------|
| `* **YOU CAN**: Read files, search the codebase with \`search_tool\` (preferred over bash+grep/find/rg), list directories, and analyze project structure.` | `* **YOU CAN**: Read files, search the codebase with \`search_tool\`, list directories, and analyze project structure.` |

**Source:** Maintainer comment 3 — "Please remove the (preferred over bash+grep/find/rg) here as that is explained in the next block."

### Fix 4 — Strengthen SEARCH PREFERENCE block to YOU MUST in instruction-planning.md

| Before | After |
|--------|-------|
| `* **SEARCH PREFERENCE**: For code search and pattern matching, always use \`search_tool\` instead of \`bash\` with \`grep\`, \`rg\`, or \`find\`. \`search_tool\` returns structured results and respects permission boundaries. The bash gate will block search commands in \`bash\` and redirect you to \`search_tool\`.` | `* **YOU MUST**: Use the \`search_tool\` instead of using the \`bash_tool\` with e.g. \`grep\`/\`find\`/\`rg\` to search for and match patterns and strings in the codebase. \`search_tool\` returns structured results and respects permission boundaries. The \`bash_tool\` will block search commands and redirect you to \`search_tool\`.` |

**Source:** Maintainer comment 4 — "Preferably use something like: **YOU MUST**: Use the search_tool instead of using the bash_tool with e.g. grep/find/rg to search for and match patterns and strings... or similar, gladly try out what gives the best result."

### Fix 5 — Revert bash tool Description() in implementations.go

| Before | After |
|--------|-------|
| `return fmt.Sprintf("For code investigation (searching source, reading files, listing directories), prefer the structured \`search_tool\`, \`read_file\`, and \`glob\` tools. Execute a %s command.", shellDisplayName())` | `return fmt.Sprintf("Execute a %s command.", shellDisplayName())` |

**Source:** Maintainer comment 5 — "This should not be included in the Bash tools description, it might bias the model. Simply restore the original here."
**Verified original:** `git show 597e08c^:internal/tool/implementations.go`

---

## 3. Files to Modify

| # | File | Action | Lines |
|---|------|--------|-------|
| 1 | `internal/assets/prompts/instruction-coding.md` | Delete line 14 | Line 14 (PREFERRED line) |
| 2 | `internal/assets/prompts/instruction-coding.md` | Reword line (now line 14 after Fix 1) | Formerly line 15, shifts to line 14 |
| 3 | `internal/assets/prompts/instruction-planning.md` | Delete parenthetical | Line 9 (within YOU CAN line) |
| 4 | `internal/assets/prompts/instruction-planning.md` | Rewrite entire block | Line 10 (SEARCH PREFERENCE → YOU MUST) |
| 5 | `internal/tool/implementations.go` | Revert Description() body | Lines 312-314 |

---

## 4. Constraints

1. **Do not touch bash gate enforcement** — `ValidateBashCommand` in `implementations.go:244-254` stays as-is
2. **Do not touch SearchTool.Description()** — `search.go:24` says "PREFERRED over bash grep/find/rg for code search." This is the search_tool's own description, appropriate and not mentioned by the maintainer
3. **Do not touch old handoff files** under `handoff/` — they are stale artifacts from the previous agent run
4. **Do not change the meaning** of existing lines — Fix 2, 4 only reword; Fix 1, 3 only delete redundant content; Fix 5 reverts to original
5. **Prompt consistency** — after all changes, the search_tool preference is communicated at the right level:
   - Coding prompt: general preference ("must prefer native tools") + gate consequence
   - Planning prompt: capability mention + YOU MUST directive + gate consequence
   - No redundancy, no bias in tool metadata

---

## 5. Non-Goals

- Do NOT add new features
- Do NOT modify files other than the 3 listed
- Do NOT modify test files (none exist for these changes)
- Do NOT change bash gate error messages or behavior
- Do NOT change SearchTool.Description() in `search.go`
- Do NOT edit old handoff/plan document artifacts

---

## 6. Validation

```bash
cd /mnt/storage/Projects/late
go build ./...              # Must compile without errors
go test -race ./...         # Must pass all tests
```

Expected results:

- `go build ./...` — clean exit code 0
- `go test -race ./...` — all tests pass (no test validates prompt content or ShellTool.Description)

Also verify the target lines changed as expected:

```bash
grep -n 'PREFERRED\|bash gate\|preferred over\|SEARCH PREFERENCE' internal/assets/prompts/instruction-coding.md internal/assets/prompts/instruction-planning.md
grep -A2 'func (t ShellTool) Description' internal/tool/implementations.go
```

After changes, grep should show:

- No `**PREFERRED**:` or `bash gate` or `preferred over` or `SEARCH PREFERENCE` anywhere in the prompt files
- ShellTool.Description() should contain only `Execute a %s command.`

---

## 7. Risks

| Risk | Severity | Mitigation |
|------|----------|------------|
| Fix 1: Removing PREFERRED line reduces explicit search_tool emphasis. Coder agents may use bash+grep slightly more. | Low | The YOU MUST in instruction-planning.md covers the planner. The bash gate (code enforcement) is the real mechanism. |
| Fix 5: Removing tool description hint means LLM sees neutral bash tool description. Model may use bash for search more often. | Low | The bash gate is the primary mechanism. Prompts also guide behavior. Gate blocks them anyway. |
| Unrelated test fails | Low | Run targeted test: `go test -race ./internal/tool/ -v -count=1` to isolate |
| Intervening commits changed the target lines | Low | Verify files before editing; report discrepancies |

**Overall risk:** Very low. All changes are cosmetic text edits or a revert to original Go code.

---

## 8. Unresolved Questions

- **None.** All 5 fixes have maintainer-approved wording, file paths verified at HEAD, original state confirmed via git, and no approval step needed.

---

## 9. Implementation-Ready Meta-Prompt

> **Role**: You are a coder subagent.
> **Task**: Implement 5 fixes across 3 files as specified below.
> **Working directory**: `/mnt/storage/Projects/late`
> **Current HEAD**: `597e08c` — verify this is where you start.
>
> **Fixes to apply in order:**
>
> **Step 1: Edit `internal/assets/prompts/instruction-coding.md`**
>
> - Read the file first to verify exact content.
> - Delete the line containing `**PREFERRED**: Use \`search_tool\` for finding files...` (currently line 14).
> - On the next line (currently line 15, `- If you use \`bash\` for a search command...`), replace that entire line with:
>   `- If you use bash for a search command (e.g. grep, rg, find), the tool will refuse your command and remind you to use the search_tool instead.`
>
> **Step 2: Edit `internal/assets/prompts/instruction-planning.md`**
>
> - Read the file first to verify exact content.
> - On the `* **YOU CAN**: Read files, search the codebase with \`search_tool\` (preferred over bash+grep/find/rg), list directories, and analyze project structure.` line:
>   - Delete `(preferred over bash+grep/find/rg)` including the space before it.
> - On the next line (`* **SEARCH PREFERENCE**: ...`), replace the entire line (which may span multiple lines, replace all of it) with:
>   `* **YOU MUST**: Use the \`search_tool\` instead of using the \`bash_tool\` with e.g. \`grep\`/\`find\`/\`rg\` to search for and match patterns and strings in the codebase. \`search_tool\` returns structured results and respects permission boundaries. The \`bash_tool\` will block search commands and redirect you to \`search_tool\`.`
>
> **Step 3: Edit `internal/tool/implementations.go`**
>
> - Find the `ShellTool.Description()` function (lines ~312-314).
> - Replace the return statement with:
>   `return fmt.Sprintf("Execute a %s command.", shellDisplayName())`
>
> **Step 4: Verify**
>
> - Run `cd /mnt/storage/Projects/late && go build ./...` — expect exit code 0.
> - Run `go test -race ./...` — expect all tests pass.
> - Run `grep -n 'PREFERRED\|bash gate\|preferred over\|SEARCH PREFERENCE' internal/assets/prompts/instruction-coding.md internal/assets/prompts/instruction-planning.md` — expect NO matches.
> - Run `grep -A2 'func (t ShellTool) Description' internal/tool/implementations.go` — expect only `Execute a %s command.` in the string.
>
> **Do NOT touch:**
>
> - `ValidateBashCommand` in `implementations.go`
> - `SearchTool.Description()` in `search.go`
> - Handoff/plan files in `handoff/`
> - Any test files
>
> **Report back with:**
>
> - List of files changed and what changed
> - Build and test results
> - Confirmation that grep found no remaining prohibited phrases

---

## Acceptance Report

```acceptance-report
{
  "criteriaSatisfied": [
    {
      "id": "criterion-1",
      "status": "satisfied",
      "evidence": "All 5 fixes are specified with exact before/after content, maintainer-approved wording, file paths verified at HEAD commit 597e08c. The plan does not widen scope — non-goals are explicitly documented (bash gate, SearchTool.Description, handoff files are all out of scope)."
    },
    {
      "id": "criterion-2",
      "status": "satisfied",
      "evidence": "This document contains: exact per-fix before/after tables, full file paths, validation commands, grep-based verification steps, build/test commands, and a ready-to-execute meta-prompt. An independent reviewer can verify all changes against the documented spec."
    }
  ],
  "changedFiles": [
    "internal/assets/prompts/instruction-coding.md (Fixes 1-2)",
    "internal/assets/prompts/instruction-planning.md (Fixes 3-4)",
    "internal/tool/implementations.go (Fix 5)"
  ],
  "testsAddedOrUpdated": [
    "None — no test validates prompt content or ShellTool.Description()"
  ],
  "commandsRun": [
    {
      "command": "read internal/assets/prompts/instruction-coding.md",
      "result": "passed",
      "summary": "Confirmed exact current content matches documented before-state"
    },
    {
      "command": "read internal/assets/prompts/instruction-planning.md",
      "result": "passed",
      "summary": "Confirmed exact current content matches documented before-state"
    },
    {
      "command": "read internal/tool/implementations.go line 310-320",
      "result": "passed",
      "summary": "Confirmed ShellTool.Description() content matches documented before-state"
    },
    {
      "command": "git show 597e08c^:internal/assets/prompts/instruction-coding.md",
      "result": "passed",
      "summary": "Confirmed pre-PR original state for verification of revert targets"
    },
    {
      "command": "git show 597e08c^:internal/tool/implementations.go",
      "result": "passed",
      "summary": "Confirmed original ShellTool.Description() was 'Execute a %s command.'"
    },
    {
      "command": "grep -n 'PREFERRED' internal/tool/search.go",
      "result": "passed",
      "summary": "Confirmed SearchTool.Description() exists at line 24 with 'PREFERRED over bash grep/find/rg' — noted as out of scope"
    }
  ],
  "validationOutput": [
    "go build ./... — to be run after implementation",
    "go test -race ./... — to be run after implementation",
    "grep -n 'PREFERRED\\|bash gate\\|preferred over\\|SEARCH PREFERENCE' on both prompt files — to be run after implementation"
  ],
  "residualRisks": [
    "Fix 1: Removing PREFERRED line may slightly increase coder agents' bash+grep usage — mitigated by bash gate enforcement and remaining 'must prefer native tools' line",
    "Fix 5: Reverting Description may slightly increase bash usage for search — mitigated by bash gate as primary enforcement"
  ],
  "noStagedFiles": true,
  "notes": "Plan is complete and ready for execution. All 5 fixes are grounded in verified file content at HEAD commit 597e08c. The old handoff/final-handoff-plan.md from the previous agent run (about adding search_tool) has been replaced with this PR review fix plan."
}
```
