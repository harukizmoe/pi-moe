package postgres

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"harukizmoe/pimoe/internal/session"
)

func newTestStore(t *testing.T) (session.SessionStore, string) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&UserRecord{}, &SessionRecord{}); err != nil {
		t.Fatalf("AutoMigrate() error = %v", err)
	}
	now := time.Date(2026, 7, 10, 10, 0, 0, 0, time.UTC)
	if err := db.Create(&UserRecord{ID: "local", DisplayName: "Local User", CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatalf("seed local user: %v", err)
	}
	root := filepath.Join(t.TempDir(), "sessions")
	return NewSessionStore(db, root), root
}

func TestSessionStoreCreateResolveListFiltersByOwner(t *testing.T) {
	ctx := context.Background()
	store, root := newTestStore(t)
	alice := session.Actor{UserID: "alice"}
	bob := session.Actor{UserID: "bob"}

	aliceSession, err := store.Create(ctx, alice, "alice prompt", session.SessionConfig{ProviderName: "fake", SessionPrompt: "brief", MaxSteps: 2})
	if err != nil {
		t.Fatalf("Create() alice error = %v", err)
	}
	bobSession, err := store.Create(ctx, bob, "bob prompt", session.SessionConfig{})
	if err != nil {
		t.Fatalf("Create() bob error = %v", err)
	}
	if aliceSession.OwnerID != "alice" || aliceSession.Path != filepath.Join(root, aliceSession.ID+".jsonl") {
		t.Fatalf("alice session = %#v", aliceSession)
	}
	if bobSession.OwnerID != "bob" {
		t.Fatalf("bob OwnerID = %q, want bob", bobSession.OwnerID)
	}

	resolved, err := store.Resolve(ctx, alice, aliceSession.ID)
	if err != nil {
		t.Fatalf("Resolve() alice error = %v", err)
	}
	if resolved.ID != aliceSession.ID || resolved.Config.ProviderName != "fake" || resolved.Config.SessionPrompt != "brief" || resolved.Config.MaxSteps != 2 {
		t.Fatalf("Resolve() = %#v, want alice config", resolved)
	}
	if _, err := store.Resolve(ctx, bob, aliceSession.ID); !session.IsNotFound(err) {
		t.Fatalf("Resolve() bob on alice err = %v, want not found", err)
	}
	aliceList, err := store.List(ctx, alice)
	if err != nil {
		t.Fatalf("List() alice error = %v", err)
	}
	if len(aliceList) != 1 || aliceList[0].ID != aliceSession.ID {
		t.Fatalf("List() alice = %#v, want only alice session", aliceList)
	}
}

func TestSessionStoreTouchAndUpdateConfigRequireOwner(t *testing.T) {
	ctx := context.Background()
	store, _ := newTestStore(t)
	alice := session.Actor{UserID: "alice"}
	bob := session.Actor{UserID: "bob"}

	created, err := store.Create(ctx, alice, "alice prompt", session.SessionConfig{ProviderName: "old"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if err := store.Touch(ctx, bob, created.ID); !session.IsNotFound(err) {
		t.Fatalf("Touch() bob error = %v, want not found", err)
	}
	updatedCfg := session.SessionConfig{ProviderName: "new", SessionPrompt: "keep", MaxSteps: 4}
	if err := store.UpdateConfig(ctx, alice, created.ID, updatedCfg); err != nil {
		t.Fatalf("UpdateConfig() alice error = %v", err)
	}
	resolved, err := store.Resolve(ctx, alice, created.ID)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if resolved.Config != updatedCfg {
		t.Fatalf("Config = %#v, want %#v", resolved.Config, updatedCfg)
	}
	if !resolved.UpdatedAt.After(created.UpdatedAt) {
		t.Fatalf("UpdatedAt = %s, want after %s", resolved.UpdatedAt, created.UpdatedAt)
	}
	if err := store.UpdateConfig(ctx, bob, created.ID, session.SessionConfig{}); !session.IsNotFound(err) {
		t.Fatalf("UpdateConfig() bob error = %v, want not found", err)
	}
}

func TestSessionStoreNormalizesBlankActorToLocal(t *testing.T) {
	ctx := context.Background()
	store, _ := newTestStore(t)
	created, err := store.Create(ctx, session.Actor{}, "local prompt", session.SessionConfig{})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if created.OwnerID != "local" {
		t.Fatalf("OwnerID = %q, want local", created.OwnerID)
	}
	if _, err := store.Resolve(ctx, session.LocalActor(), created.ID); err != nil {
		t.Fatalf("Resolve() local error = %v", err)
	}
}
