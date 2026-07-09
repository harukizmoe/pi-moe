package tools

import (
	"context"
	"reflect"
	"testing"
)

type registryTestTool struct {
	name string
}

func (t registryTestTool) Name() string { return t.name }

func (t registryTestTool) Description() string { return "test tool" }

func (t registryTestTool) Parameters() map[string]any { return map[string]any{"type": "object"} }

func (t registryTestTool) Call(context.Context, string) (string, error) { return "", nil }

func TestRegistrySchemasSortedByName(t *testing.T) {
	registry := NewRegistry()
	for _, name := range []string{"zulu", "alpha", "mike", "bravo", "echo", "delta", "charlie"} {
		registry.Register(registryTestTool{name: name})
	}

	schemas := registry.Schemas()
	got := make([]string, 0, len(schemas))
	for _, schema := range schemas {
		got = append(got, schema.Function.Name)
	}

	want := []string{"alpha", "bravo", "charlie", "delta", "echo", "mike", "zulu"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("schema names = %v, want %v", got, want)
	}
}
