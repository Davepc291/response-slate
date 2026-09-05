package transcription

import (
	"bytes"
	"context"
	"errors"
	"greenwich-fire-responder/backend/internal/httpapi"
	"greenwich-fire-responder/backend/internal/recordings"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fakeStore struct {
	job     Job
	failure *Failure
	result  Result
	saved   bool
	err     error
}

func (s *fakeStore) NextTranscription(context.Context, string, Options) (Job, bool, error) {
	return s.job, false, s.err
}
func (s *fakeStore) FinishTranscription(_ context.Context, _ Job, r Result, f *Failure, _ time.Duration) (bool, error) {
	s.result = r
	s.failure = f
	s.saved = true
	return s.err == nil, s.err
}

type providerFunc func(context.Context, *os.File) (Result, error)

func (f providerFunc) Transcribe(ctx context.Context, input *os.File) (Result, error) {
	return f(ctx, input)
}
func TestWorkerFailuresAndPreservation(t *testing.T) {
	for _, tc := range []struct {
		name    string
		err     error
		attempt int
		retry   bool
	}{{"success", nil, 1, false}, {"retry", Failure{"provider_unavailable", true}, 1, true}, {"exhausted", Failure{"provider_unavailable", true}, 3, false}, {"permanent", Failure{"invalid_response", false}, 1, false}} {
		t.Run(tc.name, func(t *testing.T) {
			f := audioFile(t, 10)
			info, _ := f.Stat()
			j := Job{ID: 1, Attempt: tc.attempt, Recording: recordings.Recording{SourceIdentity: recordings.SourceIdentity(f.Name()), SourcePath: f.Name(), SizeBytes: info.Size(), ModifiedAt: info.ModTime()}}
			store := &fakeStore{}
			var logs bytes.Buffer
			w := Worker{Options: DefaultOptions(), Directory: filepath.Dir(f.Name()), Store: store, Logger: slog.New(slog.NewJSONHandler(&logs, nil)), Provider: providerFunc(func(context.Context, *os.File) (Result, error) { return Result{"raw words", "raw words"}, tc.err })}
			w.process(context.Background(), j)
			if !store.saved {
				t.Fatal("no persistence")
			}
			if tc.err == nil {
				if store.result.RawText != "raw words" {
					t.Fatal("raw lost")
				}
			} else if store.failure == nil || store.failure.Retryable != tc.retry || store.result.RawText != "" {
				t.Fatal("retry or result incorrect")
			}
		})
	}
}
func TestShutdownDuringRequest(t *testing.T) {
	f := audioFile(t, 10)
	info, _ := f.Stat()
	started := make(chan struct{})
	p := provider(t, func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		close(started)
		<-r.Context().Done()
	}, nil)
	store := &fakeStore{}
	var logs bytes.Buffer
	w := Worker{Options: DefaultOptions(), Directory: filepath.Dir(f.Name()), Store: store, Provider: p, Logger: slog.New(slog.NewJSONHandler(&logs, nil))}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		w.process(ctx, Job{ID: 1, Attempt: 1, Recording: recordings.Recording{SourceIdentity: recordings.SourceIdentity(f.Name()), SourcePath: f.Name(), SizeBytes: info.Size(), ModifiedAt: info.ModTime()}})
	}()
	<-started
	api := httptest.NewServer(httpapi.NewHandler(nil))
	resp, err := http.Get(api.URL + "/api/health")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatal("liveness depends on transcription")
	}
	cancel()
	api.Close()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("shutdown blocked")
	}
	if store.failure == nil || store.failure.Code != "canceled" || !strings.Contains(logs.String(), "canceled") {
		t.Fatal("cancellation not persisted")
	}
}
func TestDatabaseUnavailableAndDisabled(t *testing.T) {
	var logs bytes.Buffer
	store := &fakeStore{err: errors.New("fixture-password")}
	o := DefaultOptions()
	o.Enabled = true
	w := Worker{Options: o, Directory: t.TempDir(), Store: store, Logger: slog.New(slog.NewJSONHandler(&logs, nil))}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	w.Run(ctx)
	if strings.Contains(logs.String(), "fixture-password") || !strings.Contains(logs.String(), "database_unavailable") {
		t.Fatal("unsafe logs")
	}
	w.Options.Enabled = false
	w.Run(context.Background())
}
