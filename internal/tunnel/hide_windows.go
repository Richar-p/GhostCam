// SPDX-License-Identifier: AGPL-3.0-or-later

package tunnel

import (
	"log"
	"os/exec"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// hideWindow keeps cloudflared from popping a console window when GhostCam
// itself is a GUI (windowsgui) process.
func hideWindow(cmd *exec.Cmd) {
	const createNoWindow = 0x08000000
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
}

var (
	jobOnce sync.Once
	job     windows.Handle
)

// bindToParent puts the child in a job object that Windows kills when
// GhostCam exits, even on a crash or a forced kill: no orphan tunnel stays
// open and the binary can be updated on the next launch.
func bindToParent(cmd *exec.Cmd) {
	jobOnce.Do(func() {
		h, err := windows.CreateJobObject(nil, nil)
		if err != nil {
			log.Printf("job object: %v", err)
			return
		}
		info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
			BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE},
		}
		if _, err := windows.SetInformationJobObject(h, windows.JobObjectExtendedLimitInformation,
			uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
			log.Printf("job object: %v", err)
			_ = windows.CloseHandle(h)
			return
		}
		job = h // intentionally never closed: closing it is what kills the child
	})
	if job == 0 || cmd.Process == nil {
		return
	}
	p, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if err != nil {
		log.Printf("job object: %v", err)
		return
	}
	defer windows.CloseHandle(p)
	if err := windows.AssignProcessToJobObject(job, p); err != nil {
		log.Printf("job object: %v", err)
	}
}
