package llms

// Role is the normalized chat message role used across all LLM providers.
type Role string

const (
	// RoleSystem carries system-level instructions.
	RoleSystem Role = "system"
	// RoleUser carries user input.
	RoleUser Role = "user"
	// RoleAssistant carries model responses, including tool calls.
	RoleAssistant Role = "assistant"
	// RoleTool carries local tool results returned to the model.
	RoleTool Role = "tool"
)

// ChatRequest is the provider-neutral request shape used by the agent layer.
type ChatRequest struct {
	// Model overrides the provider default model when non-empty.
	Model string
	// Messages is the ordered conversation history sent to the model.
	Messages []Message
	// Tools exposes local callable functions to models that support tool calling.
	Tools []Tool
}

// ChatResponse is the provider-neutral response shape returned by every Provider.
type ChatResponse struct {
	// Message is the assistant message returned by the model.
	Message Message
}

// Message represents one normalized chat message in the conversation history.
type Message struct {
	// Role identifies who produced the message.
	Role Role
	// Content contains visible text for user, assistant, and tool result messages.
	Content string
	// ToolCalls contains function calls requested by an assistant message.
	ToolCalls []ToolCall
	// ToolCallID links a tool result message back to the assistant tool call that requested it.
	ToolCallID string
}

// Tool describes one function-style tool exposed to the model.
type Tool struct {
	// Type is the OpenAI-compatible tool type; this project currently uses "function".
	Type string
	// Function contains the callable function metadata and JSON schema.
	Function ToolFunction
}

// ToolFunction contains the function metadata sent in a tool schema.
type ToolFunction struct {
	// Name is the stable tool name the model uses in tool calls.
	Name string
	// Description tells the model when to use the tool.
	Description string
	// Parameters is the JSON Schema object describing the tool arguments.
	Parameters map[string]any
}

// ToolCall represents one function call requested by the model.
type ToolCall struct {
	// ID uniquely identifies the tool call within the assistant message.
	ID string
	// Type is the tool-call type; this project currently uses "function".
	Type string
	// Function contains the function name and raw JSON arguments.
	Function ToolCallFunction
}

// ToolCallFunction contains the executable function name and JSON argument payload.
type ToolCallFunction struct {
	// Name selects the registered local tool to execute.
	Name string
	// Arguments is the raw JSON object string produced by the model.
	Arguments string
}
