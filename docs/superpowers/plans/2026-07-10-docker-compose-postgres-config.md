# Docker Compose PostgreSQL Config Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add local Docker Compose PostgreSQL support and YAML-driven runtime session store configuration without containerizing the pimoe server.

**Architecture:** Keep LLM provider config in `configs/providers.yaml` and add a separate app runtime config in `configs/app.example.yaml`. Compose starts only PostgreSQL, initializes the existing SQL schema on first volume creation, and server config loading composes the PostgreSQL DSN from YAML non-secret fields plus env-provided host/password.

**Tech Stack:** Go 1.26, Viper YAML loading, Docker Compose, PostgreSQL official image, existing `internal/session.SessionStore` and `internal/storage/postgres`.

---

## File Structure

- Create `docker-compose.yml`: local PostgreSQL service, named volume, migrations init mount, healthcheck.
- Create `configs/app.example.yaml`: non-sensitive runtime config with env var names for PostgreSQL host/password.
- Modify `.env.example`: add `PIMOE_POSTGRES_HOST` and `PIMOE_POSTGRES_PASSWORD` defaults only; do not edit `.env`.
- Modify `internal/config/config.go`: add app runtime config structs and `LoadApp` for server runtime config; keep existing `Load` provider behavior unchanged.
- Modify `internal/config/config_test.go`: add tests for app YAML parsing, DSN composition from env, and missing env errors.
- Modify `cmd/server/main.go`: add `--app-config`, merge YAML defaults with flags, and keep explicit flags as highest precedence.
- Modify `cmd/server/main_test.go`: add server option tests for app config defaults and flag override precedence.

---

### Task 1: Compose and Example Runtime Files

**Files:**
- Create: `docker-compose.yml`
- Create: `configs/app.example.yaml`
- Modify: `.env.example`

- [ ] **Step 1: Create Docker Compose file**

Create `docker-compose.yml` with exactly:

```yaml
services:
  postgres:
    image: postgres:16-alpine
    environment:
      POSTGRES_DB: pimoe
      POSTGRES_USER: pimoe
      POSTGRES_PASSWORD: ${PIMOE_POSTGRES_PASSWORD:-pimoe}
    ports:
      - "5432:5432"
    volumes:
      - pimoe-postgres-data:/var/lib/postgresql/data
      - ./migrations/0001_users_sessions.up.sql:/docker-entrypoint-initdb.d/0001_users_sessions.sql:ro
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U pimoe -d pimoe"]
      interval: 5s
      timeout: 3s
      retries: 10

volumes:
  pimoe-postgres-data:
```

- [ ] **Step 2: Create app config example**

Create `configs/app.example.yaml` with exactly:

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

- [ ] **Step 3: Update env example**

Append to `.env.example`:

```env
# Local PostgreSQL used by docker-compose.yml and configs/app.example.yaml.
PIMOE_POSTGRES_HOST=localhost
PIMOE_POSTGRES_PASSWORD=pimoe
```

Do not edit `.env`.

- [ ] **Step 4: Validate compose syntax**

Run:

```bash
docker compose config
```

Expected: command exits 0 and prints normalized compose config. If Docker Compose is unavailable in the environment, record the exact error and continue; do not fake success.

- [ ] **Step 5: Commit**

```bash
git add docker-compose.yml configs/app.example.yaml .env.example
git commit -m "chore: add postgres compose config"
```

---

### Task 2: App Runtime Config Loader

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`

- [ ] **Step 1: Write failing tests**

Add tests to `internal/config/config_test.go`:

```go
func TestLoadAppBuildsPostgresDSNFromYAMLAndEnv(t *testing.T) {
	t.Setenv("PIMOE_POSTGRES_HOST", "localhost")
	t.Setenv("PIMOE_POSTGRES_PASSWORD", "secret")
	path := writeConfigFile(t, `server:
  addr: ":9090"
session:
  root: "state/sessions"
  store:
    type: postgres
    postgres:
      user: pimoe
      password_env: PIMOE_POSTGRES_PASSWORD
      host_env: PIMOE_POSTGRES_HOST
      port: 5433
      database: pimoe_test
      sslmode: disable
`)

	cfg, err := LoadApp(path)
	if err != nil {
		t.Fatalf("LoadApp() error = %v", err)
	}
	if cfg.Server.Addr != ":9090" {
		t.Fatalf("Server.Addr = %q, want :9090", cfg.Server.Addr)
	}
	if cfg.Session.Root != "state/sessions" {
		t.Fatalf("Session.Root = %q, want state/sessions", cfg.Session.Root)
	}
	if cfg.Session.Store.Type != "postgres" {
		t.Fatalf("store type = %q, want postgres", cfg.Session.Store.Type)
	}
	wantDSN := "postgres://pimoe:secret@localhost:5433/pimoe_test?sslmode=disable"
	if cfg.Session.Store.Postgres.DSN != wantDSN {
		t.Fatalf("postgres DSN = %q, want %q", cfg.Session.Store.Postgres.DSN, wantDSN)
	}
}

