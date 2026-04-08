package common

import (
	"late/internal/client"
	"testing"
)

func TestReplacePlaceholders(t *testing.T) {
	tests := []struct {
		text         string
		placeholders map[string]string
		expected     string
	}{
		{
			text:         "Hello ${{CWD}}",
			placeholders: map[string]string{"${{CWD}}": "/tmp"},
			expected:     "Hello /tmp",
		},
		{
			text:         "No placeholder here",
			placeholders: map[string]string{"${{CWD}}": "/tmp"},
			expected:     "No placeholder here",
		},
		{
			text:         "Multiple ${{CWD}} in ${{CWD}}",
			placeholders: map[string]string{"${{CWD}}": "/home"},
			expected:     "Multiple /home in /home",
		},
	}

	for _, tt := range tests {
		result := ReplacePlaceholders(tt.text, tt.placeholders)
		if result != tt.expected {
			t.Errorf("ReplacePlaceholders(%q, %v) = %q; want %q", tt.text, tt.placeholders, result, tt.expected)
		}
	}
}

func TestEstimateTokenCount(t *testing.T) {
	tests := []struct {
		text     string
		expected int
	}{
		{"", 0},
		{"a", 1},    // (1+3)/4 = 1
		{"abcd", 1}, // (4+3)/4 = 1
		{"abcde", 2}, // (5+3)/4 = 2 (rounding up)
		{"12345678", 2}, // (8+3)/4 = 2
		{"123456789", 3}, // (9+3)/4 = 3
	}

	for _, tt := range tests {
		result := EstimateTokenCount(tt.text)
		if result != tt.expected {
			t.Errorf("EstimateTokenCount(%q) = %d; want %d", tt.text, result, tt.expected)
		}
	}
}

func TestEstimateMessageTokens(t *testing.T) {
	msg := client.ChatMessage{
		Role:             "assistant",
		Content:          "Hello",
		ReasoningContent: "Thinking...",
		ToolCalls: []client.ToolCall{
			{
				Function: client.FunctionCall{
					Name:      "test_tool",
					Arguments: `{"arg1": "val1"}`,
				},
			},
		},
	}

	// "Hello" = 5 chars -> 2 tokens
	// "Thinking..." = 11 chars -> 3 tokens
	// "test_tool" = 9 chars -> 3 tokens
	// `{"arg1": "val1"}` = 16 chars -> 4 tokens
	// Total = 2 + 3 + 3 + 4 = 12 tokens
	expected := 12
	result := EstimateMessageTokens(msg)
	if result != expected {
		t.Errorf("EstimateMessageTokens() = %d; want %d", result, expected)
	}
}

func TestEstimateEventTokens(t *testing.T) {
	event := ContentEvent{
		Content:          "Part1",
		ReasoningContent: "Reason",
		ToolCalls: []client.ToolCall{
			{
				Function: client.FunctionCall{
					Name:      "tool",
					Arguments: "{}",
				},
			},
		},
	}

	// "Part1" = 5 chars -> 2 tokens
	// "Reason" = 6 chars -> 2 tokens
	// "tool" = 4 chars -> 1 token
	// "{}" = 2 chars -> 1 token
	// Total = 2 + 2 + 1 + 1 = 6 tokens
	expected := 6
	result := EstimateEventTokens(event)
	if result != expected {
		t.Errorf("EstimateEventTokens() = %d; want %d", result, expected)
	}
}

func TestCalculateHistoryTokens(t *testing.T) {
	tests := []struct {
		name     string
		history  []client.ChatMessage
		expected int
	}{
		{
			name:     "empty history returns 0",
			history:  []client.ChatMessage{},
			expected: 0,
		},
		{
			name:     "nil history returns 0",
			history:  nil,
			expected: 0,
		},
		{
			name: "single message with content",
			history: []client.ChatMessage{
				{
					Role:    "user",
					Content: "Hello",
				},
			},
			expected: 2, // "Hello" = 5 chars -> 2 tokens
		},
		{
			name: "single message with reasoning content",
			history: []client.ChatMessage{
				{
					Role:             "assistant",
					Content:          "Here's the answer",
					ReasoningContent: "Let me think about this...",
				},
			},
			expected: 12, // "Here's the answer" = 17 chars -> 5 tokens, "Let me think about this..." = 26 chars -> 7 tokens, total = 12 tokens
		},
		{
			name: "multiple messages sum correctly",
			history: []client.ChatMessage{
				{
					Role:    "user",
					Content: "What is 2+2?",
				},
				{
					Role:             "assistant",
					Content:          "2+2 equals 4",
					ReasoningContent: "Simple math",
				},
			},
			expected: 9, // "What is 2+2?" = 12 chars -> 3 tokens, "2+2 equals 4" = 12 chars -> 3 tokens, "Simple math" = 11 chars -> 3 tokens, total = 9 tokens
		},
		{
			name: "message with tool calls included",
			history: []client.ChatMessage{
				{
					Role:    "user",
					Content: "Call the tool",
				},
				{
					Role:    "assistant",
					Content: "",
					ToolCalls: []client.ToolCall{
						{
							Function: client.FunctionCall{
								Name:      "calculate",
								Arguments: `{"a": 5, "b": 3}`,
							},
						},
					},
				},
			},
			expected: 11, // "Call the tool" = 13 chars -> 4 tokens, "calculate" = 9 chars -> 3 tokens, `{"a": 5, "b": 3}` = 15 chars -> 4 tokens, total = 11 tokens
		},
		{
			name: "mixed messages with all content types",
			history: []client.ChatMessage{
				{
					Role:    "user",
					Content: "Hello",
				},
				{
					Role:             "assistant",
					Content:          "Hi there",
					ReasoningContent: "Thinking...",
					ToolCalls: []client.ToolCall{
						{
							Function: client.FunctionCall{
								Name:      "greet",
								Arguments: `{"name": "user"}`,
							},
						},
					},
				},
				{
					Role:    "user",
					Content: "How are you?",
				},
			},
			expected: 16, // "Hello" = 5 chars -> 2 tokens, "Hi there" = 8 chars -> 2 tokens, "Thinking..." = 11 chars -> 3 tokens, "greet" = 5 chars -> 2 tokens, `{"name": "user"}` = 16 chars -> 4 tokens, "How are you?" = 12 chars -> 3 tokens, total = 16 tokens
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := CalculateHistoryTokens(tt.history)
			if result != tt.expected {
				t.Errorf("CalculateHistoryTokens() = %d; want %d", result, tt.expected)
			}
		})
	}
}
