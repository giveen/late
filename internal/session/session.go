package session

import (
	"context"
	"encoding/json"
	"fmt"
	"late/internal/client"
	"late/internal/common"
	"late/internal/tool"
	"strings"
)

// Session manages the chat state and interacts with the LLM client.
type Session struct {
	client       *client.Client
	HistoryPath  string
	History      []client.ChatMessage
	systemPrompt string
	useTools     bool
	Registry     *tool.Registry
}

func New(c *client.Client, historyPath string, history []client.ChatMessage, systemPrompt string, useTools bool) *Session {
	return &Session{
		client:       c,
		HistoryPath:  historyPath,
		History:      history,
		systemPrompt: systemPrompt,
		useTools:     useTools,
		Registry:     tool.NewRegistry(),
	}
}

// ExecuteTool executes a tool call and returns the response as a string.
// ExecuteTool executes a tool call and returns the response as a string.
func (s *Session) ExecuteTool(ctx context.Context, tc client.ToolCall) (string, error) {
	// First check registry
	t := s.Registry.Get(tc.Function.Name)
	if t == nil {
		return "", fmt.Errorf("tool not found: %s", tc.Function.Name)
	}
	return t.Execute(ctx, json.RawMessage(tc.Function.Arguments))
}

// AddToolResultMessage adds a tool response message to history.
func (s *Session) AddToolResultMessage(toolCallID, content string) error {
	s.History = append(s.History, client.ChatMessage{
		Role:       "tool",
		ToolCallID: toolCallID,
		Content:    content,
	})
	return SaveHistory(s.HistoryPath, s.History)
}

// AddAssistantMessageWithTools adds an assistant message with tool calls.
func (s *Session) AddAssistantMessageWithTools(content string, reasoning string, toolCalls []client.ToolCall) error {
	s.History = append(s.History, client.ChatMessage{
		Role:             "assistant",
		Content:          content,
		ReasoningContent: reasoning,
		ToolCalls:        toolCalls,
	})
	return SaveHistory(s.HistoryPath, s.History)
}

func (s *Session) GetToolDefinitions() []client.ToolDefinition {
	var defs []client.ToolDefinition
	for _, t := range s.Registry.All() {
		// Skip bash tool if disabled is handled by registry being empty of it
		defs = append(defs, client.ToolDefinition{
			Type: "function",
			Function: client.FunctionDefinition{
				Name:        t.Name(),
				Description: t.Description(),
				Parameters:  t.Parameters(),
			},
		})
	}
	return defs
}

// AddUserMessage adds a user message to history and persists it.
func (s *Session) AddUserMessage(content string) error {
	s.History = append(s.History, client.ChatMessage{Role: "user", Content: content})
	return SaveHistory(s.HistoryPath, s.History)
}

// AddAssistantMessage adds an assistant message to history and persists it.
func (s *Session) AddAssistantMessage(content, reasoning string) error {
	s.History = append(s.History, client.ChatMessage{
		Role:             "assistant",
		Content:          content,
		ReasoningContent: reasoning,
	})
	return SaveHistory(s.HistoryPath, s.History)
}

// AppendToLastMessage appends content to the last message (continuation).
func (s *Session) AppendToLastMessage(content, reasoning string) error {
	if len(s.History) == 0 {
		return fmt.Errorf("no history to append to")
	}
	lastIdx := len(s.History) - 1
	s.History[lastIdx].Content += content
	if reasoning != "" {
		if s.History[lastIdx].ReasoningContent != "" {
			s.History[lastIdx].ReasoningContent += "\n" + reasoning
		} else {
			s.History[lastIdx].ReasoningContent = reasoning
		}
	}
	return SaveHistory(s.HistoryPath, s.History)
}

// StartStream initiates a streaming response.
// It returns a standard Go channel for results and error.
func (s *Session) StartStream(ctx context.Context, extraBody map[string]any) (<-chan common.StreamResult, <-chan error) {
	outCh := make(chan common.StreamResult)
	errCh := make(chan error, 1)

	// Prepare messages with system prompt
	messages := make([]client.ChatMessage, 0, len(s.History)+1)
	if s.systemPrompt != "" {
		messages = append(messages, client.ChatMessage{Role: "system", Content: s.systemPrompt})
	}
	messages = append(messages, s.History...)

	req := client.ChatCompletionRequest{
		Messages:  messages,
		ExtraBody: extraBody,
	}

	if s.useTools {
		req.Tools = s.GetToolDefinitions()
	}

	streamOut, streamErr := s.client.ChatCompletionStream(ctx, req)

	go func() {
		defer close(outCh)
		defer close(errCh)

		for {
			select {
			case chunk, ok := <-streamOut:
				if !ok {
					return
				}
				var content, reasoning string
				var toolCalls []client.ToolCall
				if len(chunk.Choices) > 0 {
					content = chunk.Choices[0].Delta.Content
					reasoning = chunk.Choices[0].Delta.ReasoningContent
					toolCalls = chunk.Choices[0].Delta.ToolCalls
				}

				res := common.StreamResult{
					Content:          content,
					ReasoningContent: reasoning,
					ToolCalls:        toolCalls,
				}

				select {
				case outCh <- res:
				case <-ctx.Done():
					return
				}

			case err, ok := <-streamErr:
				if !ok {
					return
				}
				select {
				case errCh <- err:
				case <-ctx.Done():
					return
				}
				return
			case <-ctx.Done():
				return
			}
		}
	}()

	return outCh, errCh
}

// Impersonate returns a raw completion suggestion using the legacy format.
func (s *Session) Impersonate(ctx context.Context) (string, error) {
	var sb strings.Builder
	for _, msg := range s.History {
		sb.WriteString(fmt.Sprintf("<|im_start|>%s\n%s<|im_end|>\n", msg.Role, msg.Content))
	}
	prompt := sb.String() + "<|im_start|>user\n"

	req := client.CompletionRequest{
		Prompt:    prompt,
		Stop:      []string{"<|im_end|>", "<|im_start|>"},
		N_Predict: 50,
	}

	resp, err := s.client.Completion(ctx, req)
	if err != nil {
		return "", err
	}
	return resp.Content, nil
}
