package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/viper"

	"harukizmoe/pimoe/internal/llms"
)

// Config 是从 YAML 加载的应用根配置。
type Config struct {
	// LLMs 包含 Provider 实例和默认 Provider 选择。
	LLMs llms.Config `mapstructure:"llms"`
}

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

// Load 读取 YAML 配置文件，并解析由环境变量承载的密钥。
func Load(path string) (*Config, error) {
	v := viper.New()
	v.SetConfigFile(path)
	v.SetConfigType("yaml")

	// 先读取文件，让读取错误带上明确的配置路径。
	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("read config %q: %w", path, err)
	}

	var cfg Config
	if err := v.UnmarshalExact(&cfg); err != nil {
		return nil, fmt.Errorf("decode config %q: %w", path, err)
	}
	if err := validateProviderConfig(cfg); err != nil {
		return nil, fmt.Errorf("validate config %q: %w", path, err)
	}

	// API Key 不写入 YAML；每个 Provider 只声明需要读取的环境变量名。
	for name, provider := range cfg.LLMs.Providers {
		if provider.APIKeyEnv != "" {
			provider.APIKey = os.Getenv(provider.APIKeyEnv)
		}
		cfg.LLMs.Providers[name] = provider
	}

	return &cfg, nil
}

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

func validateProviderConfig(cfg Config) error {
	for name, provider := range cfg.LLMs.Providers {
		if provider.Type != "openai_compatible" {
			continue
		}
		if strings.TrimSpace(provider.BaseURL) == "" {
			return fmt.Errorf("provider %q openai_compatible base_url is required", name)
		}
		if strings.TrimSpace(provider.Model) == "" {
			return fmt.Errorf("provider %q openai_compatible model is required", name)
		}
		if strings.TrimSpace(provider.APIKeyEnv) == "" {
			return fmt.Errorf("provider %q openai_compatible api_key_env is required", name)
		}
	}
	return nil
}
