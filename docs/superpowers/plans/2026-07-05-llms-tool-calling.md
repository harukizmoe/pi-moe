# LLM Tool Calling 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**目标：** 跑通一个 Go CLI 练习闭环：Viper 读取 `configs/providers.yaml`，`internal/llms` 创建 Provider，`internal/agent` 执行 tool calling，`internal/tools` 提供 calculator，最后输出模型回答。

**架构：** `internal/llms` 是本项目的 AI 层，对应 `oh-my-pi/packages/ai`。Provider 只处理模型协议；Agent 只处理工具调用循环；Tools 只处理本地工具注册和执行；Config 只处理 Viper 配置读取。

**技术栈：** Go 1.26、Viper、标准库 `net/http`、`httptest`、table-driven tests。

---

## 文件结构

- 修改：`configs/providers.yaml` — 使用 `llms.default_provider` 和 provider `type/api_key_env` 配置。
- 创建/修改：`internal/config/config.go` — Viper 加载总配置。
- 创建/修改：`internal/llms/config.go` — LLM 配置类型。
- 创建：`internal/llms/type.go` — 统一 LLM 协议类型。
- 修改：`internal/llms/provider.go` — Provider 接口、Registry、工厂注册。
- 修改：`internal/llms/fake.go` — 确定性 fake Provider。
- 修改：`internal/llms/openai_compatible.go` — OpenAI Chat Completions 协议适配。
- 创建：`internal/tools/tool.go` — Tool 接口。
- 创建：`internal/tools/registry.go` — 工具注册和分发。
- 创建：`internal/tools/calculator.go` — calculator 工具。
- 修改：`internal/agent/type.go` — Agent 输入输出类型。
- 修改：`internal/agent/agent.go` — Agent 构造。
- 修改：`internal/agent/loop.go` — 一轮 tool calling 主循环。
- 修改：`internal/agent/tools.go` — Agent 调用 tools 的薄封装。
- 修改：`internal/agent/events.go` — 事件类型，当前只保留轻量定义。
- 创建/修改：`cmd/cli/main.go` — CLI 组装依赖并运行。
- 测试：`internal/config/config_test.go`、`internal/llms/fake_test.go`、`internal/llms/openai_compatible_test.go`、`internal/tools/calculator_test.go`、`internal/agent/loop_test.go`。

---

### Task 1: 配置结构和 Viper 加载

**Files:**
- Modify: `configs/providers.yaml`
- Modify: `internal/llms/config.go`
- Modify: `internal/config/config.go`
- Test: `internal/config/config_test.go`

- [ ] **Step 1: 添加 Viper 依赖**

Run:

```bash
go get github.com/spf13/viper
```

Expected: `go.mod` 出现 `github.com/spf13/viper` 依赖，命令退出码为 0。

- [ ] **Step 2: 写配置加载测试**

Create `internal/config/config_test.go`:

```go
package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadProvidersConfig(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "test-key")

	dir := t.TempDir()
	path := filepath.Join(dir, "providers.yaml")
	content := []byte(`llms:
  default_provider: deepseek
  providers:
    deepseek:
      type: openai_compatible
      base_url: "https://api.deepseek.com/v1"
      api_key_env: "DEEPSEEK_API_KEY"
      model: "deepseek-chat"
      timeout_seconds: 60
`)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.LLMs.DefaultProvider != "deepseek" {
		t.Fatalf("default provider = %q", cfg.LLMs.DefaultProvider)
	}
	provider := cfg.LLMs.Providers["deepseek"]
	if provider.Type != "openai_compatible" {
		t.Fatalf("provider type = %q", provider.Type)
	}
	if provider.APIKey != "test-key" {
		t.Fatalf("api key = %q", provider.APIKey)
	}
}
```

- [ ] **Step 3: 运行测试，确认失败**

Run:

```bash
go test ./internal/config -run TestLoadProvidersConfig -v
```

Expected: FAIL，原因是 `Load` 或配置类型尚未实现。

- [ ] **Step 4: 更新 `configs/providers.yaml`**

Replace `configs/providers.yaml`:

