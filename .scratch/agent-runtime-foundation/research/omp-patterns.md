# OMP Agent Runtime 机制审计

## 范围与判定口径

本审计只读取 `../oh-my-pi`，重点是 `packages/agent` 的 Agent loop、上下文变换、工具执行与 compaction/session 数据模型；同时读取 `packages/coding-agent/src/session` 与 `docs/session*.md`，仅用于识别 OMP 当前宿主如何把这些能力落到持久化会话。`@oh-my-pi/pi-agent` 自身被定义为“stateful agent with tool execution and event streaming”，因此不能把它的有状态 facade 直接等同于 PiMoe 已决定的无状态 Runtime 契约（`../oh-my-pi/packages/agent/README.md:1-3`）。

分类含义：

- **可迁移**：不依赖终端、文件系统、编辑器、TUI 或特定宿主；可映射到 PiMoe 的 `Run(ctx, RunRequest) -> typed event stream` 语义。
- **条件可迁移**：机制本身通用，但需要由 PiMoe 的调用方提供外部 session store、provider 能力、队列或缓存策略；不应因此把状态塞回 Runtime 对象。
- **编码 Agent 专属**：机制的语义依赖 coding-agent 的工作区、路径、终端、编辑器或 TUI；generic backend Agent Runtime 不应默认继承。

下文的“PiMoe 含义”只做契约对照，不提出 PiMoe 实现方案。

## 结论摘要

1. **Loop 核心可迁移。** OMP 将一次 run 拆成：复制/追加输入上下文、发出生命周期事件、反复生成 assistant、执行工具、把 tool result 放回上下文，直到没有工具调用、steering 或 follow-up。入口与 loop 的边界清晰，适合对应 PiMoe 的一次 `Run` 和 typed event stream（`../oh-my-pi/packages/agent/src/agent-loop.ts:302-381`；`../oh-my-pi/packages/agent/src/agent-loop.ts:724-843`；`../oh-my-pi/packages/agent/src/agent-loop.ts:1075-1129`）。
2. **上下文是两层管线。** AgentMessage transcript 先经过可选 `transformContext`，再由必需的 `convertToLlm` 变为 provider Message；随后做 provider normalization、可选 append-only cache 管理和 provider-context transform，再发给模型（`../oh-my-pi/packages/agent/src/types.ts:118-167`；`../oh-my-pi/packages/agent/src/agent-loop.ts:1173-1212`）。这比把“Runtime context”直接等同为 provider wire messages 更适合通用后端。
3. **工具执行是可迁移的协议边界。** OMP 在执行前校验参数，支持阻断/参数变换/late-bound tool context，允许 partial update，统一把异常或结果转成 `tool_execution_*` 事件及 `toolResult` 消息；同一批调用还带 shared/exclusive 调度（`../oh-my-pi/packages/agent/src/agent-loop.ts:1836-1909`；`../oh-my-pi/packages/agent/src/agent-loop.ts:1927-2050`；`../oh-my-pi/packages/agent/src/agent-loop.ts:2075-2139`）。
4. **Session/compaction 是外部状态层，条件可迁移。** Agent package 定义 parent-linked session entries、branch summary 和 compaction entry；compaction 函数明确把 I/O 留给 session manager，并在完成后重新加载 session（`../oh-my-pi/packages/agent/src/compaction/entries.ts:4-142`；`../oh-my-pi/packages/agent/src/compaction/compaction.ts:1-6`）。这与 PiMoe 的 stateless Run 相容，但持久化、恢复、分支叶子和 compaction entry 必须属于调用方/外部 store，而不是单次 Run 的隐含可变状态。
5. **文件、工作区、终端和 TUI 逻辑应排除。** OMP 的 compaction 工具路径跟踪明确镜像 coding-agent 的 `read` selector grammar，并只识别 `read`/`write`/`edit` 文件操作；工作目录动态解析也是为 GitLab Duo workspace discovery 服务的宿主能力（`../oh-my-pi/packages/agent/src/compaction/utils.ts:30-45`；`../oh-my-pi/packages/agent/src/compaction/utils.ts:97-129`；`../oh-my-pi/packages/agent/src/types.ts:356-365`）。

## 1. Agent loop 与事件流

### 1.1 入口和一次 run 的边界 — **可迁移**

