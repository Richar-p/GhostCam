// SPDX-License-Identifier: AGPL-3.0-or-later

package server

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"path"
	"slices"
	"strings"
)

// Msg is a user-facing message sent to the UI as a translation key plus
// variables. The server never sends display text: web/locales/*.json
// (i18next JSON v4) holds every string, keys included in this package.
type Msg struct {
	Key  string         `json:"key"`
	Vars map[string]any `json:"vars,omitempty"`
}

// KeyError is an error the UI can translate.
type KeyError struct{ Msg }

func (e *KeyError) Error() string { return e.Key }

func keyErr(key string, vars map[string]any) error {
	return &KeyError{Msg{Key: key, Vars: vars}}
}

// locales lists the available translation codes (web/locales/<code>.json).
func (s *Server) locales() []string {
	files, _ := fs.Glob(s.cfg.Web, "locales/*.json")
	out := make([]string, 0, len(files))
	for _, f := range files {
		out = append(out, strings.TrimSuffix(path.Base(f), ".json"))
	}
	slices.Sort(out)
	return out
}

// localeHandlers serves /i18n.js, /locales (list) and /locales/<code>.json
// on both the public (phone) and admin (PC) servers.
func (s *Server) localeHandlers(mux *http.ServeMux) {
	mux.HandleFunc("GET /i18n.js", s.static("i18n.js", "text/javascript; charset=utf-8"))
	mux.HandleFunc("GET /locales", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(s.locales())
	})
	mux.HandleFunc("GET /locales/{file}", func(w http.ResponseWriter, r *http.Request) {
		code := strings.TrimSuffix(r.PathValue("file"), ".json")
		if !slices.Contains(s.locales(), code) || !strings.HasSuffix(r.PathValue("file"), ".json") {
			http.NotFound(w, r)
			return
		}
		s.static("locales/"+code+".json", "application/json; charset=utf-8")(w, r)
	})
}
