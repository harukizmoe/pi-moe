package postgres

import "time"

// UserRecord 是 PostgreSQL users 表的 GORM record；不要作为业务层用户模型暴露。
type UserRecord struct {
	// ID 是 users 表主键，对应内置 local 用户和后续真实用户标识。
	ID string `gorm:"primaryKey;type:text"`
	// Email 是可选唯一邮箱；本地默认用户使用 nil。
	Email *string `gorm:"uniqueIndex;type:text"`
	// DisplayName 是展示名称，和迁移中的 display_name not null 保持一致。
	DisplayName string `gorm:"not null;type:text"`
	// CreatedAt 是用户记录创建时间。
	CreatedAt time.Time `gorm:"not null;type:timestamptz"`
	// UpdatedAt 是用户记录最近更新时间。
	UpdatedAt time.Time `gorm:"not null;type:timestamptz"`
}

// TableName 固定 users 表名，避免 GORM 根据结构体名推断出错。
func (UserRecord) TableName() string { return "users" }

// SessionRecord 是 PostgreSQL sessions 表的 GORM record；transcript 仍保存在 JSONL 文件中。
type SessionRecord struct {
	// ID 是 sessions 表主键，也是 transcript 文件名的来源。
	ID string `gorm:"primaryKey;type:text"`
	// OwnerID 对应 sessions.owner_id，并参与 owner + updated_at 倒序索引。
	OwnerID string `gorm:"not null;type:text;index:sessions_owner_updated_idx,priority:1"`
	// Title 是 session 列表展示用短标题。
	Title string `gorm:"not null;type:text"`
	// ProviderName 保存恢复 session 时使用的 Provider 实例名。
	ProviderName string `gorm:"type:text"`
	// SessionPrompt 保存恢复 session 时使用的 Agent 行为设定。
	SessionPrompt string `gorm:"type:text"`
	// MaxSteps 保存恢复 session 时使用的 tool-calling 最大轮数。
	MaxSteps int `gorm:"type:integer"`
	// CreatedAt 是 session 元数据创建时间。
	CreatedAt time.Time `gorm:"not null;type:timestamptz"`
	// UpdatedAt 是 session 最近使用时间，并参与 owner + updated_at 倒序索引。
	UpdatedAt time.Time `gorm:"not null;type:timestamptz;index:sessions_owner_updated_idx,priority:2,sort:desc"`
	// ArchivedAt 非空时表示 session 已归档；当前任务只定义字段，不实现归档逻辑。
	ArchivedAt *time.Time `gorm:"type:timestamptz"`
}

// TableName 固定 sessions 表名，和 SQL migration 保持一致。
func (SessionRecord) TableName() string { return "sessions" }
