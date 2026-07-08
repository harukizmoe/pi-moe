# AGENT.md

默认把时间花在推理、验证和交付上；除非需要用户决策，不要逐步汇报过程。

## 工作流约定

### 计划与执行

- 小修直接做；非琐碎任务（3+ 步骤、跨模块修改或架构取舍）先列计划。
- 计划必须写清完成条件和验证方式，不要只罗列实现动作。
- 发现需求冲突、方向错误或关键假设失效时，先修正计划再继续。
- 以当前最小闭环为准；不为“以后可能”提前抽象。

### 子代理使用

- 将研究、审查、测试编写、代码实现等和可并行分析等交给子代理，保持主上下文聚焦。
- 一个子代理只负责一个明确目标，不混塞无关任务。
- 根据计划文档具体实现时，派发子代理完成各项任务。
- 采纳子代理结论前，必须复核证据和相关文件。

### 经验沉淀

- 用户纠正后，先修正当前问题；只有可复用且会再次影响本项目的经验才写入 `docs/evolution/lessons.md`。
- 开发中发现新的架构边界、踩坑规则或 OMP/Tau 可迁移模式，也要写入该文件。
- 经验记录必须包含触发条件和正确做法，避免记录一次性偏好。
- 开发设计前可按需查阅 `docs/evolution/lessons.md`，或参考 Tau/OMP 相关经验。

### 完成标准

- 未验证的工作不能称为完成。
- 非平凡改动必须运行最小可证明测试、命令或冒烟检查，并说明覆盖范围。
- 交付前自查：实现是否简单、边界是否清楚、是否有不必要抽象；剩余风险必须明示。

### 任务记录

- 复杂任务用当前会话 todo 或 `docs/evolution/todo.md` 跟踪；简单任务不建文档。
- 使用 `superpowers` 技能时，按对应 skill 流程执行。
- 任务结束时记录结果、验证命令和遗留风险；无风险写“无”。

### 核心原则

- 简单第一：最少代码、最少文件、最少新概念。
- 根因修复：不做临时绕过，不用注释掩盖问题。
- 最小影响：只改必要范围，保护已有行为。

## 通用约定

- 不在 main 分支做任何开发任务，main 分支只merge其他分支，本身不做任何修改。
- 新功能开发在新的分支中进行。
- 本项目的 spec、plan、设计说明和任务拆解统一使用中文编写。
- 保持实现简单：先跑通最小闭环，再扩展 HTTP、数据层、记忆、流式输出等能力。
- 优先复用当前模块，不新增平行抽象；没有明确需要时不引入新目录、新接口或新依赖。
- Go 代码必须保持小包、清晰边界、显式错误处理和 `context.Context` 传递。
- 用户询问“推荐下一步开发”时，结合当前代码状态和 OMP/Tau 经验，指出项目短板并给出下一步计划。
- 完成一个阶段性开发后，主动给出下一步建议；以本项目当前缺口为依据，不照搬 OMP/Tau。

## 注释规范

- 导出的 Go 类型、接口、函数、方法、常量必须有符合 Go 规范的注释，注释以标识符名称开头。
- Go 注释统一使用中文；涉及协议字段名、HTTP 路径、Provider 类型等专有名词可保留英文原文。
- 结构体字段需要说明业务含义、配置来源或协议映射；显而易见的临时局部变量不写废话注释。
- 函数内部在关键步骤、协议转换、错误处理分支、非显然约束处添加步骤注释；避免逐行翻译代码。
- 注释必须解释“为什么”和边界条件，不重复“做了什么”的代码表面含义。
- 测试中的注释用于说明场景和断言意图，不能替代清晰的测试名称。

## 模块边界

- `internal/llms` 对应 OMP/Tau 的 AI/provider 层：统一 LLM 类型、Provider 接口、Provider 注册、OpenAI-compatible 协议适配。
- 不新增 `internal/ai`；LLM 协议相关代码都放在 `internal/llms`。
- `internal/agent` 负责已装配 Agent 执行器：Provider/tool 注册、LLM 调用、tool-calling 主循环和事件输出；不处理 HTTP 路由或持久化。
- `internal/session` 是应用层唯一会话门面：创建并持有 Agent，负责当前 prompt/run API，后续 session 持久化、resume、branch 都从这里扩展。
- `internal/tools` 只负责工具接口、工具注册、schema 暴露和工具执行。
- `internal/config` 负责 Viper 读取 YAML 和环境变量；其他业务包不直接依赖 Viper。
- `internal/logger` 负责统一日志接口和开发期输出；业务包通过接口注入，不直接操作 `slog`。
- `internal/application` 保留给后续 HTTP/business 入口；CLI 闭环跑通前不要扩展 router、middleware、data。

## 配置约定

- 配置文件放在 `configs/`，当前主配置为 `configs/providers.yaml`。
- Provider 实例名和实现类型分开：例如 `deepseek` 是实例名，`type: openai_compatible` 是实现类型。
- API Key 使用 `api_key_env` 指向环境变量，不在 YAML 中保存真实密钥，也不依赖 `${ENV}` 字符串展开。

## 日志约定

- 开发期日志使用 `internal/logger`，CLI 默认写入 stderr 和 `.moe/logs/agent.log`。
- Agent 记录阶段事件：run start/done、LLM request/error、tool call/result。
- 日志不能写入 API Key、完整密钥配置或不必要的用户隐私内容。

## 开发顺序

1. 先保证 CLI + fake provider 的确定性 tool-calling 闭环。
2. Provider 适配只处理协议转换、错误映射和响应规范化。
3. HTTP API、数据库、memory、streaming、Responses API 后置。

## 验证要求

- 非平凡行为变更必须运行最小可证明的命令或测试。
- 优先用 fake provider 验证 Agent tool calling，不依赖真实网络和付费 API。
- 真实 Provider 只验证协议适配边界，不把业务逻辑写进 provider。
