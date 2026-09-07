package transcriptreview

import (
	"os"
	"syscall"
	"testing"
	"time"
)

type reparseFixture struct {
	attrs syscall.Win32FileAttributeData
}

func (f reparseFixture) Name() string       { return "fixture" }
func (f reparseFixture) Size() int64        { return 0 }
func (f reparseFixture) Mode() os.FileMode  { return 0 }
func (f reparseFixture) ModTime() time.Time { return time.Time{} }
func (f reparseFixture) IsDir() bool        { return false }
func (f reparseFixture) Sys() any           { return &f.attrs }
func TestReparseClassification(t *testing.T) {
	if !isLink(reparseFixture{attrs: syscall.Win32FileAttributeData{FileAttributes: syscall.FILE_ATTRIBUTE_REPARSE_POINT}}) {
		t.Fatal("reparse point accepted")
	}
	if isLink(reparseFixture{}) {
		t.Fatal("regular file rejected")
	}
}