`agentLoop` 接收 prompts、`AgentContext`、loop config、abort signal 和 stream function；它复制 context.messages 后追加 prompts，先发 `agent_start`/`turn_start` 与 prompt 的 message 生命周期事件，再进入共享 loop，最终由 `EventStream` 在 `agent_end` 时结束并返回本次新增消息（`../oh-my-pi/packages/agent/src/agent-loop.ts:302-332`；`../oh-my-pi/packages/agent/src/agent-loop.ts:376-381`）。这说明 OMP 的一次 loop 可以有明确的输入快照和本次新增消息输出，而不是要求调用方观察内部对象。

`agentLoopContinue` 不添加新消息，仅从现有 context 继续；它拒绝空 context 或最后一条 assistant 消息，要求转换后的末条消息能成为 provider 可接受的 user/toolResult（`../oh-my-pi/packages/agent/src/agent-loop.ts:335-373`）。这是一个通用的 retry/continue 语义，而不是 coding-agent 特性。

### 1.2 内外两层循环 — **可迁移**

`runLoopBody` 有外层 follow-up 循环和内层 tool/steering 循环。每个边界会刷新 pending messages、可选地调用 `syncContextBeforeModelCall`，并只在一个 logical turn 中解析一次 tool-choice；随后生成 assistant，按 stop reason 处理工具、补齐未执行调用的结果，并发出 `turn_end`（`../oh-my-pi/packages/agent/src/agent-loop.ts:724-843`；`../oh-my-pi/packages/agent/src/agent-loop.ts:899-1077`）。当当前工作结束后，loop 还会在退出前排空 late steering、aside 和 follow-up；有新消息就继续，否则发出 `agent_end`（`../oh-my-pi/packages/agent/src/agent-loop.ts:1083-1132`）。

该分层可直接对应 PiMoe 的 typed stream：`agent/turn/message/tool_execution` 是不同粒度的事件，而非把整个 provider response 当作唯一结果。OMP 的事件 union 明确列出了这些生命周期与字段（`../oh-my-pi/packages/agent/src/types.ts:701-727`）。

### 1.3 Steering、follow-up、aside — **可迁移，但队列状态条件可迁移**

OMP 将 steering 定义为运行中途要注入的消息；队列只在 loop start 或完整 tool batch 结算后消费，中途只 peek，避免在工具未结算时丢失消息（`../oh-my-pi/packages/agent/src/types.ts:177-200`）。follow-up 只在 agent 本来要停止且没有更多工具/steering 时取出；aside 是不打断工具的被动通知，在 step boundary 注入（`../oh-my-pi/packages/agent/src/types.ts:212-236`）。

消息注入本身是通用 loop 行为；但队列的生命周期属于宿主运行状态。对 PiMoe 契约而言，Run 可以消费本次请求提供的 queued inputs，并把剩余队列状态交由调用方保留；不能从 OMP 的可变 Agent facade 推导出 Runtime 必须持有跨 Run 队列。

## 2. Context 管线与 provider 边界

### 2.1 AgentMessage 与 provider Message 分离 — **可迁移**

OMP 的 `AgentContext` 包含 system prompt、AgentMessage[] 和 AgentTool[]；AgentMessage 允许标准 LLM message 与 declaration-merging 扩展的 custom message（`../oh-my-pi/packages/agent/src/types.ts:506-545`；`../oh-my-pi/packages/agent/src/types.ts:694-699`）。`convertToLlm` 的契约要求每条 AgentMessage 变成 user/assistant/toolResult，或被过滤；`transformContext` 则在转换前用于 pruning 和外部上下文注入，`transformProviderContext` 在 provider 发送前处理最终 Context（`../oh-my-pi/packages/agent/src/types.ts:118-167`）。

实际发送路径严格按 `transformContext → convertToLlm → normalizeMessagesForProvider → append-only/normal Context → transformProviderContext → stream` 执行（`../oh-my-pi/packages/agent/src/agent-loop.ts:1173-1212`）。这是一条可迁移的 backend seam：Runtime 内的 typed context/event 可以保持比 provider wire schema 更丰富，同时在发送前做明确的投影。

### 2.2 Tool schema normalization — **可迁移**

`normalizeTools` 把 AgentTool 转成 provider wire schema；按配置注入 intent 字段、追加 dialect examples，并可移除描述以避免 system prompt catalog 与 native tool schema 重复（`../oh-my-pi/packages/agent/src/agent-loop.ts:602-631`）。工具类型还把执行 callback、参数 schema、result content/details/error/useless 等能力分开定义（`../oh-my-pi/packages/agent/src/types.ts:547-560`；`../oh-my-pi/packages/agent/src/types.ts:595-602`）。这些是 generic tool protocol，不含文件或终端假设。

