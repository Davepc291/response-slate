package audioanalysis

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"greenwich-fire-responder/backend/internal/httpapi"
	"greenwich-fire-responder/backend/internal/recordings"
)

type testStore struct {
	claims, completions, failures int
	claimErr, errorComplete       error
	eligible                      bool
	outcome                       string
	retryable                     bool
}

func (s *testStore) ClaimAudio(context.Context, string, int) (bool, error) {
	s.claims++
	return s.eligible, s.claimErr
}
func (s *testStore) CompleteAudio(context.Context, string, Result) (string, error) {
	s.completions++
	return s.outcome, s.errorComplete
}
func (s *testStore) FailAudio(context.Context, string, string, bool) error { s.failures++; return nil }

type analyzerFunc func(context.Context, io.ReadSeeker) (Result, error)

func (f analyzerFunc) Analyze(c context.Context, r io.ReadSeeker) (Result, error) { return f(c, r) }

func fixture(t *testing.T) (*os.File, recordings.Recording) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "synthetic.mp3")
	if err := os.WriteFile(path, []byte("synthetic"), 0600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	info, _ := f.Stat()
	return f, recordings.Recording{SourceIdentity: "test", SourcePath: path, SizeBytes: info.Size(), ModifiedAt: info.ModTime()}
}

func TestProcessorOutcomesAndRetries(t *testing.T) {
	for _, tc := range []struct {
		name    string
		err     error
		outcome string
		calls   int
	}{
		{"analyzed", nil, "analyzed", 1}, {"duplicate content", nil, "duplicate-content", 1},
		{"invalid", errInvalid, "", 1}, {"retryable", errTimeout, "", 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, r := fixture(t)
			store := &testStore{eligible: true, outcome: tc.outcome}
			var logs bytes.Buffer
			o := DefaultOptions()
			o.RetryInterval = time.Millisecond
			calls := 0
			p := Processor{o, analyzerFunc(func(context.Context, io.ReadSeeker) (Result, error) { calls++; return Result{}, tc.err }), store, slog.New(slog.NewJSONHandler(&logs, nil))}
			p.Process(context.Background(), r, f)
			if calls != tc.calls || store.claims != tc.calls {
				t.Fatal("wrong attempt count")
			}
			if tc.err == nil && store.completions != 1 {
				t.Fatal("result not persisted")
			}
			if tc.err != nil && store.failures != tc.calls {
				t.Fatal("failure not persisted")
			}
			if tc.err == errInvalid && !strings.Contains(logs.String(), `"outcome":"invalid"`) {
				t.Fatal("invalid outcome missing")
			}
		})
	}
}

func TestProcessorUnavailableDatabaseSafe(t *testing.T) {
	f, r := fixture(t)
	store := &testStore{claimErr: errors.New("postgres://user:secret@private/db")}
	var logs bytes.Buffer
	o := DefaultOptions()
	o.RetryInterval = time.Millisecond
	p := Processor{Options: o, Store: store, Logger: slog.New(slog.NewJSONHandler(&logs, nil))}
	p.Process(context.Background(), r, f)
	if store.claims != 3 || strings.Contains(logs.String(), "secret") || strings.Contains(logs.String(), "postgres://") {
		t.Fatal("unsafe/unbounded failure")
	}
}

type watcherStore struct{}

func (watcherStore) InsertRecording(context.Context, recordings.Recording) (bool, error) {
	return true, nil
}

func TestWatcherAndHTTPShutdownDuringAnalysis(t *testing.T) {
	dir := t.TempDir()
	entered := make(chan struct{})
	store := &testStore{eligible: true}
	p := &Processor{Options: DefaultOptions(), Store: store, Logger: slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Analyzer: analyzerFunc(func(ctx context.Context, _ io.ReadSeeker) (Result, error) {
			close(entered)
			<-ctx.Done()
			return Result{}, errCanceled
		})}
	o := recordings.DefaultOptions()
	o.Directory = dir
	o.PollInterval = 10 * time.Millisecond
	o.StableFor = 20 * time.Millisecond
	w, err := recordings.NewWatcher(o, watcherStore{}, p.Logger, p)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); w.Run(ctx) }()
	path := filepath.Join(dir, "20260905_081609Greenwich_Fairfield_T-NEW_GFD1__TO_57201_FROM_578060.mp3")
	if err := os.WriteFile(path, []byte("synthetic"), 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("analysis did not start")
	}
	server := httptest.NewServer(httpapi.NewHandler(nil))
	response, err := http.Get(server.URL + "/api/health")
	if err != nil {
		server.Close()
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal("analysis broke liveness")
	}
	cancel()
	server.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("analysis blocked shutdown")
	}
	if store.failures != 1 {
		t.Fatal("cancellation state not recorded")
	}
}
