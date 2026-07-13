# PiMoe Agent Runtime

PiMoe 通用后端 Agent 的核心语言。该上下文区分单次执行、会话历史、上下文压缩和可跨运行检索的长期记忆，避免把不同生命周期的状态都称为 memory。

## Language

**Agent Runtime**:
只执行一次受控 Run 的无持久状态 module；消费不可变 RunRequest，并输出 typed event stream。
_Avoid_: Agent service, session runtime, harness

**Run**:
从一个不可变 RunRequest 开始，并以 completed、failed 或 canceled 之一恰好结束的单次 Agent 执行。
_Avoid_: Task, session, conversation

**Conversation Transcript**:
一个 session 中已经完成的 user、assistant 和 tool-result 运行事实；用于恢复会话，但不等同于长期记忆。
_Avoid_: Memory, context

**Context Summary**:
为满足模型上下文预算而替换一段旧 Conversation Transcript 的可重放摘要；保留来源范围，但不自动成为长期记忆。
_Avoid_: Memory, transcript

**Long-term Memory**:
经调用方策略确认、带 provenance、可按外部 scope 跨 Run 检索的稳定事实；它是不可信 context data，不是 instruction。
_Avoid_: Transcript, context summary, model knowledge

**Memory Item**:
已经提交到外部 MemoryStore 的版本化长期记忆记录，包含稳定 key、scope、provenance 和生命周期元数据。
_Avoid_: Message, summary

**Memory Candidate**:
Runtime 在事件流中提出、尚未持久化的长期记忆 upsert 或 forget 建议；只有 completed Run 的候选可由调用方确认提交。
_Avoid_: Memory Item, automatic memory

**Allowed Tool**:
由调用方为单次 Run 授权并注入的工具声明与 executor；只有它能被模型看到和被 Runtime 执行。
_Avoid_: global tool, available tool

**Approval Gate**:
在具有副作用的 Allowed Tool 执行前，由调用方提供的 request-scoped 审批决定接口；它不创建跨 Run 的挂起状态。
_Avoid_: tool prompt, suspended run

**Tool Result**:
与一个 tool-call 配对的结构化执行结果，分离 model content 与 internal details，并区分 success、error、denied、timeout、canceled 和 skipped。
_Avoid_: tool error, raw tool output
