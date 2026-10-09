// SPDX-License-Identifier: AGPL-3.0-or-later

package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"slices"
)

// Quality is sent to the phone, which applies it at the start of each video
// (camera constraints + encoder bitrate). Bitrate is what the phone's uplink
// must sustain: above it, WebRTC degrades the picture and the relay queues
// data on the phone (= seconds not yet safe on the PC).
type Quality struct {
	ID      string `json:"id"` // label and description: pc.quality.<id>.* in web/locales
	Width   int    `json:"width"`
	Height  int    `json:"height"`
	FPS     int    `json:"fps"`
	Bitrate int    `json:"bitrate"` // bits/s
}

var qualities = []Quality{
	{ID: "eco", Width: 854, Height: 480, FPS: 24, Bitrate: 800_000},
	{ID: "standard", Width: 1280, Height: 720, FPS: 30, Bitrate: 2_000_000},
	{ID: "high", Width: 1920, Height: 1080, FPS: 30, Bitrate: 4_000_000},
	{ID: "max", Width: 1920, Height: 1080, FPS: 30, Bitrate: 8_000_000},
}

const defaultQuality = "standard"

func qualityByID(id string) (Quality, bool) {
	for _, q := range qualities {
		if q.ID == id {
			return q, true
		}
	}
	return Quality{}, false
}

type Settings struct {
	Quality  string `json:"quality"`
	OutDir   string `json:"outDir"`
	Language string `json:"language"` // PC window language; "" = follow the system
	// Buffer is the phone-side buffer in seconds (0 = real time). Above 0 the
	// phone sends its MediaRecorder stream over a reliable channel: network
	// drops delay the video instead of freezing it. The buffer lives in the
	// phone's RAM only, never in its storage.
	Buffer int `json:"buffer"`
	// NoUpdateCheck turns off the automatic check for new releases (on by
	// default: the zero value keeps existing settings files checking).
	NoUpdateCheck bool `json:"noUpdateCheck"`
}

// buffers are the allowed Settings.Buffer values, in seconds.
var buffers = []int{0, 5, 15, 30}

// LoadSettings reads the settings file; missing or invalid values fall back to def.
func LoadSettings(path string, def Settings) Settings {
	s := def
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &s)
	}
	if _, ok := qualityByID(s.Quality); !ok {
		s.Quality = def.Quality
	}
	if s.Quality == "" {
		s.Quality = defaultQuality
	}
	if s.OutDir == "" || !filepath.IsAbs(s.OutDir) {
		s.OutDir = def.OutDir
	}
	if !slices.Contains(buffers, s.Buffer) {
		s.Buffer = 0
	}
	return s
}

// setOnSettings registers a callback run after the settings change, so the
// connected phone applies them from its next video (nil to unregister).
func (s *Server) setOnSettings(fn func()) {
	s.mu.Lock()
	s.onSettings = fn
	s.mu.Unlock()
}

func (s *Server) settings() Settings {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.set
}

func (s *Server) outDir() string { return s.settings().OutDir }

func (s *Server) quality() Quality {
	q, _ := qualityByID(s.settings().Quality)
	return q
}

// updateSettings validates, applies (from the next video on) and persists.
func (s *Server) updateSettings(n Settings) error {
	if _, ok := qualityByID(n.Quality); !ok {
		return keyErr("error.unknownQuality", nil)
	}
	if n.Language != "" && !slices.Contains(s.locales(), n.Language) {
		return keyErr("error.unknownLanguage", nil)
	}
	if !slices.Contains(buffers, n.Buffer) {
		return keyErr("error.badRequest", nil)
	}
	n.OutDir = filepath.Clean(n.OutDir)
	if !filepath.IsAbs(n.OutDir) {
		return keyErr("error.dirNotAbsolute", nil)
	}
	if err := checkWritable(n.OutDir); err != nil {
		return keyErr("error.dirUnusable", map[string]any{"detail": err.Error()})
	}
	s.mu.Lock()
	s.set = n
	notify := s.onSettings
	s.mu.Unlock()
	if notify != nil {
		notify()
	}
	if s.cfg.SettingsPath == "" {
		return nil
	}
	b, _ := json.MarshalIndent(n, "", "  ")
	tmp := s.cfg.SettingsPath + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.cfg.SettingsPath)
}

func checkWritable(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".ghostcam-probe-*")
	if err != nil {
		return err
	}
	name := f.Name()
	f.Close()
	return os.Remove(name)
}

func (s *Server) settingsHandlers(mux *http.ServeMux) {
	type view struct {
		Settings
		Qualities []Quality `json:"qualities"`
		CanPick   bool      `json:"canPick"`
		Locales   []string  `json:"locales"`
		Buffers   []int     `json:"buffers"`
	}
	writeJSON := func(w http.ResponseWriter, v any, status ...int) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		if len(status) > 0 {
			w.WriteHeader(status[0])
		}
		_ = json.NewEncoder(w).Encode(v)
	}
	mux.HandleFunc("GET /api/settings", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, view{Settings: s.settings(), Qualities: qualities, CanPick: s.cfg.PickDir != nil, Locales: s.locales(), Buffers: buffers})
	})
	mux.HandleFunc("POST /api/settings", func(w http.ResponseWriter, r *http.Request) {
		if !guard(w, r) {
			return
		}
		var n Settings
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&n); err != nil {
			writeJSON(w, Msg{Key: "error.badRequest"}, http.StatusBadRequest)
			return
		}
		if err := s.updateSettings(n); err != nil {
			var ke *KeyError
			if !errors.As(err, &ke) {
				ke = &KeyError{Msg{Key: "error.saveFailed", Vars: map[string]any{"detail": err.Error()}}}
			}
			writeJSON(w, ke.Msg, http.StatusBadRequest)
			return
		}
		writeJSON(w, view{Settings: s.settings(), Qualities: qualities, CanPick: s.cfg.PickDir != nil, Locales: s.locales(), Buffers: buffers})
	})
	// Native folder picker; the UI then saves the result with POST /api/settings.
	mux.HandleFunc("POST /api/pick-folder", func(w http.ResponseWriter, r *http.Request) {
		if !guard(w, r) {
			return
		}
		if s.cfg.PickDir == nil {
			http.Error(w, "not available", http.StatusNotImplemented)
			return
		}
		dir, err := s.cfg.PickDir(s.outDir())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, map[string]string{"path": dir}) // "" = cancelled
	})
}

// guard requires the custom header that forces a CORS preflight, so other
// sites cannot trigger state-changing admin calls.
func guard(w http.ResponseWriter, r *http.Request) bool {
	if r.Header.Get("X-GhostCam") != "1" {
		http.Error(w, "forbidden", http.StatusForbidden)
		return false
	}
	return true
}
