package session

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestManagerCreateResolveListAndTouch(t *testing.T) {
	root := filepath.Join(t.TempDir(), "sessions")
	manager := NewManager(root)

	first, err := manager.Create(context.Background(), "  first prompt\nwith second line  ", SessionConfig{})
	if err != nil {
		t.Fatalf("Create() first error = %v", err)
	}
	if first.ID == "" {
		t.Fatal("Create() first ID is empty")
	}
	if first.Title != "first prompt" {
		t.Fatalf("Create() first Title = %q, want first prompt", first.Title)
	}
	if filepath.Dir(first.Path) != root {
		t.Fatalf("Create() first Path dir = %q, want %q", filepath.Dir(first.Path), root)
	}
	if filepath.Ext(first.Path) != ".jsonl" {
		t.Fatalf("Create() first Path = %q, want .jsonl extension", first.Path)
	}
	if _, err := os.Stat(filepath.Join(root, "index.json")); err != nil {
		t.Fatalf("index.json stat error = %v", err)
	}

	second, err := manager.Create(context.Background(), "second prompt", SessionConfig{})
	if err != nil {
		t.Fatalf("Create() second error = %v", err)
	}
	if first.ID == second.ID {
		t.Fatalf("Create() produced duplicate id %q", first.ID)
	}

	resolved, err := manager.Resolve(context.Background(), first.ID)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if resolved.ID != first.ID || resolved.Path != first.Path || resolved.Title != first.Title {
		t.Fatalf("Resolve() = %#v, want first meta %#v", resolved, first)
	}

	time.Sleep(time.Millisecond)
	if err := manager.Touch(context.Background(), first.ID); err != nil {
		t.Fatalf("Touch() error = %v", err)
	}
	listed, err := manager.List(context.Background())
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	gotIDs := []string{listed[0].ID, listed[1].ID}
	wantIDs := []string{first.ID, second.ID}
	if !reflect.DeepEqual(gotIDs, wantIDs) {
		t.Fatalf("List() ids = %#v, want touched first before second", gotIDs)
	}
	if listed[0].UpdatedAt.Before(listed[1].UpdatedAt) {
		t.Fatalf("List() not sorted by updated_at desc: %#v", listed)
	}
}