```yaml
llms:
  default_provider: fake

  providers:
    openai:
      type: openai_compatible
      base_url: "https://api.openai.com/v1"
      api_key_env: "OPENAI_API_KEY"
      model: "gpt-4o-mini"
      timeout_seconds: 60

    deepseek:
      type: openai_compatible
      base_url: "https://api.deepseek.com/v1"
      api_key_env: "DEEPSEEK_API_KEY"
      model: "deepseek-chat"
      timeout_seconds: 60

    fake:
      type: fake
      model: "fake-tool-model"
      timeout_seconds: 1
```

- [ ] **Step 5: 实现 `internal/llms/config.go`**

```go
package llms

type Config struct {
	DefaultProvider string                    `mapstructure:"default_provider"`
	Providers       map[string]ProviderConfig `mapstructure:"providers"`
}

type ProviderConfig struct {
	Type           string `mapstructure:"type"`
	BaseURL        string `mapstructure:"base_url"`
	APIKeyEnv      string `mapstructure:"api_key_env"`
	APIKey         string `mapstructure:"-"`
	Model          string `mapstructure:"model"`
	TimeoutSeconds int    `mapstructure:"timeout_seconds"`
}
```

- [ ] **Step 6: 实现 `internal/config/config.go`**

```go
package config

import (
	"fmt"
	"os"

	"github.com/spf13/viper"

	"harukizmoe/pimoe/internal/llms"
)

type Config struct {
	LLMs llms.Config `mapstructure:"llms"`
}

func Load(path string) (*Config, error) {
	v := viper.New()
	v.SetConfigFile(path)
	v.SetConfigType("yaml")

	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("read config %q: %w", path, err)
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("decode config %q: %w", path, err)
	}

	for name, provider := range cfg.LLMs.Providers {
		if provider.APIKeyEnv != "" {
			provider.APIKey = os.Getenv(provider.APIKeyEnv)
		}
		cfg.LLMs.Providers[name] = provider
	}

	return &cfg, nil
}
```

- [ ] **Step 7: 运行测试，确认通过**

Run:

```bash
go test ./internal/config -run TestLoadProvidersConfig -v
```

Expected: PASS。

- [ ] **Step 8: 提交**

```bash
git add configs/providers.yaml internal/llms/config.go internal/config/config.go internal/config/config_test.go go.mod go.sum
git commit -m "feat: add yaml provider config"
```

---

### Task 2: LLM 统一类型、Registry 和 Fake Provider

**Files:**
- Create: `internal/llms/type.go`
- Modify: `internal/llms/provider.go`
- Modify: `internal/llms/fake.go`
- Test: `internal/llms/fake_test.go`

- [ ] **Step 1: 写 Fake Provider 测试**

Create `internal/llms/fake_test.go`:

```go
package llms

import (
	"context"
	"testing"
)

func TestFakeProviderReturnsToolCallThenFinalAnswer(t *testing.T) {
	provider, err := NewFakeProvider(ProviderConfig{Model: "fake-tool-model"})
	if err != nil {
		t.Fatalf("NewFakeProvider() error = %v", err)
	}

	first, err := provider.Chat(context.Background(), ChatRequest{
		Messages: []Message{{Role: "user", Content: "calculate 13 * 7"}},
	})
	if err != nil {
		t.Fatalf("first Chat() error = %v", err)
	}
	if len(first.Message.ToolCalls) != 1 {
		t.Fatalf("tool calls len = %d", len(first.Message.ToolCalls))
	}
	if first.Message.ToolCalls[0].Function.Name != "calculator" {
		t.Fatalf("tool name = %q", first.Message.ToolCalls[0].Function.Name)
	}

	second, err := provider.Chat(context.Background(), ChatRequest{
		Messages: []Message{
			{Role: "user", Content: "calculate 13 * 7"},
			first.Message,
			{Role: "tool", ToolCallID: "call_fake_calculator", Content: "91"},
		},
	})
	if err != nil {
		t.Fatalf("second Chat() error = %v", err)
	}
	if second.Message.Content != "13 * 7 = 91" {
		t.Fatalf("final content = %q", second.Message.Content)
	}
}
```

- [ ] **Step 2: 运行测试，确认失败**

Run:

```bash
go test ./internal/llms -run TestFakeProviderReturnsToolCallThenFinalAnswer -v
```

Expected: FAIL，原因是 LLM 类型和 fake provider 尚未实现。

- [ ] **Step 3: 实现 `internal/llms/type.go`**

```go
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
```

- [ ] **Step 4: 实现 `internal/llms/provider.go`**

