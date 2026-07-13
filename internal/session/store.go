package session

import "context"

// SessionStore 定义 session metadata 的持久化边界；实现必须在查询边界执行 owner 过滤。
type SessionStore interface {
	Create(ctx context.Context, actor Actor, title string, cfg SessionConfig) (SessionMeta, error)
	Resolve(ctx context.Context, actor Actor, id string) (SessionMeta, error)
	List(ctx context.Context, actor Actor) ([]SessionMeta, error)
	UpdateConfig(ctx context.Context, actor Actor, id string, cfg SessionConfig) error
	Touch(ctx context.Context, actor Actor, id string) error
}

// NewNotFoundError 返回 session metadata store 使用的规范 not-found 错误。
func NewNotFoundError(id string) error {
	return notFoundError{id: id}
}

var _ SessionStore = (*Manager)(nil)
