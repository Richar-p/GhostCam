// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"os"

	"golang.org/x/sys/windows"
)

// osLanguage returns the Windows display language, e.g. "fr-FR".
func osLanguage() string {
	langs, err := windows.GetUserPreferredUILanguages(windows.MUI_LANGUAGE_NAME)
	if err != nil || len(langs) == 0 {
		return ""
	}
	return langs[0]
}

// prepareConsole makes the Windows console print UTF-8 (QR code blocks,
// accents) and understand ANSI sequences (status line refresh).
func prepareConsole() {
	_ = windows.SetConsoleOutputCP(65001)
	h := windows.Handle(os.Stdout.Fd())
	var mode uint32
	if windows.GetConsoleMode(h, &mode) == nil {
		_ = windows.SetConsoleMode(h, mode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING)
	}
}