func TestLoadAppRejectsMissingPostgresEnv(t *testing.T) {
	path := writeConfigFile(t, `session:
  store:
    type: postgres
    postgres:
      user: pimoe
      password_env: PIMOE_POSTGRES_PASSWORD
      host_env: PIMOE_POSTGRES_HOST
      port: 5432
      database: pimoe
      sslmode: disable
`)

	_, err := LoadApp(path)
	if err == nil {
		t.Fatal("LoadApp() error = nil, want missing env error")
	}
	if !strings.Contains(err.Error(), "PIMOE_POSTGRES_HOST") {
		t.Fatalf("LoadApp() error = %q, want host env name", err.Error())
	}
}
```

- [ ] **Step 2: Run tests to verify RED**

Run:

```bash
go test ./internal/config -count=1
```

Expected: FAIL because `LoadApp` and app config types do not exist.

- [ ] **Step 3: Implement app config types and loader**

In `internal/config/config.go`, add these exported types below existing `Config`:

```go
// AppConfig 是从 YAML 加载的应用运行时配置，不包含 LLM Provider 密钥。
type AppConfig struct {
	// Server 保存 HTTP server 运行参数。
	Server ServerConfig `mapstructure:"server"`
	// Session 保存 session metadata store 和 transcript 根目录配置。
	Session SessionRuntimeConfig `mapstructure:"session"`
}

// ServerConfig 保存 HTTP server 运行参数。
type ServerConfig struct {
	// Addr 是 HTTP 监听地址，例如 :8080。
	Addr string `mapstructure:"addr"`
}

// SessionRuntimeConfig 保存 session 运行时配置。
type SessionRuntimeConfig struct {
	// Root 是 transcript 文件根目录。
	Root string `mapstructure:"root"`
	// Store 保存 session metadata store 配置。
	Store SessionStoreConfig `mapstructure:"store"`
}

// SessionStoreConfig 保存 session metadata store 类型和具体配置。
type SessionStoreConfig struct {
	// Type 是 metadata store 类型：file 或 postgres。
	Type string `mapstructure:"type"`
	// Postgres 保存 PostgreSQL store 配置。
	Postgres PostgresStoreConfig `mapstructure:"postgres"`
}

// PostgresStoreConfig 保存 PostgreSQL DSN 组成项；Host 和 Password 来自环境变量。
type PostgresStoreConfig struct {
	// User 是 PostgreSQL 用户名。
	User string `mapstructure:"user"`
	// PasswordEnv 是保存 PostgreSQL 密码的环境变量名。
	PasswordEnv string `mapstructure:"password_env"`
	// HostEnv 是保存 PostgreSQL host 的环境变量名。
	HostEnv string `mapstructure:"host_env"`
	// Port 是 PostgreSQL 端口。
	Port int `mapstructure:"port"`
	// Database 是 PostgreSQL database 名称。
	Database string `mapstructure:"database"`
	// SSLMode 是 PostgreSQL sslmode 参数。
	SSLMode string `mapstructure:"sslmode"`
	// DSN 是加载时由 YAML 和环境变量组合出的连接串，不从 YAML 读取。
	DSN string `mapstructure:"-"`
}
```

Add `LoadApp` and helpers:

```go
// LoadApp 读取应用运行时 YAML 配置，并解析 PostgreSQL host/password 环境变量。
func LoadApp(path string) (*AppConfig, error) {
	v := viper.New()
	v.SetConfigFile(path)
	v.SetConfigType("yaml")
	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("read app config %q: %w", path, err)
	}

	var cfg AppConfig
	if err := v.UnmarshalExact(&cfg); err != nil {
		return nil, fmt.Errorf("decode app config %q: %w", path, err)
	}
	if err := populatePostgresDSN(&cfg); err != nil {
		return nil, fmt.Errorf("validate app config %q: %w", path, err)
	}
	return &cfg, nil
}

