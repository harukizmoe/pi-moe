# BaseSystemPrompt 与 SessionPrompt 设计

## 背景

当前实现把 `SystemPrompt` 同时用于项目级默认指令和 managed session 可恢复指令，语义混在一起。用户期望项目级 prompt 固定不变，用户另行设置会话级 prompt。

参考 OMP 的设计，系统提示词应分层：基础系统提示词是项目/产品级底座，运行时可追加项目上下文或会话指令，但不能把会话指令误认为唯一 system prompt。

项目级配置统一放入 `configs/`：YAML 等结构化配置放在 `configs/*.yaml`，prompt 文本放在 `configs/prompts/*.md`。

## 目标

- 将项目级固定提示词命名为 `BaseSystemPrompt`。
- 将会话级用户提示词命名为 `SessionPrompt`。
- `BaseSystemPrompt` 不写入 session metadata，不被 `--resume` 覆盖。
- `SessionPrompt` 写入 session metadata，恢复 session 时继续生效。
- Provider 请求中同时注入 `BaseSystemPrompt` 和可选 `SessionPrompt`。
- CLI/API 公共字段使用 `session_prompt` / `--session-prompt`，不再暴露 `system_prompt` 作为会话配置名。

## 非目标

- 不实现多 prompt 文件合并、模板变量、Handlebars 或 prompt registry。
- 不实现用户系统、用户级 Provider、用户级 BaseSystemPrompt。
- 不长期保留 `--system-prompt`、`system_prompt`、`SystemPrompt` alias。
- 不把完整 prompt 文本返回给 HTTP list/detail。

## 命名与语义

### BaseSystemPrompt

`BaseSystemPrompt` 是项目级固定系统底座：

- 来源：当前阶段通过 service/CLI config 字段传入；后续项目级 prompt 文件放在 `configs/prompts/*.md`。
- 生命周期：随项目/服务配置变化，不随 session 创建或恢复变化。
- 存储：不进入 session metadata 或 transcript。
- 权限：用户恢复会话时不能用 session 覆盖它。

Go 字段：

```go
type Options struct {
    BaseSystemPrompt string
}

type Config struct {
    BaseSystemPrompt string
}
```

### SessionPrompt

`SessionPrompt` 是会话级用户指令：

- 来源：CLI `--session-prompt` 或 HTTP `session_prompt`。
- 生命周期：属于 managed session，随 session metadata 保存和恢复。
- 存储：进入 `session.SessionConfig`，不进入 transcript。
- 权限：用户可在 resume/run 时显式覆盖；运行成功后更新 session metadata。

Go 字段：

```go
type SessionConfig struct {
    ProviderName   string `json:"provider_name,omitempty"`
    SessionPrompt string `json:"session_prompt,omitempty"`
    MaxSteps      int    `json:"max_steps,omitempty"`
}
```

## Prompt 组合规则

Agent 在发给 Provider 前组合 prompt：

1. 裁剪 `BaseSystemPrompt` 和 `SessionPrompt` 两端空白。
2. 两者都为空：不注入 system message。
3. 只有 `BaseSystemPrompt`：注入它。
4. 只有 `SessionPrompt`：注入它，并用标题标明来源。
5. 两者都有：合并成一条 system message。

合并格式：

```text
<BaseSystemPrompt>

Session prompt:
<SessionPrompt>
```

选择合并为一条 system message，而不是多条 system message，原因是 OpenAI-compatible Provider 兼容性更稳。

## CLI 行为

公开 flag：

```bash
--session-prompt <text>
```

行为：

- 一次性运行：`--session-prompt` 只影响本次运行，不持久化。
- `--new-session --session-prompt "..."`：创建 session 并保存 `session_prompt`。
- `--resume <id>`：使用 session metadata 中保存的 `session_prompt`。
- `--resume <id> --session-prompt "..."`：本轮使用新值；运行成功后更新 session metadata。
- `--list-sessions` 不能与 `--session-prompt` 组合。

`--system-prompt` 不再作为会话 prompt 入口。

## HTTP 行为

Create request 使用：

```json
{
  "title": "demo",
  "provider_name": "deepseek",
  "session_prompt": "用中文简短回答",
  "max_steps": 4
}
```

List/detail response 只返回摘要：

```json
{
  "provider_name": "deepseek",
  "max_steps": 4,
  "has_session_prompt": true
}
```

不返回完整 `session_prompt`，避免泄露用户会话指令。

## 数据迁移与兼容

当前分支尚未发布稳定外部 API，采用 clean cutover：

- Go 字段 `SystemPrompt` 改为 `BaseSystemPrompt` 或 `SessionPrompt`。
- Session metadata JSON 字段从 `system_prompt` 改为 `session_prompt`。
- HTTP 字段从 `system_prompt` 改为 `session_prompt`。
- CLI flag 从 `--system-prompt` 改为 `--session-prompt`。

不保留旧字段 alias，避免继续传播错误语义。

## 项目级配置约定

所有后续项目级配置统一放在 `configs/`：

- Provider 配置：`configs/*.yaml`，当前为 `configs/providers.yaml` 或 example 文件。
- Prompt 文本：`configs/prompts/*.md`。
- 不在业务包中散落项目级 prompt 文本。
- 代码可以先通过字段接收 `BaseSystemPrompt`，但持久项目默认值后续应从 `configs/prompts/*.md` 读取。

## 测试计划

最小证明：

- `internal/agent`
  - 只有 `BaseSystemPrompt` 时 provider 请求包含 system message。
  - 只有 `SessionPrompt` 时 provider 请求包含标记后的 session prompt。
  - 两者都有时合并为一条 system message，顺序为 Base 后 Session。
  - transcript 不包含任何 system message。
- `internal/session`
  - `SessionConfig.SessionPrompt` 持久化到 `session_prompt`。
  - 创建/更新时裁剪空白。
- `internal/application/service`
  - 创建 session 保存 `SessionPrompt`，不保存 `BaseSystemPrompt`。
  - run/stream 使用 service config 的 `BaseSystemPrompt` 和 session metadata 的 `SessionPrompt`。
  - resume/run 显式 `SessionPrompt` override 成功后更新 metadata。
- `cmd/cli`
  - `--session-prompt` 可解析。
  - `--new-session --session-prompt` 保存 session prompt。
  - `--resume` 恢复 session prompt。
  - `--resume --session-prompt` 成功后更新 prompt。
  - `--list-sessions --session-prompt` 报错。
- `internal/application/router`
  - create 接收 `session_prompt`。
  - list/detail 返回 `has_session_prompt`，不返回完整文本。

验证命令：

```bash
go test ./internal/agent ./internal/session ./internal/application/service ./internal/application/router ./cmd/cli -count=1
go test -count=1 ./...
```
