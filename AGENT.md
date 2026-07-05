# AGENT.md

优先把时间用于推理、验证和交付；除非需要用户决策，不必逐步汇报过程。

## 工作流约定

### 计划与执行

- 非琐碎任务（3+ 步骤、跨模块修改、架构决策）先写计划；小修直接做。
- 计划必须包含可验证的完成条件，不只列实现步骤。
- 执行中发现方向错误、需求冲突或关键假设失效时，先停下修正计划，再继续。
- 不为“以后可能需要”提前设计；当前最小闭环优先。

### 子代理使用

- 研究、代码审查、测试编写、并行分析可交给子代理，避免主上下文膨胀。
- 每个子代理只负责一个清晰目标；禁止把多个不相关任务塞给同一个子代理。
- 子代理结果必须被主代理复核后再采纳，不能直接当作最终事实。

### 反馈与经验沉淀

- 用户指出错误后，先修正当前问题，再判断是否需要记录到 `docs/evolution/lessons.md`。
- 项目开发中发现可复用的架构判断、边界规则、踩坑经验或从 OMP 学到的新模式时，要自主更新 `docs/evolution/lessons.md`。
- 只记录会再次影响本项目的经验；不要把一次性偏好或临时决策写成永久规则。
- 经验必须具体到触发条件和正确做法。

### 完成标准

- 未验证的工作不能标记为完成。
- 非平凡改动必须运行最小可证明的测试、命令或 smoke check。
- 涉及行为变化时，说明验证覆盖了什么；未覆盖的风险要明确写出。
- 提交前自查：实现是否简单、边界是否清楚、是否有不必要的抽象。

### 任务记录

- 复杂任务使用 `docs/evolution/todo.md` 或当前会话 todo 跟踪；简单任务不强制建文档。
- 使用 superpowers skill 时，以 skill 流程为准。
- 任务结束时记录结果、验证命令和遗留风险；没有风险就写“无”。

### 核心原则

- 简单第一：最少代码、最少文件、最少新概念。
- 根因修复：不做临时绕过，不用注释掩盖问题。
- 最小影响：只改必要范围，保护已有行为。

## 通用约定

- 本项目的 spec、plan、设计说明和任务拆解统一使用中文编写。
- 保持实现简单：先跑通最小闭环，再扩展 HTTP、数据层、记忆、流式输出等能力。
- 优先复用当前模块，不新增平行抽象；没有明确需要时不引入新目录、新接口或新依赖。
- Go 代码必须保持小包、清晰边界、显式错误处理和 `context.Context` 传递。

## 注释规范

- 导出的 Go 类型、接口、函数、方法、常量必须有符合 Go 规范的注释，注释以标识符名称开头。
- Go 注释统一使用中文；涉及协议字段名、HTTP 路径、Provider 类型等专有名词可保留英文原文。
- 结构体字段需要说明业务含义、配置来源或协议映射；显而易见的临时局部变量不写废话注释。
- 函数内部只在关键步骤、协议转换、错误处理分支、非显然约束处添加步骤注释；避免逐行翻译代码。
- 注释必须解释“为什么”和边界条件，不重复“做了什么”的代码表面含义。
- 测试中的注释用于说明场景和断言意图，不能替代清晰的测试名称。

## 模块边界

- `internal/llms` 对应 `oh-my-pi/packages/ai` 的 AI 层：统一 LLM 类型、Provider 接口、Provider 注册、OpenAI-compatible 协议适配。
- 不新增 `internal/ai`；LLM 协议相关代码都放在 `internal/llms`。
- `internal/agent` 只负责 Agent 主循环和 tool calling 流程，不处理配置读取、HTTP 路由或具体 Provider HTTP 细节。
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
