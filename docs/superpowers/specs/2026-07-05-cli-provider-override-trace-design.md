# CLI Provider Override and Trace Smoke 设计

## 背景

当前项目已经跑通 `config -> llms -> agent -> tools -> final answer` 的 fake provider 闭环，并实现了 typed Agent history、tool error 回传模型恢复、Harness typed-message API。

CLI 入口仍然偏固定：

```text
cmd/cli/main.go
  -> 固定读取 configs/providers.yaml
  -> 使用配置里的 llms.default_provider
  -> 固定隐藏 RunResult trace
```

`configs/providers.yaml` 已包含 `fake`、`openai`、`deepseek`、`moeco` 等实例，但 CLI 没有直接选择 provider 的入口。下一步应先让真实 Provider smoke 验证变得可控，再考虑 HTTP、memory、streaming 等后续能力。

OMP 可借鉴的经验是分层：provider/config/env key 边界放在 AI 层和装配层；CLI 只负责解析用户意图，不绕过底层注册与配置。当前不直接学习 OMP 的 streaming 大系统，只学习最小可落地的 provider 选择与 trace 可观测性。

## 目标

- CLI 支持通过参数覆盖配置文件路径。
- CLI 支持通过参数选择 provider 实例，例如 `fake`、`moeco`、`deepseek`。
- CLI 支持按需输出 tool trace，方便验证真实模型是否完成 tool-calling 循环。
- Harness 支持 provider override，让 CLI 不直接操作 LLM registry 细节。
- 保持默认行为不变：无参数时继续读取 `configs/providers.yaml` 并使用默认 provider，输出只包含最终回答。

## 非目标

- 不引入 Cobra、urfave/cli 等 CLI 框架。
- 不新增 HTTP API、数据库、memory、session 或 streaming。
- 不实现 OMP 的完整 tool choice、schema coercion、auth broker 或 provider in-flight limit。
- 不把真实 Provider smoke 写成依赖真实 API Key 的自动化测试。
- 不改变 `internal/llms.Provider` 的最小 `Chat` 接口。

## 方案选择

### 方案 A：CLI 直接读配置并手动选择 Provider

CLI 在 `cmd/cli` 里读取配置、查找 provider、注册工具和构造 Agent。

优点：实现直观。
缺点：CLI 会复制 Harness 的装配逻辑，破坏当前边界。

### 方案 B：Harness 增加 `ProviderName`，CLI 只解析 flag

`cmd/cli` 负责解析 `--config`、`--provider`、`--trace`，然后传入 `harness.Config`。Harness 继续负责配置读取、Provider registry、tool registry 和 Agent 构造。

优点：边界清楚；复用现有 Harness；测试集中。
缺点：需要给 Harness 增加一个字段和少量错误分支。

### 方案 C：只改 YAML default_provider，不改 CLI

用户通过编辑 `configs/providers.yaml` 切换 provider。

优点：零代码。
缺点：不适合 smoke 验证；容易把本地临时选择提交进配置。

选择：**方案 B**。

## CLI 设计

新增标准库 `flag.FlagSet` 解析逻辑，保持 `main` 简单。

支持参数：

```text
--config <path>     配置文件路径，默认 configs/providers.yaml
--provider <name>   覆盖 llms.default_provider；为空则使用配置默认值
--trace             输出 RunResult.Steps
```

示例：

```bash
go run ./cmd/cli --provider fake --trace "use calculator to compute 13 * 7"
```

输出规则：

```text
<final answer>
```

开启 trace 后：

```text
<final answer>
tool=<name> arguments=<json> result=<text>
```

如果工具失败但模型恢复，trace 使用现有格式输出 `error=<message>`。

## Harness 设计

`internal/harness.Config` 新增字段：

```go
type Config struct {
	ProviderConfigPath string
	ProviderName       string
	Logger             logger.Logger
	MaxSteps           int
	OnEvent            func(agent.Event)
}
```

Provider 选择规则：

```text
selectedProvider = cfg.ProviderName
if selectedProvider == "" {
  selectedProvider = loadedConfig.LLMs.DefaultProvider
}
```

错误规则：

- 配置文件读取失败：沿用现有错误包装。
- `ProviderName` 或 default provider 不存在：返回 `unknown provider "<name>"`。
- provider type 未注册：沿用 `unknown llm provider type "<type>"`。

CLI 不直接读取 `internal/config` 或 `internal/llms.Registry`。

## 测试设计

### `internal/harness`

新增测试：

- `ProviderName` 能覆盖 YAML 中的 `default_provider`。
- unknown provider override 返回清晰错误。
- 空 `ProviderName` 保持当前 default provider 行为。

### `cmd/cli`

新增或调整测试：

- flag parser 默认值为 `configs/providers.yaml`、空 provider、`trace=false`。
- `--config`、`--provider`、`--trace` 能正确解析，并保留剩余 prompt args。
- `formatRunResult(result, true)` 已有成功和错误 trace 测试；实现时复用，不重复测试内部格式细节。

### 手动 smoke

fake provider smoke：

```bash
go run ./cmd/cli --provider fake --trace "use calculator to compute 13 * 7"
```

真实 Provider smoke 不进入自动化测试；只在有 API Key 时手动运行，例如：

```bash
MOECO_API_KEY=... go run ./cmd/cli --provider moeco --trace "use calculator to compute 13 * 7"
```

## 验证要求

实现完成后必须运行：

```bash
go test ./cmd/cli ./internal/harness ./internal/llms
go test ./...
go vet ./...
```

如果 `golangci-lint` 已安装，再运行：

```bash
golangci-lint run
```

## 风险与边界

- 真实 Provider 是否会调用 calculator 取决于模型行为；自动化测试只验证协议和 fake 闭环。
- `--trace` 会输出工具参数和结果；开发期可接受，但不应默认开启。
- provider override 是实例名，不是实现类型；例如 `moeco` 是实例名，`openai_compatible` 是实现类型。
- 后续若引入多个工具，需要再学习 OMP 的 tool argument validation 和 schema strictness，而不是在本任务里提前实现。
