// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	ghostcam "ghostcam"
	"ghostcam/internal/server"
	"ghostcam/internal/update"
	"ghostcam/web"
)

// flavor is "cli" for the Windows console build (ghostcam-cli.exe), whose
// default is the terminal interface; set with -ldflags "-X main.flavor=cli".
var flavor = "gui"

type uiMode int

const (
	uiWindow   uiMode = iota // native window (Windows GUI build)
	uiTerminal               // QR code and live status in the terminal
	uiBrowser                // terminal status + the page opened in the browser (macOS, Linux)
)

const defaultAdmin = "127.0.0.1:8081"

func main() {
	args := os.Args[1:]
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, rest := args[0], args[1:]
		switch cmd {
		case "run":
			app(rest, uiTerminal)
		case "config":
			os.Exit(configCmd(rest))
		case "update":
			os.Exit(updateCmd(rest))
		case "version":
			fmt.Printf("GhostCam %s (%s/%s%s)\n", version, runtime.GOOS, runtime.GOARCH, map[bool]string{true: ", cli"}[flavor == "cli"])
		case "help":
			usage(os.Stdout)
		default:
			fmt.Fprintf(os.Stderr, "unknown command %q\n\n", cmd)
			usage(os.Stderr)
			os.Exit(2)
		}
		return
	}
	switch {
	case runtime.GOOS == "windows" && flavor != "cli":
		app(args, uiWindow)
	case runtime.GOOS == "windows":
		app(args, uiTerminal)
	default:
		app(args, uiBrowser)
	}
}

func usage(w io.Writer) {
	fmt.Fprintf(w, `GhostCam %s: your phone films, your computer keeps the proof.

Usage:
  ghostcam [flags]              start GhostCam (window on Windows, browser + terminal elsewhere)
  ghostcam run [flags]          start in the terminal: QR code and live status
  ghostcam config               show the settings
  ghostcam config get KEY       show one setting
  ghostcam config set KEY VALUE change a setting (applied live if GhostCam is running)
  ghostcam update [--check]     install the latest version (--check: only look)
  ghostcam version              show the version
  ghostcam run -h               list the flags (ports, folder, tunnel, TURN…)

Settings (config keys):
  quality     eco | standard | high | max
  buffer      0 | 5 | 15 | 30          network buffer in seconds (0 = real time)
  out         folder for the videos
  tunnel      cloudflare | localhostrun | custom
  tunnel-url  https://…                 your own address (tunnel = custom)
  language    auto | en | fr …
  updates     on | off                  automatic update check
`, version)
	if runtime.GOOS == "windows" && flavor != "cli" {
		fmt.Fprintln(w, "\nOn Windows, use ghostcam-cli.exe for commands in a terminal.")
	}
}

// ---------------------------------------------------------------- config

var configKeys = []string{"quality", "buffer", "out", "tunnel", "tunnel-url", "language", "updates"}

func getKey(s server.Settings, key string) (string, error) {
	switch key {
	case "quality":
		return s.Quality, nil
	case "buffer":
		return strconv.Itoa(s.Buffer), nil
	case "out":
		return s.OutDir, nil
	case "tunnel":
		return s.Tunnel, nil
	case "tunnel-url":
		return s.TunnelURL, nil
	case "language":
		if s.Language == "" {
			return "auto", nil
		}
		return s.Language, nil
	case "updates":
		return map[bool]string{true: "off", false: "on"}[s.NoUpdateCheck], nil
	}
	return "", fmt.Errorf("unknown setting %q (keys: %s)", key, strings.Join(configKeys, ", "))
}

func setKey(s *server.Settings, key, value string) error {
	switch key {
	case "quality":
		ids := []string{}
		for _, q := range server.Qualities() {
			ids = append(ids, q.ID)
		}
		if !slices.Contains(ids, value) {
			return fmt.Errorf("quality: one of %s", strings.Join(ids, ", "))
		}
		s.Quality = value
	case "buffer":
		n, err := strconv.Atoi(strings.TrimSuffix(value, "s"))
		if err != nil || !slices.Contains(server.Buffers(), n) {
			return fmt.Errorf("buffer: one of %v (seconds)", server.Buffers())
		}
		s.Buffer = n
	case "out":
		abs, err := filepath.Abs(value)
		if err != nil {
			return err
		}
		s.OutDir = abs
	case "tunnel":
		if !slices.Contains(server.Tunnels(), value) {
			return fmt.Errorf("tunnel: one of %s", strings.Join(server.Tunnels(), ", "))
		}
		s.Tunnel = value
	case "tunnel-url":
		s.TunnelURL = value
	case "language":
		if value == "auto" {
			value = ""
		}
		s.Language = value
	case "updates":
		switch value {
		case "on":
			s.NoUpdateCheck = false
		case "off":
			s.NoUpdateCheck = true
		default:
			return errors.New("updates: on or off")
		}
	default:
		_, err := getKey(*s, key)
		return err
	}
	return nil
}