func populatePostgresDSN(cfg *AppConfig) error {
	storeType := strings.ToLower(strings.TrimSpace(cfg.Session.Store.Type))
	if storeType != "postgres" {
		cfg.Session.Store.Type = storeType
		return nil
	}
	pg := cfg.Session.Store.Postgres
	hostEnv := strings.TrimSpace(pg.HostEnv)
	passwordEnv := strings.TrimSpace(pg.PasswordEnv)
	if hostEnv == "" {
		return fmt.Errorf("postgres host_env is required")
	}
	if passwordEnv == "" {
		return fmt.Errorf("postgres password_env is required")
	}
	host := strings.TrimSpace(os.Getenv(hostEnv))
	password := os.Getenv(passwordEnv)
	if host == "" {
		return fmt.Errorf("postgres host env %s is required", hostEnv)
	}
	if password == "" {
		return fmt.Errorf("postgres password env %s is required", passwordEnv)
	}
	if strings.TrimSpace(pg.User) == "" {
		return fmt.Errorf("postgres user is required")
	}
	if pg.Port <= 0 {
		return fmt.Errorf("postgres port is required")
	}
	if strings.TrimSpace(pg.Database) == "" {
		return fmt.Errorf("postgres database is required")
	}
	sslmode := strings.TrimSpace(pg.SSLMode)
	if sslmode == "" {
		sslmode = "disable"
	}
	pg.SSLMode = sslmode
	pg.DSN = fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=%s", pg.User, password, host, pg.Port, pg.Database, sslmode)
	cfg.Session.Store.Type = storeType
	cfg.Session.Store.Postgres = pg
	return nil
}
```

- [ ] **Step 4: Run config tests GREEN**

Run:

```bash
go test ./internal/config -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat: load app runtime config"
```

---

### Task 3: Server App Config Wiring

**Files:**
- Modify: `cmd/server/main.go`
- Modify: `cmd/server/main_test.go`

- [ ] **Step 1: Write failing server option tests**

Add tests to `cmd/server/main_test.go`:

```go
func TestParseServerOptionsLoadsAppConfigDefaults(t *testing.T) {
	t.Setenv("PIMOE_POSTGRES_HOST", "localhost")
	t.Setenv("PIMOE_POSTGRES_PASSWORD", "secret")
	path := writeServerAppConfig(t, `server:
  addr: ":9090"
session:
  root: "state/sessions"
  store:
    type: postgres
    postgres:
      user: pimoe
      password_env: PIMOE_POSTGRES_PASSWORD
      host_env: PIMOE_POSTGRES_HOST
      port: 5432
      database: pimoe
      sslmode: disable
`)

	got, err := parseServerOptions([]string{"--app-config", path})
	if err != nil {
		t.Fatalf("parseServerOptions() error = %v", err)
	}
	if got.addr != ":9090" {
		t.Fatalf("addr = %q, want app config value", got.addr)
	}
	if got.sessionRoot != "state/sessions" {
		t.Fatalf("sessionRoot = %q, want app config value", got.sessionRoot)
	}
	if got.sessionStore != "postgres" {
		t.Fatalf("sessionStore = %q, want postgres", got.sessionStore)
	}
	wantDSN := "postgres://pimoe:secret@localhost:5432/pimoe?sslmode=disable"
	if got.postgresDSN != wantDSN {
		t.Fatalf("postgresDSN = %q, want %q", got.postgresDSN, wantDSN)
	}
}

