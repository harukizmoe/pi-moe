package agent

import (
	"context"
	"fmt"
	"strings"
)

// MemoryItem 是调用方已授权、仅作为不可信上下文数据提供给一次 Run 的记忆事实。
type MemoryItem struct {
	ID         string
	Content    string
	Source     string
	Scope      string
	Provenance string
}

// MemoryOperation 是候选写入的稳定操作语义。
type MemoryOperation string

const (
	MemoryOperationUpsert MemoryOperation = "upsert"
	MemoryOperationForget MemoryOperation = "forget"
)

// MemoryCandidate 表示 extractor 提出的长期记忆变更；Runtime 不负责提交。
type MemoryCandidate struct {
	Operation  MemoryOperation
	Key        string
	Content    string
	Source     string
	Scope      string
	Provenance string
}

// MemoryExtractionInput 是 extractor 收到的脱敏运行事实快照。
type MemoryExtractionInput struct {
	Messages    []Message
	MemoryItems []MemoryItem
}

// MemoryExtractor 从已授权输入提出稳定且带来源的候选；默认不启用。
type MemoryExtractor interface {
	Extract(context.Context, MemoryExtractionInput) ([]MemoryCandidate, error)
}

// MemoryExtractorFunc 适配函数为 MemoryExtractor。
type MemoryExtractorFunc func(context.Context, MemoryExtractionInput) ([]MemoryCandidate, error)

// Extract 调用函数式 extractor。
func (f MemoryExtractorFunc) Extract(ctx context.Context, input MemoryExtractionInput) ([]MemoryCandidate, error) {
	return f(ctx, input)
}

// MemoryCandidateEvent 暴露 extractor 产生的候选；调用方必须等 completed 后提交。
type MemoryCandidateEvent struct {
	RunID      string
	Candidates []MemoryCandidate
}

func (MemoryCandidateEvent) AgentEvent() {}

// MemoryExtractionFailedEvent 表示候选提取失败；不改变主 Run 终态。
type MemoryExtractionFailedEvent struct {
	RunID string
	Error error
}

func (MemoryExtractionFailedEvent) AgentEvent() {}

func validateMemoryCandidates(candidates []MemoryCandidate) error {
	for i, candidate := range candidates {
		if candidate.Operation != MemoryOperationUpsert && candidate.Operation != MemoryOperationForget {
			return fmt.Errorf("candidate %d has unsupported operation %q", i, candidate.Operation)
		}
		if strings.TrimSpace(candidate.Key) == "" {
			return fmt.Errorf("candidate %d has empty key", i)
		}
		if candidate.Operation == MemoryOperationUpsert && strings.TrimSpace(candidate.Content) == "" {
			return fmt.Errorf("candidate %d has empty content", i)
		}
		if strings.TrimSpace(candidate.Source) == "" {
			return fmt.Errorf("candidate %d has empty source", i)
		}
		if strings.TrimSpace(candidate.Scope) == "" {
			return fmt.Errorf("candidate %d has empty scope", i)
		}
		if strings.TrimSpace(candidate.Provenance) == "" {
			return fmt.Errorf("candidate %d has empty provenance", i)
		}
	}
	return nil
}
