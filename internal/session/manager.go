package session

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	defaultSessionManagerRoot = ".moe/sessions"
	sessionIndexFileName      = "index.json"
	untitledSessionTitle      = "untitled session"
)

// nowUTC 返回当前 UTC 时间；测试可替换它以稳定验证时间相关行为。
var nowUTC = func() time.Time { return time.Now().UTC() }

// SetNowForTest 在测试中替换 manager 使用的当前时间。
func SetNowForTest(t interface{ Cleanup(func()) }, now func() time.Time) {
	old := nowUTC
	nowUTC = now
	t.Cleanup(func() { nowUTC = old })
}

// Manager 管理本地 session index 和 session 文件路径。
type Manager struct {
	root string
}

// SessionConfig 保存 managed session 的可恢复运行偏好；不包含密钥或完整 Provider 配置。
type SessionConfig struct {
	// ProviderName 引用项目级或未来用户级 Provider registry 中的 Provider 实例名。
	ProviderName string `json:"provider_name,omitempty"`
	// SystemPrompt 保存该 session 的 Agent 行为设定；空值表示使用当前默认值。
	SystemPrompt string `json:"system_prompt,omitempty"`
	// MaxSteps 保存 tool-calling 最大轮数偏好；小于 1 表示使用当前默认值。
	MaxSteps int `json:"max_steps,omitempty"`
}

// SessionMeta 描述一个可恢复的本地 session。
type SessionMeta struct {
	// ID 是 CLI 用于 resume 的稳定 session 标识。
	ID string
	// Path 是可传给 Open 的 JSONL transcript 文件路径。
	Path string
	// Title 是创建 session 时从首个 prompt 生成的短标题。
	Title string
	// CreatedAt 是 manager 创建索引记录的 UTC 时间。
	CreatedAt time.Time
	// UpdatedAt 是最近一次 CLI 使用该 session 的 UTC 时间。
	UpdatedAt time.Time
	// Config 是可恢复运行偏好；不包含密钥或完整 Provider 配置。
	Config SessionConfig
}

type notFoundError struct {
	id string
}

func (e notFoundError) Error() string {
	return fmt.Sprintf("session %q not found", e.id)
}

// IsNotFound 报告 err 是否表示本地索引中不存在指定 session id。
func IsNotFound(err error) bool {
	var target notFoundError
	return errors.As(err, &target)
}

type sessionIndex struct {
	Current  string             `json:"current,omitempty"`
	Sessions []sessionMetaEntry `json:"sessions"`
}