```go
package llms

import (
	"context"
	"fmt"
)

type Provider interface {
	Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error)
}

type Factory func(cfg ProviderConfig) (Provider, error)

type Registry struct {
	factories map[string]Factory
}

func NewRegistry() *Registry {
	return &Registry{factories: map[string]Factory{}}
}

func (r *Registry) Register(providerType string, factory Factory) {
	r.factories[providerType] = factory
}

func (r *Registry) NewProvider(cfg ProviderConfig) (Provider, error) {
	factory, ok := r.factories[cfg.Type]
	if !ok {
		return nil, fmt.Errorf("unknown llm provider type %q", cfg.Type)
	}
	return factory(cfg)
}
```

- [ ] **Step 5: 实现 `internal/llms/fake.go`**

```go
package llms

import "context"

type FakeProvider struct {
	model string
}

func NewFakeProvider(cfg ProviderConfig) (Provider, error) {
	return &FakeProvider{model: cfg.Model}, nil
}

func (p *FakeProvider) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	for i := len(req.Messages) - 1; i >= 0; i-- {
		msg := req.Messages[i]
		if msg.Role == RoleTool {
			return &ChatResponse{Message: Message{Role: RoleAssistant, Content: "13 * 7 = " + msg.Content}}, nil
		}
	}

	return &ChatResponse{Message: Message{
		Role: RoleAssistant,
		ToolCalls: []ToolCall{{
			ID:   "call_fake_calculator",
			Type: "function",
			Function: ToolCallFunction{
				Name:      "calculator",
				Arguments: `{"a":13,"b":7,"op":"mul"}`,
			},
		}},
	}}, nil
}
```

- [ ] **Step 6: 运行测试，确认通过**

Run:

```bash
go test ./internal/llms -run TestFakeProviderReturnsToolCallThenFinalAnswer -v
```

Expected: PASS。

- [ ] **Step 7: 提交**

```bash
git add internal/llms/type.go internal/llms/provider.go internal/llms/fake.go internal/llms/fake_test.go
git commit -m "feat: add llm provider registry and fake provider"
```

---

### Task 3: Tools Registry 和 Calculator

**Files:**
- Create: `internal/tools/tool.go`
- Create: `internal/tools/registry.go`
- Create: `internal/tools/calculator.go`
- Test: `internal/tools/calculator_test.go`

- [ ] **Step 1: 写 calculator 测试**

Create `internal/tools/calculator_test.go`:

```go
package tools

import (
	"context"
	"testing"
)

func TestCalculatorMul(t *testing.T) {
	tool := Calculator{}
	got, err := tool.Call(context.Background(), `{"a":13,"b":7,"op":"mul"}`)
	if err != nil {
		t.Fatalf("Call() error = %v", err)
	}
	if got != "91" {
		t.Fatalf("result = %q", got)
	}
}

func TestCalculatorDivideByZero(t *testing.T) {
	tool := Calculator{}
	_, err := tool.Call(context.Background(), `{"a":1,"b":0,"op":"div"}`)
	if err == nil {
		t.Fatal("Call() error = nil")
	}
}

func TestRegistrySchemasAndCall(t *testing.T) {
	registry := NewRegistry()
	registry.Register(Calculator{})

	schemas := registry.Schemas()
	if len(schemas) != 1 {
		t.Fatalf("schemas len = %d", len(schemas))
	}
	if schemas[0].Function.Name != "calculator" {
		t.Fatalf("schema tool name = %q", schemas[0].Function.Name)
	}

	got, err := registry.Call(context.Background(), "calculator", `{"a":2,"b":3,"op":"add"}`)
	if err != nil {
		t.Fatalf("Call() error = %v", err)
	}
	if got != "5" {
		t.Fatalf("result = %q", got)
	}
}
```

- [ ] **Step 2: 运行测试，确认失败**

Run:

```bash
go test ./internal/tools -v
```

Expected: FAIL，原因是 tools 包尚未实现。

- [ ] **Step 3: 实现 `internal/tools/tool.go`**

```go
package tools

import "context"

type Tool interface {
	Name() string
	Description() string
	Parameters() map[string]any
	Call(ctx context.Context, arguments string) (string, error)
}
```

- [ ] **Step 4: 实现 `internal/tools/registry.go`**

