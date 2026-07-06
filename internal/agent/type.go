package agent

// RunRequest 表示一次 Agent 运行请求。
type RunRequest struct {
	// Input 是发送给模型的用户输入文本。
	Input string
}

// RunResponse 表示一次 Agent 运行后的最终结果。
type RunResponse struct {
	// Answer 是模型在完成工具调用后返回的最终回答。
	Answer string
}

// RunResult 表示一次 Agent 运行的结构化结果。
type RunResult struct {
	// Answer 是模型完成工具调用后返回的最终回答。
	Answer string
	// ToolRounds 记录本次运行完成了多少轮 tool calling。
	ToolRounds int
	// Steps 记录每次工具执行的输入、输出和错误。
	Steps []Step
	// Events 记录本次运行产生的完整事件序列，供应用层渲染或回放。
	Events []Event
}

// Step 表示一次本地工具执行的 trace 记录。
type Step struct {
	// ToolCallID 是模型生成的 tool call 标识。
	ToolCallID string
	// ToolName 是被调用的本地工具名称。
	ToolName string
	// Arguments 是模型传入工具的原始 JSON 参数。
	Arguments string
	// Result 是工具成功执行后的文本结果。
	Result string
	// Error 是工具执行失败时的错误文本；成功时为空。
	Error string
}
