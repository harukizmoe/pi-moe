package llms

// Role 是所有 LLM Provider 共用的标准化聊天消息角色。
type Role string

const (
	// RoleSystem 承载系统级指令。
	RoleSystem Role = "system"
	// RoleUser 承载用户输入。
	RoleUser Role = "user"
	// RoleAssistant 承载模型响应，包括 tool call。
	RoleAssistant Role = "assistant"
	// RoleTool 承载返回给模型的本地工具结果。
	RoleTool Role = "tool"
)

// ChatRequest 是 agent 层使用的 Provider 无关请求结构。
type ChatRequest struct {
	// Model 非空时覆盖 Provider 的默认模型。
	Model string
	// Messages 是按顺序发送给模型的对话历史。
	Messages []Message
	// Tools 是暴露给支持 tool calling 模型的本地可调用函数。
	Tools []Tool
}

// ChatResponse 是所有 Provider 返回的标准化响应结构。
type ChatResponse struct {
	// Message 是模型返回的 assistant 消息。
	Message Message
}

// Message 表示对话历史中的一条标准化消息。
type Message struct {
	// Role 标识消息来源。
	Role Role
	// Content 保存用户、assistant 和 tool result 消息的可见文本。
	Content string
	// ToolCalls 保存 assistant 消息请求执行的函数调用。
	ToolCalls []ToolCall
	// ToolCallID 将 tool result 消息关联回对应的 assistant tool call。
	ToolCallID string
}

// Tool 描述一个暴露给模型的 function 风格工具。
type Tool struct {
	// Type 是 OpenAI-compatible 工具类型；当前项目使用 "function"。
	Type string
	// Function 保存可调用函数的元数据和 JSON Schema。
	Function ToolFunction
}

// ToolFunction 保存发送给模型的函数工具元数据。
type ToolFunction struct {
	// Name 是模型在 tool call 中使用的稳定工具名。
	Name string
	// Description 告诉模型何时使用该工具。
	Description string
	// Parameters 是描述工具参数的 JSON Schema object。
	Parameters map[string]any
}

// ToolCall 表示模型请求执行的一次函数调用。
type ToolCall struct {
	// ID 在当前 assistant 消息内唯一标识该 tool call。
	ID string
	// Type 是 tool call 类型；当前项目使用 "function"。
	Type string
	// Function 保存函数名和原始 JSON 参数。
	Function ToolCallFunction
}

// ToolCallFunction 保存可执行函数名和 JSON 参数负载。
type ToolCallFunction struct {
	// Name 选择要执行的本地注册工具。
	Name string
	// Arguments 是模型生成的原始 JSON object 字符串。
	Arguments string
}
