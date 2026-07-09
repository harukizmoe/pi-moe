package main

import "testing"

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

func TestParseServerOptionsRejectsInvalidFlags(t *testing.T) {
	if _, err := parseServerOptions([]string{"--unknown"}); err == nil {
		t.Fatal("parseServerOptions() error = nil, want invalid flag error")
	}
}
