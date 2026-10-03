// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build !windows

package tunnel

import "os/exec"

func hideWindow(*exec.Cmd) {}

// bindToParent: exec.CommandContext already kills the child on shutdown.
func bindToParent(*exec.Cmd) {}
