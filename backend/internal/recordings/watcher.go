package recordings

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

// Store returns true for an inserted recording, false for a duplicate.
type Store interface {
	InsertRecording(context.Context, Recording) (bool, error)
}

// Processor receives only a verified read-only handle, never opens a source path.
type Processor interface {
	Process(context.Context, Recording, *os.File)
}

type observation struct {
	first, stableSince, nextAttempt time.Time
	info                            os.FileInfo
	attempts                        int
	done                            bool
}

// Watcher uses reconciliation polling, so correctness does not depend on OS
// notification delivery. All state belongs to its single Run goroutine.
type Watcher struct {
	options   Options
	root      *os.Root
	path      string
	location  *time.Location
	store     Store
	logger    *slog.Logger
	seen      map[string]*observation
	processor Processor
}

func NewWatcher(options Options, store Store, logger *slog.Logger, processors ...Processor) (*Watcher, error) {
	if err := options.Validate(); err != nil {
		return nil, err
	}
	if options.Directory == "" || store == nil || logger == nil {
		return nil, errors.New("recording watcher is not configured")
	}
	path, err := filepath.Abs(options.Directory)
	if err != nil {
		return nil, errors.New("recording directory unavailable")
	}
	// Reject links/junctions in every path component before anchoring the root.
	for current := path; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err != nil || isLink(info) || !info.IsDir() {
			return nil, errors.New("recording directory must be a regular directory without links")
		}
		if current == filepath.Dir(current) {
			break
		}
	}
	before, err := os.Lstat(path)
	if err != nil {
		return nil, errors.New("recording directory unavailable")
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, errors.New("recording directory unavailable")
	}
	after, err := root.Stat(".")
	if err != nil || !os.SameFile(before, after) || isLink(before) {
		root.Close()
		return nil, errors.New("recording directory changed during startup")
	}
	location, _ := time.LoadLocation(options.Timezone)
	w := &Watcher{options: options, root: root, path: path, location: location,
		store: store, logger: logger, seen: make(map[string]*observation)}
	if len(processors) > 0 {
		w.processor = processors[0]
	}
	names, err := w.names()
	if err != nil {
		root.Close()
		return nil, errors.New("recording directory cannot be listed")
	}
	// Establish the startup boundary synchronously, before Run is launched.
	for _, name := range names {
		w.seen[name] = &observation{done: true}
	}
	logger.Info("recording_ingestion", "outcome", "ignored", "reason", "startup_snapshot", "count", len(names))
	return w, nil
}

func (w *Watcher) names() ([]string, error) {
	dir, err := w.root.Open(".")
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	return dir.Readdirnames(-1)
}

// Run owns the root handle and closes it on cancellation. Call exactly once.
func (w *Watcher) Run(ctx context.Context) {
	defer w.root.Close()
	ticker := time.NewTicker(w.options.PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			w.scan(ctx, now)
		}
	}
}

func (w *Watcher) log(name, outcome, reason string) {
	// Hash the path instead of logging arbitrary filenames or filesystem errors.
	w.logger.Info("recording_ingestion", "recording_id", SourceIdentity(filepath.Join(w.path, name)),
		"outcome", outcome, "reason", reason)
}

func (w *Watcher) scan(ctx context.Context, now time.Time) {
	names, err := w.names()
	if err != nil {
		w.logger.Warn("recording_ingestion", "outcome", "failed", "reason", "directory_scan_failed")
		return
	}
	present := make(map[string]bool, len(names))
	for _, name := range names {
		if ctx.Err() != nil {
			return
		}
		present[name] = true
		s, exists := w.seen[name]
		if !exists {
			s = &observation{first: now, stableSince: now}
			w.seen[name] = s
		}
		if s.done {
			continue
		}
		m, reason := ParseFilename(name, w.location)
		if reason != "" {
			w.log(name, "ignored", reason)
			s.done = true
			continue
		}
		info, err := w.root.Lstat(name)
		if err == nil && (isLink(info) || !info.Mode().IsRegular()) {
			w.log(name, "ignored", "not_regular_file")
			s.done = true
			continue
		}
		if now.Sub(s.first) >= w.options.MaxWait {
			w.log(name, "failed", "observation_deadline_exceeded")
			s.done = true
			continue
		}
		if err != nil {
			s.info = nil
			s.stableSince = now
			continue
		}
		if s.info == nil || !os.SameFile(s.info, info) || s.info.Size() != info.Size() || !s.info.ModTime().Equal(info.ModTime()) {
			s.info = info
			s.stableSince = now
			continue
		}
		if info.Size() == 0 || now.Sub(s.stableSince) < w.options.StableFor || now.Before(s.nextAttempt) {
			continue
		}
		// Only stat metadata is consumed; no file handle to the audio is opened.
		path := filepath.Join(w.path, name)
		r := Recording{Metadata: m, SourceIdentity: SourceIdentity(path), SourcePath: path,
			SizeBytes: info.Size(), ModifiedAt: info.ModTime()}
		attemptCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		inserted, err := w.store.InsertRecording(attemptCtx, r)
		cancel()
		if ctx.Err() != nil {
			return
		}
		s.attempts++
		if err != nil {
			w.log(name, "failed", "database_write_failed")
			s.nextAttempt = now.Add(w.options.RetryInterval)
			if s.attempts >= w.options.MaxAttempts {
				w.log(name, "failed", "retry_limit_exceeded")
				s.done = true
			}
			continue
		}
		s.done = true
		if inserted {
			w.log(name, "accepted", "metadata_stored")
		} else {
			w.log(name, "duplicate", "source_identity_exists")
		}
		if w.processor != nil {
			input, err := w.root.Open(name)
			if err != nil {
				w.processor.Process(ctx, r, nil)
				continue
			}
			opened, statErr := input.Stat()
			current, linkErr := w.root.Lstat(name)
			if statErr != nil || linkErr != nil || isLink(current) || !opened.Mode().IsRegular() ||
				!os.SameFile(info, opened) || !os.SameFile(current, opened) || opened.Size() != info.Size() || !opened.ModTime().Equal(info.ModTime()) {
				input.Close()
				w.processor.Process(ctx, r, nil)
				continue
			}
			w.processor.Process(ctx, r, input)
			input.Close()
		}
	}
	// Forget vanished names, keeping memory proportional to the directory size.
	for name := range w.seen {
		if !present[name] {
			delete(w.seen, name)
		}
	}
}
