package config_test

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"greenwich-fire-responder/backend/internal/audioanalysis"
	"greenwich-fire-responder/backend/internal/config"
	"greenwich-fire-responder/backend/internal/operations"
	"greenwich-fire-responder/backend/internal/recordings"
	"greenwich-fire-responder/backend/internal/transcription"
)

func TestLoad(t *testing.T) {
	clearRecordingEnv(t)
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
			tc.want.Recordings = recordings.DefaultOptions()
			tc.want.Audio = audioanalysis.DefaultOptions()
			tc.want.Transcription = transcription.DefaultOptions()
			tc.want.Operations = operations.DefaultOptions()
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
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatal("configuration does not match expected values")
			}
		})
	}
}

func TestLoadDoesNotReadDotEnv(t *testing.T) {
	clearRecordingEnv(t)
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

func clearRecordingEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{"QUEUE_DEPTH", "OLDEST_AGE", "REQUEST_DURATION", "CONSECUTIVE_FAILURES"} {
		t.Setenv("GFR_MONITOR_WARN_"+name, "")
	}
	for _, name := range []string{"ENABLED", "BASE_URL", "MODEL", "LANGUAGE", "BEARER_TOKEN", "PROMPT", "WORD_BOOST", "TIMEOUT", "RETRY_DELAY", "MAX_RESPONSE_BYTES", "MAX_AUDIO_BYTES", "MAX_ATTEMPTS"} {
		t.Setenv("GFR_TRANSCRIPTION_"+name, "")
	}
	for _, name := range []string{"GFR_RECORDINGS_DIR", "GFR_RECORDING_TIMEZONE", "GFR_RECORDING_POLL_INTERVAL",
		"GFR_RECORDING_STABLE_FOR", "GFR_RECORDING_MAX_WAIT", "GFR_RECORDING_RETRY_INTERVAL", "GFR_RECORDING_MAX_ATTEMPTS",
		"GFR_FFPROBE_PATH", "GFR_FFMPEG_PATH", "GFR_AUDIO_TIMEOUT", "GFR_AUDIO_MAX_DURATION", "GFR_AUDIO_MAX_ATTEMPTS", "GFR_AUDIO_RETRY_INTERVAL"} {
		t.Setenv(name, "")
	}
	for _, name := range []string{"GFR_AUTH_ENABLED", "GFR_AUTH_SESSION_IDLE_TIMEOUT", "GFR_AUTH_SESSION_MAX_LIFETIME",
		"GFR_AUTH_PASSWORD_RESET_TTL", "GFR_AUTH_SESSION_SECRET", "GFR_AUTH_ALLOWED_ORIGINS", "GFR_AUTH_TRUSTED_PROXIES",
		"GFR_AUTH_RATE_LIMIT_PER_ACCOUNT", "GFR_AUTH_RATE_LIMIT_PER_IP", "GFR_AUTH_RATE_LIMIT_INVITATION",
		"GFR_AUTH_RATE_LIMIT_PASSWORD_RESET", "GFR_AUTH_RATE_LIMIT_PASSWORD_RESET_COMPLETE", "GFR_AUTH_BREACH_CHECK_ENABLED"} {
		t.Setenv(name, "")
	}
}

func TestAudioConfiguration(t *testing.T) {
	clearRecordingEnv(t)
	t.Setenv("GFR_DATABASE_REQUIRED", "false")
	t.Setenv("GFR_FFPROBE_PATH", `C:\tools\ffprobe.exe`)
	t.Setenv("GFR_FFMPEG_PATH", `C:\tools\ffmpeg.exe`)
	t.Setenv("GFR_AUDIO_TIMEOUT", "20s")
	t.Setenv("GFR_AUDIO_MAX_DURATION", "5m")
	t.Setenv("GFR_AUDIO_MAX_ATTEMPTS", "2")
	t.Setenv("GFR_AUDIO_RETRY_INTERVAL", "1s")
	cfg, err := config.Load()
	if err != nil || cfg.Audio.Timeout != 20*time.Second || cfg.Audio.MaxDuration != 5*time.Minute || cfg.Audio.MaxAttempts != 2 || cfg.Audio.FFprobePath != `C:\tools\ffprobe.exe` {
		t.Fatal("audio overrides not loaded")
	}
	for name, value := range map[string]string{"GFR_AUDIO_TIMEOUT": "0s", "GFR_AUDIO_MAX_DURATION": "2h", "GFR_AUDIO_MAX_ATTEMPTS": "0", "GFR_AUDIO_RETRY_INTERVAL": "bad-secret"} {
		t.Run(name, func(t *testing.T) {
			t.Setenv(name, value)
			if _, err := config.Load(); err == nil {
				t.Fatal("invalid option accepted")
			}
		})
	}
}

func TestRecordingConfiguration(t *testing.T) {
	clearRecordingEnv(t)
	t.Setenv("GFR_DATABASE_REQUIRED", "false")
	t.Setenv("GFR_RECORDINGS_DIR", `C:\Users\User\SDRTrunk\recordings`)
	t.Setenv("GFR_RECORDING_TIMEZONE", "UTC")
	t.Setenv("GFR_RECORDING_STABLE_FOR", "4s")
	t.Setenv("GFR_RECORDING_MAX_ATTEMPTS", "4")
	cfg, err := config.Load()
	if err != nil || cfg.Recordings.Directory != `C:\Users\User\SDRTrunk\recordings` ||
		cfg.Recordings.Timezone != "UTC" || cfg.Recordings.StableFor != 4*time.Second || cfg.Recordings.MaxAttempts != 4 {
		t.Fatal("recording overrides were not loaded")
	}
	for name, value := range map[string]string{
		"GFR_RECORDING_TIMEZONE": "secret-invalid-zone", "GFR_RECORDING_POLL_INTERVAL": "0s",
		"GFR_RECORDING_STABLE_FOR": "bad-secret", "GFR_RECORDING_MAX_WAIT": "1s",
		"GFR_RECORDING_RETRY_INTERVAL": "-1s", "GFR_RECORDING_MAX_ATTEMPTS": "1000",
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv(name, value)
			if _, err := config.Load(); err == nil {
				t.Fatal("invalid watcher setting was accepted")
			}
		})
	}
}
