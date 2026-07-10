# Docker Compose PostgreSQL Runtime Config Design

## Goal

为本地开发提供 PostgreSQL compose 服务，并让 server 后续从应用级 YAML 读取 session store 配置。当前阶段只容器化 PostgreSQL，不容器化 pimoe server。

## Scope

- 新增 `docker-compose.yml`，提供 `postgres` 服务。
- 使用现有 `migrations/*.sql` 初始化空数据库。
- 新增应用运行时配置样例 `configs/app.example.yaml`。
- 更新 `.env.example`，提供 PostgreSQL host/password 变量名和值。
- 后续 server 支持 `--app-config` 读取应用运行时配置；CLI flags 仍可覆盖 YAML。

不做：

- 不新增 Dockerfile。
- 不把 pimoe server 放入 compose。
- 不把 PostgreSQL password 写进 YAML。
- 不把 session store 配置塞进 `configs/providers.yaml`。
- 不引入 goose/migrate 等迁移工具。

## Configuration Model

`configs/providers.yaml` 继续只负责 LLM provider 配置。新增 `configs/app.example.yaml` 负责应用运行时配置：

```yaml
server:
  addr: ":8080"

session:
  root: ".moe/sessions"
  store:
    type: postgres
    postgres:
      user: pimoe
      password_env: PIMOE_POSTGRES_PASSWORD
      host_env: PIMOE_POSTGRES_HOST
      port: 5432
      database: pimoe
      sslmode: disable
```

DSN 由代码显式读取 YAML 和 env 后组合：

```text
postgres://<user>:<password>@<host>:<port>/<database>?sslmode=<sslmode>
```

`host` 和 `password` 不直接写 YAML，只写 env var 名。这个模式沿用当前 `api_key_env` 约定，不依赖 `${ENV}` 字符串展开。

## Compose Design

`docker-compose.yml` 只包含 PostgreSQL：

- image: `postgres:16-alpine`
- database: `pimoe`
- user: `pimoe`
- password: `${PIMOE_POSTGRES_PASSWORD:-pimoe}`
- published port: `5432:5432`
- data volume: `pimoe-postgres-data`
- init mount: `./migrations/0001_users_sessions.up.sql:/docker-entrypoint-initdb.d/0001_users_sessions.sql:ro`
- healthcheck: `pg_isready -U pimoe -d pimoe`

Docker 官方 initdb 机制只在数据目录首次初始化时执行 `docker-entrypoint-initdb.d`。这里只挂载 `up.sql`，避免同时执行同目录下的 `down.sql` 后删除 schema。当前项目只有初始 schema，满足本地开发。后续需要增量迁移时，再单独引入 migration runner。

## Server Runtime Behavior

后续 `cmd/server` 增加：

- `--app-config configs/app.yaml`
- 从 app YAML 加载：
  - server addr
  - session root
  - session store type
  - PostgreSQL DSN 组成项
- flags 保持覆盖能力：
  - `--addr`
  - `--session-root`
  - `--session-store`
  - `--postgres-dsn`

优先级：

```text
flag > app YAML > 默认值
```

如果 `session.store.type=postgres`，但 host/password env 缺失，server 启动应返回明确错误，指出缺失的 env var 名。

## Developer Flow

```bash
cp configs/app.example.yaml configs/app.yaml
cp .env.example .env

docker compose up -d postgres

set -a
. ./.env
set +a

go run ./cmd/server --app-config configs/app.yaml
```

也可以继续用 flag 覆盖：

```bash
go run ./cmd/server \
  --session-store postgres \
  --postgres-dsn "postgres://pimoe:${PIMOE_POSTGRES_PASSWORD}@${PIMOE_POSTGRES_HOST}:5432/pimoe?sslmode=disable"
```

## Validation

最小验证：

```bash
docker compose config
```

Docker 可用时：

```bash
docker compose up -d postgres
docker compose exec postgres psql -U pimoe -d pimoe -c '\dt'
```

期望看到 `users` 和 `sessions` 表。

涉及 Go 配置加载和 server wiring 时，补充运行：

```bash
go test ./internal/config ./cmd/server -count=1
```

## Risks

- initdb 只在空 volume 首次执行；schema 变更不会自动应用到已有 volume。
- 本地默认端口 `5432` 可能被已有 PostgreSQL 占用；需要时通过 compose 覆盖端口。
- `.env` 可能已有本地密钥，实施时只能改 `.env.example`，不能改 `.env`。
