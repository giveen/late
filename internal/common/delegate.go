package common

type Delegate interface {
	Log(msg string)
	Confirm(prompt string) (bool, error)
	Ask(prompt string) (string, error)
	AttachSession(s any)     // s is *session.Session
	Stream(res StreamResult) // For real-time streaming updates
	// Tool lifecycle events
	OnToolStart(toolName, args string)
	OnToolFinish(toolName, output string)

	StartLoop() // Signal start of a new run loop iteration
	Finish()    // Signal completion of subagent execution
}
