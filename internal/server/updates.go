// SPDX-License-Identifier: AGPL-3.0-or-later

package server

import (
	"encoding/json"
	"errors"
	"net/http"

	"ghostcam/internal/update"
)

// Update state shown in the PC window. The update itself (download, verify,
// replace, restart) is driven by the main package through Config hooks.

// SetUpdateAvailable records the newer release found (nil: up to date).
func (s *Server) SetUpdateAvailable(rel *update.Release) {
	s.mu.Lock()
	s.updAvail = rel
	s.mu.Unlock()
}

// SetUpdateState shows an update step or result (key "" clears it).
func (s *Server) SetUpdateState(key string, vars map[string]any) {
	s.mu.Lock()
	if key == "" {
		s.updState = nil
	} else {
		s.updState = &Msg{Key: key, Vars: vars}
	}
	s.mu.Unlock()
}

// UpdateAvailable returns the newer release found, if any.
func (s *Server) UpdateAvailable() *update.Release {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.updAvail
}

// Recording reports whether a video is being recorded (no update meanwhile).
func (s *Server) Recording() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state == "recording"
}

// updateStatus is the "update" part of /api/status.
func (s *Server) updateStatus() map[string]any {
	st := map[string]any{"current": s.cfg.Version}
	if s.updAvail != nil {
		st["version"], st["page"] = s.updAvail.Version, s.updAvail.Page
	}
	if s.updState != nil {
		st["state"] = s.updState
	}
	return st
}

func (s *Server) updateHandlers(mux *http.ServeMux) {
	// Install the available update (download, verify, replace, restart).
	mux.HandleFunc("POST /api/update", func(w http.ResponseWriter, r *http.Request) {
		if !guard(w, r) {
			return
		}
		if s.cfg.ApplyUpdate == nil || s.UpdateAvailable() == nil {
			http.NotFound(w, r)
			return
		}
		if err := s.cfg.ApplyUpdate(); err != nil {
			var ke *KeyError
			if !errors.As(err, &ke) {
				ke = &KeyError{Msg{Key: "pc.update.failed", Vars: map[string]any{"detail": err.Error()}}}
			}
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusConflict)
			_ = json.NewEncoder(w).Encode(ke.Msg)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	})
	// Check now (Settings button), whatever the automatic check setting.
	mux.HandleFunc("POST /api/update/check", func(w http.ResponseWriter, r *http.Request) {
		if !guard(w, r) {
			return
		}
		if s.cfg.CheckUpdate != nil {
			go s.cfg.CheckUpdate()
		}
		w.WriteHeader(http.StatusAccepted)
	})
}

// SetUpdateHooks connects the window's update actions to the main package
// (set once, before serving).
func (s *Server) SetUpdateHooks(check func(), apply func() error) {
	s.cfg.CheckUpdate, s.cfg.ApplyUpdate = check, apply
}

// CurrentSettings returns the settings in effect.
func (s *Server) CurrentSettings() Settings { return s.settings() }

// ErrorKey returns an error the UI shows translated (key in web/locales).
func ErrorKey(key string) error { return keyErr(key, nil) }
