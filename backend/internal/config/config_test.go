package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"greenwich-fire-responder/backend/internal/config"
)

func TestLoad(t *testing.T) {
	for _, tc := range []struct {
		name, addr, url, required string
		want                      config.Config
		wantErr                   string
	}{
		{name: "defaults", want: config.Config{HTTPAddr: "127.0.0.1:8080"}},
		{name: "overrides", addr: ":8080", url: "postgres://example.invalid/dev", required: "true",
			want: config.Config{HTTPAddr: ":8080", DatabaseURL: "postgres://example.invalid/dev", DatabaseRequired: true}},
		{name: "optional without URL", required: "false", want: config.Config{HTTPAddr: "127.0.0.1:8080"}},
		{name: "required without URL", required: "true", wantErr: "GFR_DATABASE_URL is required when GFR_DATABASE_REQUIRED is true"},
		{name: "required with blank URL", url: " \t ", required: "true", wantErr: "GFR_DATABASE_URL is required when GFR_DATABASE_REQUIRED is true"},
		{name: "invalid boolean is safe", required: "secret-value", wantErr: "GFR_DATABASE_REQUIRED must be a boolean"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("GFR_HTTP_ADDR", tc.addr)
			t.Setenv("GFR_DATABASE_URL", tc.url)
			t.Setenv("GFR_DATABASE_REQUIRED", tc.required)
			got, err := config.Load()
			if tc.wantErr != "" {
				if err == nil || err.Error() != tc.wantErr {
					t.Fatalf("error = %v, want safe validation error", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatal("configuration does not match expected values")
			}
		})
	}
}

func TestLoadDoesNotReadDotEnv(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("GFR_DATABASE_REQUIRED=true\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	t.Setenv("GFR_HTTP_ADDR", "")
	t.Setenv("GFR_DATABASE_URL", "")
	t.Setenv("GFR_DATABASE_REQUIRED", "")
	cfg, err := config.Load()
	if err != nil || cfg.DatabaseRequired || cfg.DatabaseURL != "" {
		t.Fatal("Load must use only the process environment")
	}
}
