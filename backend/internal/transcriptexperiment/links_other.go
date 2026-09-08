//go:build !windows

package transcriptexperiment

import "os"

func isLink(i os.FileInfo) bool { return i.Mode()&os.ModeSymlink != 0 }
func publish(root *os.Root, from, to string, overwrite bool) error {
	if overwrite {
		if root.Rename(from, to) != nil {
			return ErrInput
		}
	} else if root.Link(from, to) != nil {
		return ErrInput
	}
	return nil
}
