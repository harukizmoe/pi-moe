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
