package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"strings"

	"harukizmoe/pimoe/internal/harness"
	"harukizmoe/pimoe/internal/logger"
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

	input, err := readInput(os.Args[1:], os.Stdin)
	if err != nil {
		log.Fatal(err)
	}

	runner, err := harness.New(context.Background(), harness.Config{
		ProviderConfigPath: "configs/providers.yaml",
		Logger:             appLogger,
	})
	if err != nil {
		log.Fatal(err)
	}

	result, err := runner.Run(context.Background(), input)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println(result.Answer)
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
