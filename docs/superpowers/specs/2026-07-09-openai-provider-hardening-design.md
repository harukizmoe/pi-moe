# OpenAI-compatible Provider Hardening 设计

## 背景

当前项目已经具备 CLI、HTTP session、SSE、managed resume 和 fake provider 的确定性闭环。下一处短板是真实 OpenAI-compatible Provider 的生产化边界：当 Provider 返回非 2xx、畸形 SSE、异常 tool call 或请求超时时，错误必须稳定、可测试、可诊断，并且不能泄露 API Key。

本设计只硬化 `internal/llms` 的协议适配层，不新增上层 API，不改变 CLI/HTTP contract，不引入真实网络 smoke。

## 目标

- 让 `internal/llms/openai_compatible.go` 对 OpenAI-compatible `/chat/completions` 的错误边界稳定可预测。
- 用 `httptest.Server` 覆盖 HTTP 状态、请求失败、SSE 协议异常和 tool call 参数异常。
- 保持现有 provider-neutral `Provider` 接口不变。
- 保持错误文案无密钥泄露、无 header 泄露、可断言。

## 非目标

- 不新增 `ProviderError` 或跨包错误 code 抽象。
- 不改 `/v1/providers/current` response。
- 不新增 HTTP endpoint 或 CLI flag。
- 不做真实 OpenAI、DeepSeek、OpenRouter 等网络 smoke。
- 不做 retry、rate limit backoff、provider/model discovery。
- 不做 Responses API。

## 设计决策

### 范围

改动集中在：

```text
internal/llms/openai_compatible.go
internal/llms/openai_compatible_test.go
```

只在文件内补小 helper。除非测试暴露真实上层缺口，否则不改 `internal/application`、`cmd/cli` 或 `cmd/server`。

### 错误模型

Provider 对外仍返回普通 `error`。本轮不引入结构化错误类型，避免过早牵动 agent、session、HTTP handler 和 CLI 输出。

错误文案必须满足：

- 包含 provider 类型上下文，例如 `openai-compatible`。
- 包含失败阶段，例如 request、status、stream chunk、tool call arguments。
- 不包含 API Key、Authorization header 或完整请求配置。
- 对 body 只使用截断 excerpt。

### 配置行为

- `base_url` 为空继续在 `NewOpenAICompatibleProvider` 返回错误。
- `timeout_seconds <= 0` 继续使用默认超时。
- `model` 为空暂不报错，避免破坏已有配置和测试；模型是否必填以后由配置诊断或真实 provider 策略决定。

### HTTP 状态错误

非 2xx 响应返回稳定错误：

```text
openai-compatible chat completions failed: status <code>: <body excerpt>
```

要求：

- JSON body 和 plain text body 都作为 excerpt。
- 空 body 返回明确占位，例如 `<empty body>`。
- 长 body 截断到固定长度。
- 不读取或暴露 response header。

### 请求失败

`doChatCompletions` 对 `http.Client.Do` 错误加阶段上下文：

```text
openai-compatible chat completions request: <underlying error>
```

`context.Canceled` 和 `context.DeadlineExceeded` 通过 error wrapping 保留 `errors.Is` 可识别性。

### SSE 协议错误

Streaming 读取继续支持当前行为：

- `[DONE]` 正常完成。
- 有 `finish_reason` 但没有 `[DONE]` 时，保持现有兼容：认为完成。
- usage-only chunk 继续忽略。

需要硬化：

- malformed JSON chunk 返回 `parse openai-compatible stream chunk: ...`。
- provider error payload 返回 `openai-compatible stream error: <message>`。
- stream 结束但没有 done/finish reason 返回 `openai-compatible stream ended before completion`。
- SSE 行读取错误带阶段上下文。

### Tool call 流式合并

继续保留已有兼容：

- tool call delta 缺失 `type` 时默认为 `function`。
- 多 chunk arguments 可增量拼接。

需要硬化：

- delta index 越界返回明确错误。
- tool call 完成后 arguments 非合法 JSON 返回明确错误。
- 缺少必要 id/name 时返回明确错误，避免生成不可执行 tool call。

## 测试矩阵

全部测试使用 `httptest.Server` 或内存 reader，不访问真实网络。

### HTTP status

- 401 + JSON body：错误包含 status 和 excerpt。
- 429 + plain text body：错误包含 status 和 excerpt。
- 500 + 空 body：错误包含 `<empty body>`。
- 长 body：错误被截断。

### Request

- server 延迟超过 provider timeout：返回 request 阶段错误，并保留 deadline 语义。
- 已取消 context：返回 request 阶段错误，并保留 canceled 语义。

### SSE

- malformed JSON chunk：返回 parse 阶段错误。
- provider error payload：返回 stream error 文案。
- 只有 usage chunk 后连接结束：返回 ended before completion。
- 有 finish_reason 但无 `[DONE]`：继续成功。

### Tool calls

- 多 chunk arguments 合并后合法 JSON：成功生成 tool call。
- 合并后非法 JSON：返回 arguments 错误。
- 缺失 type：继续规范化为 `function`。
- 空 tool result content：继续序列化为空字符串，不丢字段。

## 验收命令

最小验证：

```bash
go test ./internal/llms -count=1
go test ./internal/application/service ./internal/application/router ./cmd/cli -count=1
go vet ./...
```

完整验证：

```bash
go test -count=1 ./...
go test -race -count=1 ./...
```

## 风险与约束

- 不引入结构化错误类型意味着上层仍只能展示字符串；这是有意取舍，避免当前阶段过早抽象。
- 不做真实网络 smoke 意味着不能证明某个具体供应商当前在线可用；本轮只证明协议边界和失败处理。
- 如果测试发现现有错误文案已被上层依赖，优先保持兼容，只补更明确的内部阶段上下文。