// configCmd reads or changes the settings. When a GhostCam is running, it
// goes through its local API so the change applies at once; otherwise it
// edits the settings file with the same validation.
func configCmd(args []string) int {
	path := filepath.Join(appDataDir(), "settings.json")
	cur := server.LoadSettings(path, server.Settings{Quality: "standard", OutDir: defaultOutDir()})
	running := false
	if s, ok := runningSettings(); ok {
		cur, running = s, true
	}
	tr := newTranslator(cur.Language)

	switch {
	case len(args) == 0:
		for _, k := range configKeys {
			v, _ := getKey(cur, k)
			fmt.Printf("%-11s %s\n", k, v)
		}
		if running {
			fmt.Println("\n(read from the running GhostCam)")
		}
		return 0
	case args[0] == "get" && len(args) == 2:
		v, err := getKey(cur, args[1])
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 2
		}
		fmt.Println(v)
		return 0
	case args[0] == "set" && len(args) == 3:
		next := cur
		if err := setKey(&next, args[1], args[2]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 2
		}
		var err error
		if running {
			err = postSettings(next)
		} else if next, err = server.NormalizeSettings(next, server.LocalesIn(web.FS)); err == nil {
			err = server.SaveSettings(path, next)
		}
		if err != nil {
			var ke *server.KeyError
			if errors.As(err, &ke) {
				fmt.Fprintln(os.Stderr, tr.msg(ke))
			} else {
				fmt.Fprintln(os.Stderr, err)
			}
			return 1
		}
		v, _ := getKey(next, args[1])
		fmt.Printf("%s = %s%s\n", args[1], v, map[bool]string{true: " (applied to the running GhostCam)"}[running])
		return 0
	}
	fmt.Fprintln(os.Stderr, "usage: ghostcam config [get KEY | set KEY VALUE]")
	return 2
}

func runningSettings() (server.Settings, bool) {
	var s server.Settings
	c := http.Client{Timeout: time.Second}
	resp, err := c.Get("http://" + defaultAdmin + "/api/settings")
	if err != nil {
		return s, false
	}
	defer resp.Body.Close()
	return s, resp.StatusCode == http.StatusOK && json.NewDecoder(resp.Body).Decode(&s) == nil
}

func postSettings(s server.Settings) error {
	body, _ := json.Marshal(s)
	req, _ := http.NewRequest(http.MethodPost, "http://"+defaultAdmin+"/api/settings", bytes.NewReader(body))
	req.Header.Set("X-GhostCam", "1")
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		return nil
	}
	var m server.Msg
	if json.NewDecoder(resp.Body).Decode(&m) == nil && m.Key != "" {
		return &server.KeyError{Msg: m}
	}
	return fmt.Errorf("GhostCam answered %s", resp.Status)
}

// ---------------------------------------------------------------- update

func updateCmd(args []string) int {
	fs := flag.NewFlagSet("ghostcam update", flag.ExitOnError)
	checkOnly := fs.Bool("check", false, "only check, don't install")
	_ = fs.Parse(args)
	tr := newTranslator(server.LoadSettings(filepath.Join(appDataDir(), "settings.json"), server.Settings{}).Language)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	upd := update.ForRepo(ghostcam.Repo)
	upd.CLI = flavor == "cli"
	rel, err := upd.Check(ctx, version)
	if err != nil {
		fmt.Fprintln(os.Stderr, tr.t("pc.update.checkFailed", map[string]any{"detail": err.Error()}))
		return 1
	}
	if rel == nil {
		fmt.Printf("%s (%s)\n", tr.t("pc.update.upToDate", nil), version)
		return 0
	}
	fmt.Println(tr.t("pc.update.available", map[string]any{"version": rel.Version}))
	if *checkOnly {
		fmt.Println(rel.Page)
		return 0
	}
	exe, err := update.Executable()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	err = upd.Apply(ctx, rel, exe, func(done, total int64) {
		fmt.Printf("\r%s", tr.t("pc.update.downloading", map[string]any{"done": done >> 20, "total": total >> 20}))
	})
	fmt.Println()
	if err != nil {
		fmt.Fprintln(os.Stderr, tr.t("pc.update.failed", map[string]any{"detail": err.Error()}))
		return 1
	}
	fmt.Printf("GhostCam %s ✓\n", rel.Version)
	if _, running := runningSettings(); running {
		fmt.Println("A GhostCam is running: restart it to use the new version.")
	}
	return 0
}