### 2.3 Append-only prefix/cache — **条件可迁移**

OMP 的 StablePrefix 冻结 system prompt 与 normalized tools，并用 fingerprint 判断是否需要重建；AppendOnlyLog 只追加消息，唯一的尾部替换路径保留给 compaction（`../oh-my-pi/packages/agent/src/append-only-context.ts:1-15`；`../oh-my-pi/packages/agent/src/append-only-context.ts:42-90`；`../oh-my-pi/packages/agent/src/append-only-context.ts:98-147`）。`AppendOnlyContextManager.syncMessages` 会区分正常追加、compaction 缩短和中途重写，只截断到最长稳定前缀再追加差异尾部；digest 覆盖 provider 可能序列化的 role/content/tool-call/result/id 字段（`../oh-my-pi/packages/agent/src/append-only-context.ts:153-237`；`../oh-my-pi/packages/agent/src/append-only-context.ts:274-310`）。

这是 provider prompt/KV cache 优化，不是 agent 正确性的必要语义。PiMoe 可把它视为可选 request-context optimization：只有 provider 的 prefix cache 或大型上下文重放需要时才有价值，且仍应由单次 Run 以外的缓存/状态承载。

### 2.4 Owned/in-band tool dialect — **条件可迁移**

OMP 支持不发送 native tools，而把工具目录与历史编码进 prompt/text，再把模型文本解析回 canonical toolCall；启用 dialect 时会关闭 native tool choice，provider stream 中的 fabricated tool result 还可被中止或丢弃（`../oh-my-pi/packages/agent/src/types.ts:269-288`；`../oh-my-pi/packages/agent/src/agent-loop.ts:1214-1225`；`../oh-my-pi/packages/agent/src/agent-loop.ts:1329-1341`）。这是 provider 能力不一致时的适配层，通用性取决于 PiMoe 支持的模型集合，不应作为 generic Runtime 的必需语义。

## 3. Tool execution 机制

### 3.1 生命周期、校验和 hook — **可迁移**

OMP 为每个 assistant toolCall 建立 record，按内部 name 或 provider wire name 查找工具，并为可 interruptible 工具选择不同 abort signal；中途 steering/IRC 检查只 peek 队列，不消费消息（`../oh-my-pi/packages/agent/src/agent-loop.ts:1682-1783`）。执行前会抽取 intent、校验参数；普通校验失败直接形成 error tool result，lenient 工具才把原始参数交给执行器（`../oh-my-pi/packages/agent/src/agent-loop.ts:1836-1889`）。

执行前 hook 可以 block 或修改参数；随后解析 late-bound tool context，调用 `tool.execute`，接收 partial result 并发出 `tool_execution_update`；异常被转为 `isError` tool result。执行后的 hook 可覆盖 content/details/error/useless，但结果会再次 normalize，避免坏数据写入 transcript（`../oh-my-pi/packages/agent/src/agent-loop.ts:1927-2031`）。这套边界与后端任务执行器、权限门和请求级上下文都相容；具体工具副作用则不属于 agent loop 本身。

### 3.2 并发与副作用顺序 — **可迁移**

工具声明可用 `shared` 或 `exclusive` concurrency；OMP 让 shared 调用并行，而 exclusive 调用等待前面的 exclusive/shared 任务完成，并对动态 resolver 异常回退到 serial-safe 模式（`../oh-my-pi/packages/agent/src/types.ts:617-623`；`../oh-my-pi/packages/agent/src/agent-loop.ts:2075-2103`）。这提供了“只读/可并行”与“可能互斥/有副作用”之间的调度协议；PiMoe 是否采用取决于工具集合，但该分类不要求 coding workspace。

### 3.3 协议配对与 synthetic result — **可迁移**

当 assistant tool call 因 abort、provider error、deadline 或 `length` 未真正执行时，OMP 仍生成 synthetic tool result，以保持 provider 要求的 tool_use/tool_result 配对，并标记 `__synthetic`、`executed: false` 供 UI、telemetry、history 区分“发出但未执行”和“真实工具失败”（`../oh-my-pi/packages/agent/src/agent-loop.ts:2142-2164`；`../oh-my-pi/packages/agent/src/agent-loop.ts:1020-1055`）。这是跨 provider replay 的通用一致性规则，尤其适合 typed event stream 中明确表达 skipped/aborted/error 状态。

