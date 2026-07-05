package llms

type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

type ChatRequest struct {
	Model    string
	Messages []Message
	Tools    []Tool
}

type ChatResponse struct {
	Message Message
}

type Message struct {
	Role       Role
	Content    string
	ToolCalls  []ToolCall
	ToolCallID string
}

type Tool struct {
	Type     string
	Function ToolFunction
}

type ToolFunction struct {
	Name        string
	Description string
	Parameters  map[string]any
}

type ToolCall struct {
	ID       string
	Type     string
	Function ToolCallFunction
}

type ToolCallFunction struct {
	Name      string
	Arguments string
}
