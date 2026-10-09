// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"golang.org/x/term"

	"ghostcam/internal/server"
	"ghostcam/internal/tunnel"
	"ghostcam/internal/update"
	"ghostcam/internal/upnp"
	"ghostcam/web"

	ghostcam "ghostcam"
)

// version is set at build time from the VERSION file:
// go build -ldflags "-X main.version=$(cat VERSION)". "dev" never self-updates.
var version = "dev"

// How often a running GhostCam looks for a new release.
const updateEvery = 6 * time.Hour

// app runs GhostCam until it is closed (window, Ctrl+C or update restart).
func app(args []string, ui uiMode) {
	flags := flag.NewFlagSet("ghostcam run", flag.ExitOnError)
	var (
		listen     = flags.String("listen", "127.0.0.1:8080", "public HTTP listener (tunnel target, keep on loopback)")
		admin      = flags.String("admin", "127.0.0.1:8081", "local admin UI listener (never exposed)")
		rtcPort    = flags.Int("rtc-port", 50000, "UDP port for WebRTC media (mapped with UPnP)")
		outDir     = flags.String("out", "", "recordings directory (overrides the saved setting)")
		publicURL  = flags.String("public-url", "", "use this HTTPS base URL instead of starting a Cloudflare quick tunnel")
		cfBin      = flags.String("cloudflared", "", "path to cloudflared (default: downloaded and kept up to date automatically)")
		tunnelFlag = flags.String("tunnel", "", "tunnel provider for this run: cloudflare, localhostrun (default: the saved setting)")
		stun       = flags.String("stun", "stun:stun.cloudflare.com:3478,stun:stun.l.google.com:19302", "comma-separated STUN URLs (empty to disable)")
		turnURL    = flags.String("turn", os.Getenv("GHOSTCAM_TURN_URL"), "TURN URL, e.g. turns:turn.example.org:5349 (env GHOSTCAM_TURN_URL)")
		turnUser   = flags.String("turn-user", os.Getenv("GHOSTCAM_TURN_USER"), "TURN username (env GHOSTCAM_TURN_USER)")
		noUPnP     = flags.Bool("no-upnp", false, "do not try to open the UDP port on the router")
		headless   = flags.Bool("headless", false, "no window: QR code and status in the terminal (same as ghostcam run)")
		webDir     = flags.String("web-dir", "", "serve web assets from this directory (dev live edit)")
		waitPID    = flags.Int(update.WaitPIDFlag, 0, "internal: wait for this process to exit first (set when restarting after an update)")
		updAPI     = flags.String("update-api", "", "internal, tests: releases API URL")
		updPrefix  = flags.String("update-prefix", "", "internal, tests: allowed download URL prefix")
	)
	_ = flags.Parse(args)

	// After an update, the previous instance is still closing its recordings
	// and releasing its ports: wait for it before doing anything.
	if *waitPID > 0 {
		update.WaitExit(*waitPID, 30*time.Second)
	}

	dataDir := appDataDir()
	setupLog(dataDir)
	log.Printf("GhostCam %s", version)

	var ice []server.ICEServer
	if s := strings.TrimSpace(*stun); s != "" {
		ice = append(ice, server.ICEServer{URLs: strings.Split(s, ",")})
	}
	if *turnURL != "" {
		// Password from env only, so it never shows up in the process list.
		ice = append(ice, server.ICEServer{URLs: []string{*turnURL}, Username: *turnUser, Credential: os.Getenv("GHOSTCAM_TURN_PASS")})
	}
	var assets fs.FS = web.FS
	if *webDir != "" {
		assets = os.DirFS(*webDir)
	}

	settingsPath := filepath.Join(dataDir, "settings.json")
	settings := server.LoadSettings(settingsPath, server.Settings{Quality: "standard", OutDir: defaultOutDir()})
	if *outDir != "" {
		abs, err := filepath.Abs(*outDir)
		if err != nil {
			log.Fatal(err)
		}
		settings.OutDir = abs
	}

	srv, err := server.New(server.Config{
		PublicAddr: *listen, AdminAddr: *admin, RTCPort: *rtcPort,
		Settings: settings, SettingsPath: settingsPath,
		ICEServers: ice, Web: assets, OpenDir: openPath, PickDir: pickDir,
		Notices: ghostcam.Notices(), SourceURL: ghostcam.Repo, Version: version,
	})
	if err != nil {
		log.Fatal(err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// ------------------------------------------------------------ updates
	upd := update.ForRepo(ghostcam.Repo)
	upd.CLI = flavor == "cli"
	if *updAPI != "" {
		upd.API, upd.Prefix = *updAPI, *updPrefix
	}
	exe, exeErr := update.Executable()
	var restart atomic.Bool
	checkUpdate := func(manual bool) {
		cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		rel, err := upd.Check(cctx, version)
		if err != nil {
			log.Printf("update check: %v", err)
			if manual {
				srv.SetUpdateState("pc.update.checkFailed", map[string]any{"detail": err.Error()})
			}
			return
		}
		srv.SetUpdateAvailable(rel)
		switch {
		case rel != nil:
			log.Printf("update available: %s", rel.Version)
			srv.SetUpdateState("", nil)
		case manual:
			srv.SetUpdateState("pc.update.upToDate", nil)
		}
	}
	var updMu sync.Mutex
	updating := false
	applyUpdate := func() error {
		if srv.Recording() {
			return server.ErrorKey("pc.update.recording")
		}
		if exeErr != nil {
			return exeErr
		}
		updMu.Lock()
		defer updMu.Unlock()
		rel := srv.UpdateAvailable()
		if updating || rel == nil {
			return nil
		}
		updating = true
		go func() {
			log.Printf("updating to %s", rel.Version)
			srv.SetUpdateState("pc.update.downloading", map[string]any{"done": 0, "total": rel.Size >> 20})
			err := upd.Apply(ctx, rel, exe, func(done, total int64) {
				srv.SetUpdateState("pc.update.downloading", map[string]any{"done": done >> 20, "total": total >> 20})
			})
			if err != nil {
				log.Printf("update failed: %v", err)
				srv.SetUpdateState("pc.update.failed", map[string]any{"detail": err.Error()})
				updMu.Lock()
				updating = false
				updMu.Unlock()
				return
			}
			log.Printf("update %s installed, restarting", rel.Version)
			srv.SetUpdateState("pc.update.restarting", nil)
			restart.Store(true)
			time.Sleep(1500 * time.Millisecond) // let the window show it
			stop()
		}()
		return nil
	}
	srv.SetUpdateHooks(func() { checkUpdate(true) }, applyUpdate)
	go func() {
		// The previous binary is kept until this one has run for a minute.
		if exeErr == nil {
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Minute):
				update.Cleanup(exe)
			}
		}
	}()
	go func() {
		next := time.After(10 * time.Second)
		for {
			select {
			case <-ctx.Done():
				return
			case <-next:
				if !srv.CurrentSettings().NoUpdateCheck {
					checkUpdate(false)
				}
				next = time.After(updateEvery)
			}
		}
	}()

	go func() {
		if err := srv.ServePublic(ctx); err != nil {
			srv.SetError("error.publicListener", err, map[string]any{"addr": *listen})
		}
	}()
	go func() {
		if err := srv.ServeAdmin(ctx); err != nil {
			log.Fatalf("admin listener: %v", err) // no UI without it
		}
	}()

	// Network setup runs in the background so the window shows up at once
	// and displays progress / errors.
	netDone := make(chan struct{})
	go func() {
		defer close(netDone)
		mapped := make(chan *upnp.Mapping, 1)
		if !*noUPnP {
			go func() {
				m, err := upnp.Map(uint16(*rtcPort), "GhostCam")
				if err != nil {
					log.Printf("upnp: %v (falling back to STUN/TURN/relay)", err)
					return
				}
				log.Printf("upnp: %s:%d/udp -> local :%d", m.ExternalIP, m.Port, *rtcPort)
				srv.SetUPnP(m)
				mapped <- m
			}()
		}
		defer func() {
			select {
			case m := <-mapped:
				m.Close()
			default:
			}
		}()
		log.Printf("recordings: %s", settings.OutDir)
		runTunnels(ctx, srv, tunnelOpts{
			forceURL: *publicURL, forceProvider: *tunnelFlag, cfBin: *cfBin, listen: *listen,
			binDir: filepath.Join(dataDir, "bin"), knownHosts: filepath.Join(dataDir, "known_hosts"),
		})
	}()

	adminURL := "http://localhost:" + (*admin)[strings.LastIndex(*admin, ":")+1:] + "/"
	log.Printf("admin UI: %s", adminURL)
	if *headless {
		ui = uiTerminal
	}
	tr := newTranslator(settings.Language)
	switch {
	case ui == uiWindow && runWindow(ctx, adminURL, filepath.Join(dataDir, "webview")):
		// window closed by the user
	case ui == uiTerminal:
		terminalUI(ctx, srv, tr, adminURL)
	default:
		// No native window (macOS, Linux, or no WebView2): the same page opens
		// in the browser, the terminal shows the QR code and status too.
		_ = openPath(adminURL)
		terminalUI(ctx, srv, tr, adminURL)
	}

	log.Printf("shutting down…")
	stop()
	srv.Close() // flush and close the current recording
	<-netDone
	if restart.Load() {
		// New binary in place: start it; it waits for this process to exit.
		if err := update.Restart(exe); err != nil {
			log.Printf("restart after update: %v", err)
		}
	}
}

