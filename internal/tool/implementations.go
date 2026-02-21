package tool

import (
	"bytes"
	"context"
	"encoding/json" // used for hash generation
	"fmt"
	"io"
	"late/internal/common"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// WeatherTool returns simulated weather.
type WeatherTool struct{}

func (t WeatherTool) Name() string        { return "get_weather" }
func (t WeatherTool) Description() string { return "Get the current weather for a location" }
func (t WeatherTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"location": { "type": "string", "description": "The city and state, e.g. San Francisco, CA" }
		},
		"required": ["location"]
	}`)
}
func (t WeatherTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var params struct {
		Location string `json:"location"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", err
	}
	return fmt.Sprintf("The weather in %s is currently 72°F and sunny.", params.Location), nil
}
func (t WeatherTool) RequiresConfirmation(args json.RawMessage) bool { return false }

// ReadFileTool reads content from a file.
type ReadFileTool struct {
	LastReads map[string]ReadState
}

type ReadState struct {
	ModTime    time.Time
	Size       int64
	LastParams string
}

func NewReadFileTool() *ReadFileTool {
	return &ReadFileTool{
		LastReads: make(map[string]ReadState),
	}
}

func (t *ReadFileTool) Name() string        { return "read_file" }
func (t *ReadFileTool) Description() string { return "Read the content of a file" }
func (t *ReadFileTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": { "type": "string", "description": "Path to the file to read" },
			"start_line": { "type": "integer", "description": "Optional: Start reading from this line number (1-indexed)" },
			"end_line": { "type": "integer", "description": "Optional: Stop reading at this line number (inclusive)" }
		},
		"required": ["path"]
	}`)
}
func (t *ReadFileTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var params struct {
		Path      string `json:"path"`
		StartLine int    `json:"start_line"`
		EndLine   int    `json:"end_line"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", err
	}

	// info, err := os.Stat(params.Path)
	// if err != nil {
	// 	return "", err
	// }

	// Check for unchanged key - DISABLED per user request
	// paramsJson, _ := json.Marshal(params)
	// paramsStr := string(paramsJson)

	// if state, ok := t.LastReads[params.Path]; ok {
	// 	if state.ModTime.Equal(info.ModTime()) && state.Size == info.Size() && state.LastParams == paramsStr {
	// 		return "File has not changed since last read with these parameters.", nil
	// 	}
	// }

	// Update state - DISABLED
	// t.LastReads[params.Path] = ReadState{
	// 	ModTime:    info.ModTime(),
	// 	Size:       info.Size(),
	// 	LastParams: paramsStr,
	// }

	data, err := os.ReadFile(params.Path)
	if err != nil {
		return "", err
	}

	lines := strings.Split(string(data), "\n")
	totalLines := len(lines)

	type lineInfo struct {
		lineNum int
		content string
	}
	fileLines := make([]lineInfo, totalLines)
	for i, line := range lines {
		fileLines[i] = lineInfo{
			lineNum: i + 1,
			content: line,
		}
	}

	start := 1
	end := totalLines

	if params.StartLine > 0 {
		start = params.StartLine
	}
	if params.EndLine > 0 {
		end = params.EndLine
	}

	if start < 1 {
		start = 1
	}
	if end > totalLines {
		end = totalLines
	}
	if start > end {
		return fmt.Sprintf("Invalid range: start_line %d > end_line %d (total: %d)", start, end, totalLines), nil
	}

	result := fileLines[start-1 : end]

	var sb strings.Builder
	for _, l := range result {
		sb.WriteString(fmt.Sprintf("%d | %s\n", l.lineNum, l.content))
	}

	return sb.String(), nil
}
func (t *ReadFileTool) RequiresConfirmation(args json.RawMessage) bool { return false }

func (t *ReadFileTool) CallString(args json.RawMessage) string {
	path := getToolParam(args, "path")
	if cwd, err := os.Getwd(); err == nil {
		path = strings.Replace(path, cwd, ".", 1)
	}
	return fmt.Sprintf("Reading file %s", truncate(path, 50))
}

