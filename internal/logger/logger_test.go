package logger

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDevelopmentFileWritesLogToFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "agent.log")

	log, closeLog, err := NewDevelopmentFile(path)
	if err != nil {
		t.Fatalf("NewDevelopmentFile() error = %v", err)
	}

	log.Info(context.Background(), "agent.run.start", "model", "fake-tool-model")
	if err := closeLog(); err != nil {
		t.Fatalf("closeLog() error = %v", err)
	}

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}
	got := string(content)
	for _, want := range []string{"level=INFO", "msg=agent.run.start", "model=fake-tool-model"} {
		if !strings.Contains(got, want) {
			t.Fatalf("log file = %q, want substring %q", got, want)
		}
	}
}
