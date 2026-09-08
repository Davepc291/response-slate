package main

import (
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExplicitDatasetAndSafeErrors(t *testing.T) {
	t.Setenv("GFR_DATABASE_URL", "SYNTHETIC-SECRET")
	for _, args := range [][]string{nil, {"--dataset"}, {"--unknown=SYNTHETIC-SECRET"}, {"--dataset", "relative"}, {"--dataset", "relative", "extra"}} {
		var out, errs bytes.Buffer
		if run(args, &out, &errs) == 0 || out.Len() != 0 || strings.Contains(errs.String(), "SYNTHETIC-SECRET") {
			t.Fatal("unsafe invocation")
		}
	}
}

func TestSuccessfulCLIAndOutputFailure(t *testing.T) {
	fp := "pcm-s16le-16000-mono-v1:sha256:" + strings.Repeat("a", 64)
	id := md5.Sum([]byte(fp))
	data := fmt.Sprintf(`{"dataset_id":"gfr-audio-v1:%s","audio_fingerprint":"%s","transcription_attempt_id":1,"channel":"CH1A","tgid":57201,"duration_ms":1,"raw_model_transcript":"SYNTHETIC","human_reference_transcript":"SYNTHETIC","model":"small.en","split":"train","reviewed_at":"2026-01-01T00:00:00Z"}`, hex.EncodeToString(id[:]), fp)
	p := filepath.Join(t.TempDir(), "synthetic.jsonl")
	if e := os.WriteFile(p, []byte(data), 0600); e != nil {
		t.Fatal(e)
	}
	var out, errs bytes.Buffer
	if run([]string{"--dataset", p}, &out, &errs) != 0 || errs.Len() != 0 || !strings.Contains(out.String(), `"exact_match_rate": 1`) || strings.Contains(out.String(), "SYNTHETIC") {
		t.Fatal("CLI report", errs.String())
	}
	if run([]string{"--dataset", p}, brokenWriter{}, &errs) != 1 {
		t.Fatal("write failure ignored")
	}
}

type brokenWriter struct{}

func (brokenWriter) Write([]byte) (int, error) { return 0, fmt.Errorf("synthetic writer failure") }
