# Task 6 报告：CLI 组装和最终验证

## 任务范围
- 实现 `cmd/cli/main.go`
- 复用现有 `config`、`llms`、`tools`、`agent` 包完成 CLI 闭环组装
- 运行 `/.superpowers/sdd/task-6-brief.md` 指定的验证命令并记录结果
- 不实现 HTTP router、数据库、memory、streaming、Responses API 或额外功能

## RED 证据
### 先跑 CLI smoke
```bash
go run ./cmd/cli
```

### RED 输出
```text
no Go files in /home/harukiz/workspace/agent/pimoe-demo/cmd/cli
```

结论：`cmd/cli` 尚未完成组装，CLI 闭环不存在；先失败再实现，满足本任务的最小 TDD/烟雾验证要求。

## 实现说明
- 新增 `cmd/cli/main.go`，按任务 brief 组装最小依赖链：
  - `config.Load("configs/providers.yaml")`
  - 读取 `cfg.LLMs.DefaultProvider`
  - 用 `llms.Registry` 注册 `fake` 与 `openai_compatible`
  - 用默认 provider 配置创建 provider 实例
  - 用 `tools.Registry` 注册 `tools.Calculator{}`
  - 用 `agent.New(...)` 执行 `use calculator to compute 13 * 7`
  - 输出最终 answer
- 保持实例名与实现类型解耦：先取默认 provider 实例名，再通过 `ProviderConfig.Type` 走 registry。
- 只复用现有包能力，没有向 `internal/config`、`internal/llms`、`internal/tools`、`internal/agent` 引入额外行为。

## 验证记录
### 1. 包级验收命令
```bash
go test ./internal/config ./internal/llms ./internal/tools ./internal/agent
```

```text
go test: 4 packages ok
```

### 2. 首次 CLI smoke
```bash
go run ./cmd/cli
```

```text
13 * 7 = 91
```

### 3. gofmt
```bash
gofmt -w cmd/cli internal/config internal/llms internal/tools internal/agent
```

```text
(exit 0, no output)
```

### 4. 最终全量测试
```bash
go test ./...
```

```text
go test: 4 packages ok, 1 no tests
```

### 5. 最终 CLI smoke
```bash
go run ./cmd/cli
```

```text
13 * 7 = 91
```

## 变更文件
- `cmd/cli/main.go`
- `.superpowers/sdd/task-6-report.md`

## 注释合规说明
- 本任务未新增导出标识符。
- `main.go` 中仅在两个关键内部步骤添加中文注释：
  - 默认 provider 实例名与实现类型解耦
  - CLI 只接 calculator 作为最小闭环验证
- 没有加入逐行噪声注释。

## 自检 / Self-review
- 只修改了 CLI 入口，没有改动既有业务包行为，符合“wiring/smoke validation”范围。
- 默认配置仍是 `fake` provider，因此 `go run ./cmd/cli` 可无网络输出稳定结果 `13 * 7 = 91`。
- CLI 入口对缺失默认 provider 配置给出明确错误，避免静默 fallback。
- Provider registry 同时注册 `fake` 和 `openai_compatible`，与现有配置结构保持一致。
- 已按要求执行包级测试、首次 smoke、gofmt、`go test ./...`、最终 smoke。

## 提交信息
- 见本任务最终提交记录。
