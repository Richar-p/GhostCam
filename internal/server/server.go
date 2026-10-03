// SPDX-License-Identifier: AGPL-3.0-or-later

// Package server hosts the two HTTP listeners:
//   - public (reached through the tunnel): mobile web app + WebSocket signaling
//   - admin (127.0.0.1 only, never tunneled): QR code and recording status
package server

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/pion/webrtc/v4"
	qrcode "github.com/skip2/go-qrcode"

	"github.com/p3374/GhostCam/internal/pairing"
	"github.com/p3374/GhostCam/internal/record"
	"github.com/p3374/GhostCam/internal/upnp"
)

type ICEServer struct {
	URLs       []string `json:"urls"`
	Username   string   `json:"username,omitempty"`
	Credential string   `json:"credential,omitempty"`
}

type Config struct {
	PublicAddr   string // tunnel target, e.g. 127.0.0.1:8080
	AdminAddr    string // local UI, e.g. 127.0.0.1:8081
	RTCPort      int    // single UDP port for all WebRTC traffic
	Settings     Settings
	SettingsPath string // where settings are persisted ("" = not persisted)
	ICEServers   []ICEServer
	Web          fs.FS                              // index.html, app.js, admin.html
	OpenDir      func(dir string) error             // reveal recordings in the OS file manager
	PickDir      func(start string) (string, error) // native folder picker (nil if none)
	Notices      string                             // license texts shown at /licenses
}

type session struct {
	cancel context.CancelFunc
	done   chan struct{}
}

type Server struct {
	cfg         Config
	secret      *pairing.Secret
	api         *webrtc.API
	cert        webrtc.Certificate
	fingerprint string // sha-256 of our DTLS cert, hex, pinned in the QR code

	sessMu sync.Mutex
	cur    *session

	mu        sync.Mutex
	set       Settings
	publicURL string
	mapping   *upnp.Mapping
	err       string // fatal startup error shown in the UI (e.g. tunnel down)
	progress  string // startup step shown while the tunnel is not ready
	state     string
	mode      string
	file      *record.File
	count     int    // videos finished since start
	last      string // last finished video
	lastBytes int64
}

func New(cfg Config) (*Server, error) {
	secret, err := pairing.New()
	if err != nil {
		return nil, err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	cert, err := webrtc.GenerateCertificate(key)
	if err != nil {
		return nil, err
	}
	fps, err := cert.GetFingerprints()
	if err != nil {
		return nil, err
	}
	var fp string
	for _, f := range fps {
		if f.Algorithm == "sha-256" {
			fp = strings.ToLower(strings.ReplaceAll(f.Value, ":", ""))
		}
	}
	if fp == "" {
		return nil, errors.New("no sha-256 DTLS fingerprint")
	}
	api, err := newWebRTCAPI(cfg.RTCPort)
	if err != nil {
		return nil, err
	}
	return &Server{cfg: cfg, secret: secret, api: api, cert: *cert, fingerprint: fp, state: "waiting", set: cfg.Settings}, nil
}

func (s *Server) SetPublicURL(u string) {
	s.mu.Lock()
	s.publicURL = strings.TrimRight(u, "/")
	s.mu.Unlock()
}

func (s *Server) SetProgress(msg string) {
	s.mu.Lock()
	s.progress = msg
	s.mu.Unlock()
}

func (s *Server) SetError(err error) {
	log.Printf("error: %v", err)
	s.mu.Lock()
	s.err = err.Error()
	s.mu.Unlock()
}

func (s *Server) SetUPnP(m *upnp.Mapping) {
	s.mu.Lock()
	s.mapping = m
	s.mu.Unlock()
}

// PairingURL carries the secret and the pinned DTLS fingerprint in the URL
// fragment, which browsers never send to the server (nor to the tunnel).
func (s *Server) PairingURL() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.publicURL == "" {
		return ""
	}
	return s.publicURL + "/#t=" + s.secret.Token() + "&f=" + s.fingerprint
}

