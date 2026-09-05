package recordings

import (
	"errors"
	"os"
	"path/filepath"
	"time"
)

// OpenSource revalidates persisted metadata inside the configured directory.
// All handles are read-only. No recursive traversal or linked targets is allowed.
func OpenSource(directory string, r Recording) (*os.File, error) {
	fail := errors.New("source unavailable")
	rootPath, err := filepath.Abs(directory)
	if err != nil || directory == "" {
		return nil, fail
	}
	if filepath.Clean(filepath.Dir(r.SourcePath)) != rootPath || SourceIdentity(r.SourcePath) != r.SourceIdentity {
		return nil, fail
	}
	for current := rootPath; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err != nil || isLink(info) || !info.IsDir() {
			return nil, fail
		}
		if current == filepath.Dir(current) {
			break
		}
	}
	before, err := os.Lstat(rootPath)
	if err != nil {
		return nil, fail
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return nil, fail
	}
	defer root.Close()
	anchored, err := root.Stat(".")
	if err != nil || !os.SameFile(before, anchored) {
		return nil, fail
	}
	name := filepath.Base(r.SourcePath)
	info, err := root.Lstat(name)
	if err != nil || isLink(info) || !info.Mode().IsRegular() {
		return nil, fail
	}
	f, err := root.Open(name)
	if err != nil {
		return nil, fail
	}
	opened, err := f.Stat()
	current, linkErr := root.Lstat(name)
	if err != nil || linkErr != nil || isLink(current) || !os.SameFile(info, opened) || !os.SameFile(current, opened) || opened.Size() != r.SizeBytes || !SameStoredTime(opened.ModTime(), r.ModifiedAt) {
		f.Close()
		return nil, fail
	}
	return f, nil
}

// PostgreSQL timestamptz and pgx encode microseconds; Windows mtime has 100ns
// precision. Compare at the persisted precision, without relaxing size/identity.
func SameStoredTime(a, b time.Time) bool {
	return a.Truncate(time.Microsecond).Equal(b.Truncate(time.Microsecond))
}
