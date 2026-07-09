package postgres

import (
	"os"
	"strings"
	"testing"
)

func TestInitialMigrationDefinesUsersSessionsAndLocalUser(t *testing.T) {
	up, err := os.ReadFile("../../../migrations/0001_users_sessions.up.sql")
	if err != nil {
		t.Fatalf("read up migration: %v", err)
	}
	body := strings.ToLower(string(up))
	checks := []string{
		"create table users",
		"id text primary key",
		"email text unique",
		"create table sessions",
		"owner_id text not null references users(id)",
		"provider_name text",
		"session_prompt text",
		"max_steps integer",
		"create index sessions_owner_updated_idx",
		"on sessions(owner_id, updated_at desc)",
		"insert into users",
		"local",
		"local user",
	}
	for _, want := range checks {
		if !strings.Contains(body, want) {
			t.Fatalf("up migration missing %q in:\n%s", want, body)
		}
	}
}

func TestInitialMigrationDownDropsSessionsBeforeUsers(t *testing.T) {
	down, err := os.ReadFile("../../../migrations/0001_users_sessions.down.sql")
	if err != nil {
		t.Fatalf("read down migration: %v", err)
	}
	body := strings.ToLower(string(down))
	sessions := strings.Index(body, "drop table if exists sessions")
	users := strings.Index(body, "drop table if exists users")
	if sessions < 0 || users < 0 || sessions > users {
		t.Fatalf("down migration must drop sessions before users:\n%s", body)
	}
}
