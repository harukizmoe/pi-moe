package tools

import (
	"context"
	"encoding/json"
	"fmt"
)

type Calculator struct{}

type calculatorArgs struct {
	A  float64 `json:"a"`
	B  float64 `json:"b"`
	Op string  `json:"op"`
}

func (Calculator) Name() string {
	return "calculator"
}

func (Calculator) Description() string {
	return "Calculate two numbers."
}

func (Calculator) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"a":  map[string]any{"type": "number"},
			"b":  map[string]any{"type": "number"},
			"op": map[string]any{"type": "string", "enum": []string{"add", "sub", "mul", "div"}},
		},
		"required": []string{"a", "b", "op"},
	}
}

func (Calculator) Call(ctx context.Context, arguments string) (string, error) {
	var rawArgs map[string]json.RawMessage
	if err := json.Unmarshal([]byte(arguments), &rawArgs); err != nil {
		return "", fmt.Errorf("decode calculator arguments: %w", err)
	}
	for _, required := range []string{"a", "b", "op"} {
		if _, ok := rawArgs[required]; !ok {
			return "", fmt.Errorf("missing required argument %q", required)
		}
	}

	var args calculatorArgs
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return "", fmt.Errorf("decode calculator arguments: %w", err)
	}

	var result float64
	switch args.Op {
	case "add":
		result = args.A + args.B
	case "sub":
		result = args.A - args.B
	case "mul":
		result = args.A * args.B
	case "div":
		if args.B == 0 {
			return "", fmt.Errorf("divide by zero")
		}
		result = args.A / args.B
	default:
		return "", fmt.Errorf("unsupported calculator op %q", args.Op)
	}

	return fmt.Sprintf("%g", result), nil
}
