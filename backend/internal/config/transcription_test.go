package config_test

import (
	"greenwich-fire-responder/backend/internal/config"
	"greenwich-fire-responder/backend/internal/transcription"
	"testing"
	"time"
)

func TestTranscriptionConfiguration(t *testing.T) {
	clearRecordingEnv(t)
	t.Setenv("GFR_DATABASE_REQUIRED", "false")
	c, err := config.Load()
	if err != nil || c.Transcription != transcription.DefaultOptions() {
		t.Fatal("defaults")
	}
	t.Setenv("GFR_TRANSCRIPTION_ENABLED", "true")
	if _, err := config.Load(); err == nil {
		t.Fatal("enabled without URL")
	}
	t.Setenv("GFR_TRANSCRIPTION_BASE_URL", "https://speech.example.invalid")
	t.Setenv("GFR_TRANSCRIPTION_MODEL", "medium.en")
	t.Setenv("GFR_TRANSCRIPTION_TIMEOUT", "30s")
	t.Setenv("GFR_TRANSCRIPTION_MAX_ATTEMPTS", "2")
	t.Setenv("GFR_TRANSCRIPTION_BEARER_TOKEN", "fixture-token")
	c, err = config.Load()
	if err != nil || c.Transcription.Timeout != 30*time.Second || c.Transcription.Model != "medium.en" || c.Transcription.MaxAttempts != 2 || c.Transcription.BearerToken != "fixture-token" {
		t.Fatal("overrides")
	}
	for key, value := range map[string]string{"ENABLED": "not-bool", "BASE_URL": "http://user:secret@example.invalid", "TIMEOUT": "0s", "RETRY_DELAY": "2h", "MAX_ATTEMPTS": "6", "MAX_RESPONSE_BYTES": "0", "MAX_AUDIO_BYTES": "99999999999", "PROMPT": "unsafe\ncontrol", "BEARER_TOKEN": "unsafe\r\nheader"} {
		t.Run(key, func(t *testing.T) {
			t.Setenv("GFR_TRANSCRIPTION_"+key, value)
			if _, err := config.Load(); err == nil || err.Error() != "invalid transcription configuration" {
				t.Fatal("unsafe validation")
			}
		})
	}
}
