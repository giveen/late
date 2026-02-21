package main

import (
	"context"
	"flag"
	"fmt"
	"late/internal/agent"
	"late/internal/common"
	"late/internal/executor"
	"late/internal/orchestrator"
	"os"
	"path/filepath"

	"late/internal/assets"
	"late/internal/client"
	appconfig "late/internal/config"
	"late/internal/mcp"
	"late/internal/session"
	"late/internal/tool"
	"late/internal/tui"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
)

func main() {
	// Parse flags
	helpReq := flag.Bool("help", false, "Show help")
	systemPromptReq := flag.String("system-prompt", "", "Set the system prompt (literal string)")
	systemPromptFileReq := flag.String("system-prompt-file", "", "Set the system prompt from a file")
	useToolsReq := flag.Bool("use-tools", true, "Enable tool usage (allows LLM to call tools)")
	enableBashReq := flag.Bool("enable-bash", true, "Enable bash tool execution")
	injectCWDReq := flag.Bool("inject-cwd", true, "Replace ${{CWD}} in system prompt with current working directory")
	enableSubagentsReq := flag.Bool("enable-subagents", true, "Enable subagent usage")
	enableAskToolReq := flag.Bool("enable-ask-tool", false, "Enable ask tool for user input")
	enableNewSessionReq := flag.Bool("new-session", false, "Delete prior session history and start with a clean chat window")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage of late:\n")
		fmt.Fprintf(os.Stderr, "  late [flags]\n\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	if *helpReq {
		flag.Usage()
		return
	}

	// Determine system prompt
	// Priority: --system-prompt-file > --system-prompt > LATE_SYSTEM_PROMPT env var
	var systemPrompt string

	if *systemPromptFileReq != "" {
		content, err := os.ReadFile(*systemPromptFileReq)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading system prompt file: %v\n", err)
			os.Exit(1)
		}
		systemPrompt = string(content)
	} else if *systemPromptReq != "" {
		systemPrompt = *systemPromptReq
	} else if envPrompt := os.Getenv("LATE_SYSTEM_PROMPT"); envPrompt != "" {
		systemPrompt = envPrompt
	} else {
		content, err := assets.PromptsFS.ReadFile("prompts/instruction-planning.md")
		if err != nil {
			fmt.Fprintf(os.Stderr, "Fatal error: could not load embedded planning prompt: %v\n", err)
			os.Exit(1)
		}
		systemPrompt = string(content)
	}

	if *injectCWDReq {
		cwd, err := os.Getwd()
		if err == nil {
			systemPrompt = common.ReplacePlaceholders(systemPrompt, map[string]string{
				"${{CWD}}": cwd,
			})
		}
	}

	if !*enableBashReq {
		systemPrompt = common.ReplacePlaceholders(systemPrompt,
			map[string]string{
				"${{NOTICE}}": "Bash is disabled. You must not attempt to use execute any bash commands. Doing so will result in an error.",
			})
	}

	fmt.Println("Starting late TUI...")

	// Define history path
	homeDir, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to get user home dir: %v\n", err)
		os.Exit(1)
	}
	historyPath := filepath.Join(homeDir, ".local", "share", "late", "history.json")

	// Delete prior session history if --new-session is set
	if *enableNewSessionReq {
		if err := os.Remove(historyPath); err != nil {
			if os.IsNotExist(err) {
				fmt.Fprintf(os.Stderr, "Note: No prior session history to delete at %s\n", historyPath)
			} else {
				fmt.Fprintf(os.Stderr, "Warning: Failed to delete prior session history at %s: %v\n", historyPath, err)
			}
		} else {
			fmt.Fprintf(os.Stderr, "Deleted prior session history at %s\n", historyPath)
		}
	}

	// Load existing history
	history, err := session.LoadHistory(historyPath)
	if err != nil {
		history = []client.ChatMessage{}
	}

	// Initialize Core Components
	baseURL := os.Getenv("OPENAI_BASE_URL")
	if baseURL == "" {
		baseURL = "http://localhost:8080"
	}
	cfg := client.Config{BaseURL: baseURL}
	c := client.NewClient(cfg)

	// Initialize MCP client
	mcpClient := mcp.NewClient()
	defer mcpClient.Close()

	// Load MCP configuration
	config, err := mcp.LoadMCPConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Failed to load MCP config: %v\n", err)
	}

	// Try configuration-driven connections first
	if config != nil && len(config.McpServers) > 0 {
		fmt.Println("Connecting to MCP servers from configuration...")
		if err := mcpClient.ConnectFromConfig(context.Background(), config); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: Failed to connect to some MCP servers: %v\n", err)
		}
	}

	// Load App configuration
	appConfig, err := appconfig.LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Failed to load app config: %v\n", err)
	}
	enabledTools := make(map[string]bool)
	if appConfig != nil {
		for toolName, enabled := range appConfig.EnabledTools {
			enabledTools[toolName] = enabled
		}
	}

	// Flag overrides
	if !*enableBashReq {
		enabledTools["bash"] = false
	}
	if !*enableAskToolReq {
		enabledTools["ask"] = false
	}

	sess := session.New(c, historyPath, history, systemPrompt, *useToolsReq)
	executor.RegisterStandardTools(sess.Registry, enabledTools)

	// Register MCP tools into the session registry
	for _, t := range mcpClient.GetTools() {
		if enabledTools[t.Name()] {
			sess.Registry.Register(t)
		}
	}

	// Initialize common renderer
	renderer, _ := glamour.NewTermRenderer(
		glamour.WithStylesFromJSONBytes(tui.LateTheme),
		glamour.WithWordWrap(80),
		glamour.WithPreservedNewLines(),
	)

	// Create root orchestrator
	// We'll add middlewares later once the program is started
	rootAgent := orchestrator.NewBaseOrchestrator("main", sess, nil)

	model := tui.NewModel(rootAgent, renderer)
	p := tea.NewProgram(model, tea.WithAltScreen())

	// Wire TUI integration
	go func() {
		// Set messenger first
		p.Send(tui.SetMessengerMsg{Messenger: p})

		// Create context with InputProvider
		ctx := context.WithValue(context.Background(), common.InputProviderKey, tui.NewTUIInputProvider(p))
		rootAgent.SetContext(ctx)

		// Set middlewares (e.g. TUI confirmation)
		rootAgent.SetMiddlewares([]common.ToolMiddleware{
			tui.TUIConfirmMiddleware(p, sess.Registry),
		})

		// Start forwarding events from the root agent to the TUI
		ForwardOrchestratorEvents(p, rootAgent)
	}()

	if *enableSubagentsReq {
		runner := func(ctx context.Context, goal string, ctxFiles []string, agentType string) (string, error) {
			child, err := agent.NewSubagentOrchestrator(c, goal, ctxFiles, agentType, enabledTools, *injectCWDReq, rootAgent)
			if err != nil {
				return "", err
			}

			// Inherit TUI connection from parent
			// (This needs better wiring, maybe the Orchestrator hierarchy handles this automatically?)
			// For now, let's just execute the goal and wait.
			res, err := child.Execute("")
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("The subagent successfully completed its task. Final result:\n\n%s", res), nil
		}

		sess.Registry.Register(tool.SpawnSubagentTool{
			Runner: runner,
		})
	}

	if _, err := p.Run(); err != nil {
		fmt.Printf("Alas, there's been an error: %v", err)
		os.Exit(1)
	}
}

// ForwardOrchestratorEvents is a helper that recursively forwards all events from an orchestrator
// to the Bubble Tea program.
func ForwardOrchestratorEvents(p *tea.Program, o common.Orchestrator) {
	go func() {
		for event := range o.Events() {
			p.Send(tui.OrchestratorEventMsg{Event: event})
			if added, ok := event.(common.ChildAddedEvent); ok {
				ForwardOrchestratorEvents(p, added.Child)
			}
		}
	}()
}
