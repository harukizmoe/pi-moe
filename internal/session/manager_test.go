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

	first, err := manager.Create(context.Background(), "  first prompt\nwith second line  ")
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

	second, err := manager.Create(context.Background(), "second prompt")
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

func TestManagerCreateUsesUntitledSessionForBlankTitle(t *testing.T) {
	manager := NewManager(filepath.Join(t.TempDir(), "sessions"))

	meta, err := manager.Create(context.Background(), " \n\t ")
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

	meta, err := manager.Create(context.Background(), "prompt")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if meta.CreatedAt.Location() != time.UTC || meta.UpdatedAt.Location() != time.UTC {
		t.Fatalf("timestamps locations = %v / %v, want UTC", meta.CreatedAt.Location(), meta.UpdatedAt.Location())
	}
}