## 4. Session、compaction 与“memory”

### 4.1 Parent-linked entry 数据模型 — **条件可迁移**

agent package 的 `SessionEntryBase` 以 `id`、`parentId`、timestamp 连接条目；entry union 同时支持 message、model/thinking/service-tier 变更、compaction、branch summary、custom message/entry、label、session init 与 mode 等（`../oh-my-pi/packages/agent/src/compaction/entries.ts:4-20`；`../oh-my-pi/packages/agent/src/compaction/entries.ts:34-118`；`../oh-my-pi/packages/agent/src/compaction/entries.ts:122-142`）。`ReadonlySessionManager` 只要求按 leaf 取 branch 与按 id 查 entry，说明 compaction/branch 算法依赖的是抽象的 tree read，而不是某个文件格式（`../oh-my-pi/packages/agent/src/compaction/entries.ts:139-142`）。

这套 transcript/tree 模型可迁移为 PiMoe 的外部 session state；但 PiMoe 的 `Run` 仍应保持无状态：session entry 的读取、写入、leaf 选择与恢复由调用方在 Run 前后管理。

### 4.2 Token-aware compaction — **条件可迁移**

OMP 的 compaction 设置包括启用开关、strategy、token/百分比阈值、mid-turn、reserve、保留最近 token 数、auto-continue 与 remote options（`../oh-my-pi/packages/agent/src/compaction/compaction.ts:141-198`）。上下文 token 使用 provider usage 或 components 计算；reserve 至少取 context window 的 15% 与配置 floor 的较大值；`shouldCompact` 在超过解析出的阈值时触发（`../oh-my-pi/packages/agent/src/compaction/compaction.ts:204-217`；`../oh-my-pi/packages/agent/src/compaction/compaction.ts:248-285`）。

`prepareCompaction` 根据最新 usage、历史估算和 keepRecentTokens 找 cut point，拆出待总结消息、split-turn 前缀和完整保留的 recent messages，并保留 previous summary/preserveData；没有可总结内容时返回 no-op（`../oh-my-pi/packages/agent/src/compaction/compaction.ts:1039-1059`；`../oh-my-pi/packages/agent/src/compaction/compaction.ts:1085-1193`）。`compact` 可使用 remote compaction，失败时退回本地总结；remote 成功时把 provider-native replay 放入 preserveData，避免重复本地 LLM 总结（`../oh-my-pi/packages/agent/src/compaction/compaction.ts:1244-1270`；`../oh-my-pi/packages/agent/src/compaction/compaction.ts:1316-1426`）。

因此 compaction 是可迁移的上下文记忆策略，但具体阈值、provider replay、summary model 与 I/O 都是条件项。OMP 明确说明 compaction 函数是 pure logic，session manager 负责 I/O，完成后重新加载 session（`../oh-my-pi/packages/agent/src/compaction/compaction.ts:1-6`）。

### 4.3 Branch/compaction replay — **条件可迁移；coding 宿主实现不直接迁移**

coding-agent 当前通过 leaf 的 parent chain 重建 root→leaf path；在非 transcript LLM context 中，compaction summary 先放入 context，再按 `firstKeptEntryId` 重放保留消息，最后追加 compaction 后消息（`../oh-my-pi/packages/coding-agent/src/session/session-context.ts:141-217`；`../oh-my-pi/packages/coding-agent/src/session/session-context.ts:302-355`）。SessionManager 公开 `getBranch` 与 `buildSessionContext`，并规定 branch 只移动 leaf、既有 entries 不修改；带 summary 的 branch 追加 `branch_summary` entry（`../oh-my-pi/packages/coding-agent/src/session/session-manager.ts:1552-1565`；`../oh-my-pi/packages/coding-agent/src/session/session-manager.ts:1601-1631`）。

“用不可变 entry + leaf 指针表示分支”是通用 session 语义，适合 PiMoe 外部 session store；但这里的 SessionManager、TUI transcript 选项、文件路径和 `/tree`/`/branch` 交互属于 coding-agent 宿主，不能作为 PiMoe Runtime 的内建职责。

### 4.4 持久化与恢复 — **条件可迁移；具体文件持久化编码 Agent 专属**