func TestParseServerOptionsFlagsOverrideAppConfig(t *testing.T) {
	t.Setenv("PIMOE_POSTGRES_HOST", "localhost")
	t.Setenv("PIMOE_POSTGRES_PASSWORD", "secret")
	path := writeServerAppConfig(t, `server:
  addr: ":9090"
session:
  root: "state/sessions"
  store:
    type: postgres
    postgres:
      user: pimoe
      password_env: PIMOE_POSTGRES_PASSWORD
      host_env: PIMOE_POSTGRES_HOST
      port: 5432
      database: pimoe
      sslmode: disable
`)

	got, err := parseServerOptions([]string{
		"--app-config", path,
		"--addr", ":7070",
		"--session-root", "override/sessions",
		"--session-store", "file",
	})
	if err != nil {
		t.Fatalf("parseServerOptions() error = %v", err)
	}
	if got.addr != ":7070" {
		t.Fatalf("addr = %q, want flag override", got.addr)
	}
	if got.sessionRoot != "override/sessions" {
		t.Fatalf("sessionRoot = %q, want flag override", got.sessionRoot)
	}
	if got.sessionStore != "file" {
		t.Fatalf("sessionStore = %q, want flag override", got.sessionStore)
	}
}
```

Add helper:

```go
func writeServerAppConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "app.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write app config: %v", err)
	}
	return path
}
```

Update imports to include `os` and `path/filepath`.

- [ ] **Step 2: Run tests to verify RED**

Run:

```bash
go test ./cmd/server -count=1
```

Expected: FAIL because `--app-config` and YAML merge are not implemented.

- [ ] **Step 3: Implement server option merge**

In `cmd/server/main.go`:

- Add import `appconfig "harukizmoe/pimoe/internal/config"` if missing.
- Add field to `serverOptions`:

```go
appConfigPath string
```

- In `parseServerOptions`, use local variables to detect flag presence:

```go
flags.StringVar(&opts.appConfigPath, "app-config", "", "application runtime YAML config path")
```

After `flags.Parse(args)`, call:

```go
if strings.TrimSpace(opts.appConfigPath) != "" {
	loaded, err := appconfig.LoadApp(opts.appConfigPath)
	if err != nil {
		return serverOptions{}, err
	}
	applyAppConfigDefaults(&opts, loaded, flags)
}
```

Then call `validateServerOptions(&opts)`.

Add helper:

```go
func applyAppConfigDefaults(opts *serverOptions, cfg *appconfig.AppConfig, flags *flag.FlagSet) {
	if cfg == nil {
		return
	}
	if flagWasNotSet(flags, "addr") && strings.TrimSpace(cfg.Server.Addr) != "" {
		opts.addr = cfg.Server.Addr
	}
	if flagWasNotSet(flags, "session-root") && strings.TrimSpace(cfg.Session.Root) != "" {
		opts.sessionRoot = cfg.Session.Root
	}
	if flagWasNotSet(flags, "session-store") && strings.TrimSpace(cfg.Session.Store.Type) != "" {
		opts.sessionStore = cfg.Session.Store.Type
	}
	if flagWasNotSet(flags, "postgres-dsn") && strings.TrimSpace(cfg.Session.Store.Postgres.DSN) != "" {
		opts.postgresDSN = cfg.Session.Store.Postgres.DSN
	}
}

func flagWasNotSet(flags *flag.FlagSet, name string) bool {
	seen := false
	flags.Visit(func(flag *flag.Flag) {
		if flag.Name == name {
			seen = true
		}
	})
	return !seen
}
```

- [ ] **Step 4: Run server tests GREEN**

Run:

```bash
go test ./cmd/server -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/server/main.go cmd/server/main_test.go
git commit -m "feat: load server runtime config"
```

---

### Task 4: Final Verification

**Files:**
- No code changes unless verification exposes defects.

- [ ] **Step 1: Validate compose**

Run:

```bash
docker compose config
```

Expected: PASS if Docker Compose is available. If unavailable, capture exact command output and note it as environment limitation.

- [ ] **Step 2: Run affected Go tests**

Run:

```bash
go test ./internal/config ./cmd/server -count=1
```

Expected: PASS.

- [ ] **Step 3: Run full tests**

Run:

```bash
go test -count=1 ./...
```

Expected: PASS.

- [ ] **Step 4: Run targeted vet**

Run:

```bash
go vet ./cmd/server ./internal/config
```

Expected: no output, exit 0.

- [ ] **Step 5: Inspect final status**

Run:

```bash
git status --short
```

Expected: clean after commits.

If files remain modified, either commit intentional changes or explain why they are intentionally uncommitted.
