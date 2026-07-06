package agent

import (
	"context"
	"reflect"
	"testing"

	"harukizmoe/pimoe/internal/llms"
)

func TestAgentLoopExecutionAPIRequiresAgentMessages(t *testing.T) {
	agentType := reflect.TypeOf((*Agent)(nil))
	ctxType := reflect.TypeOf((*context.Context)(nil)).Elem()
	stringType := reflect.TypeOf("")
	llmMessagesType := reflect.TypeOf([]llms.Message(nil))
	agentMessagesType := reflect.TypeOf([]Message(nil))
	runResultType := reflect.TypeOf((*RunResult)(nil))
	errorType := reflect.TypeOf((*error)(nil)).Elem()
	eventStreamType := reflect.TypeOf((<-chan Event)(nil))

	assertRequiredLoopMethodSignature(t, agentType, "RunAgentMessages", []reflect.Type{ctxType, agentMessagesType}, []reflect.Type{runResultType, errorType})
	assertRequiredLoopMethodSignature(t, agentType, "StreamAgentMessages", []reflect.Type{ctxType, agentMessagesType}, []reflect.Type{eventStreamType})

	for i := range agentType.NumMethod() {
		method := agentType.Method(i)
		if !isLoopExecutionMethod(method.Type, ctxType, stringType, runResultType, errorType, eventStreamType) {
			continue
		}

		switch method.Type.In(2) {
		case stringType:
			t.Fatalf("forbidden raw-string loop method still present: %s%s", method.Name, method.Type)
		case llmMessagesType:
			t.Fatalf("forbidden provider-message loop method still present: %s%s", method.Name, method.Type)
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

func isLoopExecutionMethod(methodType reflect.Type, ctxType reflect.Type, stringType reflect.Type, runResultType reflect.Type, errorType reflect.Type, eventStreamType reflect.Type) bool {
	if methodType.NumIn() != 3 || methodType.In(1) != ctxType {
		return false
	}

	if methodType.NumOut() == 2 && methodType.Out(1) == errorType {
		return methodType.Out(0) == stringType || methodType.Out(0) == runResultType
	}

	return methodType.NumOut() == 1 && methodType.Out(0) == eventStreamType
}
