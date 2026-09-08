package transcriptexperiment

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFilesAndTargets(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "synthetic.txt")
	os.WriteFile(p, []byte("synthetic"), 0600)
	for _, bad := range []string{"relative", dir, p + ":stream", filepath.Join(dir, "missing")} {
		if _, _, e := readSnapshot(bad, 100); e == nil {
			t.Fatal("unsafe source", bad)
		}
	}
	if _, _, e := readSnapshot(p, 2); e == nil {
		t.Fatal("size limit")
	}
	snap, _, e := readSnapshot(p, 100)
	if e != nil {
		t.Fatal(e)
	}
	os.WriteFile(p, []byte("modified!"), 0600)
	if _, e = snap.read(); e != ErrIntegrity {
		t.Fatal("hash mismatch")
	}
	target, e := outputTarget(p, true)
	if e != nil {
		t.Fatal(e)
	}
	os.WriteFile(p, []byte("changed after target validation"), 0600)
	if target.publish([]byte("new")) == nil {
		t.Fatal("replaced changed target")
	}
	target, e = outputTarget(filepath.Join(dir, "new.jsonl"), false)
	if e != nil {
		t.Fatal(e)
	}
	if e = target.publish([]byte("synthetic")); e != nil {
		t.Fatal(e)
	}
	if e = target.publish([]byte("overwrite")); e == nil {
		t.Fatal("no-replace failed")
	}
	matches, _ := filepath.Glob(filepath.Join(dir, ".gfr-experiment-*.tmp"))
	if len(matches) != 0 {
		t.Fatal("staging files leaked")
	}
}
func TestLinkedSourcesAndAncestors(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	os.Mkdir(sub, 0700)
	p := filepath.Join(sub, "synthetic")
	os.WriteFile(p, []byte("synthetic"), 0600)
	link := filepath.Join(dir, "link")
	if e := os.Symlink(p, link); e != nil {
		t.Skip("OS denies unprivileged symlinks; Windows reparse attributes tested separately")
	}
	if _, _, e := readSnapshot(link, 100); e == nil {
		t.Fatal("symlink read")
	}
	if _, e := outputTarget(link, true); e == nil {
		t.Fatal("symlink output")
	}
	ancestor := filepath.Join(dir, "ancestor")
	if e := os.Symlink(sub, ancestor); e != nil {
		t.Fatal(e)
	}
	if _, _, e := readSnapshot(filepath.Join(ancestor, "synthetic"), 100); e == nil {
		t.Fatal("linked ancestor")
	}
}

func TestPathReplacementAndModificationTime(t *testing.T) {
	p := filepath.Join(t.TempDir(), "synthetic.mp3")
	os.WriteFile(p, []byte("synthetic"), 0600)
	snap, _, e := readSnapshot(p, 100)
	if e != nil {
		t.Fatal(e)
	}
	changed := snap.info.ModTime().Add(time.Second)
	if e = os.Chtimes(p, changed, changed); e != nil {
		t.Fatal(e)
	}
	if _, e = snap.read(); e != ErrIntegrity {
		t.Fatal("mtime change missed")
	}
	snap, _, e = readSnapshot(p, 100)
	if e != nil {
		t.Fatal(e)
	}
	// Preserve the old file under a fixture-only name so its identity cannot be reused.
	if e = os.Rename(p, p+".old"); e != nil {
		t.Fatal(e)
	}
	os.WriteFile(p, []byte("synthetic"), 0600)
	os.Chtimes(p, snap.info.ModTime(), snap.info.ModTime())
	if _, e = snap.read(); e != ErrIntegrity {
		t.Fatal("path identity change missed")
	}
}
