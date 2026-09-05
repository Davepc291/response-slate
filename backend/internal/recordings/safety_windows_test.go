package recordings

import (
	"os"
	"syscall"
	"testing"
)

type reparseInfo struct{ os.FileInfo }

func (r reparseInfo) Sys() any {
	return &syscall.Win32FileAttributeData{FileAttributes: syscall.FILE_ATTRIBUTE_REPARSE_POINT}
}

func TestWindowsReparseAttributeRejected(t *testing.T) {
	info, err := os.Stat(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if !isLink(reparseInfo{info}) {
		t.Fatal("reparse target not rejected")
	}
}
