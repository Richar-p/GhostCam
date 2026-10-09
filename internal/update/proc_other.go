// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build !windows

package update

import (
	"errors"
	"os/exec"
	"syscall"
)

func alive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// detach: the new instance keeps the terminal and outlives us (no setsid, so
// Ctrl+C in that terminal still stops it).
func detach(*exec.Cmd) {}
