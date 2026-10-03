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
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	qrcode "github.com/skip2/go-qrcode"

	"github.com/p3374/GhostCam/internal/server"
	"github.com/p3374/GhostCam/internal/tunnel"
	"github.com/p3374/GhostCam/internal/upnp"
	"github.com/p3374/GhostCam/web"

	ghostcam "github.com/p3374/GhostCam"
)

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
	)
	flag.Parse()

	dataDir := appDataDir()
	setupLog(dataDir)

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
		Notices: ghostcam.Notices(),
	})
	if err != nil {
		log.Fatal(err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		if err := srv.ServePublic(ctx); err != nil {
			srv.SetError(fmt.Errorf("serveur public (%s) : %w", *listen, err))
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
			t, err := startTunnel(ctx, *cfBin, *listen, filepath.Join(dataDir, "bin"), srv.SetProgress)
			if err != nil {
				srv.SetError(err)
				<-ctx.Done()
				return
			}
			defer t.Stop()
			base = t.URL
			// A fresh quick-tunnel hostname can take a few seconds to resolve:
			// show the QR code only once the phone can actually reach it.
			srv.SetProgress("Vérification du lien public…")
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
		_ = openPath(adminURL)
		<-ctx.Done()
	}

	log.Printf("shutting down…")
	stop()
	srv.Close() // flush and close the current recording
	<-netDone
}

// startTunnel uses -cloudflared if given; otherwise a managed copy in the app
// data folder, downloaded and updated automatically (single-file install).
// A cloudflared next to the exe or in PATH is only a fallback.
func startTunnel(ctx context.Context, bin, listen, binDir string, progress func(string)) (*tunnel.Tunnel, error) {
	if bin == "" {
		var err error
		if bin, err = tunnel.Ensure(ctx, binDir, progress); err != nil {
			log.Printf("cloudflared: %v", err)
			fb, ferr := tunnel.FindBinary()
			if ferr != nil {
				return nil, err
			}
			bin = fb
		}
	}
	progress("Ouverture du lien sécurisé…")
	log.Printf("starting Cloudflare quick tunnel (%s)…", bin)
	t, err := tunnel.StartQuick(ctx, bin, "http://"+listen)
	if err != nil {
		return nil, fmt.Errorf("tunnel Cloudflare : %w", err)
	}
	return t, nil
}

// waitReachable waits until the tunnel hostname resolves on public DNS. It
// asks 1.1.1.1 directly: querying the local resolver (often the router) too
// early would cache the "no such host" answer and break the phone's first
// attempt when it is on the same Wi-Fi.
func waitReachable(ctx context.Context, base string, max time.Duration) {
	u, err := url.Parse(base)
	if err != nil {
		return
	}
	r := &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, network, "1.1.1.1:53")
	}}
	deadline := time.Now().Add(max)
	for time.Now().Before(deadline) && ctx.Err() == nil {
		lctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		addrs, err := r.LookupHost(lctx, u.Hostname())
		cancel()
		if err == nil && len(addrs) > 0 {
			return
		}
		select {
		case <-ctx.Done():
		case <-time.After(time.Second):
		}
	}
	log.Printf("tunnel hostname not visible on public DNS after %s, showing it anyway", max)
}

func defaultOutDir() string {
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, "Videos", "GhostCam")
	}
	return "recordings"
}

// appDataDir holds the log file and the WebView2 profile (%LOCALAPPDATA%\GhostCam).
func appDataDir() string {
	base, err := os.UserCacheDir()
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
