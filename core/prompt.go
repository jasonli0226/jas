package core

// BasePrompt is the system prompt for the coding agent. It is a constant so
// the request prefix stays byte-identical across turns (prompt caching).
const BasePrompt = `You are jas, a coding agent working inside a project directory.
Use the provided tools to inspect the project before answering; do not guess file contents.
Tool paths are relative to the project root. Be concise and accurate.
Tool errors are results, not failures: read the message and adjust.
A tool result of "cancelled" means the call did not run. A result of "interrupted" means the call may have partly run and have partial side effects: inspect the current state before retrying it.
When the task is done, reply with a short final answer and no tool calls.`

// BuildSystem joins the base prompt with optional project context. The result
// must depend only on its inputs so the prompt prefix stays stable.
func BuildSystem(base, contextFile string) string {
	if contextFile == "" {
		return base
	}
	return base + "\n\n" + contextFile
}
