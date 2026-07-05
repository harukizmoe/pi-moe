package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"strings"

	"harukizmoe/pimoe/internal/agent"
	"harukizmoe/pimoe/internal/config"
	"harukizmoe/pimoe/internal/llms"
	"harukizmoe/pimoe/internal/logger"
	"harukizmoe/pimoe/internal/tools"
)

const agentLogPath = ".moe/logs/agent.log"

func main() {
	appLogger, closeLogger, err := logger.NewDevelopmentFile(agentLogPath)
	if err != nil {
		log.Fatal(err)
	}
	defer func() {
		if err := closeLogger(); err != nil {
			log.Printf("close logger: %v", err)
		}
	}()

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

	input, err := readInput(os.Args[1:], os.Stdin)
	if err != nil {
		log.Fatal(err)
	}

	a := agent.NewWithLogger(provider, toolRegistry, providerConfig.Model, appLogger)
	answer, err := a.Run(context.Background(), input)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println(answer)
}

func readInput(args []string, stdin io.Reader) (string, error) {
	input := strings.TrimSpace(strings.Join(args, " "))
	if input == "" {
		data, err := io.ReadAll(stdin)
		if err != nil {
			return "", fmt.Errorf("read stdin: %w", err)
		}
		input = strings.TrimSpace(string(data))
	}
	if input == "" {
		return "", fmt.Errorf("empty input")
	}
	return input, nil
}
