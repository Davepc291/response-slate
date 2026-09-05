package main

// Explicitly opted-in verification only. Normal tests never contact a LAN service.
import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"github.com/jackc/pgx/v5/pgxpool"
	"greenwich-fire-responder/backend/internal/config"
	"greenwich-fire-responder/backend/internal/recordings"
	"greenwich-fire-responder/backend/internal/transcription"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}
func (b *lockedBuffer) String() string { b.mu.Lock(); defer b.mu.Unlock(); return b.b.String() }

type auditTransport struct {
	base     http.RoundTripper
	mu       sync.Mutex
	requests int
	raw      string
}

func (a *auditTransport) CloseIdleConnections() {
	if c, ok := a.base.(interface{ CloseIdleConnections() }); ok {
		c.CloseIdleConnections()
	}
}

func (a *auditTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	a.mu.Lock()
	a.requests++
	a.mu.Unlock()
	response, err := a.base.RoundTrip(r)
	if err != nil {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	response.Body.Close()
	if err != nil {
		return nil, err
	}
	var v struct {
		Text string `json:"text"`
	}
	_ = json.Unmarshal(data, &v)
	a.mu.Lock()
	a.raw = v.Text
	a.mu.Unlock()
	response.Body = io.NopCloser(bytes.NewReader(data))
	return response, nil
}
func (a *auditTransport) snapshot() (int, string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.requests, a.raw
}

func TestControlledLiveTranscription(t *testing.T) {
	if os.Getenv("GFR_LIVE_TRANSCRIPTION_TEST") != "true" {
		t.Skip("explicit local integration opt-in required")
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal("live configuration invalid")
	}
	if !cfg.Transcription.Enabled || !cfg.DatabaseRequired || cfg.HTTPAddr != "127.0.0.1:8080" {
		t.Fatal("live safety configuration invalid")
	}
	source := os.Getenv("GFR_LIVE_SOURCE")
	if source == "" {
		t.Fatal("source must be explicitly supplied")
	}
	before, err := os.Stat(source)
	if err != nil {
		t.Fatal("source unavailable")
	}
	hashFile := func(path string) [32]byte {
		f, err := os.Open(path)
		if err != nil {
			t.Fatal("hash source unavailable")
		}
		defer f.Close()
		h := sha256.New()
		if _, err := io.Copy(h, f); err != nil {
			t.Fatal("hash failed")
		}
		var out [32]byte
		copy(out[:], h.Sum(nil))
		return out
	}
	originalHash := hashFile(source)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		t.Fatal("local database unavailable")
	}
	defer pool.Close()
	if pool.Ping(ctx) != nil {
		t.Fatal("local database ping failed")
	}
	t.Log("PostgreSQL ping: PASS")
	client := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for _, path := range []string{"/health", "/v1/models"} {
		resp, err := client.Get(strings.TrimRight(cfg.Transcription.BaseURL, "/") + path)
		if err != nil {
			t.Fatal("provider preflight failed")
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 65536))
		resp.Body.Close()
		if resp.StatusCode != 200 || path == "/health" && !bytes.Contains(body, []byte(`"ok"`)) || path == "/v1/models" && !bytes.Contains(body, []byte("small.en")) {
			t.Fatal("provider preflight invalid")
		}
	}
	t.Log("Whisper health: HTTP 200; models includes small.en")
	listener, err := net.Listen("tcp", cfg.HTTPAddr)
	if err != nil {
		t.Fatal("port 8080 already occupied")
	}
	listener.Close()
	dir, err := os.MkdirTemp("", "gfr-transcription-live-")
	if err != nil {
		t.Fatal("temporary directory failed")
	}
	first := filepath.Join(dir, filepath.Base(source))
	second := filepath.Join(dir, strings.Replace(filepath.Base(source), "081609", "081610", 1))
	if second == first {
		t.Fatal("live source filename mismatch")
	}
	cfg.Recordings.Directory = dir
	// Cleanup uses exact test paths, never a directory prefix or a pre-existing row.
	defer func() {
		cleanup, c := context.WithTimeout(context.Background(), 10*time.Second)
		defer c()
		for _, path := range []string{second, first} {
			_, e := pool.Exec(cleanup, `DELETE FROM transcription_attempts WHERE transmission_id IN (SELECT id FROM radio_transmissions WHERE source_path=$1 AND source_identity=$2)`, path, recordings.SourceIdentity(path))
			if e != nil {
				t.Error("attempt cleanup failed")
			}
			_, e = pool.Exec(cleanup, `DELETE FROM radio_transmissions WHERE source_path=$1 AND source_identity=$2`, path, recordings.SourceIdentity(path))
			if e != nil {
				t.Error("row cleanup failed")
			}
			if e = os.Remove(path); e != nil && !os.IsNotExist(e) {
				t.Error("copy cleanup failed")
			}
		}
		if e := os.Remove(dir); e != nil {
			t.Error("temporary directory cleanup failed")
		}
		after, e := os.Stat(source)
		if e != nil || after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) || hashFile(source) != originalHash {
			t.Error("original source changed")
		} else {
			t.Log("Original SHA-256, size and modification time unchanged; temporary files and exact test rows removed")
		}
	}()
	transport := &auditTransport{base: http.DefaultTransport.(*http.Transport).Clone()}
	var logs lockedBuffer
	defer func() {
		if t.Failed() {
			safe := logs.String()
			for _, secret := range []string{cfg.DatabaseURL, cfg.Transcription.BearerToken, os.Getenv("GFR_LIVE_PASSWORD")} {
				if secret != "" {
					safe = strings.ReplaceAll(safe, secret, "[redacted]")
				}
			}
			t.Log("Safe worker diagnostics: " + safe)
		}
	}()
	apiCtx, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- runWithConfig(apiCtx, cfg, transport, slog.New(slog.NewJSONHandler(&logs, nil))) }()
	stopped := false
	defer func() {
		if !stopped {
			stop()
			select {
			case <-done:
			case <-time.After(15 * time.Second):
				t.Error("API cleanup timeout")
			}
		}
	}()
	eventually := func(label string, fn func() bool) {
		t.Helper()
		deadline := time.Now().Add(90 * time.Second)
		for time.Now().Before(deadline) {
			if fn() {
				return
			}
			select {
			case <-ctx.Done():
				t.Fatal("live verification deadline")
			case <-time.After(200 * time.Millisecond):
			}
		}
		t.Fatalf("timed out: %s", label)
	}
	eventually("API startup", func() bool {
		return strings.Contains(logs.String(), "startup_snapshot") && func() bool {
			r, e := client.Get("http://" + cfg.HTTPAddr + "/api/health")
			if e != nil {
				return false
			}
			r.Body.Close()
			return r.StatusCode == 200
		}()
	})
	for _, path := range []string{"/api/health", "/api/ready"} {
		r, e := client.Get("http://" + cfg.HTTPAddr + path)
		if e != nil {
			t.Fatal("API endpoint failed")
		}
		b, _ := io.ReadAll(r.Body)
		r.Body.Close()
		if r.StatusCode != 200 || path == "/api/ready" && !bytes.Contains(b, []byte(`"database":"ok"`)) {
			t.Fatal("API status invalid")
		}
		t.Log(path + ": HTTP 200")
	}
	copySource := func(destination string) {
		in, e := os.Open(source)
		if e != nil {
			t.Fatal("source read failed")
		}
		defer in.Close()
		out, e := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			t.Fatal("temporary copy failed")
		}
		_, e = io.Copy(out, in)
		closeErr := out.Close()
		if e != nil || closeErr != nil {
			t.Fatal("temporary copy write failed")
		}
	}
	copySource(first)
	var id int64
	var raw, normalized, provider, model string
	var attempts int
	var finished bool
	eventually("canonical transcription", func() bool {
		var failed bool
		if pool.QueryRow(ctx, `SELECT transcription_status='failed' AND NOT transcription_retryable FROM radio_transmissions WHERE source_identity=$1`, recordings.SourceIdentity(first)).Scan(&failed) == nil && failed {
			t.Fatal("permanent transcription failure; see safe diagnostics")
		}
		return pool.QueryRow(ctx, `SELECT id,transcription_raw_text,transcript,transcription_provider,transcription_model,transcription_attempts,
 transcription_started_at IS NOT NULL AND transcription_finished_at>=transcription_started_at AND transcription_claim IS NULL AND NOT transcription_retryable
 FROM radio_transmissions WHERE source_identity=$1 AND processing_status='completed' AND transcription_status='completed' AND audio_duplicate_of IS NULL`, recordings.SourceIdentity(first)).Scan(&id, &raw, &normalized, &provider, &model, &attempts, &finished) == nil
	})
	count, response := transport.snapshot()
	if count != 1 || raw != response || provider != transcription.ProviderID || model != "small.en" || attempts != 1 || !finished || normalized != strings.Join(strings.Fields(raw), " ") {
		t.Fatal("live evidence or lifecycle mismatch")
	}
	t.Logf("Canonical transcription: one HTTP request; exact raw response %q; provider=%s model=%s attempts=%d; timestamps/claim/retry fields PASS", raw, provider, model, attempts)
	// Removing and replacing only our temporary copy forces a fresh observation.
	if err := os.Remove(first); err != nil {
		t.Fatal("temporary re-observation removal failed")
	}
	time.Sleep(2 * cfg.Recordings.PollInterval)
	copySource(first)
	copySource(second)
	eventually("duplicate alias", func() bool {
		var ok bool
		return pool.QueryRow(ctx, `SELECT processing_status='skipped' AND transcription_status='skipped' AND audio_duplicate_of=$2 AND transcription_attempts=0 FROM radio_transmissions WHERE source_identity=$1`, recordings.SourceIdentity(second), id).Scan(&ok) == nil && ok
	})
	eventually("path re-observation", func() bool { return strings.Contains(logs.String(), "source_identity_exists") })
	time.Sleep(2 * time.Second)
	count, _ = transport.snapshot()
	if count != 1 {
		t.Fatal("duplicate caused an extra request")
	}
	var rows int
	if pool.QueryRow(ctx, `SELECT count(*) FROM radio_transmissions WHERE source_path=$1 OR source_path=$2`, first, second).Scan(&rows) != nil || rows != 2 {
		t.Fatal("unexpected test row count")
	}
	t.Log("Re-observation and duplicate content: request count remains 1; two metadata rows, one canonical and one skipped alias")
	stop()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal("API shutdown failed")
		}
		stopped = true
	case <-time.After(15 * time.Second):
		t.Fatal("API shutdown timeout")
	}
	listener, err = net.Listen("tcp", cfg.HTTPAddr)
	if err != nil {
		t.Fatal("port 8080 still occupied")
	}
	listener.Close()
	t.Log("API graceful shutdown: PASS; port 8080 clear")
	for _, secret := range []string{cfg.DatabaseURL, cfg.Transcription.BearerToken, os.Getenv("GFR_LIVE_PASSWORD")} {
		if secret != "" && strings.Contains(logs.String(), secret) {
			t.Fatal("credential detected in structured logs")
		}
	}
	if !strings.Contains(logs.String(), `"outcome":"completed"`) || !strings.Contains(logs.String(), "duplicate-content") {
		t.Fatal("missing safe completion or duplicate logs")
	}
	t.Log("Structured completion and duplicate logs: PASS; no database URL, password or bearer token")
}
