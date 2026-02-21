package tool

import (
	"late/internal/common"
)

// Tool represents a primitive tool that the agent can execute.
type Tool = common.Tool

// Registry stores available tools.
type Registry = common.ToolRegistry

func NewRegistry() *Registry {
	return common.NewToolRegistry()
}
