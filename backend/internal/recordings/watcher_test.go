package recordings

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type memoryStore struct {
	rows  map[string]Recording
	calls int
	err   error
	write func(context.Context, Recording) (bool, error)
}

func (s *memoryStore) InsertRecording(ctx context.Context, r Recording) (bool, error) {
	s.calls++
	if s.write != nil {
		return s.write(ctx, r)
	}
	if s.err != nil {
		return false, s.err
	}
	if s.rows == nil {
		s.rows = make(map[string]Recording)
	}
	if _, exists := s.rows[r.SourceIdentity]; exists {
		return false, nil
	}
	s.rows[r.SourceIdentity] = r
	return true, nil
}

func newTestWatcher(t *testing.T, dir string, store Store) (*Watcher, *bytes.Buffer) {
	t.Helper()
	options := DefaultOptions()
	options.Directory = dir
	var logs bytes.Buffer
	w, err := NewWatcher(options, store, slog.New(slog.NewJSONHandler(&logs, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.root.Close() })
	return w, &logs
}

func writeFixture(t *testing.T, dir, name, data string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestStabilityAndMetadataOnly(t *testing.T) {
	for _, change := range []string{"size", "mtime"} {
		t.Run(change, func(t *testing.T) {
			dir := t.TempDir()
			store := &memoryStore{}
			w, _ := newTestWatcher(t, dir, store)
			path := writeFixture(t, dir, nativeExample, "synthetic")
			now := time.Now()
			w.scan(context.Background(), now)
			w.scan(context.Background(), now.Add(2*time.Second))
			if store.calls != 0 {
				t.Fatal("accepted before stability window")
			}
			if change == "size" {
				writeFixture(t, dir, nativeExample, "synthetic-growing")
			} else {
				if err := os.Chtimes(path, now, now.Add(time.Minute)); err != nil {
					t.Fatal(err)
				}
			}
			w.scan(context.Background(), now.Add(3*time.Second))
			w.scan(context.Background(), now.Add(5*time.Second))
			if store.calls != 0 {
				t.Fatal("change did not reset stability")
			}
			before, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			w.scan(context.Background(), now.Add(6*time.Second))
			w.scan(context.Background(), now.Add(7*time.Second))
			if store.calls != 1 || len(store.rows) != 1 {
				t.Fatal("stable file not ingested exactly once")
			}
			r := store.rows[SourceIdentity(path)]
			if r.SourcePath != path || r.RID != 578060 || r.SizeBytes != before.Size() || !r.ModifiedAt.Equal(before.ModTime()) {
				t.Fatal("incorrect metadata")
			}
			after, _ := os.Stat(path)
			if before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
				t.Fatal("source changed")
			}
		})
	}
}

func TestStartupSnapshotNoReplay(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, nativeExample, "historical")
	store := &memoryStore{}
	w, _ := newTestWatcher(t, dir, store)
	writeFixture(t, dir, nativeExample, "historical-modified-after-start")
	now := time.Now()
	w.scan(context.Background(), now)
	w.scan(context.Background(), now.Add(time.Minute))
	if store.calls != 0 {
		t.Fatal("historical file replayed")
	}
	newName := strings.Replace(nativeExample, "081609", "081610", 1)
	writeFixture(t, dir, newName, "new")
	w.scan(context.Background(), now.Add(time.Minute))
	w.scan(context.Background(), now.Add(time.Minute+3*time.Second))
	if store.calls != 1 {
		t.Fatal("new file was not reconciled")
	}
}

func TestDuplicateObservation(t *testing.T) {
	dir := t.TempDir()
	store := &memoryStore{}
	w, logs := newTestWatcher(t, dir, store)
	path := writeFixture(t, dir, nativeExample, "synthetic")
	now := time.Now()
	w.scan(context.Background(), now)
	w.scan(context.Background(), now.Add(3*time.Second))
	// Simulate another observer rediscovering a path already in PostgreSQL.
	delete(w.seen, nativeExample)
	w.scan(context.Background(), now.Add(4*time.Second))
	w.scan(context.Background(), now.Add(7*time.Second))
	if store.calls != 2 || len(store.rows) != 1 || store.rows[SourceIdentity(path)].RID != 578060 {
		t.Fatal("duplicate was not idempotent")
	}
	if !strings.Contains(logs.String(), `"outcome":"duplicate"`) {
		t.Fatal("duplicate not logged")
	}
}

