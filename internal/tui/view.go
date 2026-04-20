package tui

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

func (m Model) View() string {
	if m.Width == 0 || m.Height == 0 {
		return ""
	}

	baseView := appStyle.Width(m.Width).Height(m.Height).Render(
		lipgloss.JoinVertical(
			lipgloss.Left,
			m.Viewport.View(),
			m.inputView(),
			m.statusBarView(),
		),
	)

	return baseView
}

func (m *Model) inputView() string {
	w := m.Width - 4 // Internal padding for input
	if w < 1 {
		w = 1
	}
	bgColor := lipgloss.Color("#191919")

	// Render textarea directly — its styles already set background via FocusedStyle/BlurredStyle
	textareaView := m.Input.View()
	content := inputStyle.Width(w - 2).Render(textareaView)

	// Wrap in a fixed-size container that fills the background
	return lipgloss.NewStyle().
		Width(m.Width).
		Height(InputHeight).
		Background(bgColor).
		Padding(0, 2).
		AlignVertical(lipgloss.Bottom).
		Render(content)
}

func (m *Model) statusBarView() string {
	w := max(m.Width, 1)

	s := m.GetAgentState(m.Focused.ID())

	modeStr := " CHAT "
	statusText := s.StatusText

	switch s.State {
	case StateThinking:
		modeStr = " THINKING "
	case StateStreaming:
		modeStr = " STREAMING "
	case StateConfirmTool:
		modeStr = " CONFIRM "
		statusText = "Authorize Tool Execution (y/n)"
	}

	// Check if any other agent is waiting for confirmation
	otherWaiting := false
	for id, state := range m.AgentStates {
		if id != m.Focused.ID() && state.State == StateConfirmTool {
			otherWaiting = true
			break
		}
	}

	var warning string
	if otherWaiting {
		warning = statusWarningStyle.Render(" SUBAGENT CONFIRMATION REQUIRED ")
		if strings.Contains(statusText, "Spawned") {
			statusText = ""
		}
	}

	mode := statusModeStyle.Render(modeStr)
	status := statusTextStyle.Render(statusText)

	// Build key hints
	stopKey := statusKeyStyle.Render("Ctrl+g") + " Stop "

	// Add hierarchy hints
	var hierarchyHint string
	if m.Focused.Parent() != nil {
		hierarchyHint = statusKeyStyle.Render("Esc") + " Back "
	}
	if len(m.Focused.Children()) > 0 {
		hierarchyHint += statusKeyStyle.Render("Tab") + " Subagents "
	}

	// Token count display (after status, before space)
	tokenDisplay := fmt.Sprintf("| %d tokens", s.CumulativeTokenCount)
	tokenStyled := statusKeyStyle.Render(tokenDisplay)
	hints := lipgloss.JoinHorizontal(lipgloss.Left, hierarchyHint, stopKey)

	spaceWidth := w - lipgloss.Width(mode) - lipgloss.Width(status) - lipgloss.Width(warning) - lipgloss.Width(tokenStyled) - lipgloss.Width(hints)
	if spaceWidth < 0 {
		spaceWidth = 0
	}
	space := strings.Repeat(" ", spaceWidth)

	content := lipgloss.JoinHorizontal(lipgloss.Left, mode, status, warning, tokenStyled, space, hints)
	return statusBarBaseStyle.Width(w).Render(content)
}

