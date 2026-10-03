// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"syscall"

	webview2 "github.com/jchv/go-webview2"
)

// The Win32 message loop must run on the main OS thread.
func init() { runtime.LockOSThread() }

// runWindow shows the admin UI in a native window (WebView2, preinstalled on
// Windows 10/11) and blocks until it is closed. It returns false if WebView2
// is unavailable, so the caller can fall back to the default browser.
func runWindow(ctx context.Context, url, dataDir string) bool {
	w := webview2.NewWithOptions(webview2.WebViewOptions{
		DataPath:  dataDir,
		AutoFocus: true,
		WindowOptions: webview2.WindowOptions{
			Title: "GhostCam", Width: 440, Height: 760, Center: true,
			IconId: 1, // RT_GROUP_ICON #1 from winres/winres.json (rsrc_windows_*.syso)
		},
	})
	if w == nil {
		return false
	}
	defer w.Destroy()

	closed := make(chan struct{})
	defer close(closed)
	go func() {
		select {
		case <-ctx.Done():
			w.Dispatch(w.Terminate)
		case <-closed:
		}
	}()
	w.Navigate(url)
	w.Run()
	return true
}

// pickDir shows the Windows folder dialog (via the PowerShell that ships with
// Windows, no extra dependency). Returns "" if the user cancels.
func pickDir(start string) (string, error) {
	const script = `[Console]::OutputEncoding = [Text.Encoding]::UTF8
Add-Type -AssemblyName System.Windows.Forms
$owner = New-Object System.Windows.Forms.Form -Property @{ TopMost = $true }
$d = New-Object System.Windows.Forms.FolderBrowserDialog
$d.Description = 'Dossier des vidéos GhostCam'
$d.ShowNewFolderButton = $true
$d.SelectedPath = $env:GHOSTCAM_START
if ($d.ShowDialog($owner) -eq 'OK') { $d.SelectedPath }`
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-STA", "-Command", script)
	cmd.Env = append(os.Environ(), "GHOSTCAM_START="+start)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
