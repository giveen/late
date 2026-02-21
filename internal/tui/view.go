package tui

import (
	"encoding/json"
	"fmt"
	"unicode"

	"strings"

	"github.com/charmbracelet/lipgloss"
)

// sanitizeForDisplay strips control characters, unicode line/paragraph
// separators, and emoji that cause layout glitches in the TUI.
// Normal printable unicode (CJK, accented latin, cyrillic, etc.) is preserved.
func sanitizeForDisplay(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\t':
			return ' '
		case r == '\n', r == '\r':
			return -1
		case unicode.IsControl(r):
			return -1
		case isEmoji(r):
			return -1
		default:
			return r
		}
	}, s)
}

func isEmoji(r rune) bool {
	switch {
	case r >= 0x2600 && r <= 0x26FF: // Misc Symbols
		return true
	case r >= 0x2700 && r <= 0x27BF: // Dingbats
		return true
	case r >= 0x2B50 && r <= 0x2B55: // Stars, circles
		return true
	case r >= 0xFE00 && r <= 0xFE0F: // Variation Selectors
		return true
	case r >= 0x1F000 && r <= 0x1FAFF: // All major emoji blocks
		return true
	case r >= 0xE0020 && r <= 0xE007F: // Tag characters (flag sequences)
		return true
	case r == 0x200D: // Zero-width joiner (emoji sequences)
		return true
	case r == 0x200B, r == 0x200C: // Zero-width space/non-joiner
		return true
	case r == 0x20E3: // Combining enclosing keycap
		return true
	case r == 0xFEFF: // BOM
		return true
	case r >= 0x2028 && r <= 0x2029: // Line/paragraph separator
		return true
	case r >= 0x231A && r <= 0x23F3: // Misc technical (watch, hourglass, etc.)
		return true
	case r >= 0x2934 && r <= 0x2935: // Arrows
		return true
	case r >= 0x25AA && r <= 0x25FE: // Geometric shapes
		return true
	case r >= 0x2190 && r <= 0x21FF: // Arrows block
		return true
	case r >= 0x3000 && r <= 0x303F: // CJK symbols (ideographic space etc.)
		// Keep most CJK but strip ideographic space
		return r == 0x3000
	default:
		return false
	}
}

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
	case StateAsk:
		modeStr = " PROMPT "
		statusText = "User Input Required"
	}

	mode := statusModeStyle.Render(modeStr)
	status := statusTextStyle.Render(statusText)

	if s.State == StateConfirmTool || s.State == StateAsk {
		mode = statusModeStyle.Background(lipgloss.Color("#FFD700")).Foreground(lipgloss.Color("#000000")).Bold(true).Render(modeStr)
		status = statusTextStyle.Foreground(lipgloss.Color("#FFD700")).Bold(true).Render(statusText)
	}

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

	hints := lipgloss.JoinHorizontal(lipgloss.Left, hierarchyHint, stopKey)

	spaceWidth := w - lipgloss.Width(mode) - lipgloss.Width(status) - lipgloss.Width(hints)
	if spaceWidth < 0 {
		spaceWidth = 0
	}
	space := strings.Repeat(" ", spaceWidth)

	content := lipgloss.JoinHorizontal(lipgloss.Left, mode, status, space, hints)
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

	// Simplified history rendering (symmetric for all agents)
	var blocks []string
	for _, msg := range history {
		switch msg.Role {
		case "user":
			blocks = append(blocks, userMsgStyle.Width(msgWidth).Render(msg.Content))
		case "assistant":
			var assistantParts []string
			if msg.ReasoningContent != "" {
				assistantParts = append(assistantParts, tagStyle.Width(msgWidth+1).Render("Thinking Process:"))
				assistantParts = append(assistantParts, thinkingStyle.Width(msgWidth-2).Render(msg.ReasoningContent))
			}
			if msg.Content != "" {
				md, _ := m.Renderer.Render(msg.Content)
				assistantParts = append(assistantParts, aiMsgStyle.Width(msgWidth).Render(strings.TrimRight(md, "\n")))
			}
			for _, tc := range msg.ToolCalls {
				// Try to use CallString() for meaningful display
				callStr := tc.Function.Name
				if registry := m.Root.Registry(); registry != nil {
					if tool := registry.Get(tc.Function.Name); tool != nil {
						if args := json.RawMessage(tc.Function.Arguments); len(args) > 0 {
							callStr = tool.CallString(args)
						}
					}
				}
				assistantParts = append(assistantParts, tagStyle.Width(msgWidth+1).Render(fmt.Sprintf("◆ %s", callStr)))
			}
			blocks = append(blocks, lipgloss.JoinVertical(lipgloss.Left, assistantParts...))
		}
	}

	s := m.GetAgentState(m.Focused.ID())

	// Render streaming content if active
	// Dedup check: Only render streaming if NOT in an interaction state (where history already has the tools)
	if (s.State == StateStreaming || s.State == StateThinking) && s.State != StateAsk && s.State != StateConfirmTool {
		var activeParts []string
		if s.StreamingState.ReasoningContent != "" {
			activeParts = append(activeParts, tagStyle.Width(msgWidth+1).Render("Thinking Process:"))
			activeParts = append(activeParts, thinkingStyle.Width(msgWidth-2).Render(s.StreamingState.ReasoningContent))
		}
		if s.StreamingState.Content != "" {
			md, _ := m.Renderer.Render(s.StreamingState.Content)
			activeParts = append(activeParts, aiMsgStyle.Width(msgWidth).Render(strings.TrimRight(md, "\n")))
		}
		for _, tc := range s.StreamingState.ToolCalls {
			// Try to use CallString() for meaningful display (no trailing ... since CallString adds it)
			callStr := tc.Function.Name
			if registry := m.Root.Registry(); registry != nil {
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

	if s.State == StateAsk && s.PendingPrompt != nil {
		req := s.PendingPrompt.Request
		prompt := fmt.Sprintf("**%s**\n\n%s", req.Title, req.Description)

		// Parse schema for options
		var schema struct {
			Enum []string `json:"enum"`
		}
		if err := json.Unmarshal(req.Schema, &schema); err == nil && len(schema.Enum) > 0 {
			prompt += "\n\n"
			for i, opt := range schema.Enum {
				prompt += fmt.Sprintf("**%d.** %s  ", i+1, opt)
			}
			prompt += "\n\n> Type an option number or your response"
		}

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
