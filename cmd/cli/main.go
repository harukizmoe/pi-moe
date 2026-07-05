package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"strings"

	"harukizmoe/pimoe/internal/agent"
	"harukizmoe/pimoe/internal/harness"
	"harukizmoe/pimoe/internal/logger"
)

const (
	agentLogPath                 = ".moe/logs/agent.log"
	defaultCLIProviderConfigPath = "configs/providers.yaml"
)

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

	opts, err := parseCLIOptions(os.Args[1:])
	if err != nil {
		log.Fatal(err)
	}

	input, err := readInput(opts.promptArgs, os.Stdin)
	if err != nil {
		log.Fatal(err)
	}

	runner, err := harness.New(context.Background(), harness.Config{
		ProviderConfigPath: opts.configPath,
		ProviderName:       opts.providerName,
		Logger:             appLogger,
	})
	if err != nil {
		log.Fatal(err)
	}

	result, err := runner.Run(context.Background(), input)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Print(formatRunResult(result, opts.includeTrace))
}

type cliOptions struct {
	configPath   string
	providerName string
	includeTrace bool
	promptArgs   []string
}

func parseCLIOptions(args []string) (cliOptions, error) {
	opts := cliOptions{configPath: defaultCLIProviderConfigPath}
	flags := flag.NewFlagSet("pimoe", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&opts.configPath, "config", opts.configPath, "providers YAML config path")
	flags.StringVar(&opts.providerName, "provider", "", "provider instance name")
	flags.BoolVar(&opts.includeTrace, "trace", false, "print tool trace")

	if err := flags.Parse(args); err != nil {
		return cliOptions{}, fmt.Errorf("parse flags: %w", err)
	}
	opts.promptArgs = flags.Args()
	return opts, nil
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

func formatRunResult(result *agent.RunResult, includeTrace bool) string {
	if result == nil {
		return ""
	}

	var out strings.Builder
	out.WriteString(result.Answer)
	out.WriteByte('\n')
	if !includeTrace {
		return out.String()
	}

	for _, step := range result.Steps {
		fmt.Fprintf(&out, "tool=%s arguments=%s", step.ToolName, step.Arguments)
		if step.Result != "" {
			fmt.Fprintf(&out, " result=%s", step.Result)
		}
		if step.Error != "" {
			fmt.Fprintf(&out, " error=%s", step.Error)
		}
		out.WriteByte('\n')
	}

	return out.String()
}
