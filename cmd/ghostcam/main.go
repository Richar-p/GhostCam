// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
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

	qrcode "github.com/skip2/go-qrcode"

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

func main() {
	var (
		listen    = flag.String("listen", "127.0.0.1:8080", "public HTTP listener (tunnel target, keep on loopback)")
		admin     = flag.String("admin", "127.0.0.1:8081", "local admin UI listener (never exposed)")
		rtcPort   = flag.Int("rtc-port", 50000, "UDP port for WebRTC media (mapped with UPnP)")
		outDir    = flag.String("out", "", "recordings directory (overrides the saved setting)")
		publicURL = flag.String("public-url", "", "use this HTTPS base URL instead of starting a Cloudflare quick tunnel")
		cfBin     = flag.String("cloudflared", "", "path to cloudflared (default: downloaded and kept up to date automatically)")
		stun      = flag.String("stun", "stun:stun.cloudflare.com:3478,stun:stun.l.google.com:19302", "comma-separated STUN URLs (empty to disable)")
		turnURL   = flag.String("turn", os.Getenv("GHOSTCAM_TURN_URL"), "TURN URL, e.g. turns:turn.example.org:5349 (env GHOSTCAM_TURN_URL)")
		turnUser  = flag.String("turn-user", os.Getenv("GHOSTCAM_TURN_USER"), "TURN username (env GHOSTCAM_TURN_USER)")
		noUPnP    = flag.Bool("no-upnp", false, "do not try to open the UDP port on the router")
		headless  = flag.Bool("headless", false, "no window: print the QR code in the terminal (servers, Docker)")
		webDir    = flag.String("web-dir", "", "serve web assets from this directory (dev live edit)")
		waitPID   = flag.Int(update.WaitPIDFlag, 0, "internal: wait for this process to exit first (set when restarting after an update)")
		updAPI    = flag.String("update-api", "", "internal, tests: releases API URL")
		updPrefix = flag.String("update-prefix", "", "internal, tests: allowed download URL prefix")
	)
	flag.Parse()

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
		base := *publicURL
		if base == "" {
			t, errKey, err := startTunnel(ctx, *cfBin, *listen, filepath.Join(dataDir, "bin"), srv.SetProgress)
			if err != nil {
				srv.SetError(errKey, err, nil)
				<-ctx.Done()
				return
			}
			defer t.Stop()
			base = t.URL
			// A fresh quick-tunnel hostname can take a few seconds to resolve:
			// show the QR code only once the phone can actually reach it.
			srv.SetProgress("progress.publicCheck", nil)
			waitReachable(ctx, base, 30*time.Second)
		}
		srv.SetPublicURL(base)
		log.Printf("public URL: %s", base)
		log.Printf("recordings: %s", settings.OutDir)
		if *headless {
			if q, err := qrcode.New(srv.PairingURL(), qrcode.Medium); err == nil {
				fmt.Println(q.ToSmallString(false))
			}
		}
		<-ctx.Done()
	}()

	adminURL := "http://localhost:" + (*admin)[strings.LastIndex(*admin, ":")+1:] + "/"
	log.Printf("admin UI: %s", adminURL)
	switch {
	case *headless:
		<-ctx.Done()
	case runWindow(ctx, adminURL, filepath.Join(dataDir, "webview")):
		// window closed by the user
	default:
		// No native window (macOS, Linux): the same page opens in the browser.
		_ = openPath(adminURL)
		fmt.Printf("\nGhostCam is running. Open %s in your browser if it didn't open.\nVideos: %s\nPress Ctrl+C to quit.\n\n", adminURL, settings.OutDir)
		<-ctx.Done()
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

// startTunnel uses -cloudflared if given; otherwise a managed copy in the app
// data folder, downloaded and updated automatically (single-file install).
// A cloudflared next to the exe or in PATH is only a fallback.
// On failure it returns the translation key of the error to show.
func startTunnel(ctx context.Context, bin, listen, binDir string, progress tunnel.Progress) (*tunnel.Tunnel, string, error) {
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
func setupLog(dir string) {
	f, err := os.OpenFile(filepath.Join(dir, "ghostcam.log"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
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