OMP 的 coding-agent 文档规定 session 是 append-only JSONL：首行为 header，其余行为 SessionEntry；分支导航只移动 `leafId`，不改写旧 entries（`../oh-my-pi/docs/session.md:63-70`）。实际 SessionManager 每次 record entry 都更新内存数组/index 并追加 session file；entry 的 `parentId` 取当前 leaf（`../oh-my-pi/packages/coding-agent/src/session/session-manager.ts:783-796`）。

持久化层还提供 File/Memory/索引后端的抽象 writer、atomic rewrite、drain；writer 的 append contract 与 backend queue 由 `SessionStorage` 定义（`../oh-my-pi/packages/coding-agent/src/session/session-storage.ts:15-77`）。这些说明“session store 应可替换”是可迁移的边界；但 `~/.omp/agent/sessions/...` 路径、JSONL、标题 slot、blob store 和 terminal breadcrumb 是 OMP coding-agent 的部署细节，不应进入 PiMoe generic Runtime（`../oh-my-pi/docs/session.md:33-61`）。

### 4.5 Compaction/memory 内容的安全边界 — **条件可迁移**

核心消息转换把 custom/hook message 映射为 developer content，把 branch summary 映射为带模板的 user content，把 compaction summary 映射为 user content，并保留 providerPayload；未知 custom role 会被过滤，默认 transformer 只保留 core LLM roles 和 package-owned compaction messages（`../oh-my-pi/packages/agent/src/compaction/messages.ts:152-225`；`../oh-my-pi/packages/agent/src/compaction/messages.ts:228-237`）。这表明“长期记忆”在 OMP 中首先是可重放 transcript/summary，而不是脱离上下文的隐式向量记忆；PiMoe 可以把同一类 summary/preserveData 当作外部状态，但其 schema 必须由调用方控制。

## 5. 明确排除的 coding-agent 机制

| 机制 | 分类 | 排除依据 |
|---|---|---|
| `cwd` 每次 provider call 动态解析 | **编码 Agent 专属** | 配置注释直接说明它用于 session move 后的 workspace-scoped provider discovery（`../oh-my-pi/packages/agent/src/types.ts:356-365`）；发送前读取 `getCwd` 的代码在 `../oh-my-pi/packages/agent/src/agent-loop.ts:1271-1273`。 |
| compaction 中的文件操作汇总 | **编码 Agent 专属** | 工具路径 grammar 镜像 coding-agent `path-utils.ts`，且只扫描 `read`/`write`/`edit` 的 path（`../oh-my-pi/packages/agent/src/compaction/utils.ts:30-45`；`../oh-my-pi/packages/agent/src/compaction/utils.ts:97-143`）。 |
| coding-assistant 摘要 prompt 与 `<files>` 标签 | **编码 Agent 专属** | 摘要系统 prompt 明确要求总结“users and AI coding assistants”（`../oh-my-pi/packages/agent/src/compaction/prompts/summarization-system.md:1-4`）；文件操作格式化写入 `<files>` 结构（`../oh-my-pi/packages/agent/src/compaction/utils.ts:146-192`）。 |
| `read_file`/filesystem 工具示例 | **编码 Agent 专属示例** | README 示例直接调用 `fs.readFile`，参数是文件 path（`../oh-my-pi/packages/agent/README.md:280-307`）。通用可迁移的只是 AgentTool schema/execute/update/error contract，不是文件工具本身。 |
| session JSONL 路径、terminal breadcrumb、TUI transcript/tree | **编码 Agent 专属宿主实现** | OMP session 文档将这些放在 coding-agent session storage/layout 中（`../oh-my-pi/docs/session.md:18-31`；`../oh-my-pi/docs/session.md:33-61`）；树导航文档还列出 selector/TUI 与 `/tree`/`/branch` 路由（`../oh-my-pi/docs/session-tree-plan.md:16-25`；`../oh-my-pi/docs/session-tree-plan.md:68-109`）。 |

## 6. 面向 PiMoe stateless Runtime 的契约对照