type tunnelOpts struct {
	forceURL      string // -public-url: the user's own address, overrides the setting
	forceProvider string // -tunnel
	cfBin         string
	listen        string // local server the tunnel forwards to
	binDir        string // managed cloudflared
	knownHosts    string // localhost.run host key (trust on first use)
}

// runTunnels keeps the phone page reachable from the Internet until ctx ends:
// it opens the chosen provider, reconnects when the tunnel drops, and
// switches when the setting changes. Each new URL means a new QR code.
func runTunnels(ctx context.Context, srv *server.Server, o tunnelOpts) {
	current := func() (string, string) {
		if o.forceURL != "" {
			return tunnel.Custom, o.forceURL
		}
		set := srv.CurrentSettings()
		if o.forceProvider != "" {
			return o.forceProvider, set.TunnelURL
		}
		return set.Tunnel, set.TunnelURL
	}
	// changed waits up to d (forever if 0) for the setting to change.
	changed := func(provider, custom string, d time.Duration, down <-chan struct{}) bool {
		tick := time.NewTicker(time.Second)
		defer tick.Stop()
		var timeout <-chan time.Time
		if d > 0 {
			timeout = time.After(d)
		}
		for {
			select {
			case <-ctx.Done():
				return false
			case <-down:
				return false
			case <-timeout:
				return false
			case <-tick.C:
				if p, c := current(); p != provider || c != custom {
					return true
				}
			}
		}
	}
	for ctx.Err() == nil {
		provider, custom := current()
		srv.SetPublicURL("")
		t, errKey, err := openTunnel(ctx, provider, custom, o, srv.SetProgress)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			srv.SetError(errKey, err, nil)
			changed(provider, custom, 30*time.Second, nil) // retry, sooner if the setting changes
			srv.ClearError()
			continue
		}
		srv.ClearError()
		if provider != tunnel.Custom {
			// A fresh hostname can take a few seconds to resolve: show the QR
			// code only once the phone can actually reach it.
			srv.SetProgress("progress.publicCheck", nil)
			waitReachable(ctx, t.URL(), 30*time.Second)
		}
		srv.SetPublicURL(t.URL())
		log.Printf("public URL (%s): %s", provider, t.URL())
		switched := changed(provider, custom, 0, t.Done())
		t.Stop()
		if ctx.Err() != nil {
			return
		}
		if !switched {
			log.Printf("tunnel %s lost, reconnecting", provider)
			srv.SetProgress("progress.tunnelLost", nil)
			select {
			case <-ctx.Done():
				return
			case <-time.After(3 * time.Second):
			}
		}
	}
}