// UpdateFileTool updates a file using a Search/Replace strategy.
type UpdateFileTool struct{}

func (t UpdateFileTool) Name() string        { return "update_file" }
func (t UpdateFileTool) Description() string { return "Update a file by replacing text blocks" }
func (t UpdateFileTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": { "type": "string", "description": "Path to the file to update" },
			"edits": { 
				"type": "string", 
				"description": "A string containing text replacement blocks. Use this format:\n<<<<\n[Exact content to find]\n====\n[New content to insert]\n>>>>\n\nYou can provide multiple blocks. The 'content to find' must EXACTLY match the existing file content (including whitespace/indentation) and be unique." 
			}
		},
		"required": ["path", "edits"]
	}`)
}

func (t UpdateFileTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var params struct {
		Path  string `json:"path"`
		Edits string `json:"edits"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", err
	}

	data, err := os.ReadFile(params.Path)
	if err != nil {
		return "", err
	}
	content := string(data)

	// Simple parser for custom block format
	// <<<<
	// SEARCH
	// ====
	// REPLACE
	// >>>>

	lines := strings.Split(params.Edits, "\n")
	var searchBuilder, replaceBuilder strings.Builder
	inSearch := false
	inReplace := false

	// We will collect edits and apply them.
	// To avoid overlapping issues or complex state, applying them sequentially to the 'content' string
	// is the most robust way to handle multiple patches in one go.

	updates := 0

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		if trimmed == "<<<<" && !inSearch && !inReplace {
			inSearch = true
			searchBuilder.Reset()
			continue
		}

		if trimmed == "====" && inSearch {
			inSearch = false
			inReplace = true
			replaceBuilder.Reset()
			continue
		}

		if trimmed == ">>>>" && inReplace {
			inReplace = false

			// Process the block
			searchStr := searchBuilder.String()
			replaceStr := replaceBuilder.String()

			// Remove the trailing newline added by the loop
			if len(searchStr) > 0 && searchStr[len(searchStr)-1] == '\n' {
				searchStr = searchStr[:len(searchStr)-1]
			}
			if len(replaceStr) > 0 && replaceStr[len(replaceStr)-1] == '\n' {
				replaceStr = replaceStr[:len(replaceStr)-1]
			}

			// Validate uniqueness
			count := strings.Count(content, searchStr)
			if count == 0 {
				return "", fmt.Errorf("search block not found in file:\n%s", searchStr)
			}
			if count > 1 {
				return "", fmt.Errorf("search block matches %d times, must be unique:\n%s", count, searchStr)
			}

			// Apply replacement
			content = strings.Replace(content, searchStr, replaceStr, 1)
			updates++
			continue
		}

		if inSearch {
			searchBuilder.WriteString(line + "\n")
		} else if inReplace {
			replaceBuilder.WriteString(line + "\n")
		}
		// Text outside blocks is ignored (allows for comments/explanations if the model adds them)
	}

	if inSearch || inReplace {
		return "", fmt.Errorf("incomplete block detected")
	}

	if err := os.WriteFile(params.Path, []byte(content), 0644); err != nil {
		return "", err
	}

	return fmt.Sprintf("Successfully applied %d updates to %s", updates, params.Path), nil
}
func (t UpdateFileTool) RequiresConfirmation(args json.RawMessage) bool {
	var params struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return true
	}
	return !IsSafePath(params.Path)
}

func (t UpdateFileTool) CallString(args json.RawMessage) string {
	path := getToolParam(args, "path")
	if cwd, err := os.Getwd(); err == nil {
		path = strings.Replace(path, cwd, ".", 1)
	}
	return fmt.Sprintf("Updating file %s", truncate(path, 50))
}

// WriteFileTool writes content to a file.
type WriteFileTool struct{}