- **一次 Run 的输入边界**：可把 OMP 的 `AgentContext`、prompts、工具目录、abort/deadline、steering/follow-up 输入视为本次执行所需输入；OMP 入口会复制 context 并把 prompts 追加到副本，而不是要求调用方先修改共享 transcript（`../oh-my-pi/packages/agent/src/agent-loop.ts:302-326`）。
- **一次 Run 的输出边界**：OMP 以 typed lifecycle events 表达 agent、turn、message、tool execution，并在 `agent_end` 携带本次新增 messages（`../oh-my-pi/packages/agent/src/types.ts:705-726`；`../oh-my-pi/packages/agent/src/agent-loop.ts:697-705`）。这与 PiMoe 的 typed event stream 方向一致。
- **Context 的可观察投影**：不要把 provider Message 当作唯一领域消息；OMP 明确保留 AgentMessage/custom message，再在发送前投影和过滤（`../oh-my-pi/packages/agent/src/types.ts:118-167`；`../oh-my-pi/packages/agent/src/compaction/messages.ts:152-237`）。
- **Tool result 的终止保证**：无论真实执行、阻断、abort、provider error 或 length truncation，都要有与 tool call 配对的结果事件/消息；OMP 的 synthetic result 机制给出了可观察的 `executed: false` 区分（`../oh-my-pi/packages/agent/src/agent-loop.ts:2142-2164`）。
- **跨 Run session/memory**：OMP 的 entry tree、compaction summary、preserveData 与 provider replay 都是可独立存取的 session 数据；其纯 compaction 函数把 I/O 留给 SessionManager（`../oh-my-pi/packages/agent/src/compaction/compaction.ts:1-6`；`../oh-my-pi/packages/agent/src/compaction/entries.ts:4-142`）。因此对 PiMoe 的直接含义是：Run 可消费已恢复 context 并产生事件，session persistence/compaction 完成后的状态提交属于 Runtime 外部边界。
- **不应迁移的假设**：不应从 OMP coding-agent 的 cwd、文件路径摘要、JSONL session path、terminal breadcrumb、TUI tree selector 或 `/tree`/`/branch` 交互推导 generic backend Agent Runtime 的 API（`../oh-my-pi/packages/agent/src/types.ts:356-365`；`../oh-my-pi/docs/session.md:33-61`；`../oh-my-pi/docs/session-tree-plan.md:68-109`）。

## 来源索引（直接源码/一方架构文档）

| 主题 | 直接来源与行范围 |
|---|---|
| package 定位与 message/event 概览 | `../oh-my-pi/packages/agent/README.md:1-3,36-53,59-128` |
| Agent loop 入口、continue、EventStream | `../oh-my-pi/packages/agent/src/agent-loop.ts:302-381` |
| loop 状态机、steering/follow-up、tool continuation | `../oh-my-pi/packages/agent/src/agent-loop.ts:724-843,899-1129` |
| Agent loop 配置、上下文 transform、队列 hook | `../oh-my-pi/packages/agent/src/types.ts:89-167,176-254` |
| dynamic model/reasoning、append-only 与 tool dialect | `../oh-my-pi/packages/agent/src/types.ts:269-334` |
| tool hook、AgentMessage、AgentTool、AgentContext、AgentEvent | `../oh-my-pi/packages/agent/src/types.ts:356-428,506-727` |
| provider context 构建与 streaming | `../oh-my-pi/packages/agent/src/agent-loop.ts:1156-1212,1314-1458` |
| tool 查找、校验、执行、结果与调度 | `../oh-my-pi/packages/agent/src/agent-loop.ts:1682-1913,1927-2139` |
| stable prefix / append-only log / digest | `../oh-my-pi/packages/agent/src/append-only-context.ts:1-15,42-90,98-147,153-237,274-349` |
| session entry union 与抽象 manager | `../oh-my-pi/packages/agent/src/compaction/entries.ts:4-142` |
| compaction token policy / prepare / compact | `../oh-my-pi/packages/agent/src/compaction/compaction.ts:1-6,141-331,697-843,1039-1193,1244-1458` |
| core message → LLM projection | `../oh-my-pi/packages/agent/src/compaction/messages.ts:152-237` |
| coding-agent context replay | `../oh-my-pi/packages/coding-agent/src/session/session-context.ts:141-217,251-355` |
| coding-agent append/persist/leaf/tree API | `../oh-my-pi/packages/coding-agent/src/session/session-manager.ts:628-672,783-796,1552-1631` |
| storage backend abstraction | `../oh-my-pi/packages/coding-agent/src/session/session-storage.ts:15-77` |
| session JSONL/tree/layout 一方文档 | `../oh-my-pi/docs/session.md:18-31,33-70,91-122`；`../oh-my-pi/docs/session-tree-plan.md:7-25,27-47,49-127` |
| coding-specific file summary and prompt | `../oh-my-pi/packages/agent/src/compaction/utils.ts:30-45,97-192`；`../oh-my-pi/packages/agent/src/compaction/prompts/summarization-system.md:1-4` |
