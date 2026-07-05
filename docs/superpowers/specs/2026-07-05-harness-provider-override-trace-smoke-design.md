# Harness Provider Override and CLI Smoke 设计

## 背景

本项目的目标不是交付一个 CLI 程序，而是为后续业务开发沉淀可复用的 Agent 能力：配置读取、Provider 装配、tool-calling 主循环、工具注册、结构化 trace、错误恢复和后续 HTTP/business 入口。

CLI 只是当前阶段的 smoke driver：用最小入口验证 Harness 是否能把 `config -> llms -> agent -> tools -> final answer` 串起来。CLI 不应拥有业务编排逻辑，也不应绕过 Harness 直接组装 Provider、Tools 或 Agent。

当前状态：

```text
internal/harness
  -> 读取 configs/providers.yaml
  -> 按 llms.default_provider 构造 Provider
  -> 注册 calculator
  -> 构造 Agent 并返回 RunResult

cmd/cli
  -> 固定读取 configs/providers.yaml
  -> 固定使用 default provider
  -> 固定隐藏 RunResult trace
```

`configs/providers.yaml` 已包含 `fake`、`openai`、`deepseek`、`moeco` 等实例。下一步应完善 Harness 的可测试装配能力：允许调用方选择 provider，并让 CLI 作为临时 smoke 入口暴露这个能力。

OMP 可借鉴的经验是分层：CLI/上层入口只表达用户意图；Provider/config/env key 边界属于 AI 装配层；业务入口复用同一个 Harness，而不是复制装配逻辑。当前不直接学习 OMP 的 streaming 大系统，只学习最小可落地的 provider 选择与 trace 可观测性。

## 目标

- Harness 支持按实例名覆盖默认 provider，例如 `fake`、`moeco`、`deepseek`。
- Harness 保持默认行为：未指定 provider 时继续使用 `llms.default_provider`。
- CLI 作为 smoke driver 支持传入 provider override 和 trace 开关，用来验证 Harness。
- CLI 保持薄入口：只解析参数、读取输入、调用 Harness、格式化输出。
- trace 输出复用 `RunResult.Steps`，便于确认真实模型是否完成 tool-calling 循环。

## 非目标

- 不把 CLI 发展成正式产品入口。
- 不引入 Cobra、urfave/cli 等 CLI 框架。
- 不新增 HTTP API、数据库、memory、session 或 streaming。
- 不实现 OMP 的完整 tool choice、schema coercion、auth broker 或 provider in-flight limit。
- 不把真实 Provider smoke 写成依赖真实 API Key 的自动化测试。
- 不改变 `internal/llms.Provider` 的最小 `Chat` 接口。

## 方案选择

### 方案 A：Harness-first provider override

`internal/harness.Config` 增加 `ProviderName`。Harness 继续负责配置读取、Provider registry、tool registry 和 Agent 构造。CLI 只把 `--provider` 传给 Harness。

优点：符合项目目标；后续 HTTP/business 入口可复用同一个 Harness；CLI 不复制装配逻辑。
缺点：需要给 Harness 增加一个字段和少量错误分支。

### 方案 B：CLI-centered provider selection

CLI 直接读取配置、查找 provider、注册工具和构造 Agent。

优点：短期实现直观。
缺点：把 smoke driver 做成了主入口；后续 HTTP/business 入口还要再复制一遍装配逻辑。

### 方案 C：只改 YAML default_provider

用户通过编辑 `configs/providers.yaml` 切换 provider。

优点：零代码。
缺点：不适合反复 smoke；容易把本地临时选择提交进配置；不能覆盖未来业务入口的调用需求。

选择：**方案 A**。

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

Harness 仍然是业务入口可复用的最小装配层；CLI、后续 HTTP API、测试 harness 都应通过它进入 Agent。

## CLI smoke 设计

CLI 新增标准库 `flag.FlagSet` 解析逻辑，保持 `main` 简单。

支持参数：

```text
--config <path>     配置文件路径，默认 configs/providers.yaml
--provider <name>   传给 harness.Config.ProviderName；为空则使用配置默认值
--trace             输出 RunResult.Steps
```

示例：

```bash
go run ./cmd/cli --provider fake --trace "use calculator to compute 13 * 7"
```

默认输出：

```text
<final answer>
```

开启 trace 后：

```text
<final answer>
tool=<name> arguments=<json> result=<text>
```

如果工具失败但模型恢复，trace 使用现有格式输出 `error=<message>`。

CLI 不直接读取 `internal/config`，不直接创建 `internal/llms.Registry`，不直接注册工具。

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
- `main` 组装时只把解析结果传给 Harness；不在 CLI 层复制 provider/tool 装配。
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
- `--trace` 会输出工具参数和结果；开发期 smoke 可接受，但不默认开启。
- provider override 是实例名，不是实现类型；例如 `moeco` 是实例名，`openai_compatible` 是实现类型。
- 本任务只完善 Harness 装配能力和 CLI smoke；后续业务入口应继续复用 Harness，而不是以 CLI 为中心扩展。
- 后续若引入多个工具，需要再学习 OMP 的 tool argument validation 和 schema strictness，而不是在本任务里提前实现。