```go
package tools

import (
	"context"
	"fmt"

	"harukizmoe/pimoe/internal/llms"
)

type Registry struct {
	tools map[string]Tool
}

func NewRegistry() *Registry {
	return &Registry{tools: map[string]Tool{}}
}

func (r *Registry) Register(tool Tool) {
	r.tools[tool.Name()] = tool
}

func (r *Registry) Schemas() []llms.Tool {
	schemas := make([]llms.Tool, 0, len(r.tools))
	for _, tool := range r.tools {
		schemas = append(schemas, llms.Tool{
			Type: "function",
			Function: llms.ToolFunction{
				Name:        tool.Name(),
				Description: tool.Description(),
				Parameters:  tool.Parameters(),
			},
		})
	}
	return schemas
}

func (r *Registry) Call(ctx context.Context, name string, arguments string) (string, error) {
	tool, ok := r.tools[name]
	if !ok {
		return "", fmt.Errorf("unknown tool %q", name)
	}
	return tool.Call(ctx, arguments)
}
```

- [ ] **Step 5: 实现 `internal/tools/calculator.go`**

```go
package tools

import (
	"context"
	"encoding/json"
	"fmt"
)

type Calculator struct{}

type calculatorArgs struct {
	A  float64 `json:"a"`
	B  float64 `json:"b"`
	Op string  `json:"op"`
}

func (Calculator) Name() string {
	return "calculator"
}

func (Calculator) Description() string {
	return "Calculate two numbers."
}

func (Calculator) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"a":  map[string]any{"type": "number"},
			"b":  map[string]any{"type": "number"},
			"op": map[string]any{"type": "string", "enum": []string{"add", "sub", "mul", "div"}},
		},
		"required": []string{"a", "b", "op"},
	}
}

func (Calculator) Call(ctx context.Context, arguments string) (string, error) {
	var args calculatorArgs
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return "", fmt.Errorf("decode calculator arguments: %w", err)
	}

	var result float64
	switch args.Op {
	case "add":
		result = args.A + args.B
	case "sub":
		result = args.A - args.B
	case "mul":
		result = args.A * args.B
	case "div":
		if args.B == 0 {
			return "", fmt.Errorf("divide by zero")
		}
		result = args.A / args.B
	default:
		return "", fmt.Errorf("unsupported calculator op %q", args.Op)
	}

	return fmt.Sprintf("%g", result), nil
}
```

- [ ] **Step 6: 运行测试，确认通过**

Run:

```bash
go test ./internal/tools -v
```

Expected: PASS。

- [ ] **Step 7: 提交**

```bash
git add internal/tools/tool.go internal/tools/registry.go internal/tools/calculator.go internal/tools/calculator_test.go
git commit -m "feat: add calculator tool registry"
```

---

### Task 4: Agent 一轮 Tool Calling Loop

**Files:**
- Modify: `internal/agent/type.go`
- Modify: `internal/agent/agent.go`
- Modify: `internal/agent/loop.go`
- Modify: `internal/agent/tools.go`
- Modify: `internal/agent/events.go`
- Test: `internal/agent/loop_test.go`

- [ ] **Step 1: 写 Agent loop 测试**

Create `internal/agent/loop_test.go`:

```go
package agent

import (
	"context"
	"testing"

	"harukizmoe/pimoe/internal/llms"
	"harukizmoe/pimoe/internal/tools"
)

func TestAgentRunExecutesToolCall(t *testing.T) {
	provider, err := llms.NewFakeProvider(llms.ProviderConfig{Model: "fake-tool-model"})
	if err != nil {
		t.Fatalf("NewFakeProvider() error = %v", err)
	}

	toolRegistry := tools.NewRegistry()
	toolRegistry.Register(tools.Calculator{})

	a := New(provider, toolRegistry, "fake-tool-model")
	got, err := a.Run(context.Background(), "use calculator to compute 13 * 7")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got != "13 * 7 = 91" {
		t.Fatalf("answer = %q", got)
	}
}
```

- [ ] **Step 2: 运行测试，确认失败**

Run:

```bash
go test ./internal/agent -run TestAgentRunExecutesToolCall -v
```

Expected: FAIL，原因是 Agent 尚未实现。

- [ ] **Step 3: 实现 `internal/agent/type.go`**

```go
package agent

type RunRequest struct {
	Input string
}

type RunResponse struct {
	Answer string
}
```