func (m *Model) updateViewport() {
	if m.Focused == nil {
		return
	}

	history := m.Focused.History()
	msgWidth := m.Viewport.Width - 2
	if msgWidth < 1 {
		msgWidth = 80
	}

	s := m.GetAgentState(m.Focused.ID())
	s.LastRenderTime = time.Now().UnixMilli()

	// If history was reset or messages were removed, clear the cache
	if len(history) < len(s.RenderedHistory) {
		s.RenderedHistory = nil
	}

	// Render only new messages and add to cache
	for i := len(s.RenderedHistory); i < len(history); i++ {
		msg := history[i]
		var rendered string
		switch msg.Role {
		case "user":
			rendered = userMsgStyle.Width(msgWidth).Render(msg.Content)
		case "assistant":
			var assistantParts []string
			if msg.ReasoningContent != "" {
				assistantParts = append(assistantParts, tagStyle.Width(msgWidth+1).Render("Thinking Process:"))
				assistantParts = append(assistantParts, thinkingStyle.Width(msgWidth-2).Render(msg.ReasoningContent))
			}
			if msg.Content != "" {
				// Calculate inner width based on message style overhead
				// Subtract a small buffer to prevent Glamour table borders from being truncated by Lipgloss
				innerWidth := m.Viewport.Width - AIMsgOverhead - 2
				if innerWidth < 1 {
					innerWidth = 1
				}

				md, _ := m.GetRenderer(innerWidth).Render(msg.Content)

				resetInjection := "\x1b[38;2;85;85;85;48;2;25;25;25m"
				md = strings.ReplaceAll(md, "\x1b[0m", "\x1b[0m"+resetInjection)
				md = strings.ReplaceAll(md, "\x1b[m", "\x1b[m"+resetInjection)

				lines := strings.Split(md, "\n")
				// Style for the inner content (background and a default grey for structural elements like borders)
				lineStyle := lipgloss.NewStyle().
					Background(aiMsgBg).
					Foreground(lipgloss.Color("#555555"))

				// Final width for padding back to the full bubble width
				fullWidth := m.Viewport.Width - AIMsgOverhead

				for i, line := range lines {
					if strings.TrimSpace(line) == "" && i == len(lines)-1 {
						continue
					}
					// Ensure the background is applied to the full width without truncating
					lines[i] = lineStyle.Width(fullWidth).Render(line)
				}
				fullMD := lipgloss.JoinVertical(lipgloss.Left, lines...)
				assistantParts = append(assistantParts, aiMsgStyle.Render(fullMD))
			}
			for _, tc := range msg.ToolCalls {
				// Try to use CallString() for meaningful display
				callStr := tc.Function.Name
				if registry := m.Focused.Registry(); registry != nil {
					if tool := registry.Get(tc.Function.Name); tool != nil {
						if args := json.RawMessage(tc.Function.Arguments); len(args) > 0 {
							callStr = tool.CallString(args)
						}
					}
				}
				assistantParts = append(assistantParts, tagStyle.Width(msgWidth+1).Render(fmt.Sprintf("◆ %s", callStr)))
			}
			rendered = lipgloss.JoinVertical(lipgloss.Left, assistantParts...)
		}
		// We always append to keep cache in sync with history length
		s.RenderedHistory = append(s.RenderedHistory, rendered)
	}

	// Build the full block list from cached history + active content
	var blocks []string
	for _, r := range s.RenderedHistory {
		if r != "" {
			blocks = append(blocks, r)
		}
	}

	// Render streaming content if active
	// Dedup check: Only render streaming if NOT in an interaction state (where history already has the tools)
	if (s.State == StateStreaming || s.State == StateThinking) && s.State != StateConfirmTool {
		var activeParts []string
		if s.StreamingState.ReasoningContent != "" {
			activeParts = append(activeParts, tagStyle.Width(msgWidth+1).Render("Thinking Process:"))
			activeParts = append(activeParts, thinkingStyle.Width(msgWidth-2).Render(s.StreamingState.ReasoningContent))
		}
		if s.StreamingState.Content != "" {
			innerWidth := m.Viewport.Width - AIMsgOverhead - 2
			if innerWidth < 1 {
				innerWidth = 1
			}

			md, _ := m.GetRenderer(innerWidth).Render(s.StreamingState.Content)

			// Robust fix for transparency and structural color
			resetInjection := "\x1b[38;2;85;85;85;48;2;25;25;25m"
			md = strings.ReplaceAll(md, "\x1b[0m", "\x1b[0m"+resetInjection)
			md = strings.ReplaceAll(md, "\x1b[m", "\x1b[m"+resetInjection)

			lines := strings.Split(md, "\n")
			lineStyle := lipgloss.NewStyle().
				Background(aiMsgBg).
				Foreground(lipgloss.Color("#555555"))

			fullWidth := m.Viewport.Width - AIMsgOverhead

			for i, line := range lines {
				if strings.TrimSpace(line) == "" && i == len(lines)-1 {
					continue
				}
				// Ensure the background is applied to the full width
				lines[i] = lineStyle.Width(fullWidth).Render(line)
			}
			fullMD := lipgloss.JoinVertical(lipgloss.Left, lines...)
			activeParts = append(activeParts, aiMsgStyle.Render(fullMD))
		}
		for _, tc := range s.StreamingState.ToolCalls {
			// Try to use CallString() for meaningful display (no trailing ... since CallString adds it)
			callStr := tc.Function.Name
			if registry := m.Focused.Registry(); registry != nil {
				if tool := registry.Get(tc.Function.Name); tool != nil {
					if args := json.RawMessage(tc.Function.Arguments); len(args) > 0 {
						callStr = tool.CallString(args)
					}
				}
			}
			activeParts = append(activeParts, tagStyle.Width(msgWidth+1).Render(fmt.Sprintf("%s %s", m.Spinner.View(), callStr)))
		}
		if len(activeParts) > 0 {
			blocks = append(blocks, lipgloss.JoinVertical(lipgloss.Left, activeParts...))
		} else if s.State == StateThinking {
			blocks = append(blocks, thinkingStyle.Render("Thinking..."))
		}
	}

	// Render Interactions
	if s.State == StateConfirmTool && s.PendingConfirm != nil {
		tc := s.PendingConfirm.ToolCall
		prompt := fmt.Sprintf("The agent wants to execute **%s**.\n\n```json\n%s\n```\n\n> Press **[ y ]** to Approve  |  **[ n ]** to Deny", tc.Function.Name, tc.Function.Arguments)
		md, _ := m.Renderer.Render(prompt)
		blocks = append(blocks, aiMsgStyle.Width(msgWidth).Border(lipgloss.DoubleBorder()).BorderForeground(lipgloss.Color("#FFD700")).Render(md))
	}

	if m.Err != nil {
		blocks = append(blocks, thinkingStyle.Foreground(lipgloss.Color("#FF0000")).Render(fmt.Sprintf("Error: %v", m.Err)))
	}

	fullContent := lipgloss.JoinVertical(lipgloss.Left, blocks...)
	atBottom := m.Viewport.AtBottom()
	m.Viewport.SetContent(fullContent)
	if atBottom {
		m.Viewport.GotoBottom()
	}
}
