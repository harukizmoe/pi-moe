package agent

import (
	"context"
	"reflect"
	"testing"
)

func TestAgentLoopExecutionAPIIsEventStreamOnly(t *testing.T) {
	agentType := reflect.TypeOf((*Agent)(nil))
	ctxType := reflect.TypeOf((*context.Context)(nil)).Elem()
	agentMessagesType := reflect.TypeOf([]Message(nil))
	eventStreamType := reflect.TypeOf((<-chan Event)(nil))

	assertRequiredLoopMethodSignature(t, agentType, "Stream", []reflect.Type{ctxType, agentMessagesType}, []reflect.Type{eventStreamType})

	for _, forbidden := range []string{"RunAgentMessages", "StreamAgentMessages"} {
		if method, ok := agentType.MethodByName(forbidden); ok {
			t.Fatalf("deprecated loop method still present: %s%s", forbidden, method.Type)
		}
	}
}

func assertRequiredLoopMethodSignature(t *testing.T, agentType reflect.Type, name string, wantIn []reflect.Type, wantOut []reflect.Type) {
	t.Helper()

	method, ok := agentType.MethodByName(name)
	if !ok {
		t.Fatalf("required loop method missing: %s", name)
	}

	if method.Type.NumIn() != len(wantIn)+1 {
		t.Fatalf("%s input count = %d, want %d", name, method.Type.NumIn()-1, len(wantIn))
	}
	for i, want := range wantIn {
		if got := method.Type.In(i + 1); got != want {
			t.Fatalf("%s input %d = %v, want %v", name, i, got, want)
		}
	}
	if method.Type.NumOut() != len(wantOut) {
		t.Fatalf("%s output count = %d, want %d", name, method.Type.NumOut(), len(wantOut))
	}
	for i, want := range wantOut {
		if got := method.Type.Out(i); got != want {
			t.Fatalf("%s output %d = %v, want %v", name, i, got, want)
		}
	}
}