// openTunnel starts one provider. On failure it returns the translation key
// of the error to show.
func openTunnel(ctx context.Context, provider, custom string, o tunnelOpts, progress tunnel.Progress) (tunnel.Tunnel, string, error) {
	switch provider {
	case tunnel.Custom:
		if custom == "" {
			return nil, "error.tunnelURL", errors.New("no custom address set")
		}
		return tunnel.NewStatic(strings.TrimRight(custom, "/")), "", nil
	case tunnel.LocalhostRun:
		progress("progress.tunnel", nil)
		t, err := tunnel.StartLocalhostRun(ctx, tunnel.LocalhostRunAddr, o.listen, o.knownHosts)
		if err != nil {
			return nil, "error.tunnel", err
		}
		return t, "", nil
	default:
		t, key, err := startTunnel(ctx, o.cfBin, o.listen, o.binDir, progress)
		if err != nil {
			return nil, key, err
		}
		return t, "", nil
	}
}

// startTunnel uses -cloudflared if given; otherwise a managed copy in the app
// data folder, downloaded and updated automatically (single-file install).
// A cloudflared next to the exe or in PATH is only a fallback.
// On failure it returns the translation key of the error to show.
func startTunnel(ctx context.Context, bin, listen, binDir string, progress tunnel.Progress) (*tunnel.CloudflaredTunnel, string, error) {
	if bin == "" {
		var err error
		if bin, err = tunnel.Ensure(ctx, binDir, progress); err != nil {
			log.Printf("cloudflared: %v", err)
			fb, ferr := tunnel.FindBinary()
			if ferr != nil {
				return nil, "error.cloudflared", err
			}
			bin = fb
		}
	}
	progress("progress.tunnel", nil)
	log.Printf("starting Cloudflare quick tunnel (%s)…", bin)
	t, err := tunnel.StartQuick(ctx, bin, "http://"+listen)
	if err != nil {
		return nil, "error.tunnel", err
	}
	return t, "", nil
}