- [ ] **Step 4: 实现 `internal/agent/agent.go`**

```go
package agent

import (
	"harukizmoe/pimoe/internal/llms"
	"harukizmoe/pimoe/internal/tools"
)

type Agent struct {
	provider llms.Provider
	tools    *tools.Registry
	model    string
}

func New(provider llms.Provider, tools *tools.Registry, model string) *Agent {
	return &Agent{
		provider: provider,
		tools:    tools,
		model:    model,
	}
}
```

- [ ] **Step 5: 实现 `internal/agent/tools.go`**

```go
package agent

import (
	"context"
	"fmt"

	"harukizmoe/pimoe/internal/llms"
)

func (a *Agent) runToolCall(ctx context.Context, call llms.ToolCall) (llms.Message, error) {
	result, err := a.tools.Call(ctx, call.Function.Name, call.Function.Arguments)
	if err != nil {
		return llms.Message{}, fmt.Errorf("call tool %q: %w", call.Function.Name, err)
	}
	return llms.Message{
		Role:       llms.RoleTool,
		ToolCallID: call.ID,
		Content:    result,
	}, nil
}
```

- [ ] **Step 6: 实现 `internal/agent/loop.go`**

```go
package agent

import (
	"context"
	"fmt"

	"harukizmoe/pimoe/internal/llms"
)

func (a *Agent) Run(ctx context.Context, input string) (string, error) {
	messages := []llms.Message{{Role: llms.RoleUser, Content: input}}

	first, err := a.provider.Chat(ctx, llms.ChatRequest{
		Model:    a.model,
		Messages: messages,
		Tools:    a.tools.Schemas(),
	})
	if err != nil {
		return "", fmt.Errorf("first llm chat: %w", err)
	}

	assistantMessage := first.Message
	if len(assistantMessage.ToolCalls) == 0 {
		return assistantMessage.Content, nil
	}

	messages = append(messages, assistantMessage)
	for _, call := range assistantMessage.ToolCalls {
		toolMessage, err := a.runToolCall(ctx, call)
		if err != nil {
			return "", err
		}
		messages = append(messages, toolMessage)
	}

	final, err := a.provider.Chat(ctx, llms.ChatRequest{
		Model:    a.model,
		Messages: messages,
	})
	if err != nil {
		return "", fmt.Errorf("final llm chat: %w", err)
	}

	return final.Message.Content, nil
}
```

- [ ] **Step 7: 实现 `internal/agent/events.go`**

```go
package agent

type EventType string

const (
	EventToolCall EventType = "tool_call"
	EventFinal    EventType = "final"
)

type Event struct {
	Type    EventType
	Message string
}
```

- [ ] **Step 8: 运行测试，确认通过**

Run:

```bash
go test ./internal/agent -run TestAgentRunExecutesToolCall -v
```

Expected: PASS。

- [ ] **Step 9: 提交**

```bash
git add internal/agent/type.go internal/agent/agent.go internal/agent/loop.go internal/agent/tools.go internal/agent/events.go internal/agent/loop_test.go
git commit -m "feat: add agent tool calling loop"
```

---

### Task 5: OpenAI-compatible Provider

**Files:**
- Modify: `internal/llms/openai_compatible.go`
- Test: `internal/llms/openai_compatible_test.go`

- [ ] **Step 1: 写 OpenAI-compatible 请求测试**

Create `internal/llms/openai_compatible_test.go`:

