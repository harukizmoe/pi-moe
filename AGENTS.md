# PiMoe Agent Guide

## 工作方式

- 除非需要用户决策，不逐步汇报过程；把时间用于推理、验证和交付。
- 小修直接完成。涉及 3+ 步骤、跨模块变更或架构取舍时，先写出完成条件和验证方式；关键假设失效时先修正计划。
- 规格、计划、设计说明和任务拆解使用中文。
- 优先完成当前最小闭环；不为未来假设增加目录、接口、依赖或平行抽象。

## 交付要求

- 修复根因，不用临时绕过或注释掩盖问题；只改必要范围并保护已有行为。
- 未验证的变更不能称为完成。非平凡改动运行最小、能证明行为的测试或冒烟检查，并说明覆盖范围和剩余风险。
- 交付前检查边界是否清晰、实现是否简单、是否引入不必要抽象。

## Go 约定

- 保持包小、边界清晰、显式处理错误，并沿调用链传递 `context.Context`。
- 代码实现中的注释必须使用中文。导出的 Go 类型、接口、函数、方法和常量写以标识符开头、足够详细且能说明用途与约束的中文注释；协议字段、HTTP 路径和 Provider 类型等专名保留英文。
- 函数内部只在关键步骤添加注释，说明意图、不变量、协议转换、执行顺序以及重要错误分支的处理依据；不得逐行复述代码。
- 测试名称说明场景和断言；测试注释仅补充断言意图。

## 模块边界

- `internal/llms`：LLM 类型、Provider 接口与注册，以及 OpenAI-compatible 协议适配。
- `internal/agent`：装配 Provider 与工具，执行 tool-calling 循环并输出事件；不处理 HTTP 路由或会话持久化。
- `internal/session`：应用层的会话门面，创建并持有已装配的 Agent；会话持久化、恢复和分支从这里扩展。
- `internal/tools`：工具接口、注册、schema 暴露和执行。
- `internal/config`：使用 Viper 读取 YAML 和环境变量；其他业务包不直接依赖 Viper。
- `internal/logger`：统一日志接口和开发期输出；业务包通过接口注入，不直接操作 `slog`。
- `internal/application`：HTTP 应用层，负责 router、handler 和 service 的协议转换与用例编排；不承载 LLM 实现或持久化细节。
- `internal/storage`：持久化适配层；PostgreSQL 实现位于 `internal/storage/postgres`。

## 配置

- 配置文件放在 `configs/`，主 Provider 配置为 `configs/providers.yaml`。
- Provider 实例名和实现类型分开，例如 `deepseek` 是实例名，`type: openai_compatible` 是实现类型。
- API Key 使用 `api_key_env` 指向环境变量；不把真实密钥放入 YAML，也不依赖 `${ENV}` 字符串展开。

## 经验与领域文档

- 开发设计或排障前，按需阅读 `docs/evolution/lessons.md`。只记录经验证、会重复影响本仓库的实践；领域术语放入 `CONTEXT.md`，架构取舍放入 ADR，而非 lessons。
- 按 `docs/agents/domain.md` 的规则，在相关文件存在时阅读 `CONTEXT.md` 和 ADR；不存在时不创建占位文档。

## Agent skills

### Issue tracker

Issues and specs are local Markdown files under `.scratch/`. See `docs/agents/issue-tracker.md`.

### Triage labels

Uses the default five canonical triage labels. See `docs/agents/triage-labels.md`.

### Domain docs

Uses the single-context layout. See `docs/agents/domain.md`.

