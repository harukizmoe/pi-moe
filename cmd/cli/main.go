package main

import (
	"context"
	"fmt"
	"log"

	"harukizmoe/pimoe/internal/agent"
	"harukizmoe/pimoe/internal/config"
	"harukizmoe/pimoe/internal/llms"
	"harukizmoe/pimoe/internal/tools"
)

func main() {
	cfg, err := config.Load("configs/providers.yaml")
	if err != nil {
		log.Fatal(err)
	}

	providerName := cfg.LLMs.DefaultProvider
	// 用实例名选择配置，让默认 Provider 和底层实现类型保持解耦。
	providerConfig, ok := cfg.LLMs.Providers[providerName]
	if !ok {
		log.Fatalf("unknown default provider %q", providerName)
	}

	llmRegistry := llms.NewRegistry()
	llmRegistry.Register("fake", llms.NewFakeProvider)
	llmRegistry.Register("openai_compatible", llms.NewOpenAICompatibleProvider)

	provider, err := llmRegistry.NewProvider(providerConfig)
	if err != nil {
		log.Fatal(err)
	}

	toolRegistry := tools.NewRegistry()
	// 当前 CLI 只接 calculator，用于验证完整的 tool calling 最小闭环。
	toolRegistry.Register(tools.Calculator{})

	a := agent.New(provider, toolRegistry, providerConfig.Model)
	answer, err := a.Run(context.Background(), "use calculator to compute 13 * 7")
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println(answer)
}
