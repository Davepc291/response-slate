package transcriptexperiment

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func safePath(path string) bool {
	return filepath.IsAbs(path) && filepath.Clean(path) == path && !strings.HasPrefix(path, `\\`) && !strings.ContainsAny(path, "\x00\r\n") && !strings.Contains(strings.TrimPrefix(path, filepath.VolumeName(path)), ":")
}
func ancestors(path string) (map[string]os.FileInfo, error) {
	if !safePath(path) {
		return nil, ErrInput
	}
	infos := map[string]os.FileInfo{}
	for p := filepath.Dir(path); ; p = filepath.Dir(p) {
		info, e := os.Lstat(p)
		if e != nil || isLink(info) || !info.IsDir() {
			return nil, ErrInput
		}
		infos[p] = info
		if p == filepath.Dir(p) {
			break
		}
	}
	return infos, nil
}
func checkAncestors(infos map[string]os.FileInfo) error {
	for p, before := range infos {
		after, e := os.Lstat(p)
		if e != nil || isLink(after) || !os.SameFile(before, after) {
			return ErrInput
		}
	}
	return nil
}
func sum(data []byte) string { h := sha256.Sum256(data); return hex.EncodeToString(h[:]) }

type snapshot struct {
	path    string
	info    os.FileInfo
	parents map[string]os.FileInfo
	hash    string
	limit   int64
}

func readSnapshot(path string, limit int64) (*snapshot, []byte, error) {
	parents, e := ancestors(path)
	if e != nil {
		return nil, nil, ErrInput
	}
	root, e := os.OpenRoot(filepath.Dir(path))
	if e != nil {
		return nil, nil, ErrInput
	}
	defer root.Close()
	anchored, e := root.Stat(".")
	if e != nil || !os.SameFile(anchored, parents[filepath.Dir(path)]) {
		return nil, nil, ErrInput
	}
	name := filepath.Base(path)
	before, e := root.Lstat(name)
	if e != nil || isLink(before) || !before.Mode().IsRegular() || before.Size() > limit {
		return nil, nil, ErrInput
	}
	f, e := root.Open(name)
	if e != nil {
		return nil, nil, ErrInput
	}
	defer f.Close()
	opened, e := f.Stat()
	if e != nil || !os.SameFile(before, opened) {
		return nil, nil, ErrInput
	}
	data, e := io.ReadAll(io.LimitReader(f, limit+1))
	if e != nil || int64(len(data)) > limit {
		return nil, nil, ErrInput
	}
	after, e := root.Lstat(name)
	if e != nil || isLink(after) || !os.SameFile(opened, after) || opened.Size() != after.Size() || !opened.ModTime().Equal(after.ModTime()) || int64(len(data)) != opened.Size() || checkAncestors(parents) != nil {
		return nil, nil, ErrInput
	}
	return &snapshot{path, opened, parents, sum(data), limit}, data, nil
}
func (s *snapshot) read() ([]byte, error) {
	next, data, e := readSnapshot(s.path, s.limit)
	if e != nil || !os.SameFile(s.info, next.info) || s.info.Size() != next.info.Size() || !s.info.ModTime().Equal(next.info.ModTime()) || s.hash != next.hash || checkAncestors(s.parents) != nil {
		return nil, ErrIntegrity
	}
	return data, nil
}
func inside(dir, path string) bool {
	rel, e := filepath.Rel(dir, path)
	return e == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) && !filepath.IsAbs(rel)
}

type target struct {
	path    string
	info    os.FileInfo
	parents map[string]os.FileInfo
}

func outputTarget(path string, overwrite bool) (*target, error) {
	parents, e := ancestors(path)
	if e != nil {
		return nil, ErrInput
	}
	info, e := os.Lstat(path)
	if os.IsNotExist(e) {
		return &target{path: path, parents: parents}, nil
	}
	if e != nil || isLink(info) || !info.Mode().IsRegular() || !overwrite {
		return nil, ErrInput
	}
	return &target{path, info, parents}, nil
}
func (t *target) check() error {
	if checkAncestors(t.parents) != nil {
		return ErrInput
	}
	info, e := os.Lstat(t.path)
	if t.info == nil {
		if os.IsNotExist(e) {
			return nil
		}
		return ErrInput
	}
	if e != nil || isLink(info) || !os.SameFile(t.info, info) || info.Size() != t.info.Size() || !info.ModTime().Equal(t.info.ModTime()) {
		return ErrInput
	}
	return nil
}
func (t *target) publish(data []byte) error {
	if t.check() != nil {
		return ErrInput
	}
	root, e := os.OpenRoot(filepath.Dir(t.path))
	if e != nil {
		return ErrInput
	}
	defer root.Close()
	anchor, e := root.Stat(".")
	if e != nil || !os.SameFile(anchor, t.parents[filepath.Dir(t.path)]) {
		return ErrInput
	}
	// Random exclusive file in the same checked directory; only this file is removed.
	var nonce [16]byte
	if _, e = rand.Read(nonce[:]); e != nil {
		return ErrInput
	}
	name := ".gfr-experiment-" + hex.EncodeToString(nonce[:]) + ".tmp"
	f, e := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return ErrInput
	}
	defer root.Remove(name)
	defer f.Close()
	if _, e = f.Write(data); e != nil {
		return ErrInput
	}
	if f.Sync() != nil || f.Close() != nil || t.check() != nil {
		return ErrInput
	}
	return publish(root, name, filepath.Base(t.path), t.info != nil)
}
