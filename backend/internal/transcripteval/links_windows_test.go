package transcripteval

import (
	"os"
	"syscall"
	"testing"
	"time"
)

type fakeInfo struct {
	attrs uint32
	mode  os.FileMode
}

func (f fakeInfo) Name() string       { return "synthetic" }
func (f fakeInfo) Size() int64        { return 0 }
func (f fakeInfo) Mode() os.FileMode  { return f.mode }
func (f fakeInfo) ModTime() time.Time { return time.Time{} }
func (f fakeInfo) IsDir() bool        { return f.mode.IsDir() }
func (f fakeInfo) Sys() any           { return &syscall.Win32FileAttributeData{FileAttributes: f.attrs} }
func TestReparseAttributes(t *testing.T) {
	for _, mode := range []os.FileMode{0, os.ModeDir} {
		if !isLink(fakeInfo{attrs: syscall.FILE_ATTRIBUTE_REPARSE_POINT, mode: mode}) {
			t.Fatal("reparse target/ancestor accepted")
		}
	}
	if isLink(fakeInfo{}) || !isLink(fakeInfo{mode: os.ModeSymlink}) {
		t.Fatal("link classification")
	}
}
