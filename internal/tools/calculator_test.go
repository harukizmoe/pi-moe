package tools

import (
	"context"
	"testing"
)

func TestCalculatorMul(t *testing.T) {
	tool := Calculator{}
	got, err := tool.Call(context.Background(), `{"a":13,"b":7,"op":"mul"}`)
	if err != nil {
		t.Fatalf("Call() error = %v", err)
	}
	if got != "91" {
		t.Fatalf("result = %q", got)
	}
}

func TestCalculatorDivideByZero(t *testing.T) {
	tool := Calculator{}
	_, err := tool.Call(context.Background(), `{"a":1,"b":0,"op":"div"}`)
	if err == nil {
		t.Fatal("Call() error = nil")
	}
}

func TestRegistrySchemasAndCall(t *testing.T) {
	registry := NewRegistry()
	registry.Register(Calculator{})

	schemas := registry.Schemas()
	if len(schemas) != 1 {
		t.Fatalf("schemas len = %d", len(schemas))
	}
	if schemas[0].Function.Name != "calculator" {
		t.Fatalf("schema tool name = %q", schemas[0].Function.Name)
	}

	got, err := registry.Call(context.Background(), "calculator", `{"a":2,"b":3,"op":"add"}`)
	if err != nil {
		t.Fatalf("Call() error = %v", err)
	}
	if got != "5" {
		t.Fatalf("result = %q", got)
	}
}
