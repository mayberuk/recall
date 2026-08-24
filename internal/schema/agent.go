package schema

// Agent identifies which coding agent a session came from. It is a property
// of the store a turn was decoded from, stamped on by the provider that read
// it — never a field parsed out of a raw record.
//
// AgentGemini and AgentCursor exist for detection and for the explicit
// "unknown agent" error message; no provider is registered for either yet.
type Agent string

const (
	AgentClaudeCode Agent = "claude-code"
	AgentCodex      Agent = "codex"
	AgentGemini     Agent = "gemini"
	AgentCursor     Agent = "cursor"
)
