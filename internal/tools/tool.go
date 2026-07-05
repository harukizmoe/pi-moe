package tools

import "context"

// Tool is a local function the model can request through tool calling.
type Tool interface {
	// Name returns the stable function name exposed to the model.
	Name() string
	// Description explains when the model should use this tool.
	Description() string
	// Parameters returns the JSON Schema object for the tool arguments.
	Parameters() map[string]any
	// Call executes the tool with raw JSON arguments from the model.
	Call(ctx context.Context, arguments string) (string, error)
}