// waitReachable waits until the phone page actually loads through the tunnel
// (HTTP 200: a fresh tunnel can briefly answer 530). The hostname is resolved
// with 1.1.1.1 directly: querying the local resolver (often the router) too
// early would cache the "no such host" answer and break the phone's first
// attempt when it is on the same Wi-Fi.
func waitReachable(ctx context.Context, base string, max time.Duration) {
	r := &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, network, "1.1.1.1:53")
	}}
	client := &http.Client{Timeout: 4 * time.Second, Transport: &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			ips, err := r.LookupHost(ctx, host)
			if err != nil || len(ips) == 0 {
				return nil, fmt.Errorf("resolve %s: %v", host, err)
			}
			var d net.Dialer
			return d.DialContext(ctx, network, net.JoinHostPort(ips[0], port))
		},
	}}
	deadline := time.Now().Add(max)
	for time.Now().Before(deadline) && ctx.Err() == nil {
		if resp, err := client.Get(base + "/"); err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		select {
		case <-ctx.Done():
		case <-time.After(time.Second):
		}
	}
	log.Printf("phone page not reachable through the tunnel after %s, showing the QR code anyway", max)
}

func defaultOutDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "recordings"
	}
	if runtime.GOOS == "darwin" {
		return filepath.Join(home, "Movies", "GhostCam")
	}
	return filepath.Join(home, "Videos", "GhostCam")
}

// appDataDir holds settings, logs, cloudflared and the WebView2 profile:
// %LOCALAPPDATA%\GhostCam on Windows, ~/Library/Application Support/GhostCam
// on macOS, ~/.config/GhostCam on Linux (not a cache dir, which may be wiped).
func appDataDir() string {
	base, err := os.UserConfigDir()
	if runtime.GOOS == "windows" {
		base, err = os.UserCacheDir() // %LOCALAPPDATA%, unchanged since 1.0
	}
	if err != nil {
		base = os.TempDir()
	}
	dir := filepath.Join(base, "GhostCam")
	_ = os.MkdirAll(dir, 0o700)
	return dir
}

// The Windows build has no console, so logs also go to a file.
// In a terminal showing the live status, logs go to the file only (they
// would break the display); otherwise (Docker, redirected output) to stderr too.
func setupLog(dir string) {
	f, err := os.OpenFile(filepath.Join(dir, "ghostcam.log"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return
	}
	if term.IsTerminal(int(os.Stdout.Fd())) {
		log.SetOutput(f)
		return
	}
	log.SetOutput(io.MultiWriter(os.Stderr, f))
}

// openPath opens a URL or a folder with the OS default handler.
func openPath(target string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		if strings.HasPrefix(target, "http") {
			cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", target)
		} else {
			cmd = exec.Command("explorer.exe", target)
		}
	case "darwin":
		cmd = exec.Command("open", target)
	default:
		cmd = exec.Command("xdg-open", target)
	}
	return cmd.Start()
}
