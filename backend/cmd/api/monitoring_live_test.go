package main

// This opt-in test uses only the managed loopback provider and an explicit source.
// Ordinary go test runs neither this test nor any external service requests.
import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"greenwich-fire-responder/backend/internal/audioanalysis"
	"greenwich-fire-responder/backend/internal/config"
	"greenwich-fire-responder/backend/internal/operations"
	"greenwich-fire-responder/backend/internal/recordings"
)

func TestControlledLiveMonitoring(t *testing.T) {
	if os.Getenv("GFR_LIVE_MONITORING_TEST") != "true" {
		t.Skip("explicit managed local monitoring opt-in required")
	}
	cfg, err := config.Load()
	if err != nil || !cfg.DatabaseRequired || !cfg.Transcription.Enabled || cfg.Transcription.BaseURL != "http://127.0.0.1:8001" || cfg.HTTPAddr != "127.0.0.1:8080" {
		t.Fatal("unsafe live configuration")
	}
	source := os.Getenv("GFR_LIVE_SOURCE")
	before, err := os.Lstat(source)
	if err != nil || !before.Mode().IsRegular() {
		t.Fatal("source unavailable or not regular")
	}
	hashFile := func(path string) [32]byte {
		f, e := os.Open(path)
		if e != nil {
			t.Fatal("source hash open failed")
		}
		defer f.Close()
		h := sha256.New()
		if _, e = io.Copy(h, f); e != nil {
			t.Fatal("source hash failed")
		}
		var sum [32]byte
		copy(sum[:], h.Sum(nil))
		return sum
	}
	originalHash := hashFile(source)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		t.Fatal("database configuration failed")
	}
	defer pool.Close()
	if pool.Ping(ctx) != nil {
		t.Fatal("database unavailable")
	}
	f, err := os.Open(source)
	if err != nil {
		t.Fatal("source open failed")
	}
	analysis, err := (audioanalysis.Analyzer{Options: cfg.Audio, Tools: audioanalysis.ProcessTools{}}).Analyze(ctx, f)
	f.Close()
	if err != nil {
		t.Fatal("source not valid audio")
	}
	var represented int
	if pool.QueryRow(ctx, `SELECT count(*) FROM radio_transmissions WHERE audio_fingerprint=$1`, analysis.Fingerprint).Scan(&represented) != nil || represented != 0 {
		t.Fatal("source already represented or fingerprint query failed")
	}
	client := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	checkWhisper := func() {
		r, e := client.Get("http://127.0.0.1:8001")
		if e != nil {
			t.Fatal("local Whisper unavailable")
		}
		r.Body.Close()
		if r.StatusCode != 200 {
			t.Fatal("local Whisper not healthy")
		}
	}
	checkWhisper()
	l, err := net.Listen("tcp", cfg.HTTPAddr)
	if err != nil {
		t.Fatal("8080 occupied")
	}
	l.Close()
	var baselineRows, baselineAttempts, baselineDecisions int
	if pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM radio_transmissions),(SELECT count(*) FROM transcription_attempts),(SELECT count(*) FROM unit_status_decisions)`).Scan(&baselineRows, &baselineAttempts, &baselineDecisions) != nil || baselineDecisions != 0 {
		t.Fatal("unexpected database baseline")
	}
	dir, err := os.MkdirTemp("", "gfr-monitor-live-")
	if err != nil {
		t.Fatal("temporary directory unavailable")
	}
	path := filepath.Join(dir, filepath.Base(source))
	cfg.Recordings.Directory = dir
	defer func() {
		cleanup, c := context.WithTimeout(context.Background(), 10*time.Second)
		defer c()
		tx, e := pool.Begin(cleanup)
		if e != nil {
			t.Error("cleanup transaction failed")
			return
		}
		defer tx.Rollback(cleanup)
		if _, e = tx.Exec(cleanup, `DELETE FROM transcription_attempts WHERE transmission_id IN (SELECT id FROM radio_transmissions WHERE source_path=$1 AND source_identity=$2)`, path, recordings.SourceIdentity(path)); e != nil {
			t.Error("attempt cleanup failed")
			return
		}
		if _, e = tx.Exec(cleanup, `DELETE FROM radio_transmissions WHERE source_path=$1 AND source_identity=$2`, path, recordings.SourceIdentity(path)); e != nil {
			t.Error("record cleanup failed")
			return
		}
		if tx.Commit(cleanup) != nil {
			t.Error("cleanup commit failed")
			return
		}
		if e = os.Remove(path); e != nil && !os.IsNotExist(e) {
			t.Error("temporary copy cleanup failed")
		}
		if os.Remove(dir) != nil {
			t.Error("temporary directory cleanup failed")
		}
		after, e := os.Stat(source)
		if e != nil || after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) || hashFile(source) != originalHash {
			t.Error("original changed")
		}
		var rows, attempts, decisions int
		if pool.QueryRow(cleanup, `SELECT (SELECT count(*) FROM radio_transmissions),(SELECT count(*) FROM transcription_attempts),(SELECT count(*) FROM unit_status_decisions)`).Scan(&rows, &attempts, &decisions) != nil || rows != baselineRows || attempts != baselineAttempts || decisions != baselineDecisions {
			t.Error("database baseline not restored")
		}
		t.Logf("cleanup: rows=%d attempts=%d decisions=%d; source unchanged: size=%d mtime=%s SHA256=%x", rows, attempts, decisions, before.Size(), before.ModTime().UTC().Format(time.RFC3339Nano), originalHash)
	}()
	var logs lockedBuffer
	transport := &auditTransport{base: http.DefaultTransport.(*http.Transport).Clone()}
	apiCtx, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- runWithConfig(apiCtx, cfg, transport, slog.New(slog.NewJSONHandler(&logs, nil))) }()
	defer func() {
		stop()
		select {
		case e := <-done:
			if e != nil {
				t.Error("API shutdown failed")
			}
		case <-time.After(15 * time.Second):
			t.Error("API shutdown timed out")
		}
		listener, e := net.Listen("tcp", cfg.HTTPAddr)
		if e != nil {
			t.Error("8080 not clear")
		} else {
			listener.Close()
		}
		checkWhisper()
		for _, secret := range []string{cfg.DatabaseURL, cfg.Transcription.BearerToken, os.Getenv("GFR_LIVE_PASSWORD")} {
			if secret != "" && strings.Contains(logs.String(), secret) {
				t.Error("credential in logs")
			}
		}
		t.Log("API gracefully stopped; port 8080 clear; managed Whisper HTTP 200; credential scan clear")
	}()
	eventually := func(label string, fn func() bool) {
		t.Helper()
		deadline := time.Now().Add(90 * time.Second)
		for time.Now().Before(deadline) {
			if fn() {
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
		t.Fatal("timeout: " + label)
	}
	eventually("empty snapshot", func() bool { return strings.Contains(logs.String(), `"reason":"startup_snapshot","count":0`) })
	for _, route := range []string{"health", "ready"} {
		r, e := client.Get("http://" + cfg.HTTPAddr + "/api/" + route)
		if e != nil {
			t.Fatal("API unavailable")
		}
		r.Body.Close()
		if r.StatusCode != 200 {
			t.Fatal("health/readiness not 200")
		}
	}
	readSnapshot := func() operations.Snapshot {
		r, e := client.Get("http://" + cfg.HTTPAddr + "/api/operations/transcription")
		if e != nil {
			t.Fatal("monitor endpoint failed")
		}
		defer r.Body.Close()
		raw, e := io.ReadAll(io.LimitReader(r.Body, 32769))
		if e != nil || len(raw) > 32768 || r.StatusCode != 200 {
			t.Fatal("invalid monitoring response")
		}
		for _, private := range []string{path, filepath.Base(source), cfg.DatabaseURL, cfg.Transcription.BaseURL} {
			if strings.Contains(string(raw), private) {
				t.Fatal("monitoring leak")
			}
		}
		var s operations.Snapshot
		if json.Unmarshal(raw, &s) != nil || s.Metrics == nil {
			t.Fatal("monitor JSON invalid")
		}
		return s
	}
	printSnapshot := func(label string, s operations.Snapshot) { b, _ := json.Marshal(s); t.Log(label + ": " + string(b)) }
	initial := readSnapshot()
	if initial.Metrics.Waiting != 0 || initial.Metrics.Attempts != 0 {
		t.Fatal("initial scope not empty")
	}
	printSnapshot("before", initial)
	copySource := func() {
		f, e := os.Open(source)
		if e != nil {
			t.Fatal("source unavailable")
		}
		defer f.Close()
		out, e := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			t.Fatal("copy failed")
		}
		_, e = io.Copy(out, f)
		closeErr := out.Close()
		if e != nil || closeErr != nil {
			t.Fatal("copy failed")
		}
	}
	copySource()
	sawQueue, sawRequest := false, false
	eventually("completion", func() bool {
		s := readSnapshot()
		if s.Metrics.Waiting > 0 && !sawQueue {
			printSnapshot("queued", s)
			sawQueue = true
		}
		if s.Worker == "requesting" && !sawRequest {
			printSnapshot("during", s)
			sawRequest = true
		}
		return s.Metrics.Completed == 1
	})
	final := readSnapshot()
	printSnapshot("after", final)
	if final.Metrics.Attempts != 1 || final.Metrics.Recent.ProviderRequest.Samples != 1 || final.Metrics.Recent.ClaimToRequest.Samples != 1 {
		t.Fatal("timing evidence missing")
	}
	if !sawQueue {
		t.Log("eligible waiting interval not observable at 100ms polling")
	}
	if !sawRequest {
		t.Log("active request not observable at 100ms polling")
	}
	if os.Remove(path) != nil {
		t.Fatal("temporary re-observation removal failed")
	}
	time.Sleep(3 * time.Second)
	copySource()
	eventually("duplicate observation", func() bool { return strings.Contains(logs.String(), `"reason":"source_identity_exists"`) })
	time.Sleep(2 * time.Second)
	requests, _ := transport.snapshot()
	final = readSnapshot()
	var rows, canonical, decisions int
	if pool.QueryRow(ctx, `SELECT count(*),count(*) FILTER(WHERE audio_fingerprint=$2 AND audio_duplicate_of IS NULL) FROM radio_transmissions WHERE source_path=$1`, path, analysis.Fingerprint).Scan(&rows, &canonical) != nil || rows != 1 || canonical != 1 {
		t.Fatal("canonical count mismatch")
	}
	if pool.QueryRow(ctx, `SELECT count(*) FROM unit_status_decisions`).Scan(&decisions) != nil || decisions != 0 {
		t.Fatal("unexpected decisions")
	}
	if requests != 1 || final.Metrics.Attempts != 1 {
		t.Fatal("duplicate request")
	}
	t.Log("health=200 ready=200; canonical rows=1; provider requests=1; re-observation idempotent; decisions=0; no classifier/incident/WebSocket/CAD components started")
}
