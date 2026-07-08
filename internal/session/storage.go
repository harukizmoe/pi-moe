package session

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"harukizmoe/pimoe/internal/agent"
	"harukizmoe/pimoe/internal/llms"
)

const (
	// sessionEntryMessage 表示一条已经进入 terminal transcript 的 Agent 消息。
	sessionEntryMessage = "message"
	// sessionEntryLeaf 标记一次完整 run 的最新可恢复节点；取消或失败的半成品不会推进 leaf。
	sessionEntryLeaf = "leaf"
)

// nextSessionEntrySequence 在同一纳秒内补充单进程递增序号，降低测试和快速写入时的 ID 碰撞风险。
var nextSessionEntrySequence atomic.Uint64

// fileStore 管理一个 append-only JSONL session 文件及其当前恢复位置。
type fileStore struct {
	// mu 串行化同一 Session 内的读写，避免 parentID 与文件尾部写入顺序不一致。
	mu sync.Mutex
	// path 是 CLI 传入的 session JSONL 文件路径。
	path string
	// parentID 指向当前可恢复 transcript 的最后一条 message entry。
	parentID string
}

// fileEntry 是 JSONL 的顶层记录；message 保存 transcript，leaf 保存恢复锚点。
type fileEntry struct {
	// ID 是记录在文件内的稳定标识，供 parent_id 和 leaf.entry_id 引用。
	ID string `json:"id"`
	// ParentID 指向上一条 message entry，用于后续支持 branch/resume。
	ParentID string `json:"parent_id,omitempty"`
	// Type 区分 message 和 leaf，读取时遇到未知类型直接报错，避免静默丢历史。
	Type string `json:"type"`
	// Timestamp 记录写入时间，当前只用于排查和后续审计，不参与恢复顺序。
	Timestamp time.Time `json:"timestamp"`
	// Message 保存一条可发送给 Agent 的 terminal transcript 消息。
	Message *messageEntry `json:"message,omitempty"`
	// Leaf 保存最近一次完整 run 的恢复锚点。
	Leaf *leafEntry `json:"leaf,omitempty"`
}

// messageEntry 是 agent.Message 的 JSON 表示，避免把接口类型直接写入文件。
type messageEntry struct {
	// Role 对应 llms.Role，用于恢复具体的 agent.Message 类型。
	Role string `json:"role"`
	// Content 保存 user/assistant/tool 的文本内容。
	Content string `json:"content,omitempty"`
	// ToolCalls 只在 assistant message 上出现，保留模型请求的函数调用原文。
	ToolCalls []llms.ToolCall `json:"tool_calls,omitempty"`
	// ToolCallID 将 tool result 关联回对应 assistant tool call。
	ToolCallID string `json:"tool_call_id,omitempty"`
	// ToolName 保存本地工具名，恢复后仍能保留 trace 和错误摘要语义。
	ToolName string `json:"tool_name,omitempty"`
	// IsError 表示 tool result 是否为失败摘要。
	IsError bool `json:"is_error,omitempty"`
}

// leafEntry 指向一次完整 run 后应恢复到的最后一条 message entry。
type leafEntry struct {
	// EntryID 是最新可恢复 message entry 的 ID。
	EntryID string `json:"entry_id"`
}

// newFileStore 只记录路径；文件不存在时由 load/append 按各自语义处理。
func newFileStore(path string) *fileStore {
	return &fileStore{path: path}
}

// newSessionEntryID 生成仅需在单个 session 文件内唯一的轻量 ID。
func newSessionEntryID() string {
	return fmt.Sprintf("entry-%d-%d", time.Now().UTC().UnixNano(), nextSessionEntrySequence.Add(1))
}

// load 读取磁盘 transcript 并同步 parentID，后续 append 会接在该节点之后。
func (s *fileStore) load() ([]agent.Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	messages, parentID, err := loadSessionMessages(s.path)
	if err != nil {
		return nil, err
	}
	s.parentID = parentID
	return messages, nil
}

// loadSessionMessages 读取 JSONL 并按最新 leaf 恢复 transcript；没有 leaf 的旧文件按顺序读取 message。
func loadSessionMessages(path string) ([]agent.Message, string, error) {
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, "", nil
		}
		return nil, "", fmt.Errorf("open session file %q: %w", path, err)
	}
	defer file.Close()

	entriesByID := make(map[string]fileEntry)
	messageIDs := make([]string, 0)
	latestLeafID := ""

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var entry fileEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			return nil, "", fmt.Errorf("parse session file %q line %d: %w", path, lineNumber, err)
		}
		if entry.ID == "" {
			return nil, "", fmt.Errorf("parse session file %q line %d: missing entry id", path, lineNumber)
		}
		if _, exists := entriesByID[entry.ID]; exists {
			return nil, "", fmt.Errorf("parse session file %q line %d: duplicate entry id %q", path, lineNumber, entry.ID)
		}

		switch entry.Type {
		case sessionEntryMessage:
			if entry.Message == nil {
				return nil, "", fmt.Errorf("parse session file %q line %d: message entry without message", path, lineNumber)
			}
			messageIDs = append(messageIDs, entry.ID)
		case sessionEntryLeaf:
			if entry.Leaf == nil || entry.Leaf.EntryID == "" {
				return nil, "", fmt.Errorf("parse session file %q line %d: leaf entry without entry_id", path, lineNumber)
			}
			latestLeafID = entry.Leaf.EntryID
		default:
			return nil, "", fmt.Errorf("parse session file %q line %d: unknown entry type %q", path, lineNumber, entry.Type)
		}
		entriesByID[entry.ID] = entry
	}
	if err := scanner.Err(); err != nil {
		return nil, "", fmt.Errorf("read session file %q: %w", path, err)
	}

	pathIDs := messageIDs
	if latestLeafID != "" {
		var err error
		pathIDs, err = messagePathToLeaf(entriesByID, latestLeafID)
		if err != nil {
			return nil, "", fmt.Errorf("load session file %q: %w", path, err)
		}
	}

	messages := make([]agent.Message, 0, len(pathIDs))
	for _, id := range pathIDs {
		message, err := decodeMessageEntry(entriesByID[id].Message)
		if err != nil {
			return nil, "", fmt.Errorf("load session file %q entry %q: %w", path, id, err)
		}
		messages = append(messages, message)
	}

	parentID := ""
	if len(pathIDs) > 0 {
		parentID = pathIDs[len(pathIDs)-1]
	}
	return messages, parentID, nil
}

