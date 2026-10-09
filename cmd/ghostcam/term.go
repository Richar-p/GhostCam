// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	qrcode "github.com/skip2/go-qrcode"
	"golang.org/x/term"

	"ghostcam/internal/server"
)

// terminalUI shows the pairing QR code and a live status line until ctx ends.
// When stdout is not a terminal (logs, Docker), it prints one line per change.
func terminalUI(ctx context.Context, srv *server.Server, tr *translator, adminURL string) {
	prepareConsole()
	tty := term.IsTerminal(int(os.Stdout.Fd()))
	fmt.Printf("GhostCam %s · %s %s · Ctrl+C\n\n", version, tr.t("pc.settingsButton", nil), adminURL)

	var shownURL, lastLine, shownUpdate string
	render := func() {
		st := srv.Status()
		if url, _ := st["url"].(string); url != "" && url != shownURL {
			shownURL = url
			if tty {
				fmt.Print("\r\033[K")
			}
			if q, err := qrcode.New(url, qrcode.Medium); err == nil {
				fmt.Println(q.ToSmallString(false))
			}
			fmt.Printf("%s\n%s\n\n", tr.t("pc.pair.title", nil), url)
			lastLine = ""
		}
		if u, _ := st["update"].(map[string]any); u != nil {
			if v, _ := u["version"].(string); v != "" && v != shownUpdate {
				shownUpdate = v
				if tty {
					fmt.Print("\r\033[K")
				}
				cmd := "ghostcam update"
				if flavor == "cli" {
					cmd = "ghostcam-cli update"
				}
				fmt.Printf("%s → %s\n", tr.t("pc.update.available", map[string]any{"version": v}), cmd)
				lastLine = ""
			}
		}
		line := statusLine(st, tr)
		switch {
		case tty:
			fmt.Printf("\r\033[K%s", line)
		case line != lastLine:
			fmt.Println(line)
		}
		lastLine = line
	}
	render()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			fmt.Println()
			return
		case <-tick.C:
			render()
		}
	}
}

func statusLine(st map[string]any, tr *translator) string {
	if m, _ := st["error"].(*server.Msg); m != nil {
		return "✗ " + tr.msg(m)
	}
	if url, _ := st["url"].(string); url == "" {
		if m, _ := st["progress"].(*server.Msg); m != nil {
			return "… " + tr.msg(m)
		}
		return "… " + tr.t("pc.starting.title", nil)
	}
	mode := tr.t("common.mode.relay", nil)
	if st["mode"] == "webrtc" {
		mode = tr.t("common.mode.direct", nil)
	}
	if b, _ := st["buffered"].(bool); b {
		mode = tr.t("pc.live.buffered", map[string]any{"mode": mode})
	}
	var parts []string
	switch st["state"] {
	case "recording":
		secs, _ := toInt(st["seconds"])
		bytes, _ := toInt(st["bytes"])
		parts = append(parts, fmt.Sprintf("● REC %s", fmtClock(secs)), tr.size(int64(bytes)), mode)
		if lag, _ := st["lag"].(float64); lag >= 1 {
			parts = append(parts, tr.t("pc.live.lag", map[string]any{"n": int(lag + 0.5)}))
		}
	case "connected":
		parts = append(parts, "✓ "+tr.t("pc.ready.title", nil), mode)
	default:
		parts = append(parts, "○ "+tr.t("pc.pill.waiting", nil))
	}
	if n, _ := toInt(st["count"]); n > 0 {
		parts = append(parts, tr.t("pc.history.count", map[string]any{"count": n}))
	}
	return strings.Join(parts, " · ")
}

func fmtClock(s int) string {
	if s >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", s/3600, s/60%60, s%60)
	}
	return fmt.Sprintf("%02d:%02d", s/60, s%60)
}

// size formats bytes as MB/GB in the current language ("1,5 Mo" / "1.5 MB").
func (tr *translator) size(b int64) string {
	key, n := "common.mb", float64(b)/(1<<20)
	if b >= 1<<30 {
		key, n = "common.gb", float64(b)/(1<<30)
	}
	s := fmt.Sprintf("%.1f", n)
	if tr.lang == "fr" {
		s = strings.Replace(s, ".", ",", 1)
	}
	return tr.t(key, map[string]any{"n": s})
}
