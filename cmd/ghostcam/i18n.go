// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"regexp"
	"strings"

	"ghostcam/internal/server"
	"ghostcam/web"
)

// Command-line texts come from the same web/locales/*.json files as the
// interface (i18next JSON v4), so one translation covers both.

type translator struct {
	lang     string
	dict     map[string]string
	fallback map[string]string
}

var tvar = regexp.MustCompile(`\{\{\s*(\w+)\s*\}\}`)

// newTranslator picks the setting's language, else the system's, else English.
func newTranslator(setting string) *translator {
	available := server.LocalesIn(web.FS)
	lang := "en"
	for _, want := range []string{setting, systemLanguage()} {
		want = strings.ToLower(strings.ReplaceAll(want, "_", "-"))
		if want == "" {
			continue
		}
		for _, a := range available {
			if strings.ToLower(a) == want || strings.ToLower(a) == strings.SplitN(want, "-", 2)[0] {
				lang = a
				break
			}
		}
		if lang != "en" || strings.HasPrefix(want, "en") {
			break
		}
	}
	return &translator{lang: lang, dict: loadLocale(lang), fallback: loadLocale("en")}
}

func loadLocale(code string) map[string]string {
	out := map[string]string{}
	b, err := fs.ReadFile(web.FS, "locales/"+code+".json")
	if err != nil {
		return out
	}
	var tree map[string]any
	if json.Unmarshal(b, &tree) == nil {
		flatten("", tree, out)
	}
	return out
}

func flatten(prefix string, node map[string]any, out map[string]string) {
	for k, v := range node {
		key := k
		if prefix != "" {
			key = prefix + "." + k
		}
		switch v := v.(type) {
		case string:
			out[key] = v
		case map[string]any:
			flatten(key, v, out)
		}
	}
}

// t translates key; vars["count"] selects the plural form (_one/_other).
func (tr *translator) t(key string, vars map[string]any) string {
	if n, ok := vars["count"]; ok {
		form := "_other"
		if c, ok := toInt(n); ok && (c == 1 || (tr.lang == "fr" && c == 0)) {
			form = "_one"
		}
		if _, ok := tr.lookup(key + form); ok {
			key += form
		} else if _, ok := tr.lookup(key + "_other"); ok {
			key += "_other"
		}
	}
	s, ok := tr.lookup(key)
	if !ok {
		return key
	}
	return tvar.ReplaceAllStringFunc(s, func(m string) string {
		name := tvar.FindStringSubmatch(m)[1]
		if v, ok := vars[name]; ok {
			return fmt.Sprint(v)
		}
		return m
	})
}

func (tr *translator) lookup(key string) (string, bool) {
	if s, ok := tr.dict[key]; ok {
		return s, true
	}
	s, ok := tr.fallback[key]
	return s, ok
}

// msg translates a server message ({key, vars}).
func (tr *translator) msg(m any) string {
	switch m := m.(type) {
	case *server.Msg:
		if m != nil {
			return tr.t(m.Key, m.Vars)
		}
	case *server.KeyError:
		return tr.t(m.Key, m.Vars)
	}
	return ""
}

func toInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	}
	return 0, false
}

// systemLanguage reads the usual environment variables (Unix, Git Bash);
// Windows also asks the OS (see console_windows.go).
func systemLanguage() string {
	for _, k := range []string{"LC_ALL", "LC_MESSAGES", "LANG", "LANGUAGE"} {
		// "C", "C.UTF-8", "POSIX": no language preference, look further.
		if v := strings.SplitN(os.Getenv(k), ".", 2)[0]; v != "" && v != "C" && v != "POSIX" {
			return v
		}
	}
	return osLanguage()
}
