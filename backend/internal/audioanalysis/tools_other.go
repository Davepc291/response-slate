//go:build !windows

package audioanalysis

import "os/exec"

func configureCommand(cmd *exec.Cmd) {}
