# Tau 可迁移 Agent Runtime 机制审计

## 范围与判定口径

本审计只读取 `../tau` 的一手实现与 Tau 自己的架构记录，聚焦四个问题：Agent loop 如何驱动模型与工具、上下文如何进入下一次 provider 请求、session/memory 如何恢复与分支、工具如何执行并把结果放回 transcript。Tau 自己把 `tau_agent` 定义为可复用 harness 层，把 `tau_coding` 定义为编码会话环境，把 TUI 定义为一种前端；该分层是本审计区分“通用 backend-agent 机制”和“编码 agent 机制”的依据。[`../tau/dev-notes/architecture/phase-4-agent-harness.zh.md:26-46`](../tau/dev-notes/architecture/phase-4-agent-harness.zh.md#L26-L46)

分类含义：

- **可迁移**：不依赖终端、文件系统、工作区、编辑器、项目资源或 TUI，可直接作为通用 backend Agent Runtime 的行为/边界参考。
- **有条件可迁移**：抽象本身通用，但 Tau 的触发策略、存储布局、模型配置或交互语义属于上层；迁移到 PiMoe 时只能迁移抽象和不变量，不能照搬 coding 侧实现。
- **编码 agent 专属**：直接依赖本地项目、cwd、文件/命令、编码 prompt 或 TUI/CLI；不应进入通用 backend Runtime。

PiMoe 已决定的目标契约是无状态 `Run(ctx, RunRequest) -> typed event stream`。下文把该契约视为既定事实：研究只说明哪些 Tau 机制能作为事件、状态重建和执行语义的参考，不提出 PiMoe 的代码或最终设计。

## 一、可迁移机制

### 1. 纯 loop：模型流式响应驱动的 turn 状态机

`run_agent_loop()` 接收 provider、模型名、system prompt、可变消息列表、工具列表、可选轮次上限、取消令牌和队列回调；它明确把消息 transcript 的所有权留给调用方，并在运行中追加 assistant/tool-result 消息，因此 loop 本身不保存持久 transcript 状态。[`../tau/src/tau_agent/loop.py:37-56`](../tau/src/tau_agent/loop.py#L37-L56) 这与 PiMoe 的无状态 Run 边界高度相容：每次 Run 可由 `RunRequest` 提供输入快照，由事件流输出过程事实；跨 Run 的状态不应隐含在 loop 对象里。[推断，依据同上]

每轮先发 `TurnStartEvent`，再调用 provider 的 `stream_response()`；provider 的 response start/text delta/thinking delta/retry/response end/error 被转换为 agent 层的消息或错误事件。[`../tau/src/tau_agent/loop.py:67-107`](../tau/src/tau_agent/loop.py#L67-L107) provider 接口本身只要求以模型、system、消息、工具和取消令牌流式返回 provider-neutral 事件。[`../tau/src/tau_ai/provider.py:21-34`](../tau/src/tau_ai/provider.py#L21-L34) 该“provider 事件 → Agent 事件”的隔离可迁移：上游适配器可以变化，Runtime 仍只暴露稳定的 typed event union。[`../tau/src/tau_agent/events.py:14-134`](../tau/src/tau_agent/events.py#L14-L134)

assistant response 完成时被追加到 transcript；无 tool call 时结束本轮并在没有排队消息时结束整个 loop；有 tool call 时执行工具、追加 tool result，再进入下一轮模型请求。[`../tau/src/tau_agent/loop.py:97-166`](../tau/src/tau_agent/loop.py#L97-L166) 这建立了一个可迁移的不变量：模型看到的下一次上下文必须包含本轮完整 assistant message 及其对应的 tool result。[`../tau/src/tau_agent/messages.py:22-47`](../tau/src/tau_agent/messages.py#L22-L47)

loop 在 `max_turns` 小于 1 时发不可恢复错误；provider 没有 assistant message 时区分取消、provider error 和异常结束；达到上限时发可恢复错误；正常结束统一发 `AgentEndEvent`。[`../tau/src/tau_agent/loop.py:57-70`](../tau/src/tau_agent/loop.py#L57-L70)[`../tau/src/tau_agent/loop.py:109-166`](../tau/src/tau_agent/loop.py#L109-L166) 这些停止/错误边界可迁移到 PiMoe 的 typed event stream，尤其是“错误是流中的结构化事件，而不是偷偷吞掉或改写为普通文本”。[推断，依据同上]

**判定：可迁移。** 可迁移的不是 Tau 函数签名本身，而是无持久 loop 状态、显式 turn 边界、provider-neutral 事件、tool-result 回写、可观察错误和取消语义。

### 2. 队列注入的时机与可观察性

Tau 将 steering 消息安排在当前 assistant 轮次及其工具批处理完成后消费，将 follow-up 消息安排在 assistant 没有 tool call、运行本应停止时消费；消费后的消息直接追加到 transcript，并在下一次 provider 调用前发 user message start/end 和 queue update 事件。[`../tau/src/tau_agent/loop.py:120-159`](../tau/src/tau_agent/loop.py#L120-L159)[`../tau/src/tau_agent/loop.py:169-187`](../tau/src/tau_agent/loop.py#L169-L187) Harness 侧用两个 deque 保存队列，并以 `one_at_a_time` 或 `all` 控制一次消费多少条。[`../tau/src/tau_agent/harness.py:22-44`](../tau/src/tau_agent/harness.py#L22-L44)[`../tau/src/tau_agent/harness.py:139-175`](../tau/src/tau_agent/harness.py#L139-L175)

该机制的通用价值是定义“运行中的外部输入何时成为模型上下文”的时序，并让队列状态进入 typed event，而不是让 UI 或调用方猜测。Tau 的架构记录也明确 provider 不感知队列，队列在下一次 provider 调用前变成普通 transcript 消息。[`../tau/dev-notes/architecture/queued-steering-follow-ups.zh.md:9-18`](../tau/dev-notes/architecture/queued-steering-follow-ups.zh.md#L9-L18)[`../tau/dev-notes/architecture/queued-steering-follow-ups.zh.md:41-43`](../tau/dev-notes/architecture/queued-steering-follow-ups.zh.md#L41-L43)

**判定：有条件可迁移。** “注入时机 + queue update 事件”可迁移；`steer`/`follow_up` 这两个名字及交互语义来自持续运行的交互式 harness，PiMoe 是否允许 Run 期间追加输入需由既定 RunRequest/事件协议另行决定，不能把可变队列隐含进无状态 Run。[推断，依据同上]

### 3. 工具抽象、顺序执行和失败隔离

`AgentTool` 只有名称、描述、JSON-like input schema、异步 executor 以及可选 prompt 元数据；executor 接收参数和最小取消接口。`ToolCall` 包含 id/name/arguments，`AgentToolResult` 包含 tool_call_id、name、ok、content、data/details/error。[`../tau/src/tau_agent/tools.py:14-75`](../tau/src/tau_agent/tools.py#L14-L75) 这些字段足以让 backend Runtime 在不知工具业务的情况下完成注册、调用、归因和结果序列化。[推断，依据同上]

loop 为工具名建立映射，按 assistant 给出的 tool-call 顺序逐个执行；每个调用先发 `ToolExecutionStartEvent`，执行后把结构化结果转成 `ToolResultMessage` 追加到 transcript，再发 `ToolExecutionEndEvent`。[`../tau/src/tau_agent/loop.py:190-214`](../tau/src/tau_agent/loop.py#L190-L214) 未知工具不会让 loop 崩溃，而是生成失败结果；executor 异常也被隔离为失败的 `AgentToolResult`；返回结果的 tool_call_id 不一致时会被校正为当前调用 id。[`../tau/src/tau_agent/loop.py:207-235`](../tau/src/tau_agent/loop.py#L207-L235)

取消发生在工具批次中时，剩余调用被逐个写入“Tool call cancelled”失败结果并发可恢复错误；这保持 transcript 的 tool-call/result 配对，而不是留下半个 assistant 调用。[`../tau/src/tau_agent/loop.py:196-203`](../tau/src/tau_agent/loop.py#L196-L203) 工具事件和错误事件的 typed 结构定义在 agent 层，不绑定 shell 或文件系统。[`../tau/src/tau_agent/events.py:87-116`](../tau/src/tau_agent/events.py#L87-L116)

**判定：可迁移。** 可迁移的是“声明式工具 + JSON-like 参数/结果 + 顺序批处理 + 每调用 start/end 事件 + 未知/异常/取消结果化 + transcript 配对不变量”。是否并行执行、是否允许副作用、权限和超时策略未由这些 Tau agent-core 文件决定，不应从 Tau 代码外推。[推断，依据同上]

### 4. 最小取消接口与中间事件

Tau 的 provider 和工具都只依赖 `is_cancelled()` 的最小协议；Harness 用简单布尔令牌暴露 `cancel()`，loop 在新 turn 和工具批次中检查它。[`../tau/src/tau_ai/provider.py:13-18`](../tau/src/tau_ai/provider.py#L13-L18)[`../tau/src/tau_agent/tools.py:14-19`](../tau/src/tau_agent/tools.py#L14-L19)[`../tau/src/tau_agent/harness.py:47-59`](../tau/src/tau_agent/harness.py#L47-L59)[`../tau/src/tau_agent/loop.py:67-70`](../tau/src/tau_agent/loop.py#L67-L70) 这为 PiMoe 的 Run cancellation 提供了可迁移的最小依赖方向：执行器不需要知道 UI、HTTP 或任务调度器，只需观察取消信号并把结果/错误送入事件流。[推断，依据同上]

### 5. typed event stream 作为前端/调用方边界

Tau 的事件 union 覆盖 agent start/end、turn start/end、retry、queue update、消息 start/delta/end、thinking delta、tool execution start/update/end 和 error。[`../tau/src/tau_agent/events.py:14-134`](../tau/src/tau_agent/events.py#L14-L134) Harness 不渲染事件，而是把 loop 事件通知订阅者并继续 yield；事件监听器既可同步也可异步。[`../tau/src/tau_agent/harness.py:124-132`](../tau/src/tau_agent/harness.py#L124-L132)[`../tau/src/tau_agent/harness.py:193-230`](../tau/src/tau_agent/harness.py#L193-L230)

**判定：可迁移。** 对 PiMoe，Tau 最有价值的边界是事件是 Runtime 输出的事实记录，渲染、日志、指标和持久化是消费方；但 Tau Harness 的订阅机制不应被误认为 PiMoe Run 的必要持久对象，因为 Run 已决定无状态。[推断，依据同上]

## 二、会话、memory 与上下文管理

### 1. append-only typed session log

Tau 的 session entry 以 `id`、`parent_id`、timestamp 为公共字段，且用判别字段 `type` 区分 message、model_change、thinking_level_change、compaction、branch_summary、label、leaf、session_info、custom 等条目。[`../tau/src/tau_agent/session/entries.py:15-32`](../tau/src/tau_agent/session/entries.py#L15-L32)[`../tau/src/tau_agent/session/entries.py:35-114`](../tau/src/tau_agent/session/entries.py#L35-L114) `SessionStorage` 只定义异步 append/read_all；内置 JSONL storage 以追加方式写入、按文件顺序读取，缺失文件视为空会话。[`../tau/src/tau_agent/session/storage.py:12-40`](../tau/src/tau_agent/session/storage.py#L12-L40) JSONL 每行使用 Pydantic discriminator 解析，错误带行号包装为 `SessionJsonlError`。[`../tau/src/tau_agent/session/jsonl.py:12-37`](../tau/src/tau_agent/session/jsonl.py#L12-L37)

该机制把“durable history”和“active context”分开：历史是不可变追加记录，active context 是对记录的重放结果，而不是直接编辑旧消息。Tau 的一手架构说明明确把追加、可检查、可分支和压缩后保留历史作为 append-only 的目的。[`../tau/dev-notes/architecture/phase-7-session-tree.zh.md:23-40`](../tau/dev-notes/architecture/phase-7-session-tree.zh.md#L23-L40)

**判定：有条件可迁移。** append-only、typed entries、可替换 storage protocol 和 replay 是 backend 通用能力；本地 JSONL 文件、目录和 session index 属于 Tau coding 外壳，不能视为 PiMoe Runtime 必须采用的存储方案。[`../tau/dev-notes/architecture/phase-14-session-manager-resume.zh.md:40-85`](../tau/dev-notes/architecture/phase-14-session-manager-resume.zh.md#L40-L85) [推断]

### 2. replay、leaf path 与分支隔离

`SessionState.from_entries()` 默认线性重放 storage 顺序；传入 leaf_id 时只重放 root-to-leaf 路径，显式传入 `None` 则重放空路径。重放期间 message 进入消息列表，model/thinking/label/leaf/session_info/custom/compaction/branch_summary 各自更新对应状态。[`../tau/src/tau_agent/session/memory.py:21-103`](../tau/src/tau_agent/session/memory.py#L21-L103) `path_to_entry()` 通过 parent_id 向根回溯，拒绝重复 id、循环和缺失父条目。[`../tau/src/tau_agent/session/tree.py:12-40`](../tau/src/tau_agent/session/tree.py#L12-L40)

这提供了一个重要可迁移不变量：恢复某个会话视图时，输入上下文由选定路径决定，兄弟分支不能泄漏进当前模型请求。Tau 架构文档直接说明指定 leaf_id 只重放根到叶子路径，以防止兄弟分支泄漏。[`../tau/dev-notes/architecture/phase-7-session-tree.zh.md:93-114`](../tau/dev-notes/architecture/phase-7-session-tree.zh.md#L93-L114)

**判定：有条件可迁移。** 路径选择、父子校验和 active leaf 语义可迁移；是否需要分支、是否持久化 leaf entry、以及分支导航 UI 属于 session 产品能力而非每次无状态 Run 的必需部分。[推断，依据同上]

### 3. compaction 是 replay 语义，不是删除历史

`CompactionEntry` 记录 summary 和要替换的 message entry ids。[`../tau/src/tau_agent/session/entries.py:56-62`](../tau/src/tau_agent/session/entries.py#L56-L62) replay 遇到 compaction 时，`_apply_compaction()` 移除指定消息，在第一次被替换的位置插入一条 provider-neutral 的 `UserMessage` 摘要；若没有命中被替换消息，也会插入摘要，且未被替换消息保留。[`../tau/src/tau_agent/session/memory.py:84-90`](../tau/src/tau_agent/session/memory.py#L84-L90)[`../tau/src/tau_agent/session/memory.py:106-129`](../tau/src/tau_agent/session/memory.py#L106-L129) 这意味着 compaction 改变的是“重建出的 active messages”，而不是修改或删除原始 session entries。[推断，依据同上]

Tau 的 coding 侧自动 compaction 计划保留最近约定 token 范围的消息，将旧消息生成摘要，再追加 compaction 和 leaf 并用重放结果替换 Harness transcript。[`../tau/src/tau_coding/session.py:1355-1368`](../tau/src/tau_coding/session.py#L1355-L1368)[`../tau/src/tau_coding/session.py:1430-1474`](../tau/src/tau_coding/session.py#L1430-L1474) 其中模型摘要请求、阈值、最近消息保留数属于 coding/application 层；`tau_agent` 只承担 entry 与 replay 语义。[`../tau/dev-notes/architecture/phase-22-compaction-foundation.zh.md:35-63`](../tau/dev-notes/architecture/phase-22-compaction-foundation.zh.md#L35-L63)[`../tau/dev-notes/architecture/phase-22-compaction-foundation.zh.md:91-95`](../tau/dev-notes/architecture/phase-22-compaction-foundation.zh.md#L91-L95)

**判定：有条件可迁移。** “历史不删、active context 可由摘要替换、replay 后得到新上下文”可迁移；Tau 的“Previous conversation summary”文本格式、模型摘要 prompt、保留 20,000 token、上下文窗口减 16,384 reserve 等数值不可直接迁移。[`../tau/src/tau_agent/session/memory.py:128-129`](../tau/src/tau_agent/session/memory.py#L128-L129)[`../tau/src/tau_coding/context_window.py:10-17`](../tau/src/tau_coding/context_window.py#L10-L17)

### 4. context accounting 的边界

Tau 的 `tau_coding.context_window` 用字符/4 的确定性粗估，分别统计 system、message、tool token，并给出 message/tool 数量；tool message 还把名称、内容和 assistant tool-call 参数纳入估算。[`../tau/src/tau_coding/context_window.py:102-186`](../tau/src/tau_coding/context_window.py#L102-L186) `CodingSession.context_usage` 直接以当前 harness 的 system、messages 和 tools 生成快照。[`../tau/src/tau_coding/session.py:541-574`](../tau/src/tau_coding/session.py#L541-L574)

**判定：有条件可迁移。** “在发 provider 请求前对 system + messages + tool definitions 做可解释的 usage estimate”是通用运行时能力；字符/4、固定 overhead、模型窗口 fallback 和自动阈值是 Tau coding 的近似策略，不能当作 PiMoe 的 tokenizer 或模型上下文事实。[`../tau/src/tau_coding/context_window.py:114-186`](../tau/src/tau_coding/context_window.py#L114-L186)[`../tau/src/tau_coding/context_window.py:162-167`](../tau/src/tau_coding/context_window.py#L162-L167)

## 三、明确排除的编码 agent 机制

### 1. 项目上下文、资源发现和 system prompt 拼装

Tau 的项目上下文发现位于 `tau_coding`，按用户/项目/cwd 等路径发现 Markdown 指令，再把结果传给 `BuildSystemPromptOptions`；`tau_agent` 只接收已经构建好的 system string。[`../tau/dev-notes/architecture/phase-19-context-discovery.zh.md:16-49`](../tau/dev-notes/architecture/phase-19-context-discovery.zh.md#L16-L49) 因此项目 `AGENTS.md`、skills、prompt templates、资源 reload 和项目根目录判定均是编码环境机制，不属于通用 backend Agent Runtime。

**判定：编码 agent 专属。** PiMoe 的 backend Runtime 可以接收调用方已经确定的 system/context 输入，但不应隐含本地 workspace 文件扫描。[推断，依据同上]

### 2. read/write/edit/bash 工具

Tau 的 `create_coding_tools()` 固定返回 read、write、edit、bash 四个工具，按 cwd 解析相对路径，并为同一路径的 write/edit 使用进程内锁；模块文档明确说本地 filesystem/shell 行为留在 `tau_coding` 之外。[`../tau/src/tau_coding/tools.py:1-7`](../tau/src/tau_coding/tools.py#L1-L7)[`../tau/src/tau_coding/tools.py:96-116`](../tau/src/tau_coding/tools.py#L96-L116) read 直接读取 cwd 下文件或图片并做行/字节截断；write/edit 进行本地文件写入；bash 创建 cwd 下 shell 子进程、合并输出、处理 timeout/cancellation。[`../tau/src/tau_coding/tools.py:119-133`](../tau/src/tau_coding/tools.py#L119-L133)[`../tau/src/tau_coding/tools.py:260-317`](../tau/src/tau_coding/tools.py#L260-L317)[`../tau/src/tau_coding/tools.py:320-430`](../tau/src/tau_coding/tools.py#L320-L430)[`../tau/src/tau_coding/tools.py:433-503`](../tau/src/tau_coding/tools.py#L433-L503)

**判定：编码 agent 专属。** 可迁移的是 `AgentTool` 扩展点和结果结构，而不是任何文件、编辑器、workspace 或 shell 能力。Tau 架构说明也明确 `tau_agent.loop` 只知道如何执行 `AgentTool`，编码工具才知道读写编辑文件或运行 bash。[`../tau/dev-notes/architecture/phase-3-agent-loop.zh.md:193-207`](../tau/dev-notes/architecture/phase-3-agent-loop.zh.md#L193-L207)

### 3. CodingSession、CLI/TUI 与用户会话索引

`CodingSession` 在 Harness 外包住 durable entries、默认 coding tools 和命令 seam；load 时读取存储、恢复 state、构建 coding tools、加载资源和 system prompt，再创建 Harness。[`../tau/src/tau_coding/session.py:199-205`](../tau/src/tau_coding/session.py#L199-L205)[`../tau/src/tau_coding/session.py:245-316`](../tau/src/tau_coding/session.py#L245-L316) 它还负责把 Harness 的 MessageEnd 生命周期持久化为 MessageEntry + LeafEntry、更新 session index，以及在 prompt 前后触发自动 compaction。[`../tau/src/tau_coding/session.py:1121-1214`](../tau/src/tau_coding/session.py#L1121-L1214)[`../tau/src/tau_coding/session.py:1232-1265`](../tau/src/tau_coding/session.py#L1232-L1265)

**判定：编码 agent 专属（外壳职责），但其边界是可迁移的架构参考。** PiMoe 不应把 CodingSession、项目 cwd、TUI、slash command、session index 或本地存储路径塞进无状态 Run；可借鉴的只有“Runtime 输出事件，外层决定如何持久化/恢复”的职责分离。[`../tau/dev-notes/architecture/phase-4-agent-harness.zh.md:36-46`](../tau/dev-notes/architecture/phase-4-agent-harness.zh.md#L36-L46)[`../tau/dev-notes/architecture/phase-20-1-context-accounting.zh.md:44-54`](../tau/dev-notes/architecture/phase-20-1-context-accounting.zh.md#L44-L54)

### 4. 中断后修复未配对 tool call 的 coding/provider 适配

Harness 在 prompt/continue 开始前以及取消后的 finally 中扫描最近一个开放的 assistant tool-call；如果缺少对应 ToolResultMessage，就追加“Tool call interrupted by user”，因为 OpenAI-compatible provider 会拒绝未配对 transcript。[`../tau/src/tau_agent/harness.py:177-224`](../tau/src/tau_agent/harness.py#L177-L224)[`../tau/src/tau_agent/harness.py:253-299`](../tau/src/tau_agent/harness.py#L253-L299)

**判定：有条件可迁移。** “每次 provider 请求前验证 transcript 合法性、为中断调用补齐失败结果”是通用可靠性不变量；具体错误文本和“OpenAI-compatible provider”前提是 Tau 当前适配环境，不应作为 PiMoe 的唯一协议假设。[推断，依据同上]

## 四、对 PiMoe 无状态 Run 契约的含义

1. **把 loop 的输入输出事实化。** Tau 的纯 loop 已证明 provider、system、消息、工具和取消信号足以驱动一次运行，assistant/tool-result 追加过程可完全由事件观察到。[`../tau/src/tau_agent/loop.py:37-56`](../tau/src/tau_agent/loop.py#L37-L56)[`../tau/src/tau_agent/loop.py:76-100`](../tau/src/tau_agent/loop.py#L76-L100) 对 PiMoe 的 `Run(ctx, RunRequest)`，这支持将所有跨层可见变化表达为 typed events，而不依赖 Runtime 内部可变 session。

2. **把 transcript/context 的所有权留在调用方或显式 session 层。** Tau loop 不拥有持久 transcript，Harness 才拥有内存 transcript，session replay 才从 append-only entries 重建 active messages。[`../tau/src/tau_agent/loop.py:50-56`](../tau/src/tau_agent/loop.py#L50-L56)[`../tau/src/tau_agent/harness.py:62-87`](../tau/src/tau_agent/harness.py#L62-L87)[`../tau/src/tau_agent/session/memory.py:21-57`](../tau/src/tau_agent/session/memory.py#L21-L57) 因而 PiMoe 应把 memory/session 恢复结果作为 RunRequest/ctx 的显式输入，而不是让 Run 隐式读取或更新 session。[推断，依据同上]

3. **保留工具结果配对和失败结果化。** Tau 将未知工具、异常、取消都编码为失败结果并追加 transcript，避免下一轮请求看到悬空 tool call。[`../tau/src/tau_agent/loop.py:196-235`](../tau/src/tau_agent/loop.py#L196-L235) 对 PiMoe，至少应把 tool-call id、结构化结果、错误和取消状态作为事件/上下文可重放事实；这是可迁移不变量，不是编码工具实现。[推断，依据同上]

4. **将 compaction 视为显式上下文变换。** Tau 的 session replay 通过 CompactionEntry 替换 active messages、保留 durable history；摘要生成和 token 阈值在 coding 层。[`../tau/src/tau_agent/session/memory.py:84-125`](../tau/src/tau_agent/session/memory.py#L84-L125)[`../tau/dev-notes/architecture/phase-22-compaction-foundation.zh.md:35-63`](../tau/dev-notes/architecture/phase-22-compaction-foundation.zh.md#L35-L63) 对 PiMoe，compaction 若存在，应作为 Run 输入准备或 typed event 事实处理；不能依靠 Runtime 私有 mutable memory。[推断，依据同上]

5. **不要迁移 Tau 的 coding 外壳。** 项目上下文发现、cwd、read/write/edit/bash、CodingSession、TUI queue shortcuts、session index 和本地 JSONL 路径都在 `tau_coding` 或前端层；它们不是 generic backend Agent Runtime 的必要依赖。[`../tau/dev-notes/architecture/phase-19-context-discovery.zh.md:33-49`](../tau/dev-notes/architecture/phase-19-context-discovery.zh.md#L33-L49)[`../tau/src/tau_coding/tools.py:1-7`](../tau/src/tau_coding/tools.py#L1-L7)[`../tau/dev-notes/architecture/phase-14-session-manager-resume.zh.md:74-85`](../tau/dev-notes/architecture/phase-14-session-manager-resume.zh.md#L74-L85)

## 五、来源表（主实现与最近架构记录）

| 主题 | 直接来源（路径:行范围） | 证据用途 |
|---|---|---|
| 纯 Agent loop 输入、无状态 transcript、turn/stop/error | [`../tau/src/tau_agent/loop.py:37-166`](../tau/src/tau_agent/loop.py#L37-L166) | loop 的输入、provider 流、消息追加、工具循环、取消、max_turns、结束事件 |
| 队列消费与注入时机 | [`../tau/src/tau_agent/loop.py:120-187`](../tau/src/tau_agent/loop.py#L120-L187)；[`../tau/src/tau_agent/harness.py:139-175`](../tau/src/tau_agent/harness.py#L139-L175) | steering/follow-up 的时机、队列模式、QueueUpdateEvent |
| Agent/Harness 事件边界 | [`../tau/src/tau_agent/events.py:14-134`](../tau/src/tau_agent/events.py#L14-L134)；[`../tau/src/tau_agent/harness.py:193-230`](../tau/src/tau_agent/harness.py#L193-L230) | typed event union、监听与异步流式消费 |
| Provider 抽象 | [`../tau/src/tau_ai/provider.py:13-34`](../tau/src/tau_ai/provider.py#L13-L34)；[`../tau/src/tau_ai/events.py:14-91`](../tau/src/tau_ai/events.py#L14-L91) | provider-neutral stream 与取消协议 |
| transcript 消息 | [`../tau/src/tau_agent/messages.py:13-47`](../tau/src/tau_agent/messages.py#L13-L47) | user/assistant/tool 三类消息及 tool call 关联 |
| 通用工具模型与结果 | [`../tau/src/tau_agent/tools.py:14-75`](../tau/src/tau_agent/tools.py#L14-L75) | AgentTool、ToolCall、AgentToolResult、executor/cancellation |
| 工具顺序执行与失败隔离 | [`../tau/src/tau_agent/loop.py:190-235`](../tau/src/tau_agent/loop.py#L190-L235) | start/end 事件、未知工具、异常、取消、id 校正 |
| append-only entries | [`../tau/src/tau_agent/session/entries.py:15-114`](../tau/src/tau_agent/session/entries.py#L15-L114) | entry 类型、父指针、compaction/branch/custom 元数据 |
| session storage/JSONL | [`../tau/src/tau_agent/session/storage.py:12-40`](../tau/src/tau_agent/session/storage.py#L12-L40)；[`../tau/src/tau_agent/session/jsonl.py:12-37`](../tau/src/tau_agent/session/jsonl.py#L12-L37) | append/read_all、缺失文件、序列化与错误行号 |
| memory replay 与 compaction | [`../tau/src/tau_agent/session/memory.py:21-137`](../tau/src/tau_agent/session/memory.py#L21-L137) | active state、leaf path、消息重建、摘要替换 |
| 分支路径校验 | [`../tau/src/tau_agent/session/tree.py:8-40`](../tau/src/tau_agent/session/tree.py#L8-L40) | duplicate/cycle/missing parent 检查与 root-to-leaf path |
| context accounting/阈值（coding 层） | [`../tau/src/tau_coding/context_window.py:102-186`](../tau/src/tau_coding/context_window.py#L102-L186)；[`../tau/src/tau_coding/session.py:541-574`](../tau/src/tau_coding/session.py#L541-L574) | 粗略 token 估算及其 coding 边界 |
| coding compaction 触发/重建 | [`../tau/src/tau_coding/session.py:1355-1474`](../tau/src/tau_coding/session.py#L1355-L1474) | 自动阈值、摘要请求、保留近期消息、追加 compaction/leaf |
| coding 工具（排除项） | [`../tau/src/tau_coding/tools.py:1-7`](../tau/src/tau_coding/tools.py#L1-L7)；[`../tau/src/tau_coding/tools.py:96-116`](../tau/src/tau_coding/tools.py#L96-L116)；[`../tau/src/tau_coding/tools.py:119-503`](../tau/src/tau_coding/tools.py#L119-L503) | cwd、filesystem、edit、shell、截断、锁等 coding-specific 机制 |
| 分层边界架构记录 | [`../tau/dev-notes/architecture/phase-3-agent-loop.zh.md:5-28`](../tau/dev-notes/architecture/phase-3-agent-loop.zh.md#L5-L28)；[`../tau/dev-notes/architecture/phase-4-agent-harness.zh.md:26-46`](../tau/dev-notes/architecture/phase-4-agent-harness.zh.md#L26-L46) | Tau 对 loop/harness/coding/TUI 的职责划分 |
| session append-only/replay 架构记录 | [`../tau/dev-notes/architecture/phase-7-session-tree.zh.md:15-32`](../tau/dev-notes/architecture/phase-7-session-tree.zh.md#L15-L32)；[`../tau/dev-notes/architecture/phase-7-session-tree.zh.md:73-114`](../tau/dev-notes/architecture/phase-7-session-tree.zh.md#L73-L114) | durable history、memory replay、分支隔离 |
| compaction 边界架构记录 | [`../tau/dev-notes/architecture/phase-22-compaction-foundation.zh.md:19-44`](../tau/dev-notes/architecture/phase-22-compaction-foundation.zh.md#L19-L44)；[`../tau/dev-notes/architecture/phase-22-compaction-foundation.zh.md:91-95`](../tau/dev-notes/architecture/phase-22-compaction-foundation.zh.md#L91-L95) | compaction replay 语义与 coding 层边界 |
