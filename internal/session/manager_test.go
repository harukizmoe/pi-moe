package session

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestManagerCreateResolveListAndTouch(t *testing.T) {
	root := filepath.Join(t.TempDir(), "sessions")
	manager := NewManager(root)

	first, err := manager.Create(context.Background(), LocalActor(), "  first prompt\nwith second line  ", SessionConfig{})
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

	second, err := manager.Create(context.Background(), LocalActor(), "second prompt", SessionConfig{})
	if err != nil {
		t.Fatalf("Create() second error = %v", err)
	}
	if first.ID == second.ID {
		t.Fatalf("Create() produced duplicate id %q", first.ID)
	}

	resolved, err := manager.Resolve(context.Background(), LocalActor(), first.ID)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if resolved.ID != first.ID || resolved.Path != first.Path || resolved.Title != first.Title {
		t.Fatalf("Resolve() = %#v, want first meta %#v", resolved, first)
	}

	time.Sleep(time.Millisecond)
	if err := manager.Touch(context.Background(), LocalActor(), first.ID); err != nil {
		t.Fatalf("Touch() error = %v", err)
	}
	listed, err := manager.List(context.Background(), LocalActor())
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
func TestManagerConcurrentCreatesKeepAllEntries(t *testing.T) {
	manager := NewManager(filepath.Join(t.TempDir(), "sessions"))
	const count = 256

	start := make(chan struct{})
	metas := make(chan SessionMeta, count)
	errs := make(chan error, count)
	var wg sync.WaitGroup
	for i := range count {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			meta, err := manager.Create(context.Background(), LocalActor(), fmt.Sprintf("prompt %03d", i), SessionConfig{})
			if err != nil {
				errs <- err
				return
			}
			metas <- meta
		}(i)
	}

	close(start)
	wg.Wait()
	close(errs)
	close(metas)

	for err := range errs {
		t.Fatalf("Create() concurrent error = %v", err)
	}
	created := make(map[string]struct{}, count)
	for meta := range metas {
		created[meta.ID] = struct{}{}
	}
	if len(created) != count {
		t.Fatalf("created unique ids = %d, want %d", len(created), count)
	}

	listed, err := manager.List(context.Background(), LocalActor())
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(listed) != count {
		t.Fatalf("List() len = %d, want %d", len(listed), count)
	}
	listedIDs := make(map[string]struct{}, len(listed))
	for _, meta := range listed {
		listedIDs[meta.ID] = struct{}{}
	}
	for id := range created {
		if _, ok := listedIDs[id]; !ok {
			t.Fatalf("List() missing created session %q", id)
		}
	}
}

func TestManagerCreateResolveListPersistsConfig(t *testing.T) {
	manager := NewManager(filepath.Join(t.TempDir(), "sessions"))
	cfg := SessionConfig{ProviderName: "deepseek", SessionPrompt: "be concise", MaxSteps: 4}

	created, err := manager.Create(context.Background(), LocalActor(), "prompt", cfg)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if !reflect.DeepEqual(created.Config, cfg) {
		t.Fatalf("Create() Config = %#v, want %#v", created.Config, cfg)
	}

	resolved, err := manager.Resolve(context.Background(), LocalActor(), created.ID)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if !reflect.DeepEqual(resolved.Config, cfg) {
		t.Fatalf("Resolve() Config = %#v, want %#v", resolved.Config, cfg)
	}
	if resolved.Config.SessionPrompt != "be concise" {
		t.Fatalf("SessionPrompt = %q, want be concise", resolved.Config.SessionPrompt)
	}
	indexBytes, err := os.ReadFile(filepath.Join(manager.root, "index.json"))
	if err != nil {
		t.Fatalf("ReadFile(index) error = %v", err)
	}
	indexJSON := string(indexBytes)
	if !strings.Contains(indexJSON, `"session_prompt": "be concise"`) {
		t.Fatalf("index JSON = %s, want session_prompt", indexJSON)
	}
	assertOnlySessionConfigFields(t, indexBytes)

	listed, err := manager.List(context.Background(), LocalActor())
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(listed) != 1 || !reflect.DeepEqual(listed[0].Config, cfg) {
		t.Fatalf("List() = %#v, want config %#v", listed, cfg)
	}
}

