package llms

// Config 描述从 configs/providers.yaml 加载的所有 LLM Provider 实例。
type Config struct {
	// DefaultProvider 是调用方未显式选择时使用的 Provider 实例名。
	DefaultProvider string `mapstructure:"default_provider"`
	// Providers 将 Provider 实例名（例如 "deepseek"）映射到对应配置。
	Providers map[string]ProviderConfig `mapstructure:"providers"`
}

// ProviderConfig 包含 Provider 实现类型和运行时连接设置。
type ProviderConfig struct {
	// Type 选择 Registry 中注册的 Provider 实现，例如 "openai_compatible" 或 "fake"。
	Type string `mapstructure:"type"`
	// BaseURL 是 OpenAI-compatible 接口根地址，通常以 /v1 结尾。
	BaseURL string `mapstructure:"base_url"`
	// APIKeyEnv 是保存 Provider API Key 的环境变量名。
	APIKeyEnv string `mapstructure:"api_key_env"`
	// APIKey 在加载配置时由 APIKeyEnv 填充，不直接从 YAML 解码。
	APIKey string `mapstructure:"-"`
	// Model 是发送给 Provider 的模型名称。
	Model string `mapstructure:"model"`
	// TimeoutSeconds 限制 Provider HTTP 调用或 fake Provider 等待的最长秒数。
	TimeoutSeconds int `mapstructure:"timeout_seconds"`
}
