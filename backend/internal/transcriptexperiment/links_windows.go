package transcriptexperiment

import (
	"os"
	"path/filepath"
	"syscall"
	"unsafe"
)

func isLink(info os.FileInfo) bool {
	a, ok := info.Sys().(*syscall.Win32FileAttributeData)
	return info.Mode()&os.ModeSymlink != 0 || !ok || a.FileAttributes&syscall.FILE_ATTRIBUTE_REPARSE_POINT != 0
}
func publish(root *os.Root, from, to string, overwrite bool) error {
	disk, e := os.Lstat(root.Name())
	if e != nil || isLink(disk) {
		return ErrInput
	}
	anchor, e := root.Stat(".")
	if e != nil || !os.SameFile(disk, anchor) {
		return ErrInput
	}
	a, e := syscall.UTF16PtrFromString(filepath.Join(root.Name(), from))
	if e != nil {
		return ErrInput
	}
	b, e := syscall.UTF16PtrFromString(filepath.Join(root.Name(), to))
	if e != nil {
		return ErrInput
	}
	flags := uintptr(0)
	if overwrite {
		flags = 1
	}
	ok, _, _ := syscall.NewLazyDLL("kernel32.dll").NewProc("MoveFileExW").Call(uintptr(unsafe.Pointer(a)), uintptr(unsafe.Pointer(b)), flags)
	if ok == 0 {
		return ErrInput
	}
	return nil
}