func assertOnlySessionConfigFields(t *testing.T, indexBytes []byte) {
	t.Helper()

	var index struct {
		Sessions []struct {
			Config map[string]json.RawMessage `json:"config"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal(indexBytes, &index); err != nil {
		t.Fatalf("decode index JSON error = %v; body = %s", err, indexBytes)
	}
	if len(index.Sessions) != 1 {
		t.Fatalf("index sessions len = %d, want 1", len(index.Sessions))
	}
	allowed := map[string]struct{}{
		"provider_name":  {},
		"session_prompt": {},
		"max_steps":      {},
	}
	for field := range index.Sessions[0].Config {
		if _, ok := allowed[field]; !ok {
			t.Fatalf("config exposes unexpected field %q in %s", field, indexBytes)
		}
	}
}

func TestManagerUpdateConfigPersistsPreferenceAndTouchesSession(t *testing.T) {
	manager := NewManager(filepath.Join(t.TempDir(), "sessions"))
	created, err := manager.Create(context.Background(), LocalActor(), "prompt", SessionConfig{ProviderName: "old"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	time.Sleep(time.Millisecond)
	updatedCfg := SessionConfig{ProviderName: "new", SessionPrompt: "keep", MaxSteps: 2}

	if err := manager.UpdateConfig(context.Background(), LocalActor(), created.ID, updatedCfg); err != nil {
		t.Fatalf("UpdateConfig() error = %v", err)
	}
	resolved, err := manager.Resolve(context.Background(), LocalActor(), created.ID)
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

	meta, err := NewManager(root).Resolve(context.Background(), LocalActor(), "legacy")
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if meta.Config != (SessionConfig{}) {
		t.Fatalf("Resolve() Config = %#v, want empty legacy config", meta.Config)
	}
}

func TestManagerCreateUsesUntitledSessionForBlankTitle(t *testing.T) {
	manager := NewManager(filepath.Join(t.TempDir(), "sessions"))

	meta, err := manager.Create(context.Background(), LocalActor(), " \n\t ", SessionConfig{})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if meta.Title != "untitled session" {
		t.Fatalf("Create() Title = %q, want untitled session", meta.Title)
	}
}

func TestManagerResolveMissingSessionReturnsError(t *testing.T) {
	manager := NewManager(filepath.Join(t.TempDir(), "sessions"))

	_, err := manager.Resolve(context.Background(), LocalActor(), "missing-session")
	if err == nil {
		t.Fatal("Resolve() error = nil, want missing session error")
	}
	if !strings.Contains(err.Error(), "missing-session") {
		t.Fatalf("Resolve() error = %q, want missing id context", err.Error())
	}
}

func TestManagerListMissingIndexReturnsEmptyList(t *testing.T) {
	manager := NewManager(filepath.Join(t.TempDir(), "sessions"))

	metas, err := manager.List(context.Background(), LocalActor())
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

	_, err := NewManager(root).List(context.Background(), LocalActor())
	if err == nil {
		t.Fatal("List() error = nil, want malformed index error")
	}
	if !strings.Contains(err.Error(), indexPath) {
		t.Fatalf("List() error = %q, want index path context", err.Error())
	}
}

func TestManagerTouchMissingSessionReturnsError(t *testing.T) {
	manager := NewManager(filepath.Join(t.TempDir(), "sessions"))

	err := manager.Touch(context.Background(), LocalActor(), "missing-session")
	if err == nil {
		t.Fatal("Touch() error = nil, want missing session error")
	}
	if !strings.Contains(err.Error(), "missing-session") {
		t.Fatalf("Touch() error = %q, want missing id context", err.Error())
	}
}

func TestManagerTimestampsAreUTC(t *testing.T) {
	manager := NewManager(filepath.Join(t.TempDir(), "sessions"))

	meta, err := manager.Create(context.Background(), LocalActor(), "prompt", SessionConfig{})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if meta.CreatedAt.Location() != time.UTC || meta.UpdatedAt.Location() != time.UTC {
		t.Fatalf("timestamps locations = %v / %v, want UTC", meta.CreatedAt.Location(), meta.UpdatedAt.Location())
	}
}

func TestSessionStoreInterfaceUsesManagerOwnershipRules(t *testing.T) {
	var store SessionStore = NewManager(filepath.Join(t.TempDir(), "sessions"))
	ctx := context.Background()
	alice := Actor{UserID: "alice"}
	bob := Actor{UserID: "bob"}

	created, err := store.Create(ctx, alice, "alice prompt", SessionConfig{ProviderName: "fake"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if created.OwnerID != "alice" {
		t.Fatalf("Create() OwnerID = %q, want alice", created.OwnerID)
	}
	if _, err := store.Resolve(ctx, bob, created.ID); !IsNotFound(err) {
		t.Fatalf("Resolve() bob error = %v, want not found", err)
	}
	listed, err := store.List(ctx, alice)
	if err != nil {
		t.Fatalf("List() alice error = %v", err)
	}
	if len(listed) != 1 || listed[0].ID != created.ID {
		t.Fatalf("List() alice = %#v, want created session", listed)
	}
}

func TestManagerCreateResolveListFiltersByOwner(t *testing.T) {
	manager := NewManager(filepath.Join(t.TempDir(), "sessions"))
	alice := Actor{UserID: "alice"}
	bob := Actor{UserID: "bob"}

	aliceSession, err := manager.Create(context.Background(), alice, "alice prompt", SessionConfig{ProviderName: "fake"})
	if err != nil {
		t.Fatalf("Create() alice error = %v", err)
	}
	bobSession, err := manager.Create(context.Background(), bob, "bob prompt", SessionConfig{})
	if err != nil {
		t.Fatalf("Create() bob error = %v", err)
	}
	if aliceSession.OwnerID != "alice" {
		t.Fatalf("alice OwnerID = %q, want alice", aliceSession.OwnerID)
	}
	if bobSession.OwnerID != "bob" {
		t.Fatalf("bob OwnerID = %q, want bob", bobSession.OwnerID)
	}

	resolved, err := manager.Resolve(context.Background(), alice, aliceSession.ID)
	if err != nil {
		t.Fatalf("Resolve() alice error = %v", err)
	}
	if resolved.ID != aliceSession.ID || resolved.OwnerID != "alice" {
		t.Fatalf("Resolve() = %#v, want alice session", resolved)
	}
	if _, err := manager.Resolve(context.Background(), bob, aliceSession.ID); !IsNotFound(err) {
		t.Fatalf("Resolve() bob on alice err = %v, want not found", err)
	}

	aliceList, err := manager.List(context.Background(), alice)
	if err != nil {
		t.Fatalf("List() alice error = %v", err)
	}
	if len(aliceList) != 1 || aliceList[0].ID != aliceSession.ID {
		t.Fatalf("List() alice = %#v, want only alice session", aliceList)
	}
	bobList, err := manager.List(context.Background(), bob)
	if err != nil {
		t.Fatalf("List() bob error = %v", err)
	}
	if len(bobList) != 1 || bobList[0].ID != bobSession.ID {
		t.Fatalf("List() bob = %#v, want only bob session", bobList)
	}
}

func TestManagerOwnerMismatchCannotTouchOrUpdateConfig(t *testing.T) {
	manager := NewManager(filepath.Join(t.TempDir(), "sessions"))
	alice := Actor{UserID: "alice"}
	bob := Actor{UserID: "bob"}
	created, err := manager.Create(context.Background(), alice, "prompt", SessionConfig{ProviderName: "old"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	if err := manager.Touch(context.Background(), bob, created.ID); !IsNotFound(err) {
		t.Fatalf("Touch() owner mismatch err = %v, want not found", err)
	}
	if err := manager.UpdateConfig(context.Background(), bob, created.ID, SessionConfig{ProviderName: "new"}); !IsNotFound(err) {
		t.Fatalf("UpdateConfig() owner mismatch err = %v, want not found", err)
	}

	resolved, err := manager.Resolve(context.Background(), alice, created.ID)
	if err != nil {
		t.Fatalf("Resolve() after rejected writes error = %v", err)
	}
	if resolved.Config.ProviderName != "old" {
		t.Fatalf("ProviderName = %q, want old", resolved.Config.ProviderName)
	}
}

func TestManagerReadsLegacyEmptyOwnerAsLocal(t *testing.T) {
	root := filepath.Join(t.TempDir(), "sessions")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	indexJSON := `{
  "current": "sess-legacy",
  "sessions": [
    {
      "id": "sess-legacy",
      "path": "` + filepath.ToSlash(filepath.Join(root, "sess-legacy.jsonl")) + `",
      "title": "legacy prompt",
      "created_at": "2026-07-09T00:00:00Z",
      "updated_at": "2026-07-09T00:00:00Z"
    }
  ]
}`
	if err := os.WriteFile(filepath.Join(root, "index.json"), []byte(indexJSON), 0o600); err != nil {
		t.Fatalf("WriteFile(index) error = %v", err)
	}
	manager := NewManager(root)

	resolved, err := manager.Resolve(context.Background(), LocalActor(), "sess-legacy")
	if err != nil {
		t.Fatalf("Resolve() legacy local error = %v", err)
	}
	if resolved.OwnerID != LocalActor().UserID {
		t.Fatalf("legacy OwnerID = %q, want local", resolved.OwnerID)
	}
	if _, err := manager.Resolve(context.Background(), Actor{UserID: "alice"}, "sess-legacy"); !IsNotFound(err) {
		t.Fatalf("Resolve() non-local legacy err = %v, want not found", err)
	}
}