func TestManagerCreateResolveListPersistsConfig(t *testing.T) {
	manager := NewManager(filepath.Join(t.TempDir(), "sessions"))
	cfg := SessionConfig{ProviderName: "deepseek", SystemPrompt: "be concise", MaxSteps: 4}

	created, err := manager.Create(context.Background(), "prompt", cfg)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if !reflect.DeepEqual(created.Config, cfg) {
		t.Fatalf("Create() Config = %#v, want %#v", created.Config, cfg)
	}

	resolved, err := manager.Resolve(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if !reflect.DeepEqual(resolved.Config, cfg) {
		t.Fatalf("Resolve() Config = %#v, want %#v", resolved.Config, cfg)
	}

	listed, err := manager.List(context.Background())
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(listed) != 1 || !reflect.DeepEqual(listed[0].Config, cfg) {
		t.Fatalf("List() = %#v, want config %#v", listed, cfg)
	}
}

func TestManagerUpdateConfigPersistsPreferenceAndTouchesSession(t *testing.T) {
	manager := NewManager(filepath.Join(t.TempDir(), "sessions"))
	created, err := manager.Create(context.Background(), "prompt", SessionConfig{ProviderName: "old"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	time.Sleep(time.Millisecond)
	updatedCfg := SessionConfig{ProviderName: "new", SystemPrompt: "keep", MaxSteps: 2}

	if err := manager.UpdateConfig(context.Background(), created.ID, updatedCfg); err != nil {
		t.Fatalf("UpdateConfig() error = %v", err)
	}
	resolved, err := manager.Resolve(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if !reflect.DeepEqual(resolved.Config, updatedCfg) {
		t.Fatalf("Resolve() Config = %#v, want %#v", resolved.Config, updatedCfg)
	}
	if !resolved.UpdatedAt.After(created.UpdatedAt) {
		t.Fatalf("UpdateConfig() UpdatedAt = %v, want after %v", resolved.UpdatedAt, created.UpdatedAt)
	}
}

func TestManagerReadsLegacyIndexWithoutConfig(t *testing.T) {
	root := filepath.Join(t.TempDir(), "sessions")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	createdAt := time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC)
	legacy := `{"current":"legacy","sessions":[{"id":"legacy","path":"` + filepath.Join(root, "legacy.jsonl") + `","title":"legacy session","created_at":"` + createdAt.Format(time.RFC3339) + `","updated_at":"` + createdAt.Format(time.RFC3339) + `"}]}`
	if err := os.WriteFile(filepath.Join(root, "index.json"), []byte(legacy), 0o600); err != nil {
		t.Fatalf("write legacy index: %v", err)
	}

	meta, err := NewManager(root).Resolve(context.Background(), "legacy")
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if meta.Config != (SessionConfig{}) {
		t.Fatalf("Resolve() Config = %#v, want empty legacy config", meta.Config)
	}
}

func TestManagerCreateUsesUntitledSessionForBlankTitle(t *testing.T) {
	manager := NewManager(filepath.Join(t.TempDir(), "sessions"))

	meta, err := manager.Create(context.Background(), " \n\t ", SessionConfig{})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if meta.Title != "untitled session" {
		t.Fatalf("Create() Title = %q, want untitled session", meta.Title)
	}
}

func TestManagerResolveMissingSessionReturnsError(t *testing.T) {
	manager := NewManager(filepath.Join(t.TempDir(), "sessions"))

	_, err := manager.Resolve(context.Background(), "missing-session")
	if err == nil {
		t.Fatal("Resolve() error = nil, want missing session error")
	}
	if !strings.Contains(err.Error(), "missing-session") {
		t.Fatalf("Resolve() error = %q, want missing id context", err.Error())
	}
}

func TestManagerListMissingIndexReturnsEmptyList(t *testing.T) {
	manager := NewManager(filepath.Join(t.TempDir(), "sessions"))

	metas, err := manager.List(context.Background())
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(metas) != 0 {
		t.Fatalf("List() len = %d, want 0", len(metas))
	}
}

func TestManagerMalformedIndexReturnsPathError(t *testing.T) {
	root := filepath.Join(t.TempDir(), "sessions")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	indexPath := filepath.Join(root, "index.json")
	if err := os.WriteFile(indexPath, []byte("{not-json}"), 0o600); err != nil {
		t.Fatalf("write malformed index: %v", err)
	}

	_, err := NewManager(root).List(context.Background())
	if err == nil {
		t.Fatal("List() error = nil, want malformed index error")
	}
	if !strings.Contains(err.Error(), indexPath) {
		t.Fatalf("List() error = %q, want index path context", err.Error())
	}
}

func TestManagerTouchMissingSessionReturnsError(t *testing.T) {
	manager := NewManager(filepath.Join(t.TempDir(), "sessions"))

	err := manager.Touch(context.Background(), "missing-session")
	if err == nil {
		t.Fatal("Touch() error = nil, want missing session error")
	}
	if !strings.Contains(err.Error(), "missing-session") {
		t.Fatalf("Touch() error = %q, want missing id context", err.Error())
	}
}

func TestManagerTimestampsAreUTC(t *testing.T) {
	manager := NewManager(filepath.Join(t.TempDir(), "sessions"))

	meta, err := manager.Create(context.Background(), "prompt", SessionConfig{})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if meta.CreatedAt.Location() != time.UTC || meta.UpdatedAt.Location() != time.UTC {
		t.Fatalf("timestamps locations = %v / %v, want UTC", meta.CreatedAt.Location(), meta.UpdatedAt.Location())
	}
}
