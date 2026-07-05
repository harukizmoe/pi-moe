# Task 3 报告：Tools Registry 和 Calculator

## RED 证据

- 测试文件：`internal/tools/calculator_test.go`
- 执行者：`Task3Tools.Task3Tester`
- 命令：`go test ./internal/tools -v`
- 失败输出（来自 IRC handoff）：

```text
build failures:
- undefined: Calculator at internal/tools/calculator_test.go:9
- undefined: Calculator at internal/tools/calculator_test.go:20
- undefined: NewRegistry at internal/tools/calculator_test.go:28
- undefined: Calculator at internal/tools/calculator_test.go:29
```

- 预期原因：Task 3 的测试先写好后，`internal/tools` 中的 `Calculator`、`NewRegistry` 以及相关工具接口/实现尚未创建，因此包应当先以未定义符号失败，符合 TDD 的 RED 阶段预期。

## GREEN 证据

- 命令：`go test ./internal/tools -v`
- 通过输出：

```text
=== RUN   TestCalculatorMul
--- PASS: TestCalculatorMul (0.00s)
=== RUN   TestCalculatorDivideByZero
--- PASS: TestCalculatorDivideByZero (0.00s)
=== RUN   TestRegistrySchemasAndCall
--- PASS: TestRegistrySchemasAndCall (0.00s)
PASS
ok  	harukizmoe/pimoe/internal/tools	0.004s
```

## 变更文件

- `internal/tools/tool.go`
- `internal/tools/registry.go`
- `internal/tools/calculator.go`
- `internal/tools/calculator_test.go`
- `.superpowers/sdd/task-3-report.md`

## 实现说明

- 新增 `Tool` 接口，统一工具名称、描述、参数 schema 和调用入口。
- 新增 `Registry`，负责：
  - 注册本地工具；
  - 导出 OpenAI-style `function` schema（基于 `internal/llms.Tool` / `llms.ToolFunction`）；
  - 按名称分发工具调用。
- 新增 `Calculator` 工具，支持 `add` / `sub` / `mul` / `div` 四种运算。
- `Calculator.Call` 使用 JSON 解析参数，除零和不支持的操作都会返回显式错误。
- 测试覆盖：
  - `13 * 7 == 91`
  - `div` 除零报错
  - `Registry` 的 schema 导出和调用转发

## Self-review findings

- 实现范围严格限制在 `internal/tools`，没有修改 agent loop、provider、CLI 或其他非目标模块。
- `Registry.Schemas()` 直接复用 `internal/llms` 中的协议类型，符合 AGENT.md 的模块边界要求。
- 错误路径覆盖了未知 tool、非法 JSON、除零、非法 op 等基础失败场景，其中本任务测试明确验证了除零和 registry 调用闭环。
- 当前 `Schemas()` 迭代 map 的顺序不稳定，但本任务只注册一个工具，且验收只要求导出 calculator schema，因此当前实现满足需求且不引入额外复杂度。

## Required arguments fix

### RED evidence

- Added `TestCalculatorMissingRequiredArguments` in `internal/tools/calculator_test.go`.
- Command: `go test ./internal/tools -run TestCalculatorMissingRequiredArguments -v`
- Failure output:

```text
=== RUN   TestCalculatorMissingRequiredArguments
=== RUN   TestCalculatorMissingRequiredArguments/missing_a
    calculator_test.go:43: Call() error = nil
=== RUN   TestCalculatorMissingRequiredArguments/missing_b
    calculator_test.go:46: Call() error = "divide by zero", want substring "missing required argument \"b\""
=== RUN   TestCalculatorMissingRequiredArguments/missing_op
    calculator_test.go:46: Call() error = "unsupported calculator op \"\"", want substring "missing required argument \"op\""
--- FAIL: TestCalculatorMissingRequiredArguments (0.00s)
    --- FAIL: TestCalculatorMissingRequiredArguments/missing_a (0.00s)
    --- FAIL: TestCalculatorMissingRequiredArguments/missing_b (0.00s)
    --- FAIL: TestCalculatorMissingRequiredArguments/missing_op (0.00s)
FAIL
FAIL    harukizmoe/pimoe/internal/tools    0.005s
FAIL
```

### GREEN evidence

- Implementation now rejects missing `a`, `b`, and `op` before decoding into zero values.
- Focused command: `go test ./internal/tools -run TestCalculatorMissingRequiredArguments -v`

```text
=== RUN   TestCalculatorMissingRequiredArguments
=== RUN   TestCalculatorMissingRequiredArguments/missing_a
=== RUN   TestCalculatorMissingRequiredArguments/missing_b
=== RUN   TestCalculatorMissingRequiredArguments/missing_op
--- PASS: TestCalculatorMissingRequiredArguments (0.00s)
    --- PASS: TestCalculatorMissingRequiredArguments/missing_a (0.00s)
    --- PASS: TestCalculatorMissingRequiredArguments/missing_b (0.00s)
    --- PASS: TestCalculatorMissingRequiredArguments/missing_op (0.00s)
PASS
ok      harukizmoe/pimoe/internal/tools    0.003s
```

- Acceptance command: `go test ./internal/tools -v`

```text
=== RUN   TestCalculatorMul
--- PASS: TestCalculatorMul (0.00s)
=== RUN   TestCalculatorDivideByZero
--- PASS: TestCalculatorDivideByZero (0.00s)
=== RUN   TestCalculatorMissingRequiredArguments
=== RUN   TestCalculatorMissingRequiredArguments/missing_a
=== RUN   TestCalculatorMissingRequiredArguments/missing_b
=== RUN   TestCalculatorMissingRequiredArguments/missing_op
--- PASS: TestCalculatorMissingRequiredArguments (0.00s)
    --- PASS: TestCalculatorMissingRequiredArguments/missing_a (0.00s)
    --- PASS: TestCalculatorMissingRequiredArguments/missing_b (0.00s)
    --- PASS: TestCalculatorMissingRequiredArguments/missing_op (0.00s)
=== RUN   TestRegistrySchemasAndCall
--- PASS: TestRegistrySchemasAndCall (0.00s)
PASS
ok      harukizmoe/pimoe/internal/tools    0.004s
```
