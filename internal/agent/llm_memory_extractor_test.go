package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"harukizmoe/pimoe/internal/llms"
)

func TestLLMMemoryExtractorReturnsValidatedScopedCandidates(t *testing.T) {
	var request llms.ChatRequest
	provider := streamFunc(func(ctx context.Context, req llms.ChatRequest) (<-chan llms.ChatStreamEvent, error) {
		request = req
		return streamEvents(assistantDone(llms.Message{Role: llms.RoleAssistant, Content: `{"candidates":[{"operation":"upsert","key":"profile/name","content":"Ada","source":"transcript:0","provenance":"explicit user statement"},{"operation":"forget","key":"profile/old-name","content":"","source":"transcript:0","provenance":"explicit forget request"}]}`})), nil
	})
	extractor, err := NewLLMMemoryExtractor(provider, "extractor-model", "tenant:opaque")
	if err != nil {
		t.Fatalf("NewLLMMemoryExtractor() error = %v", err)
	}

	candidates, err := extractor.Extract(context.Background(), MemoryExtractionInput{
		Messages: []Message{UserMessage{Content: "Remember that my name is Ada; forget my old name."}},
	})
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if len(candidates) != 2 {
		t.Fatalf("candidates = %#v, want upsert and forget", candidates)
	}
	if candidates[0].Scope != "tenant:opaque" || candidates[0].Operation != MemoryOperationUpsert {
		t.Fatalf("upsert candidate = %#v, want caller scope", candidates[0])
	}
	if candidates[1].Scope != "tenant:opaque" || candidates[1].Operation != MemoryOperationForget {
		t.Fatalf("forget candidate = %#v, want caller scope", candidates[1])
	}
	if request.Model != "extractor-model" || len(request.Messages) != 2 {
		t.Fatalf("provider request = %#v, want isolated extractor request", request)
	}
	if request.Messages[0].Role != llms.RoleSystem || !strings.Contains(request.Messages[0].Content, "stable long-term facts") {
		t.Fatalf("extractor instructions = %#v, want stable-fact policy", request.Messages[0])
	}
	if request.Messages[1].Role != llms.RoleUser || !strings.Contains(request.Messages[1].Content, "Untrusted extraction data") {
		t.Fatalf("extractor payload = %#v, want untrusted data envelope", request.Messages[1])
	}
}

func TestLLMMemoryExtractorRejectsUnsafeProviderOutput(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{name: "markdown", content: "```json\n{\"candidates\":[]}\n```", want: "decode memory extraction response"},
		{name: "unknown field", content: `{"candidates":[],"reasoning":"private"}`, want: "unknown field"},
		{name: "missing provenance", content: `{"candidates":[{"operation":"upsert","key":"profile/name","content":"Ada","source":"transcript:0"}]}`, want: "empty provenance"},
		{name: "unsupported operation", content: `{"candidates":[{"operation":"delete","key":"profile/name","source":"transcript:0","provenance":"request"}]}`, want: "unsupported operation"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			provider := streamFunc(func(ctx context.Context, req llms.ChatRequest) (<-chan llms.ChatStreamEvent, error) {
				return streamEvents(assistantDone(llms.Message{Role: llms.RoleAssistant, Content: test.content})), nil
			})
			extractor, err := NewLLMMemoryExtractor(provider, "extractor-model", "tenant:opaque")
			if err != nil {
				t.Fatalf("NewLLMMemoryExtractor() error = %v", err)
			}

			_, err = extractor.Extract(context.Background(), MemoryExtractionInput{Messages: []Message{UserMessage{Content: "hello"}}})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Extract() error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestLLMMemoryExtractorPropagatesProviderFailure(t *testing.T) {
	provider := streamFunc(func(ctx context.Context, req llms.ChatRequest) (<-chan llms.ChatStreamEvent, error) {
		return nil, errors.New("provider unavailable")
	})
	extractor, err := NewLLMMemoryExtractor(provider, "extractor-model", "tenant:opaque")
	if err != nil {
		t.Fatalf("NewLLMMemoryExtractor() error = %v", err)
	}

	_, err = extractor.Extract(context.Background(), MemoryExtractionInput{Messages: []Message{UserMessage{Content: "hello"}}})
	if err == nil || !strings.Contains(err.Error(), "provider unavailable") {
		t.Fatalf("Extract() error = %v, want provider failure", err)
	}
}
