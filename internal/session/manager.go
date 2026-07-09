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
	"sync"
	"time"
)

const (
	defaultSessionManagerRoot = ".moe/sessions"
	sessionIndexFileName      = "index.json"
	untitledSessionTitle      = "untitled session"
)

const localActorUserID = "local"

// Actor 表示当前请求的业务身份；认证方式由上层入口负责。
type Actor struct {
	// UserID 是 session owner 边界使用的稳定用户标识；空值会归一化为 local。
	UserID string
}

// LocalActor 返回当前 CLI 和未认证 HTTP 使用的默认单用户身份。
func LocalActor() Actor {
	return Actor{UserID: localActorUserID}
}

// NormalizeActor 将空白用户归一化为 local，避免调用方漏传导致无 owner session。
func NormalizeActor(actor Actor) Actor {
	actor.UserID = strings.TrimSpace(actor.UserID)
	if actor.UserID == "" {
		actor.UserID = localActorUserID
	}
	return actor
}

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
	mu   sync.Mutex
}

// SessionConfig 保存 managed session 的可恢复运行偏好；不包含密钥或完整 Provider 配置。
type SessionConfig struct {
	// ProviderName 引用项目级或未来用户级 Provider registry 中的 Provider 实例名。
	ProviderName string `json:"provider_name,omitempty"`
	// SessionPrompt 保存该 session 的 Agent 行为设定；空值表示使用当前默认值。
	SessionPrompt string `json:"session_prompt,omitempty"`
	// MaxSteps 保存 tool-calling 最大轮数偏好；小于 1 表示使用当前默认值。
	MaxSteps int `json:"max_steps,omitempty"`
}

// SessionMeta 描述一个可恢复的本地 session。
type SessionMeta struct {
	// ID 是 CLI 用于 resume 的稳定 session 标识。
	ID string
	// OwnerID 是拥有该 session 的稳定用户标识。
	OwnerID string
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
	ID        string        `json:"id"`
	OwnerID   string        `json:"owner_id,omitempty"`
	Path      string        `json:"path"`
	Title     string        `json:"title"`
	CreatedAt time.Time     `json:"created_at"`
	UpdatedAt time.Time     `json:"updated_at"`
	Config    SessionConfig `json:"config,omitempty"`
}

// NewManager 创建使用 root 目录的 session manager；root 为空时使用 .moe/sessions。
func NewManager(root string) *Manager {
	if strings.TrimSpace(root) == "" {
		root = defaultSessionManagerRoot
	}
	return &Manager{root: root}
}

// Create 创建一条归属 actor 的索引记录并返回可传给 Open 的 session 文件路径。
func (m *Manager) Create(ctx context.Context, actor Actor, title string, cfg SessionConfig) (SessionMeta, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return SessionMeta{}, err
	}
	actor = NormalizeActor(actor)
	m.mu.Lock()
	defer m.mu.Unlock()
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
		OwnerID:   actor.UserID,
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

// Resolve 根据 id 和 actor 返回已有 session 元数据。
func (m *Manager) Resolve(ctx context.Context, actor Actor, id string) (SessionMeta, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return SessionMeta{}, err
	}
	actor = NormalizeActor(actor)
	index, err := m.loadIndex()
	if err != nil {
		return SessionMeta{}, err
	}
	for _, meta := range index.Sessions {
		if meta.ID == id && meta.ownerID() == actor.UserID {
			return meta.toSessionMeta(), nil
		}
	}
	return SessionMeta{}, notFoundError{id: id}
}

// List 按 updated_at 倒序返回 actor 可见的 session 列表。
func (m *Manager) List(ctx context.Context, actor Actor) ([]SessionMeta, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	actor = NormalizeActor(actor)
	index, err := m.loadIndex()
	if err != nil {
		return nil, err
	}
	metas := make([]SessionMeta, 0, len(index.Sessions))
	for _, entry := range index.Sessions {
		if entry.ownerID() == actor.UserID {
			metas = append(metas, entry.toSessionMeta())
		}
	}
	sort.SliceStable(metas, func(i, j int) bool {
		return metas[i].UpdatedAt.After(metas[j].UpdatedAt)
	})
	return metas, nil
}

// Touch 更新 actor 拥有的 session 的 updated_at，并将它设为 current。
func (m *Manager) Touch(ctx context.Context, actor Actor, id string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	actor = NormalizeActor(actor)
	m.mu.Lock()
	defer m.mu.Unlock()

	index, err := m.loadIndex()
	if err != nil {
		return err
	}
	for i := range index.Sessions {
		if index.Sessions[i].ID == id && index.Sessions[i].ownerID() == actor.UserID {
			index.Sessions[i].OwnerID = actor.UserID
			index.Sessions[i].UpdatedAt = nowUTC()
			index.Current = id
			return m.saveIndex(index)
		}
	}
	return notFoundError{id: id}
}

// UpdateConfig 更新 actor 拥有的 session 的可恢复运行偏好、updated_at 和 current 指针。
func (m *Manager) UpdateConfig(ctx context.Context, actor Actor, id string, cfg SessionConfig) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	actor = NormalizeActor(actor)
	m.mu.Lock()
	defer m.mu.Unlock()

	index, err := m.loadIndex()
	if err != nil {
		return err
	}
	for i := range index.Sessions {
		if index.Sessions[i].ID == id && index.Sessions[i].ownerID() == actor.UserID {
			index.Sessions[i].OwnerID = actor.UserID
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
	tmp, err := os.CreateTemp(m.root, sessionIndexFileName+"-*.tmp")
	if err != nil {
		return fmt.Errorf("create session index temp file: %w", err)
	}
	tmpPath := tmp.Name()
	removeTmp := true
	defer func() {
		if removeTmp {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write session index temp file %q: %w", tmpPath, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close session index temp file %q: %w", tmpPath, err)
	}
	if err := os.Rename(tmpPath, m.indexPath()); err != nil {
		return fmt.Errorf("replace session index %q: %w", m.indexPath(), err)
	}
	removeTmp = false
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
	cfg.SessionPrompt = strings.TrimSpace(cfg.SessionPrompt)
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

func (e sessionMetaEntry) ownerID() string {
	return NormalizeActor(Actor{UserID: e.OwnerID}).UserID
}

func (e sessionMetaEntry) toSessionMeta() SessionMeta {
	return SessionMeta{
		ID:        e.ID,
		OwnerID:   e.ownerID(),
		Path:      e.Path,
		Title:     e.Title,
		CreatedAt: e.CreatedAt.UTC(),
		UpdatedAt: e.UpdatedAt.UTC(),
		Config:    e.Config,
	}
}