func (t WriteFileTool) Name() string { return "write_file" }
func (t WriteFileTool) Description() string {
	return "Write content to a file. Requires confirmation if writing outside CWD."
}
func (t WriteFileTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": { "type": "string", "description": "Path to the file to write" },
			"content": { "type": "string", "description": "Content to write to the file" }
		},
		"required": ["path", "content"]
	}`)
}
func (t WriteFileTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var params struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", err
	}

	if params.Content == "" {
		return "", fmt.Errorf("Your edit to %s failed: content cannot be empty", params.Path)
	}
	if err := os.WriteFile(params.Path, []byte(params.Content), 0644); err != nil {
		return "", err
	}
	return fmt.Sprintf("Successfully wrote to %s", params.Path), nil
}
func (t WriteFileTool) RequiresConfirmation(args json.RawMessage) bool {
	var params struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return true // Default to safe if we can't parse
	}
	return !IsSafePath(params.Path)
}

func (t WriteFileTool) CallString(args json.RawMessage) string {
	path := getToolParam(args, "path")
	if cwd, err := os.Getwd(); err == nil {
		path = strings.Replace(path, cwd, ".", 1)
	}
	return fmt.Sprintf("Writing to file %s", truncate(path, 50))
}

// ListDirTool lists contents of a directory.
type ListDirTool struct{}

func (t ListDirTool) Name() string        { return "list_dir" }
func (t ListDirTool) Description() string { return "List the contents of a directory" }
func (t ListDirTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": { "type": "string", "description": "Path to the directory to list" }
		},
		"required": ["path"]
	}`)
}
func (t ListDirTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var params struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", err
	}

	entries, err := os.ReadDir(params.Path)
	if err != nil {
		return "", err
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Contents of %s:\n", params.Path))
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			sb.WriteString(fmt.Sprintf("- %s (error getting info)\n", entry.Name()))
			continue
		}
		typeStr := "file"
		if info.IsDir() {
			typeStr = "dir"
		}
		sb.WriteString(fmt.Sprintf("- %-20s [%s] %d bytes\n", entry.Name(), typeStr, info.Size()))
	}
	return sb.String(), nil
}
func (t ListDirTool) RequiresConfirmation(args json.RawMessage) bool { return false }

func (t ListDirTool) CallString(args json.RawMessage) string {
	path := getToolParam(args, "path")
	if cwd, err := os.Getwd(); err == nil {
		path = strings.Replace(path, cwd, ".", 1)
	}
	return fmt.Sprintf("Listing directory %s", truncate(path, 50))
}

// MkdirTool creates a new directory.
type MkdirTool struct{}

func (t MkdirTool) Name() string { return "mkdir" }
func (t MkdirTool) Description() string {
	return "Create a new directory (including parent directories)"
}
func (t MkdirTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": { "type": "string", "description": "Path to the directory to create" }
		},
		"required": ["path"]
	}`)
}
func (t MkdirTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var params struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", err
	}
	if err := os.MkdirAll(params.Path, 0755); err != nil {
		return "", err
	}
	return fmt.Sprintf("Successfully created directory %s", params.Path), nil
}
func (t MkdirTool) RequiresConfirmation(args json.RawMessage) bool {
	var params struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return true
	}
	return !IsSafePath(params.Path)
}

func (t MkdirTool) CallString(args json.RawMessage) string {
	path := getToolParam(args, "path")
	if cwd, err := os.Getwd(); err == nil {
		path = strings.Replace(path, cwd, ".", 1)
	}
	return fmt.Sprintf("Creating directory %s", truncate(path, 50))
}

// GrepTool searches for a pattern in files within a directory.
type GrepTool struct{}

func NewGrepTool() *GrepTool {
	return &GrepTool{}
}

func (t GrepTool) Name() string        { return "grep_search" }
func (t GrepTool) Description() string { return "Search for a pattern in files within a directory" }
func (t GrepTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"query": { "type": "string", "description": "The pattern to search for" },
			"path": { "type": "string", "description": "The directory path to search within" },
			"case_sensitive": { "type": "boolean", "description": "Whether the search should be case-sensitive (default: false)" },
			"is_regex": { "type": "boolean", "description": "Whether the query is a regular expression (default: false)" }
		},
		"required": ["query", "path"]
	}`)
}

