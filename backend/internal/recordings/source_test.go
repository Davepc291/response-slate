package recordings

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestOpenPersistedSource(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "source.mp3")
	if err := os.WriteFile(path, []byte("synthetic"), 0600); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(path)
	r := Recording{SourcePath: path, SourceIdentity: SourceIdentity(path), SizeBytes: info.Size(), ModifiedAt: info.ModTime().Truncate(time.Microsecond)}
	f, err := OpenSource(dir, r)
	if err != nil {
		t.Fatal("persisted microsecond timestamp rejected")
	}
	f.Close()
	r.ModifiedAt = r.ModifiedAt.Add(time.Microsecond)
	if f, err = OpenSource(dir, r); err == nil {
		f.Close()
		t.Fatal("changed source accepted")
	}
	if f, err = OpenSource(t.TempDir(), r); err == nil {
		f.Close()
		t.Fatal("outside directory accepted")
	}
	a := time.Unix(1, 123456700)
	if !SameStoredTime(a, time.Unix(1, 123456000)) || SameStoredTime(a, time.Unix(1, 123457000)) {
		t.Fatal("database precision comparison")
	}
}
