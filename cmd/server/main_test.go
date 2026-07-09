package main

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseServerOptionsDefaults(t *testing.T) {
	got, err := parseServerOptions(nil)
	if err != nil {
		t.Fatalf("parseServerOptions(nil) error = %v", err)
	}

	if got.configPath != "configs/providers.yaml" {
		t.Fatalf("configPath = %q, want %q", got.configPath, "configs/providers.yaml")
	}
	if got.sessionRoot != ".moe/sessions" {
		t.Fatalf("sessionRoot = %q, want %q", got.sessionRoot, ".moe/sessions")
	}
	if got.addr != ":8080" {
		t.Fatalf("addr = %q, want %q", got.addr, ":8080")
	}
	if got.providerName != "" {
		t.Fatalf("providerName = %q, want empty", got.providerName)
	}
	if gotSessionStore := serverOptionString(t, got, "sessionStore"); gotSessionStore != "file" {
		t.Fatalf("sessionStore = %q, want file", gotSessionStore)
	}
}

func TestParseServerOptionsAcceptsFlagOverrides(t *testing.T) {
	got, err := parseServerOptions([]string{
		"--addr", "127.0.0.1:9090",
		"--config", "testdata/providers.yaml",
		"--session-root", "state/sessions",
		"--provider", "fake-local",
	})
	if err != nil {
		t.Fatalf("parseServerOptions() error = %v", err)
	}

	if got.addr != "127.0.0.1:9090" {
		t.Fatalf("addr = %q, want %q", got.addr, "127.0.0.1:9090")
	}
	if got.configPath != "testdata/providers.yaml" {
		t.Fatalf("configPath = %q, want %q", got.configPath, "testdata/providers.yaml")
	}
	if got.sessionRoot != "state/sessions" {
		t.Fatalf("sessionRoot = %q, want %q", got.sessionRoot, "state/sessions")
	}
	if got.providerName != "fake-local" {
		t.Fatalf("providerName = %q, want %q", got.providerName, "fake-local")
	}
}

func TestParseServerOptionsAcceptsPostgresSessionStore(t *testing.T) {
	got, err := parseServerOptions([]string{
		"--session-store", "postgres",
		"--postgres-dsn", "postgres://pimoe:test@localhost:5432/pimoe?sslmode=disable",
	})
	if err != nil {
		t.Fatalf("parseServerOptions() error = %v", err)
	}

	if gotSessionStore := serverOptionString(t, got, "sessionStore"); gotSessionStore != "postgres" {
		t.Fatalf("sessionStore = %q, want postgres", gotSessionStore)
	}
	if gotDSN := serverOptionString(t, got, "postgresDSN"); gotDSN != "postgres://pimoe:test@localhost:5432/pimoe?sslmode=disable" {
		t.Fatalf("postgresDSN = %q, want flag value", gotDSN)
	}
}

func TestParseServerOptionsValidatesSessionStore(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{name: "unknown store", args: []string{"--session-store", "sqlite"}, wantErr: "sqlite"},
		{name: "postgres requires dsn", args: []string{"--session-store", "postgres"}, wantErr: "--postgres-dsn"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseServerOptions(tt.args)
			if err == nil {
				t.Fatalf("parseServerOptions(%#v) error = nil, want validation error", tt.args)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("parseServerOptions(%#v) error = %q, want containing %q", tt.args, err.Error(), tt.wantErr)
			}
		})
	}
}

func TestParseServerOptionsRejectsInvalidFlags(t *testing.T) {
	if _, err := parseServerOptions([]string{"--unknown"}); err == nil {
		t.Fatal("parseServerOptions() error = nil, want invalid flag error")
	}
}

func serverOptionString(t *testing.T, opts serverOptions, name string) string {
	t.Helper()
	field := reflect.ValueOf(opts).FieldByName(name)
	if !field.IsValid() {
		t.Fatalf("serverOptions.%s is missing", name)
	}
	if field.Kind() != reflect.String {
		t.Fatalf("serverOptions.%s kind = %s, want string", name, field.Kind())
	}
	return field.String()
}