const maxGrepResults = 10

type grepMatch struct {
	relPath    string
	line       int
	content    string
	matchCount int
	topDir     string // first path segment for diversity
}

func (t GrepTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var params struct {
		Query         string `json:"query"`
		Path          string `json:"path"`
		CaseSensitive bool   `json:"case_sensitive"`
		IsRegex       bool   `json:"is_regex"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", err
	}

	// Prepare regex if needed
	var regex *regexp.Regexp
	var err error
	if params.IsRegex {
		pattern := params.Query
		if !params.CaseSensitive {
			pattern = "(?i)" + pattern
		}
		regex, err = regexp.Compile(pattern)
		if err != nil {
			return "", fmt.Errorf("invalid regular expression: %v", err)
		}
	} else {
		// Non-regex: prepare for strings.Contains
		if !params.CaseSensitive {
			params.Query = strings.ToLower(params.Query)
		}
	}

	// Collect all matches grouped by top-level directory
	buckets := make(map[string][]grepMatch)
	totalMatches := 0

	filepath.WalkDir(params.Path, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if d.Name() == ".git" || d.Name() == "node_modules" || d.Name() == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}

		// Check for binary file
		f, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer f.Close()

		// Read first 512 bytes to check for binary content
		buf := make([]byte, 512)
		n, err := f.Read(buf)
		if err != nil && err != io.EOF {
			return nil
		}

		// excessive null bytes is a good indicator of binary files
		if bytes.IndexByte(buf[:n], 0) != -1 {
			return nil
		}

		// Reset file pointer to read full content or just read again
		// Since we're reading small files, just read the whole thing now
		content, err := os.ReadFile(path)
		if err != nil {
			return nil
		}

		text := string(content)

		matched := false
		if params.IsRegex {
			matched = regex.MatchString(text)
		} else {
			checkText := text
			if !params.CaseSensitive {
				checkText = strings.ToLower(text)
			}
			matched = strings.Contains(checkText, params.Query)
		}

		if matched {
			lines := strings.Split(text, "\n")
			matchCount := 0
			var firstLine int
			var firstContent string

			for i, line := range lines {
				lineMatched := false
				if params.IsRegex {
					lineMatched = regex.MatchString(line)
				} else {
					checkLine := line
					if !params.CaseSensitive {
						checkLine = strings.ToLower(line)
					}
					lineMatched = strings.Contains(checkLine, params.Query)
				}

				if lineMatched {
					matchCount++
					if matchCount == 1 {
						firstLine = i + 1
						firstContent = strings.TrimSpace(line)
					}
				}
			}

			if matchCount > 0 {
				relPath, _ := filepath.Rel(params.Path, path)
				if relPath == "" {
					relPath = path
				}

				// Get top-level directory for bucketing
				topDir := "."
				if parts := strings.SplitN(relPath, string(filepath.Separator), 2); len(parts) > 1 {
					topDir = parts[0]
				}

				buckets[topDir] = append(buckets[topDir], grepMatch{
					relPath:    relPath,
					line:       firstLine,
					content:    firstContent,
					matchCount: matchCount,
					topDir:     topDir,
				})
				totalMatches++
			}
		}
		return nil
	})

	if totalMatches == 0 {
		return "No matches found.", nil
	}

	// Round-robin select from buckets for diversity
	var selected []grepMatch
	for len(selected) < maxGrepResults && len(selected) < totalMatches {
		for dir := range buckets {
			if len(buckets[dir]) > 0 && len(selected) < maxGrepResults {
				selected = append(selected, buckets[dir][0])
				buckets[dir] = buckets[dir][1:]
			}
		}
	}

	// Format output
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Matches for '%s' in %s:\n", params.Query, params.Path))
	for _, m := range selected {
		sb.WriteString(fmt.Sprintf("%s:%d: %s\n", m.relPath, m.line, m.content))
		if m.matchCount > 1 {
			sb.WriteString(fmt.Sprintf("... (%d matches in %s)\n", m.matchCount, filepath.Base(m.relPath)))
		}
	}

	if totalMatches > maxGrepResults {
		sb.WriteString(fmt.Sprintf("\n... (showing %d of %d matching files)\n", len(selected), totalMatches))
	}

	return sb.String(), nil
}
func (t GrepTool) RequiresConfirmation(args json.RawMessage) bool { return false }

func (t GrepTool) CallString(args json.RawMessage) string {
	query := getToolParam(args, "query")
	path := getToolParam(args, "path")
	if cwd, err := os.Getwd(); err == nil {
		path = strings.Replace(path, cwd, ".", 1)
	}
	return fmt.Sprintf("Searching for '%s' in %s", truncate(query, 30), truncate(path, 50))
}

// BashTool executes a bash command.
type BashTool struct{}

func (t BashTool) Name() string        { return "bash" }
func (t BashTool) Description() string { return "Execute a bash command. Use cautiously." }
func (t BashTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"command": { "type": "string", "description": "The bash command to execute" }
		},
		"required": ["command"]
	}`)
}
func (t BashTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var params struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", err
	}

	cmd := exec.CommandContext(ctx, "bash", "-c", params.Command)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Sprintf("Error executing command: %v\nOutput:\n%s", err, string(out)), nil
	}
	return string(out), nil
}
func (t BashTool) RequiresConfirmation(args json.RawMessage) bool { return true }