```go
package llms

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOpenAICompatibleProviderSendsChatCompletionPayload(t *testing.T) {
	var captured map[string]any

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("authorization = %q", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("decode request: %v", err)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
  "choices": [
    {
      "message": {
        "role": "assistant",
        "tool_calls": [
          {
            "id": "call_1",
            "type": "function",
            "function": {
              "name": "calculator",
              "arguments": "{\"a\":13,\"b\":7,\"op\":\"mul\"}"
            }
          }
        ]
      }
    }
  ]
}`))
	}))
	defer server.Close()

	provider, err := NewOpenAICompatibleProvider(ProviderConfig{
		BaseURL:        server.URL + "/v1",
		APIKey:         "test-key",
		Model:          "test-model",
		TimeoutSeconds: 3,
	})
	if err != nil {
		t.Fatalf("NewOpenAICompatibleProvider() error = %v", err)
	}

	resp, err := provider.Chat(context.Background(), ChatRequest{
		Messages: []Message{{Role: RoleUser, Content: "calculate"}},
		Tools: []Tool{{
			Type: "function",
			Function: ToolFunction{
				Name:        "calculator",
				Description: "Calculate two numbers.",
				Parameters: map[string]any{"type": "object"},
			},
		}},
	})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}

	if captured["model"] != "test-model" {
		t.Fatalf("model = %v", captured["model"])
	}
	if len(resp.Message.ToolCalls) != 1 {
		t.Fatalf("tool calls len = %d", len(resp.Message.ToolCalls))
	}
	if resp.Message.ToolCalls[0].Function.Name != "calculator" {
		t.Fatalf("tool name = %q", resp.Message.ToolCalls[0].Function.Name)
	}
}
```

- [ ] **Step 2: 运行测试，确认失败**

Run:

```bash
go test ./internal/llms -run TestOpenAICompatibleProviderSendsChatCompletionPayload -v
```

Expected: FAIL，原因是 OpenAI-compatible provider 尚未实现。

- [ ] **Step 3: 实现 `internal/llms/openai_compatible.go`**

```go
package llms

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type OpenAICompatibleProvider struct {
	baseURL string
	apiKey  string
	model   string
	client  *http.Client
}

type openAIChatRequest struct {
	Model    string          `json:"model"`
	Messages []openAIMessage `json:"messages"`
	Tools    []openAITool    `json:"tools,omitempty"`
}

type openAIMessage struct {
	Role       string           `json:"role"`
	Content    string           `json:"content,omitempty"`
	ToolCalls  []openAIToolCall `json:"tool_calls,omitempty"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
}

type openAITool struct {
	Type     string             `json:"type"`
	Function openAIToolFunction `json:"function"`
}

type openAIToolFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters"`
}

type openAIToolCall struct {
	ID       string                 `json:"id"`
	Type     string                 `json:"type"`
	Function openAIToolCallFunction `json:"function"`
}

type openAIToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type openAIChatResponse struct {
	Choices []openAIChoice `json:"choices"`
}

type openAIChoice struct {
	Message openAIMessage `json:"message"`
}

func NewOpenAICompatibleProvider(cfg ProviderConfig) (Provider, error) {
	if cfg.BaseURL == "" {
		return nil, fmt.Errorf("openai-compatible base_url is required")
	}
	if cfg.TimeoutSeconds <= 0 {
		cfg.TimeoutSeconds = 60
	}
	return &OpenAICompatibleProvider{
		baseURL: strings.TrimRight(cfg.BaseURL, "/"),
		apiKey:  cfg.APIKey,
		model:   cfg.Model,
		client:  &http.Client{Timeout: time.Duration(cfg.TimeoutSeconds) * time.Second},
	}, nil
}

func (p *OpenAICompatibleProvider) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	model := req.Model
	if model == "" {
		model = p.model
	}

	body, err := json.Marshal(openAIChatRequest{
		Model:    model,
		Messages: toOpenAIMessages(req.Messages),
		Tools:    toOpenAITools(req.Tools),
	})
	if err != nil {
		return nil, fmt.Errorf("marshal openai chat request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create openai chat request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if p.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)
	}

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("send openai chat request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("openai chat returned status %d", resp.StatusCode)
	}

	var decoded openAIChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return nil, fmt.Errorf("decode openai chat response: %w", err)
	}
	if len(decoded.Choices) == 0 {
		return nil, fmt.Errorf("openai chat returned empty choices")
	}

	return &ChatResponse{Message: fromOpenAIMessage(decoded.Choices[0].Message)}, nil
}

func toOpenAIMessages(messages []Message) []openAIMessage {
	out := make([]openAIMessage, 0, len(messages))
	for _, msg := range messages {
		out = append(out, openAIMessage{
			Role:       string(msg.Role),
			Content:    msg.Content,
			ToolCalls:  toOpenAIToolCalls(msg.ToolCalls),
			ToolCallID: msg.ToolCallID,
		})
	}
	return out
}

func toOpenAITools(tools []Tool) []openAITool {
	out := make([]openAITool, 0, len(tools))
	for _, tool := range tools {
		out = append(out, openAITool{
			Type: tool.Type,
			Function: openAIToolFunction{
				Name:        tool.Function.Name,
				Description: tool.Function.Description,
				Parameters:  tool.Function.Parameters,
			},
		})
	}
	return out
}

