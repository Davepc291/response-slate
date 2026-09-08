package transcriptexperiment

import (
	"os"
	"syscall"
	"testing"
	"time"
)

type attributeInfo struct {
	mode       os.FileMode
	attributes uint32
}

func (i attributeInfo) Name() string       { return "synthetic" }
func (i attributeInfo) Size() int64        { return 0 }
func (i attributeInfo) Mode() os.FileMode  { return i.mode }
func (i attributeInfo) ModTime() time.Time { return time.Time{} }
func (i attributeInfo) IsDir() bool        { return i.mode.IsDir() }
func (i attributeInfo) Sys() any {
	return &syscall.Win32FileAttributeData{FileAttributes: i.attributes}
}
func TestWindowsReparsePoints(t *testing.T) {
	for _, mode := range []os.FileMode{0, os.ModeDir} {
		if !isLink(attributeInfo{mode, syscall.FILE_ATTRIBUTE_REPARSE_POINT}) {
			t.Fatal("reparse file/ancestor accepted")
		}
	}
	if isLink(attributeInfo{}) || !isLink(attributeInfo{mode: os.ModeSymlink}) {
		t.Fatal("link classification")
	}
}
