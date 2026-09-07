package transcriptreview

import (
	"os"
	"path/filepath"
	"syscall"
	"unsafe"
)

func isLink(info os.FileInfo) bool {
	attrs, ok := info.Sys().(*syscall.Win32FileAttributeData)
	return info.Mode()&os.ModeSymlink != 0 || !ok || attrs.FileAttributes&syscall.FILE_ATTRIBUTE_REPARSE_POINT != 0
}
func publishExport(root *os.Root, temporary, name string, overwrite bool) error {
	// Ensure the native rename addresses the same parent as our anchored writes.
	disk, e := os.Lstat(root.Name())
	if e != nil || isLink(disk) {
		return ErrInput
	}
	anchor, e := root.Stat(".")
	if e != nil || !os.SameFile(disk, anchor) {
		return ErrInput
	}
	from, e := syscall.UTF16PtrFromString(filepath.Join(root.Name(), temporary))
	if e != nil {
		return ErrInput
	}
	to, e := syscall.UTF16PtrFromString(filepath.Join(root.Name(), name))
	if e != nil {
		return ErrInput
	}
	flags := uintptr(0)
	if overwrite {
		flags = 1
	} // MOVEFILE_REPLACE_EXISTING, no cross-volume copy fallback.
	result, _, callErr := syscall.NewLazyDLL("kernel32.dll").NewProc("MoveFileExW").Call(uintptr(unsafe.Pointer(from)), uintptr(unsafe.Pointer(to)), flags)
	if result == 0 {
		if callErr == syscall.ERROR_ALREADY_EXISTS || callErr == syscall.ERROR_FILE_EXISTS {
			return ErrExists
		}
		return ErrInput
	}
	return nil
}