func TestDatabaseFailureBoundedAndSafe(t *testing.T) {
	dir := t.TempDir()
	store := &memoryStore{err: errors.New("postgres://user:secret@internal/db")}
	w, logs := newTestWatcher(t, dir, store)
	writeFixture(t, dir, nativeExample, "synthetic")
	now := time.Now()
	for _, seconds := range []int{0, 3, 4, 8, 13, 18, 120, 180} {
		w.scan(context.Background(), now.Add(time.Duration(seconds)*time.Second))
	}
	if store.calls != 3 {
		t.Fatalf("attempts = %d, want 3", store.calls)
	}
	if strings.Contains(logs.String(), "secret") || strings.Contains(logs.String(), "postgres://") || !strings.Contains(logs.String(), "retry_limit_exceeded") {
		t.Fatal("unsafe or missing failure log")
	}
}

func TestDatabaseRecoveryWithinRetryWindow(t *testing.T) {
	dir := t.TempDir()
	store := &memoryStore{err: errors.New("unavailable")}
	w, _ := newTestWatcher(t, dir, store)
	writeFixture(t, dir, nativeExample, "synthetic")
	now := time.Now()
	w.scan(context.Background(), now)
	w.scan(context.Background(), now.Add(3*time.Second))
	store.err = nil
	w.scan(context.Background(), now.Add(8*time.Second))
	if store.calls != 2 || len(store.rows) != 1 {
		t.Fatal("retry did not recover")
	}
}

func TestEmptyAndUnstableDeadline(t *testing.T) {
	for _, data := range []string{"", "growing"} {
		dir := t.TempDir()
		store := &memoryStore{}
		w, logs := newTestWatcher(t, dir, store)
		path := writeFixture(t, dir, nativeExample, data)
		now := time.Now()
		for i := 0; i <= 120; i++ {
			if data != "" {
				if err := os.Chtimes(path, now, now.Add(time.Duration(i)*time.Second)); err != nil {
					t.Fatal(err)
				}
			}
			w.scan(context.Background(), now.Add(time.Duration(i)*time.Second))
		}
		if store.calls != 0 || !strings.Contains(logs.String(), "observation_deadline_exceeded") {
			t.Fatal("unstable file not bounded")
		}
	}
}

func TestRejectDirectoriesAndUnsupportedFiles(t *testing.T) {
	dir := t.TempDir()
	store := &memoryStore{}
	w, logs := newTestWatcher(t, dir, store)
	if err := os.Mkdir(filepath.Join(dir, nativeExample), 0700); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(dir, nativeExample), nativeExample, "nested")
	writeFixture(t, dir, "unsupported.txt", "synthetic")
	writeFixture(t, dir, "TEST_"+nativeExample, "synthetic")
	now := time.Now()
	w.scan(context.Background(), now)
	w.scan(context.Background(), now.Add(time.Minute))
	if store.calls != 0 || !strings.Contains(logs.String(), "not_regular_file") {
		t.Fatal("unsafe entry accepted")
	}
}

func TestSymlinksRejected(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	store := &memoryStore{}
	w, _ := newTestWatcher(t, dir, store)
	target := writeFixture(t, outside, nativeExample, "outside")
	if err := os.Symlink(target, filepath.Join(dir, nativeExample)); err != nil {
		t.Skip("OS does not permit creating test symlinks")
	}
	now := time.Now()
	w.scan(context.Background(), now)
	w.scan(context.Background(), now.Add(time.Minute))
	if store.calls != 0 {
		t.Fatal("symlink followed")
	}
	link := filepath.Join(dir, "linked-directory")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	options := DefaultOptions()
	options.Directory = link
	if watcher, err := NewWatcher(options, store, slog.Default()); err == nil {
		watcher.root.Close()
		t.Fatal("linked root accepted")
	}
}

func TestCancellationAndCleanShutdown(t *testing.T) {
	dir := t.TempDir()
	entered := make(chan struct{})
	store := &memoryStore{write: func(ctx context.Context, r Recording) (bool, error) {
		if _, ok := ctx.Deadline(); !ok {
			return false, errors.New("missing deadline")
		}
		close(entered)
		<-ctx.Done()
		return false, ctx.Err()
	}}
	w, _ := newTestWatcher(t, dir, store)
	w.options.PollInterval = 10 * time.Millisecond
	w.options.StableFor = 20 * time.Millisecond
	writeFixture(t, dir, nativeExample, "synthetic")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); w.Run(ctx) }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("watcher did not reconcile")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("watcher did not shut down")
	}
	if _, err := w.root.Stat("."); err == nil {
		t.Fatal("root handle left open")
	}
}

func TestInvalidDirectoryReturnsSafeError(t *testing.T) {
	options := DefaultOptions()
	options.Directory = filepath.Join(t.TempDir(), "secret-missing-dir")
	if _, err := NewWatcher(options, &memoryStore{}, slog.Default()); err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatal("expected safe directory failure")
	}
}