// recorded notes a finished video for the admin UI.
func (s *Server) recorded(path string, n int64) {
	if path == "" {
		return
	}
	log.Printf("saved %s (%d bytes)", path, n)
	s.mu.Lock()
	s.count, s.last, s.lastBytes = s.count+1, filepath.Base(path), n
	s.mu.Unlock()
}

func (s *Server) setStatus(state, mode string, f *record.File) {
	s.mu.Lock()
	s.state, s.mode, s.file = state, mode, f
	s.mu.Unlock()
}

// ---------------------------------------------------------------- public side

func (s *Server) ServePublic(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.static("index.html", "text/html; charset=utf-8"))
	mux.HandleFunc("GET /app.js", s.static("app.js", "text/javascript; charset=utf-8"))
	mux.HandleFunc("GET /icon.svg", s.static("icon.svg", "image/svg+xml"))
	mux.HandleFunc("GET /icon.png", s.static("icon.png", "image/png"))
	mux.HandleFunc("GET /ws", s.handleWS)
	return serve(ctx, s.cfg.PublicAddr, publicHeaders(mux))
}

func publicHeaders(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hd := w.Header()
		hd.Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'unsafe-inline'; img-src 'self'; connect-src 'self'; media-src 'self' blob: mediastream:; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
		hd.Set("Referrer-Policy", "no-referrer")
		hd.Set("X-Content-Type-Options", "nosniff")
		hd.Set("Permissions-Policy", "camera=(self), microphone=(self)")
		hd.Set("Cache-Control", "no-store")
		h.ServeHTTP(w, r)
	})
}

type helloMsg struct {
	Type       string      `json:"type"`
	Nonce      string      `json:"nonce"`
	ICEServers []ICEServer `json:"iceServers"`
	Quality    Quality     `json:"quality"`
}

type clientMsg struct {
	Type string `json:"type"` // "offer" (WebRTC) or "relay" (fallback)
	SDP  string `json:"sdp,omitempty"`
	MAC  string `json:"mac"`
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	// Default options reject cross-origin WebSockets (Origin must match Host).
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer c.CloseNow()
	c.SetReadLimit(16 << 20)
	ctx := r.Context()

	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return
	}
	if err := wsjson.Write(ctx, c, helloMsg{Type: "hello", Nonce: base64.RawURLEncoding.EncodeToString(nonce), ICEServers: s.cfg.ICEServers, Quality: s.quality()}); err != nil {
		return
	}

	var m clientMsg
	rctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	err = wsjson.Read(rctx, c, &m)
	cancel()
	if err != nil {
		return
	}
	if (m.Type != "offer" && m.Type != "relay") || !s.secret.Verify(nonce, m.Type, m.SDP, m.MAC) {
		log.Printf("ws: rejected unauthenticated client")
		c.Close(websocket.StatusPolicyViolation, "unauthorized")
		return
	}

	// One phone at a time. A new authenticated session (e.g. after switching
	// from Wi-Fi to 4G) preempts the previous one instead of waiting for it.
	sctx, cur := s.beginSession(ctx)
	defer s.endSession(cur)

	if m.Type == "offer" {
		err = s.runWebRTC(sctx, c, m.SDP)
	} else {
		err = s.runRelay(sctx, c, nonce)
	}
	if err != nil && !errors.Is(err, context.Canceled) {
		log.Printf("session (%s) ended: %v", m.Type, err)
	} else {
		log.Printf("session (%s) ended", m.Type)
	}
}

func (s *Server) beginSession(ctx context.Context) (context.Context, *session) {
	s.sessMu.Lock()
	defer s.sessMu.Unlock()
	if s.cur != nil {
		s.cur.cancel()
		<-s.cur.done
	}
	sctx, cancel := context.WithCancel(ctx)
	s.cur = &session{cancel: cancel, done: make(chan struct{})}
	return sctx, s.cur
}

