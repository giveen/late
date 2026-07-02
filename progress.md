# Progress

## 2026-06-20 — Research: Native Search Tool Design for Go Coding Agents

### Completed

- **handoff/external-reference.md** — Comprehensive research document on search tool implementations across 9 coding agents (Claude Code, Codex, OpenCode, Gemini CLI, Cursor CLI, Roo Code, Pi, OpenHands, and a Go code_search example)
- Covers: Go libraries (gocodewalker, git-pkgs/gitignore, fastwalk, godirwalk, xaverkapeller/go-gitignore), best practices, truncation strategies, parameter design, comparison tables
- 13 findings with inline citations, 18 kept sources, 7 excluded sources

### Next

- This research can inform implementation of a Go-based search tool for the `late` project
- Key decision: shell-out to ripgrep (fast, proven) vs pure Go (no dep, slower) vs hybrid
