# 定义受治理的工具执行模型

Type: grilling
Status: resolved
Blocked by: 01, 02

## Question

在保留现有 tool-calling loop 的前提下，Runtime 怎样定义 tool 的声明、选择、执行、超时、错误公开面和审计信息？确定哪些权限和人工确认能力是 Runtime 必需的，哪些必须留给未来业务适配层。

## Answer

Tool execution 的核心 seam 是 request-scoped `Allowed Tool` 集合：Application Service 根据 actor、Agent 类型和业务策略选出本次 Run 可见且可执行的工具，再把工具声明与执行 capability 放入 `RunRequest`。Runtime 不读取全局 Registry 中未授权的工具，也不内建用户、租户或业务权限模型。

### Tool 声明与授权

- Allowed Tool 至少包含稳定 name/version、description、JSON Schema、side-effect/concurrency policy、timeout policy 和 executor。
- 模型只能看到 Allowed Tool 的 schema；模型请求未知或未授权名称时，不执行 executor，而生成与该 tool-call 配对的 `denied`/`unknown_tool` 结果，让 loop 可以继续或正常结束。
- executor 使用调用方注入的 request-scoped opaque capability。凭据、session token、业务 actor 和连接细节不进入 schema、model message、transcript 或通用事件。

### Approval 与执行

- 具有副作用或策略标记为 `requires_approval` 的调用，在执行前经过 request-scoped `ApprovalGate`。
- Runtime 发出 approval requested/decided 生命周期事件，并在同一 Run 的 context 内等待 Gate；不创建跨 Run 的 suspended 状态或 pending store。
- Gate 缺失或拒绝时生成 `denied` Tool Result，不调用 executor；取消通过 context 结束等待。
- 默认每批工具串行、exclusive、无自动重试。只有明确声明 read-only/shared 且实现线程安全的工具才允许并行；只有 policy 明确声明幂等和重试策略时才可重试。
- 每次调用的 effective timeout 是 Run deadline 与 Tool policy timeout 中更短者。超时、取消和未执行调用都必须生成配对结果，不能留下悬空 assistant tool-call。

### 结果与错误

每个 tool-call 必须得到结构化 Tool Result，状态至少区分 `success`、`error`、`denied`、`timeout`、`canceled` 和 `skipped`。结果分为两路：

- **model content**：安全、短小、可继续推理的摘要；失败时不能包含凭据、内部 URL、原始响应体或堆栈。
- **internal details/error**：供可信事件消费者、日志和审计使用，默认不写入 model transcript。

参数 JSON 校验失败、工具不存在、ApprovalGate 拒绝和 executor 异常都必须转换为可观察的 Tool Result；不能把 executor 错误直接升级成无法区分的 Runtime 崩溃。真正的 Run 终态仍由 Runtime 以 `completed`、`failed` 或 `canceled` 表示。

### 审计与隐私

每次调用的最小审计元数据包括：run_id、tool_call_id、稳定 tool name/version、授权与审批决定、status、开始/结束时间，以及参数/输出 digest。默认不记录 secrets、完整凭据、原始响应或完整参数正文。HTTP/SSE 默认只暴露安全结果和非敏感状态；可信适配层才能消费内部 details。

Application Service 负责“谁能使用什么工具”和 capability 的创建；Runtime 负责“已授权工具如何按统一协议执行、超时、审批、结果化和发出审计事件”；具体业务 Tool 负责自身业务操作，不负责改变 Runtime 事件契约。
