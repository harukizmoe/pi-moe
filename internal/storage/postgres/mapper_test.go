package postgres

import (
	"path/filepath"
	"testing"
	"time"

	"harukizmoe/pimoe/internal/session"
)

func TestSessionRecordToMetaComputesTranscriptPathAndConfig(t *testing.T) {
	root := filepath.Join(t.TempDir(), "sessions")
	createdAt := time.Date(2026, 7, 10, 10, 0, 0, 0, time.UTC)
	updatedAt := createdAt.Add(time.Minute)
	record := SessionRecord{
		ID:            "session-1",
		OwnerID:       "alice",
		Title:         "alice prompt",
		ProviderName:  "fake-local",
		SessionPrompt: "be brief",
		MaxSteps:      3,
		CreatedAt:     createdAt,
		UpdatedAt:     updatedAt,
	}

	meta := sessionRecordToMeta(root, record)

	if meta.ID != "session-1" || meta.OwnerID != "alice" || meta.Title != "alice prompt" {
		t.Fatalf("meta identity = %#v", meta)
	}
	if meta.Path != filepath.Join(root, "session-1.jsonl") {
		t.Fatalf("meta Path = %q, want computed transcript path", meta.Path)
	}
	wantCfg := session.SessionConfig{ProviderName: "fake-local", SessionPrompt: "be brief", MaxSteps: 3}
	if meta.Config != wantCfg {
		t.Fatalf("meta Config = %#v, want %#v", meta.Config, wantCfg)
	}
	if !meta.CreatedAt.Equal(createdAt) || !meta.UpdatedAt.Equal(updatedAt) {
		t.Fatalf("meta times = %s/%s, want %s/%s", meta.CreatedAt, meta.UpdatedAt, createdAt, updatedAt)
	}
}

func TestSessionMetaToRecordDropsTranscriptPath(t *testing.T) {
	createdAt := time.Date(2026, 7, 10, 10, 0, 0, 0, time.UTC)
	meta := session.SessionMeta{
		ID:        "session-1",
		OwnerID:   "alice",
		Path:      "/tmp/sessions/session-1.jsonl",
		Title:     "alice prompt",
		CreatedAt: createdAt,
		UpdatedAt: createdAt,
		Config:    session.SessionConfig{ProviderName: "fake-local", SessionPrompt: "be brief", MaxSteps: 3},
	}

	record := sessionMetaToRecord(meta)

	if record.ID != meta.ID || record.OwnerID != meta.OwnerID || record.Title != meta.Title {
		t.Fatalf("record identity = %#v", record)
	}
	if record.ProviderName != "fake-local" || record.SessionPrompt != "be brief" || record.MaxSteps != 3 {
		t.Fatalf("record config fields = %#v", record)
	}
}
