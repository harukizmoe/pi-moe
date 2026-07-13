# 定义 Agent Runtime 核心契约

Type: grilling
Status: resolved

## Question

在 PiMoe 中，什么能力属于通用 Agent Runtime，什么能力必须留在 session、application 或未来业务适配层？定义一个深 module 的 interface：调用方需要提供什么、能观察什么事件、需要遵守哪些状态和取消约束，以及哪些实现细节必须隐藏。

## Answer

PiMoe 的 Agent Runtime 是一个只执行单次受控运行的深 module，不拥有 session、业务对象或持久化状态。

- **Interface**：只保留单一流式入口，概念上为 `Run(ctx, RunRequest) -> typed event stream`；不同时维护同步与流式两套运行语义。
- **输入**：调用方传入不可变、已授权的 `RunRequest`，其中包含 conversation、instructions 与 allowed tools。Runtime 不接收 session ID，不读取数据库，也不自行加载业务上下文；它可以按后续确定的 context policy 处理这份输入。
- **输出**：事件流包含生命周期、模型增量与工具执行事件，并且每次运行必须以 `completed`、`failed` 或 `canceled` 之一恰好结束。取消是独立终态，不伪装为普通错误。
- **取消与并发**：取消唯一通过调用方传入的 `context.Context` 传播。Runtime 不维护跨请求 run registry 或 `Cancel(runID)` interface；同一会话 turn 的互斥、取消句柄与持久化提交由 session 持有。
- **职责分配**：session 负责 transcript、恢复、持久化和根据终态提交或回滚；application 负责 HTTP/CLI/SSE 协议转换与业务适配；未来业务层负责身份、权限和业务数据。Runtime 只编排模型与已获准工具，具体工具治理留给“定义受治理的工具执行模型”。

这让 Runtime 的 interface 保持小，而 session、application 与未来业务适配能在各自 seam 内保持 locality。