// messagePathToLeaf 沿 parent_id 回溯恢复路径，显式检查断链和环，避免损坏文件被当作空历史。
func messagePathToLeaf(entriesByID map[string]fileEntry, leafID string) ([]string, error) {
	seen := make(map[string]struct{})
	reversed := make([]string, 0)
	for id := leafID; id != ""; {
		if _, exists := seen[id]; exists {
			return nil, fmt.Errorf("cycle in session parent chain at %q", id)
		}
		seen[id] = struct{}{}

		entry, ok := entriesByID[id]
		if !ok {
			return nil, fmt.Errorf("leaf points to missing entry %q", id)
		}
		if entry.Type != sessionEntryMessage {
			return nil, fmt.Errorf("leaf points to non-message entry %q", id)
		}
		reversed = append(reversed, id)
		id = entry.ParentID
	}

	path := make([]string, len(reversed))
	for i := range reversed {
		path[len(reversed)-1-i] = reversed[i]
	}
	return path, nil
}

// decodeMessageEntry 将稳定 JSON schema 转回 Agent 内部消息类型。
func decodeMessageEntry(entry *messageEntry) (agent.Message, error) {
	if entry == nil {
		return nil, fmt.Errorf("nil message entry")
	}
	switch llms.Role(entry.Role) {
	case llms.RoleUser:
		return agent.UserMessage{Content: entry.Content}, nil
	case llms.RoleAssistant:
		return agent.AssistantMessage{Content: entry.Content, ToolCalls: append([]llms.ToolCall(nil), entry.ToolCalls...)}, nil
	case llms.RoleTool:
		return agent.ToolResultMessage{ToolCallID: entry.ToolCallID, ToolName: entry.ToolName, Content: entry.Content, IsError: entry.IsError}, nil
	default:
		return nil, fmt.Errorf("unknown message role %q", entry.Role)
	}
}

// encodeMessageEntry 将 Agent 内部消息压成稳定 JSON schema，避免持久化 Go 接口细节。
func encodeMessageEntry(message agent.Message) (*messageEntry, error) {
	switch msg := message.(type) {
	case agent.UserMessage:
		return &messageEntry{Role: string(llms.RoleUser), Content: msg.Content}, nil
	case agent.AssistantMessage:
		return &messageEntry{Role: string(llms.RoleAssistant), Content: msg.Content, ToolCalls: append([]llms.ToolCall(nil), msg.ToolCalls...)}, nil
	case agent.ToolResultMessage:
		return &messageEntry{Role: string(llms.RoleTool), ToolCallID: msg.ToolCallID, ToolName: msg.ToolName, Content: msg.Content, IsError: msg.IsError}, nil
	default:
		return nil, fmt.Errorf("unsupported session message type %T", message)
	}
}

// appendMessages 只追加已完成 run 的 terminal messages，最后再写 leaf 提交本次恢复点。
func (s *fileStore) appendMessages(messages []agent.Message) error {
	if len(messages) == 0 {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("create session dir: %w", err)
	}
	file, err := os.OpenFile(s.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open session file for append: %w", err)
	}
	defer file.Close()

	encoder := json.NewEncoder(file)
	parentID := s.parentID
	lastMessageID := ""
	for _, message := range messages {
		encoded, err := encodeMessageEntry(message)
		if err != nil {
			return err
		}
		entryID := newSessionEntryID()
		entry := fileEntry{
			ID:        entryID,
			ParentID:  parentID,
			Type:      sessionEntryMessage,
			Timestamp: time.Now().UTC(),
			Message:   encoded,
		}
		if err := encoder.Encode(entry); err != nil {
			return fmt.Errorf("write session message entry: %w", err)
		}
		parentID = entryID
		lastMessageID = entryID
	}

	leaf := fileEntry{
		ID:        newSessionEntryID(),
		ParentID:  lastMessageID,
		Type:      sessionEntryLeaf,
		Timestamp: time.Now().UTC(),
		Leaf:      &leafEntry{EntryID: lastMessageID},
	}
	if err := encoder.Encode(leaf); err != nil {
		return fmt.Errorf("write session leaf entry: %w", err)
	}
	s.parentID = lastMessageID
	return nil
}
