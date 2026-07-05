# Task 1 报告：配置结构和 Viper 加载

## 执行摘要
- 按 brief 添加了 `github.com/spf13/viper` 依赖。
- 先写 `internal/config/config_test.go`，执行聚焦测试确认 RED。
- 按 brief 重写 `configs/providers.yaml`，改为 `llms.default_provider` + `type/api_key_env` 结构。
- 实现了 `internal/llms/config.go` 与 `internal/config/config.go`。
- 再次执行聚焦测试确认 GREEN。
- 为了让 `internal/llms` 包可编译，补了 `internal/llms/provider.go` 的最小 `package llms` 声明。

## RED 证据
命令：

```bash
go test ./internal/config -run TestLoadProvidersConfig -v
```

输出：

```text
# harukizmoe/pimoe/internal/config [harukizmoe/pimoe/internal/config.test]
internal/config/config_test.go:28:14: undefined: Load
FAIL    harukizmoe/pimoe/internal/config [build failed]
FAIL
```

结论：测试先失败，失败原因符合预期（`Load` 尚未实现）。

## GREEN 证据
命令：

```bash
go test ./internal/config -run TestLoadProvidersConfig -v
```

输出：

```text
=== RUN   TestLoadProvidersConfig
--- PASS: TestLoadProvidersConfig (0.00s)
PASS
ok      harukizmoe/pimoe/internal/config    0.007s
```

结论：聚焦测试通过，`Load(path)` 能从 YAML 读取 `llms` 配置，并按 `api_key_env` 注入环境变量中的 API Key。

## 文件变更
### 目标文件
- `configs/providers.yaml`
  - 根键从 `ai` 改为 `llms`
  - 去掉 `${ENV}` 形式的 `api_key`
  - 改为 `default_provider` + `providers.<name>.type/api_key_env/model/timeout_seconds`
- `internal/llms/config.go`
  - 新增 `llms.Config`
  - 新增 `llms.ProviderConfig`
- `internal/config/config.go`
  - 新增 `Config`
  - 实现 `Load(path string)`
  - `internal/config` 成为唯一直接依赖 Viper 的业务包
- `internal/config/config_test.go`
  - 新增 `TestLoadProvidersConfig`
- `go.mod`
  - 添加 `github.com/spf13/viper` 及其依赖记录
- `go.sum`
  - 添加 Viper 相关校验和

### 额外最小修复
- `internal/llms/provider.go`
  - 原文件为空，导入 `internal/llms` 后会导致包编译失败
  - 补充最小 `package llms` 声明，避免影响本任务验收

## 自检
- [x] 只实现了 Task 1 要求的配置结构和 Viper 加载
- [x] 未扩展 provider 实现、tools、agent loop、CLI
- [x] `configs/providers.yaml` 不再使用 `${ENV}`
- [x] `internal/config` 是唯一直接导入 Viper 的业务包
- [x] 按 TDD 执行了 RED -> GREEN
- [x] 只运行了 brief 指定的聚焦测试，没有跑项目级测试、lint、formatter

## 待主任务方知晓的点
- `internal/llms/provider.go` 原本为空文件，若不补最小包声明，`internal/config` 在导入 `internal/llms` 后无法编译。该修复不改变行为，只保证包可编译。
