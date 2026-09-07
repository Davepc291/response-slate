//go:build !windows

package transcriptreview

import "os"

func isLink(info os.FileInfo) bool { return info.Mode()&os.ModeSymlink != 0 }
func publishExport(root *os.Root, temporary, name string, overwrite bool) error {
	if overwrite {
		if root.Rename(temporary, name) != nil {
			return ErrInput
		}
		return nil
	}
	// Atomic no-replace publication on Unix; never race a check with an overwriting rename.
	if e := root.Link(temporary, name); e != nil {
		if os.IsExist(e) {
			return ErrExists
		}
		return ErrInput
	}
	return nil
}
