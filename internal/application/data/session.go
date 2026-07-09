package data

import (
	"context"
	"fmt"
	"strings"

	"harukizmoe/pimoe/internal/session"
)

// SessionMeta 描述应用层可持久化和返回的 session 元数据。
type SessionMeta = session.SessionMeta

// SessionStore 定义 session metadata 的数据层边界。
type SessionStore interface {
	Create(ctx context.Context, title string) (SessionMeta, error)
	Resolve(ctx context.Context, id string) (SessionMeta, error)
	List(ctx context.Context) ([]SessionMeta, error)
	Touch(ctx context.Context, id string) error
}

// ManagerSessionStore 使用现有 session.Manager 适配数据层接口。
type ManagerSessionStore struct {
	manager *session.Manager
}

// NewManagerSessionStore 创建基于本地 manager index 的 SessionStore。
func NewManagerSessionStore(root string) *ManagerSessionStore {
	return &ManagerSessionStore{manager: session.NewManager(root)}
}

// Create 创建一条 session metadata 记录。
func (s *ManagerSessionStore) Create(ctx context.Context, title string) (SessionMeta, error) {
	if s == nil || s.manager == nil {
		return SessionMeta{}, fmt.Errorf("session store is nil")
	}
	return s.manager.Create(ctx, title)
}

// Resolve 根据 id 查询 session metadata。
func (s *ManagerSessionStore) Resolve(ctx context.Context, id string) (SessionMeta, error) {
	if s == nil || s.manager == nil {
		return SessionMeta{}, fmt.Errorf("session store is nil")
	}
	if strings.TrimSpace(id) == "" {
		return SessionMeta{}, fmt.Errorf("session id must not be empty")
	}
	return s.manager.Resolve(ctx, id)
}

// List 按数据层实现的排序返回 sessions。
func (s *ManagerSessionStore) List(ctx context.Context) ([]SessionMeta, error) {
	if s == nil || s.manager == nil {
		return nil, fmt.Errorf("session store is nil")
	}
	return s.manager.List(ctx)
}

// Touch 更新 session 最近使用时间。
func (s *ManagerSessionStore) Touch(ctx context.Context, id string) error {
	if s == nil || s.manager == nil {
		return fmt.Errorf("session store is nil")
	}
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("session id must not be empty")
	}
	return s.manager.Touch(ctx, id)
}