func (t BashTool) CallString(args json.RawMessage) string {
	cmd := getToolParam(args, "command")
	// Handle multi-line by taking first line only
	if lines := strings.Split(cmd, "\n"); len(lines) > 0 {
		cmd = lines[0]
	}
	// Truncate to 40 chars
	truncated := cmd
	if len(cmd) > 37 {
		truncated = cmd[:37] + "..."
	}
	return fmt.Sprintf("Executing command: %s", truncated)
}

// AskTool asks the user a question.
type AskTool struct{}

func (t AskTool) Name() string { return "ask" }
func (t AskTool) Description() string {
	return "Ask the user a question. Supports single-choice (free text) and multi-choice (dropdown) modes."
}
func (t AskTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"prompt": { "type": "string", "description": "The question to ask the user" },
			"options": { "type": "array", "items": { "type": "string" }, "description": "If provided, user must choose one of these options" }
		},
		"required": ["prompt"]
	}`)
}
func (t AskTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var params struct {
		Prompt  string   `json:"prompt"`
		Options []string `json:"options"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", err
	}

	provider := common.GetInputProvider(ctx)
	if provider == nil {
		return "", fmt.Errorf("ask tool: no input provider found in context")
	}

	// Create a generic prompt request
	schema := json.RawMessage(`{"type": "string"}`)
	if len(params.Options) > 0 {
		optionsArr, _ := json.Marshal(params.Options)
		schema = json.RawMessage(fmt.Sprintf(`{"type": "string", "enum": %s}`, string(optionsArr)))
	}

	req := common.PromptRequest{
		Title:       "User Input Required",
		Description: params.Prompt,
		Schema:      schema,
	}

	result, err := provider.Prompt(ctx, req)
	if err != nil {
		return "", fmt.Errorf("ask tool prompt failed: %w", err)
	}

	var userInput string
	if err := json.Unmarshal(result, &userInput); err != nil {
		return "", fmt.Errorf("ask tool: failed to parse provider response: %w", err)
	}

	return userInput, nil
}
func (t AskTool) RequiresConfirmation(args json.RawMessage) bool { return false }

func (t AskTool) CallString(args json.RawMessage) string {
	prompt := getToolParam(args, "prompt")
	return fmt.Sprintf("Asking user: %s", truncate(prompt, 50))
}