// Close stops the active session so its recording is flushed and closed.
func (s *Server) Close() {
	s.sessMu.Lock()
	cur := s.cur
	s.sessMu.Unlock()
	if cur != nil {
		cur.cancel()
		<-cur.done
	}
}

func (s *Server) endSession(cur *session) {
	cur.cancel()
	s.setStatus("waiting", "", nil)
	close(cur.done)
	s.sessMu.Lock()
	if s.cur == cur {
		s.cur = nil
	}
	s.sessMu.Unlock()
}

// ----------------------------------------------------------------- admin side

func (s *Server) ServeAdmin(ctx context.Context) error {
	_, port, err := net.SplitHostPort(s.cfg.AdminAddr)
	if err != nil {
		return err
	}
	allowed := map[string]bool{"127.0.0.1:" + port: true, "localhost:" + port: true, "[::1]:" + port: true}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.static("admin.html", "text/html; charset=utf-8"))
	mux.HandleFunc("GET /icon.svg", s.static("icon.svg", "image/svg+xml"))
	mux.HandleFunc("GET /qr.png", func(w http.ResponseWriter, r *http.Request) {
		u := s.PairingURL()
		if u == "" {
			http.Error(w, "tunnel not ready", http.StatusServiceUnavailable)
			return
		}
		png, err := qrcode.Encode(u, qrcode.Medium, 512)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(png)
	})
	mux.HandleFunc("GET /api/status", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		st := map[string]any{"url": "", "state": s.state, "mode": s.mode, "file": "", "bytes": 0, "seconds": 0, "upnp": "", "error": s.err, "progress": s.progress, "outDir": s.set.OutDir,
			"count": s.count, "last": s.last, "lastBytes": s.lastBytes}
		if q, ok := qualityByID(s.set.Quality); ok {
			st["quality"], st["targetBitrate"] = q.Label, q.Bitrate
		}
		if s.file != nil {
			st["file"], st["bytes"] = filepath.Base(s.file.Path()), s.file.Written()
			st["seconds"] = int(time.Since(s.file.Started()).Seconds())
		}
		if s.mapping != nil {
			st["upnp"] = fmt.Sprintf("%s:%d/udp", s.mapping.ExternalIP, s.mapping.Port)
		}
		s.mu.Unlock()
		st["url"] = s.PairingURL()
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(st)
	})

	// The custom header forces a CORS preflight, so other sites cannot POST here.
	mux.HandleFunc("POST /api/open-folder", func(w http.ResponseWriter, r *http.Request) {
		if !guard(w, r) || s.cfg.OpenDir == nil {
			return
		}
		dir := s.outDir()
		err := os.MkdirAll(dir, 0o700)
		if err == nil {
			err = s.cfg.OpenDir(dir)
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	s.settingsHandlers(mux)
	// Opens the licenses page or the source repository in the default browser
	// (the app window has no navigation controls).
	mux.HandleFunc("POST /api/open/{what}", func(w http.ResponseWriter, r *http.Request) {
		if !guard(w, r) || s.cfg.OpenDir == nil {
			return
		}
		targets := map[string]string{
			"licenses": "http://" + r.Host + "/licenses",
			"source":   "https://github.com/p3374/GhostCam",
		}
		u, ok := targets[r.PathValue("what")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_ = s.cfg.OpenDir(u)
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /licenses", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = io.WriteString(w, s.cfg.Notices)
	})

	// Host check defeats DNS rebinding: a web page cannot read the token
	// through a hostname that resolves to 127.0.0.1.
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !allowed[r.Host] {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; img-src 'self'; connect-src 'self'; frame-ancestors 'none'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		mux.ServeHTTP(w, r)
	})
	return serve(ctx, s.cfg.AdminAddr, h)
}

// ---------------------------------------------------------------------- utils

func (s *Server) static(name, ctype string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		b, err := fs.ReadFile(s.cfg.Web, name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", ctype)
		_, _ = w.Write(b)
	}
}

func serve(ctx context.Context, addr string, h http.Handler) error {
	srv := &http.Server{Addr: addr, Handler: h, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
