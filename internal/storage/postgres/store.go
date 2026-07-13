package postgres

import (
	"context"
	"crypto/rand"
	"database/sql/driver"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"harukizmoe/pimoe/internal/session"
)

const defaultTranscriptRoot = ".moe/sessions"

// Store 是 PostgreSQL/GORM-backed session metadata store；transcript 仍保存在 JSONL 文件中。
type Store struct {
	// db 是调用方注入并管理生命周期的 GORM 连接。
	db *gorm.DB
	// transcriptRoot 是 JSONL transcript 文件根目录，metadata 只保存可恢复配置。
	transcriptRoot string
}

var _ session.SessionStore = (*Store)(nil)

// NewSessionStore 创建 PostgreSQL session metadata store；db 由调用方负责生命周期管理。
func NewSessionStore(db *gorm.DB, transcriptRoot string) *Store {
	transcriptRoot = strings.TrimSpace(transcriptRoot)
	if transcriptRoot == "" {
		transcriptRoot = defaultTranscriptRoot
	}
	return &Store{db: db, transcriptRoot: transcriptRoot}
}

// Create 创建一条归属 actor 的 session metadata；缺失用户会按最小本地语义创建用户记录。
func (s *Store) Create(ctx context.Context, actor session.Actor, title string, cfg session.SessionConfig) (session.SessionMeta, error) {
	if err := s.ready(ctx); err != nil {
		return session.SessionMeta{}, err
	}
	actor = session.NormalizeActor(actor)
	now := time.Now().UTC()
	id, err := newSessionID(now)
	if err != nil {
		return session.SessionMeta{}, err
	}
	record := SessionRecord{
		ID:            id,
		OwnerID:       actor.UserID,
		Title:         normalizeTitle(title),
		ProviderName:  strings.TrimSpace(cfg.ProviderName),
		SessionPrompt: strings.TrimSpace(cfg.SessionPrompt),
		MaxSteps:      normalizeMaxSteps(cfg.MaxSteps),
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := ensureUser(ctx, tx, actor.UserID, now); err != nil {
			return err
		}
		return tx.Create(&record).Error
	})
	if err != nil {
		return session.SessionMeta{}, fmt.Errorf("create session metadata: %w", err)
	}
	return sessionRecordToMeta(s.transcriptRoot, record), nil
}

// Resolve 返回 actor 拥有的 session metadata；owner 不匹配时返回 not found 风格错误。
func (s *Store) Resolve(ctx context.Context, actor session.Actor, id string) (session.SessionMeta, error) {
	if err := s.ready(ctx); err != nil {
		return session.SessionMeta{}, err
	}
	actor = session.NormalizeActor(actor)
	var row sessionRecordRow
	err := s.db.WithContext(ctx).Table((SessionRecord{}).TableName()).Where("id = ? and owner_id = ?", id, actor.UserID).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return session.SessionMeta{}, session.NewNotFoundError(id)
	}
	if err != nil {
		return session.SessionMeta{}, fmt.Errorf("resolve session metadata: %w", err)
	}
	return sessionRecordToMeta(s.transcriptRoot, row.toRecord()), nil
}

// List 按 updated_at 倒序返回 actor 拥有的 sessions。
func (s *Store) List(ctx context.Context, actor session.Actor) ([]session.SessionMeta, error) {
	if err := s.ready(ctx); err != nil {
		return nil, err
	}
	actor = session.NormalizeActor(actor)
	var rows []sessionRecordRow
	if err := s.db.WithContext(ctx).Table((SessionRecord{}).TableName()).Where("owner_id = ?", actor.UserID).Order("updated_at desc").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list session metadata: %w", err)
	}
	out := make([]session.SessionMeta, 0, len(rows))
	for _, row := range rows {
		out = append(out, sessionRecordToMeta(s.transcriptRoot, row.toRecord()))
	}
	return out, nil
}

