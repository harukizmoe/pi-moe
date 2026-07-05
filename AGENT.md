# AGENT.md

## 通用约定

- 本项目的 spec、plan、设计说明和任务拆解统一使用中文编写。
- 保持实现简单：先跑通最小闭环，再扩展 HTTP、数据层、记忆、流式输出等能力。
- 优先复用当前模块，不新增平行抽象；没有明确需要时不引入新目录、新接口或新依赖。
- Go 代码必须保持小包、清晰边界、显式错误处理和 `context.Context` 传递。

## 注释规范

- 导出的 Go 类型、接口、函数、方法、常量必须有符合 Go 规范的注释，注释以标识符名称开头。
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
- `internal/application` 保留给后续 HTTP/business 入口；CLI 闭环跑通前不要扩展 router、middleware、data。

## 配置约定

- 配置文件放在 `configs/`，当前主配置为 `configs/providers.yaml`。
- Provider 实例名和实现类型分开：例如 `deepseek` 是实例名，`type: openai_compatible` 是实现类型。
- API Key 使用 `api_key_env` 指向环境变量，不在 YAML 中保存真实密钥，也不依赖 `${ENV}` 字符串展开。

## 当前优先级

1. 用 CLI 跑通 `config -> llms -> agent -> tools -> final answer`。
2. 先支持 fake provider 的确定性 tool call 测试。
3. 再接 OpenAI-compatible `/v1/chat/completions`。
4. 最后再考虑 HTTP API、数据库、memory、streaming、Responses API。

## 验证要求

- 非平凡行为变更必须运行最小可证明的命令或测试。
- 优先用 fake provider 验证 Agent tool calling，不依赖真实网络和付费 API。
- 真实 Provider 只验证协议适配边界，不把业务逻辑写进 provider。