type sessionMetaEntry struct {
	ID        string    `json:"id"`
	Path      string    `json:"path"`
	Title     string    `json:"title"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Config    SessionConfig `json:"config,omitempty"`
}

// NewManager 创建使用 root 目录的 session manager；root 为空时使用 .moe/sessions。
func NewManager(root string) *Manager {
	if strings.TrimSpace(root) == "" {
		root = defaultSessionManagerRoot
	}
	return &Manager{root: root}
}

// Create 创建一条索引记录并返回可传给 Open 的 session 文件路径。
func (m *Manager) Create(ctx context.Context, title string, cfg SessionConfig) (SessionMeta, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return SessionMeta{}, err
	}
	index, err := m.loadIndex()
	if err != nil {
		return SessionMeta{}, err
	}
	now := nowUTC()
	id, err := newSessionID(now)
	if err != nil {
		return SessionMeta{}, err
	}
	meta := sessionMetaEntry{
		ID:        id,
		Path:      filepath.Join(m.root, id+".jsonl"),
		Title:     normalizeSessionTitle(title),
		CreatedAt: now,
		UpdatedAt: now,
		Config:    normalizeSessionConfig(cfg),
	}
	index.Current = id
	index.Sessions = append(index.Sessions, meta)
	if err := m.saveIndex(index); err != nil {
		return SessionMeta{}, err
	}
	return meta.toSessionMeta(), nil
}

// Resolve 根据 id 返回已有 session 元数据。
func (m *Manager) Resolve(ctx context.Context, id string) (SessionMeta, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return SessionMeta{}, err
	}
	index, err := m.loadIndex()
	if err != nil {
		return SessionMeta{}, err
	}
	for _, meta := range index.Sessions {
		if meta.ID == id {
			return meta.toSessionMeta(), nil
		}
	}
	return SessionMeta{}, notFoundError{id: id}
}

// List 按 updated_at 倒序返回 session 列表。
func (m *Manager) List(ctx context.Context) ([]SessionMeta, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	index, err := m.loadIndex()
	if err != nil {
		return nil, err
	}
	metas := make([]SessionMeta, 0, len(index.Sessions))
	for _, entry := range index.Sessions {
		metas = append(metas, entry.toSessionMeta())
	}
	sort.SliceStable(metas, func(i, j int) bool {
		return metas[i].UpdatedAt.After(metas[j].UpdatedAt)
	})
	return metas, nil
}

// Touch 更新 session 的 updated_at，并将它设为 current。
func (m *Manager) Touch(ctx context.Context, id string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	index, err := m.loadIndex()
	if err != nil {
		return err
	}
	for i := range index.Sessions {
		if index.Sessions[i].ID == id {
			index.Sessions[i].UpdatedAt = nowUTC()
			index.Current = id
			return m.saveIndex(index)
		}
	}
	return notFoundError{id: id}
}

// UpdateConfig 更新 session 的可恢复运行偏好、updated_at 和 current 指针。
func (m *Manager) UpdateConfig(ctx context.Context, id string, cfg SessionConfig) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	index, err := m.loadIndex()
	if err != nil {
		return err
	}
	for i := range index.Sessions {
		if index.Sessions[i].ID == id {
			index.Sessions[i].Config = normalizeSessionConfig(cfg)
			index.Sessions[i].UpdatedAt = nowUTC()
			index.Current = id
			return m.saveIndex(index)
		}
	}
	return notFoundError{id: id}
}

func (m *Manager) indexPath() string {
	return filepath.Join(m.root, sessionIndexFileName)
}

func (m *Manager) loadIndex() (sessionIndex, error) {
	data, err := os.ReadFile(m.indexPath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return sessionIndex{}, nil
		}
		return sessionIndex{}, fmt.Errorf("read session index %q: %w", m.indexPath(), err)
	}
	var index sessionIndex
	if err := json.Unmarshal(data, &index); err != nil {
		return sessionIndex{}, fmt.Errorf("parse session index %q: %w", m.indexPath(), err)
	}
	return index, nil
}

func (m *Manager) saveIndex(index sessionIndex) error {
	if err := os.MkdirAll(m.root, 0o700); err != nil {
		return fmt.Errorf("create session index dir: %w", err)
	}
	data, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return fmt.Errorf("encode session index: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(m.indexPath(), data, 0o600); err != nil {
		return fmt.Errorf("write session index %q: %w", m.indexPath(), err)
	}
	return nil
}

func normalizeSessionTitle(title string) string {
	line := strings.TrimSpace(strings.Split(strings.ReplaceAll(title, "\r\n", "\n"), "\n")[0])
	if line == "" {
		return untitledSessionTitle
	}
	if len(line) > 80 {
		return line[:80]
	}
	return line
}

func normalizeSessionConfig(cfg SessionConfig) SessionConfig {
	cfg.ProviderName = strings.TrimSpace(cfg.ProviderName)
	cfg.SystemPrompt = strings.TrimSpace(cfg.SystemPrompt)
	if cfg.MaxSteps < 1 {
		cfg.MaxSteps = 0
	}
	return cfg
}

func newSessionID(now time.Time) (string, error) {
	var random [3]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", fmt.Errorf("generate session id: %w", err)
	}
	return now.UTC().Format("20060102-150405") + "-" + hex.EncodeToString(random[:]), nil
}

func (e sessionMetaEntry) toSessionMeta() SessionMeta {
	return SessionMeta{
		ID:        e.ID,
		Path:      e.Path,
		Title:     e.Title,
		CreatedAt: e.CreatedAt.UTC(),
		UpdatedAt: e.UpdatedAt.UTC(),
		Config:    e.Config,
	}
}