// UpdateConfig 更新 actor 拥有的 session 偏好和 updated_at。
func (s *Store) UpdateConfig(ctx context.Context, actor session.Actor, id string, cfg session.SessionConfig) error {
	if err := s.ready(ctx); err != nil {
		return err
	}
	actor = session.NormalizeActor(actor)
	updates := map[string]any{
		"provider_name":  strings.TrimSpace(cfg.ProviderName),
		"session_prompt": strings.TrimSpace(cfg.SessionPrompt),
		"max_steps":      normalizeMaxSteps(cfg.MaxSteps),
		"updated_at":     time.Now().UTC(),
	}
	result := s.db.WithContext(ctx).Model(&SessionRecord{}).Where("id = ? and owner_id = ?", id, actor.UserID).Updates(updates)
	if result.Error != nil {
		return fmt.Errorf("update session metadata: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return session.NewNotFoundError(id)
	}
	return nil
}

// Touch 更新 actor 拥有的 session updated_at。
func (s *Store) Touch(ctx context.Context, actor session.Actor, id string) error {
	if err := s.ready(ctx); err != nil {
		return err
	}
	actor = session.NormalizeActor(actor)
	result := s.db.WithContext(ctx).Model(&SessionRecord{}).Where("id = ? and owner_id = ?", id, actor.UserID).Update("updated_at", time.Now().UTC())
	if result.Error != nil {
		return fmt.Errorf("touch session metadata: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return session.NewNotFoundError(id)
	}
	return nil
}

func (s *Store) ready(ctx context.Context) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("postgres session store is nil")
	}
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}

func ensureUser(ctx context.Context, tx *gorm.DB, userID string, now time.Time) error {
	user := UserRecord{ID: userID, DisplayName: userID, CreatedAt: now, UpdatedAt: now}
	return tx.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&user).Error
}

type sessionRecordRow struct {
	ID            string      `gorm:"column:id"`
	OwnerID       string      `gorm:"column:owner_id"`
	Title         string      `gorm:"column:title"`
	ProviderName  string      `gorm:"column:provider_name"`
	SessionPrompt string      `gorm:"column:session_prompt"`
	MaxSteps      int         `gorm:"column:max_steps"`
	CreatedAt     dbTimestamp `gorm:"column:created_at"`
	UpdatedAt     dbTimestamp `gorm:"column:updated_at"`
	ArchivedAt    *time.Time  `gorm:"column:archived_at"`
}

func (r sessionRecordRow) toRecord() SessionRecord {
	return SessionRecord{
		ID:            r.ID,
		OwnerID:       r.OwnerID,
		Title:         r.Title,
		ProviderName:  r.ProviderName,
		SessionPrompt: r.SessionPrompt,
		MaxSteps:      r.MaxSteps,
		CreatedAt:     r.CreatedAt.Time.UTC(),
		UpdatedAt:     r.UpdatedAt.Time.UTC(),
		ArchivedAt:    r.ArchivedAt,
	}
}

type dbTimestamp struct {
	time.Time
}

func (t *dbTimestamp) Scan(value any) error {
	switch value := value.(type) {
	case time.Time:
		t.Time = value.UTC()
		return nil
	case string:
		return t.scanString(value)
	case []byte:
		return t.scanString(string(value))
	case nil:
		t.Time = time.Time{}
		return nil
	default:
		return fmt.Errorf("scan timestamp value %T", value)
	}
}

func (t dbTimestamp) Value() (driver.Value, error) {
	if t.Time.IsZero() {
		return nil, nil
	}
	return t.Time, nil
}

func (t *dbTimestamp) scanString(value string) error {
	parsed, err := parseDBTimestamp(value)
	if err != nil {
		return err
	}
	t.Time = parsed.UTC()
	return nil
}

var dbTimestampLayouts = []string{
	time.RFC3339Nano,
	"2006-01-02 15:04:05.999999999-07:00",
	"2006-01-02 15:04:05.999999999Z07:00",
	"2006-01-02 15:04:05.999999999",
	"2006-01-02 15:04:05-07:00",
	"2006-01-02 15:04:05Z07:00",
	"2006-01-02 15:04:05",
	"2006-01-02",
}

func parseDBTimestamp(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, fmt.Errorf("empty timestamp")
	}
	for _, layout := range dbTimestampLayouts {
		var (
			parsed time.Time
			err    error
		)
		if strings.Contains(layout, "Z07:00") || strings.Contains(layout, "-07:00") || layout == time.RFC3339Nano {
			parsed, err = time.Parse(layout, value)
		} else {
			parsed, err = time.ParseInLocation(layout, value, time.UTC)
		}
		if err == nil {
			return parsed.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("parse timestamp %q", value)
}

func normalizeTitle(title string) string {
	line := strings.TrimSpace(strings.Split(strings.ReplaceAll(title, "\r\n", "\n"), "\n")[0])
	if line == "" {
		return "untitled session"
	}
	if len(line) > 80 {
		return line[:80]
	}
	return line
}

func normalizeMaxSteps(maxSteps int) int {
	if maxSteps < 1 {
		return 0
	}
	return maxSteps
}

func newSessionID(now time.Time) (string, error) {
	var random [3]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", fmt.Errorf("generate session id: %w", err)
	}
	return now.UTC().Format("20060102-150405") + "-" + hex.EncodeToString(random[:]), nil
}
