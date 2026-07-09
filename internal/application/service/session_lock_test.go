package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestSessionServiceUnknownSessionDoesNotRetainRunLock(t *testing.T) {
	svc, err := NewSessionService(SessionConfig{
		SessionRoot:        filepath.Join(t.TempDir(), "sessions"),
		ProviderConfigPath: writeLockTestProvidersConfig(t),
		ProviderName:       "fake-local",
	})
	if err != nil {
		t.Fatalf("NewSessionService() error = %v", err)
	}

	for _, id := range []string{"missing-a", "missing-b"} {
		if _, err := svc.Run(context.Background(), id, "hello"); err == nil {
			t.Fatalf("Run(%q) error = nil, want missing session error", id)
		}
	}

	if got := len(svc.sessionRunLocks); got != 0 {
		t.Fatalf("sessionRunLocks len = %d, want 0 for missing sessions", got)
	}
}

func TestSessionServiceLockSessionRunHonorsCanceledContext(t *testing.T) {
	svc, err := NewSessionService(SessionConfig{
		SessionRoot:        filepath.Join(t.TempDir(), "sessions"),
		ProviderConfigPath: writeLockTestProvidersConfig(t),
		ProviderName:       "fake-local",
	})
	if err != nil {
		t.Fatalf("NewSessionService() error = %v", err)
	}

	unlock, err := svc.lockSessionRun(context.Background(), "session-a")
	if err != nil {
		t.Fatalf("lockSessionRun() first error = %v", err)
	}
	defer unlock()
	canceled, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := svc.lockSessionRun(canceled, "session-a"); err == nil {
		t.Fatal("lockSessionRun() error = nil, want context cancellation")
	}
}

func writeLockTestProvidersConfig(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "providers.yaml")
	content := `llms:
  default_provider: fake-local
  providers:
    fake-local:
      type: fake
      model: fake-tool-model
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write providers config: %v", err)
	}
	return path
}