func toOpenAIToolCalls(calls []ToolCall) []openAIToolCall {
	out := make([]openAIToolCall, 0, len(calls))
	for _, call := range calls {
		out = append(out, openAIToolCall{
			ID:   call.ID,
			Type: call.Type,
			Function: openAIToolCallFunction{
				Name:      call.Function.Name,
				Arguments: call.Function.Arguments,
			},
		})
	}
	return out
}

func fromOpenAIMessage(msg openAIMessage) Message {
	return Message{
		Role:       Role(msg.Role),
		Content:    msg.Content,
		ToolCalls:  fromOpenAIToolCalls(msg.ToolCalls),
		ToolCallID: msg.ToolCallID,
	}
}

func fromOpenAIToolCalls(calls []openAIToolCall) []ToolCall {
	out := make([]ToolCall, 0, len(calls))
	for _, call := range calls {
		out = append(out, ToolCall{
			ID:   call.ID,
			Type: call.Type,
			Function: ToolCallFunction{
				Name:      call.Function.Name,
				Arguments: call.Function.Arguments,
			},
		})
	}
	return out
}
```

- [ ] **Step 4: 运行测试，确认通过**

Run:

```bash
go test ./internal/llms -run TestOpenAICompatibleProviderSendsChatCompletionPayload -v
```

Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add internal/llms/openai_compatible.go internal/llms/openai_compatible_test.go
git commit -m "feat: add openai compatible provider"
```

---

### Task 6: CLI 组装和最终验证

**Files:**
- Modify: `cmd/cli/main.go`
- Optional Test: use existing package tests.

- [ ] **Step 1: 实现 `cmd/cli/main.go`**

```go
package main

import (
	"context"
	"fmt"
	"log"

	"harukizmoe/pimoe/internal/agent"
	"harukizmoe/pimoe/internal/config"
	"harukizmoe/pimoe/internal/llms"
	"harukizmoe/pimoe/internal/tools"
)

func main() {
	cfg, err := config.Load("configs/providers.yaml")
	if err != nil {
		log.Fatal(err)
	}

	providerName := cfg.LLMs.DefaultProvider
	providerConfig, ok := cfg.LLMs.Providers[providerName]
	if !ok {
		log.Fatalf("unknown default provider %q", providerName)
	}

	llmRegistry := llms.NewRegistry()
	llmRegistry.Register("fake", llms.NewFakeProvider)
	llmRegistry.Register("openai_compatible", llms.NewOpenAICompatibleProvider)

	provider, err := llmRegistry.NewProvider(providerConfig)
	if err != nil {
		log.Fatal(err)
	}

	toolRegistry := tools.NewRegistry()
	toolRegistry.Register(tools.Calculator{})

	a := agent.New(provider, toolRegistry, providerConfig.Model)
	answer, err := a.Run(context.Background(), "use calculator to compute 13 * 7")
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println(answer)
}
```

- [ ] **Step 2: 运行所有当前测试**

Run:

```bash
go test ./internal/config ./internal/llms ./internal/tools ./internal/agent
```

Expected: PASS。

- [ ] **Step 3: 运行 CLI fake provider smoke test**

Run:

```bash
go run ./cmd/cli
```

Expected output contains:

```text
13 * 7 = 91
```

- [ ] **Step 4: 格式化**

Run:

```bash
gofmt -w cmd/cli internal/config internal/llms internal/tools internal/agent
```

Expected: 命令退出码为 0。

- [ ] **Step 5: 运行最终验证**

Run:

```bash
go test ./...
```

Expected: PASS。

Run:

```bash
go run ./cmd/cli
```

Expected output contains:

```text
13 * 7 = 91
```

- [ ] **Step 6: 提交**

```bash
git add cmd/cli internal configs go.mod go.sum
git commit -m "feat: wire cli tool calling demo"
```

---

## 自检

- 已覆盖配置读取、LLM 类型、Provider Registry、fake provider、OpenAI-compatible provider、tools registry、calculator、agent loop、CLI wiring。
- 没有新增 `internal/ai`。
- 当前阶段不实现 HTTP、数据库、memory、streaming、Responses API。
- Fake provider 提供无网络验证路径。
- OpenAI-compatible provider 通过 `httptest` 验证协议边界。
