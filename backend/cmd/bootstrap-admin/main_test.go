package main

import (
	"bytes"
	"errors"
	"flag"
	"os"
	"strings"
	"testing"
)

func fakeGetenv(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}

func TestParseFlags_ValidValues(t *testing.T) {
	getenv := fakeGetenv(map[string]string{"GFR_DATABASE_URL": "postgres://gfr_dev:pw@127.0.0.1:5432/db"})
	var usage bytes.Buffer

	cfg, err := parseFlags([]string{
		"-email", "admin@example.test",
		"-display-name", "Test Admin",
		"-confirm",
	}, getenv, &usage)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Email != "admin@example.test" || cfg.DisplayName != "Test Admin" || !cfg.Confirmed {
		t.Fatalf("unexpected config: %+v", cfg)
	}
	if cfg.DatabaseURL != "postgres://gfr_dev:pw@127.0.0.1:5432/db" {
		t.Errorf("expected DatabaseURL to come from getenv, got %q", cfg.DatabaseURL)
	}
}

func TestParseFlags_ConfirmDefaultsFalse(t *testing.T) {
	getenv := fakeGetenv(nil)
	var usage bytes.Buffer

	cfg, err := parseFlags([]string{"-email", "admin@example.test", "-display-name", "Test Admin"}, getenv, &usage)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Confirmed {
		t.Error("Confirmed must default to false when -confirm is not passed")
	}
}

// TestParseFlags_RejectsPasswordFlag proves that no -password (or similarly
// named) flag exists: passing one must be a parse error, never a silently
// accepted value, structurally enforcing "never accept passwords through
// command arguments."
func TestParseFlags_RejectsPasswordFlag(t *testing.T) {
	getenv := fakeGetenv(nil)
	var usage bytes.Buffer

	_, err := parseFlags([]string{"-password", "hunter2"}, getenv, &usage)
	if err == nil {
		t.Fatal("expected an error: no -password flag should exist")
	}
	if strings.Contains(err.Error(), "hunter2") {
		t.Error("a rejected -password value must never be echoed back in the parse error")
	}
}

func TestParseFlags_HelpFlagDocumentsRequirements(t *testing.T) {
	getenv := fakeGetenv(nil)
	var usage bytes.Buffer

	_, err := parseFlags([]string{"-h"}, getenv, &usage)
	if !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("expected flag.ErrHelp, got %v", err)
	}
	text := usage.String()
	for _, want := range []string{"confirm", "GFR_DATABASE_URL", "127.0.0.1", "LOCAL DEVELOPMENT ONLY"} {
		if !strings.Contains(text, want) {
			t.Errorf("usage text missing %q:\n%s", want, text)
		}
	}
}

// TestRealReadPassword_NonTerminalStdin verifies the production password
// reader refuses to fall back to visible input when stdin is not an
// interactive terminal (for example, a pipe), rather than silently echoing
// the password.
func TestRealReadPassword_NonTerminalStdin(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer r.Close()
	defer w.Close()

	var out bytes.Buffer
	reader := realReadPassword(r, &out)
	_, err = reader("Password: ")
	if err == nil {
		t.Fatal("expected an error when stdin is not a terminal")
	}
	if !strings.Contains(err.Error(), "terminal") {
		t.Errorf("expected a terminal-related error, got %v", err)
	}
}
