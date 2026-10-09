// SPDX-License-Identifier: AGPL-3.0-or-later

package update

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// WaitPIDFlag tells a freshly updated instance which process to wait for.
const WaitPIDFlag = "wait-pid"

// Restart starts exe with the current arguments plus -wait-pid=<this pid>, so
// the new instance waits for this one to release its ports and finish its
// recordings before starting.
func Restart(exe string) error {
	args := []string{}
	for i := 1; i < len(os.Args); i++ {
		a := strings.TrimLeft(os.Args[i], "-")
		if a == WaitPIDFlag { // "-wait-pid N": skip the value too
			i++
			continue
		}
		if strings.HasPrefix(a, WaitPIDFlag+"=") {
			continue
		}
		args = append(args, os.Args[i])
	}
	args = append(args, fmt.Sprintf("-%s=%d", WaitPIDFlag, os.Getpid()))
	cmd := exec.Command(exe, args...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	detach(cmd)
	return cmd.Start()
}

// WaitExit waits until process pid has exited, at most max.
func WaitExit(pid int, max time.Duration) bool {
	deadline := time.Now().Add(max)
	for time.Now().Before(deadline) {
		if !alive(pid) {
			return true
		}
		time.Sleep(200 * time.Millisecond)
	}
	return !alive(pid)
}
