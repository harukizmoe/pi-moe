package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"strings"

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

	session := runner.NewSession()
	output, err := collectRunOutput(session.Prompt(context.Background(), input))
	if err != nil {
		log.Fatal(err)
	}

	fmt.Print(formatRunOutput(output, opts.includeTrace))
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

type runOutput struct {
	Answer string
	Steps  []toolStep
}

type toolStep struct {
	ToolCallID string
	ToolName   string
	Arguments  string
	Result     string
	Error      string
}

func collectRunOutput(events <-chan harness.Event) (runOutput, error) {
	var output runOutput
	stepByCallID := make(map[string]int)

	for event := range events {
		switch event := event.(type) {
		case harness.ToolExecutionStartEvent:
			stepByCallID[event.ToolCallID] = len(output.Steps)
			output.Steps = append(output.Steps, toolStep{
				ToolCallID: event.ToolCallID,
				ToolName:   event.ToolName,
				Arguments:  event.Arguments,
			})
		case harness.ToolExecutionEndEvent:
			stepIndex, ok := stepByCallID[event.ToolCallID]
			if !ok {
				stepIndex = len(output.Steps)
				stepByCallID[event.ToolCallID] = stepIndex
				output.Steps = append(output.Steps, toolStep{ToolCallID: event.ToolCallID, ToolName: event.Result.ToolName})
			}
			if event.Error != nil {
				output.Steps[stepIndex].Error = event.Error.Error()
			} else if event.Result.IsError {
				output.Steps[stepIndex].Error = event.Result.Content
			} else {
				output.Steps[stepIndex].Result = event.Result.Content
			}
		case harness.MessageEndEvent:
			if len(event.Message.ToolCalls) == 0 {
				output.Answer = event.Message.Content
			}
		case harness.ErrorEvent:
			if event.Error != nil {
				return output, event.Error
			}
		}
	}

	return output, nil
}

func formatRunOutput(output runOutput, includeTrace bool) string {
	var builder strings.Builder
	builder.WriteString(output.Answer)
	builder.WriteByte('\n')

	if !includeTrace {
		return builder.String()
	}

	for _, step := range output.Steps {
		builder.WriteString("\n")
		builder.WriteString("tool=")
		builder.WriteString(step.ToolName)
		builder.WriteByte('\n')
		builder.WriteString("arguments=")
		builder.WriteString(step.Arguments)
		builder.WriteByte('\n')
		if step.Error != "" {
			builder.WriteString("error=")
			builder.WriteString(step.Error)
			builder.WriteByte('\n')
			continue
		}
		builder.WriteString("result=")
		builder.WriteString(step.Result)
		builder.WriteByte('\n')
	}

	return builder.String()
}
