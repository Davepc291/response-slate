package transcriptreview

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// A review file must be an absolute local path. Check every existing ancestor,
// then anchor reads/writes with os.Root and revalidate file identity after open.
func fileRoot(path string) (*os.Root, string, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || strings.HasPrefix(path, `\\`) || strings.ContainsAny(path, "\x00\r\n") {
		return nil, "", ErrInput
	}
	if strings.Contains(strings.TrimPrefix(path, filepath.VolumeName(path)), ":") {
		return nil, "", ErrInput
	}
	parent := filepath.Dir(path)
	for p := parent; ; p = filepath.Dir(p) {
		info, e := os.Lstat(p)
		if e != nil || isLink(info) || !info.IsDir() {
			return nil, "", ErrInput
		}
		if p == filepath.Dir(p) {
			break
		}
	}
	before, e := os.Lstat(parent)
	if e != nil {
		return nil, "", ErrInput
	}
	root, e := os.OpenRoot(parent)
	if e != nil {
		return nil, "", ErrInput
	}
	anchored, e := root.Stat(".")
	if e != nil || !os.SameFile(before, anchored) {
		root.Close()
		return nil, "", ErrInput
	}
	return root, filepath.Base(path), nil
}
func ReadCorrection(path string) (string, error) {
	root, name, e := fileRoot(path)
	if e != nil {
		return "", e
	}
	defer root.Close()
	before, e := root.Lstat(name)
	if e != nil || isLink(before) || !before.Mode().IsRegular() || before.Size() > MaxTextBytes+3 {
		return "", ErrInput
	}
	f, e := root.Open(name)
	if e != nil {
		return "", ErrInput
	}
	defer f.Close()
	opened, e := f.Stat()
	if e != nil || !os.SameFile(before, opened) {
		return "", ErrInput
	}
	raw, e := io.ReadAll(io.LimitReader(f, MaxTextBytes+4))
	if e != nil {
		return "", ErrInput
	}
	after, e := root.Lstat(name)
	if e != nil || isLink(after) || !os.SameFile(opened, after) || opened.Size() != after.Size() || !opened.ModTime().Equal(after.ModTime()) {
		return "", ErrInput
	}
	// A UTF-8 BOM is encoding metadata, not reference transcript text.
	raw = bytes.TrimPrefix(raw, []byte{0xef, 0xbb, 0xbf})
	if !validText(string(raw), MaxTextBytes, true) {
		return "", ErrInput
	}
	return string(raw), nil
}

func checkTarget(root *os.Root, name string, overwrite bool) error {
	info, e := root.Lstat(name)
	if os.IsNotExist(e) {
		return nil
	}
	if e != nil || isLink(info) || !info.Mode().IsRegular() {
		return ErrInput
	}
	if !overwrite {
		return ErrExists
	}
	return nil
}

// ExportAtomic streams a bounded result into a same-directory temporary file.
// Failure before publication leaves the old export untouched and removes the temp.
func ExportAtomic(ctx context.Context, path string, overwrite bool, limit int, store Store) (int, error) {
	if limit < 1 || limit > MaxExportRecords || !strings.HasSuffix(strings.ToLower(path), ".jsonl") {
		return 0, ErrInput
	}
	root, name, e := fileRoot(path)
	if e != nil {
		return 0, e
	}
	defer root.Close()
	if e = checkTarget(root, name, overwrite); e != nil {
		return 0, e
	}
	var token [16]byte
	if _, e = rand.Read(token[:]); e != nil {
		return 0, ErrInput
	}
	temporary := ".gfr-review-" + hex.EncodeToString(token[:]) + ".tmp"
	f, e := root.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return 0, ErrInput
	}
	defer root.Remove(temporary)
	defer f.Close()
	count := 0
	last := ""
	encoder := json.NewEncoder(f)
	e = store.Export(ctx, limit, func(r ExportRecord) error {
		if ctx.Err() != nil {
			return ErrUnavailable
		}
		if count >= limit || (last != "" && r.Fingerprint <= last) {
			return ErrInput
		}
		if e := encoder.Encode(r); e != nil {
			return ErrInput
		}
		count++
		last = r.Fingerprint
		return nil
	})
	if e != nil {
		return 0, e
	}
	if ctx.Err() != nil {
		return 0, ErrUnavailable
	}
	if f.Sync() != nil || f.Close() != nil {
		return 0, ErrInput
	}
	if e = checkTarget(root, name, overwrite); e != nil {
		return 0, e
	}
	// Native Windows publication uses a no-replace rename unless explicitly opted in.
	if e = publishExport(root, temporary, name, overwrite); e != nil {
		return 0, e
	}
	return count, nil
}
