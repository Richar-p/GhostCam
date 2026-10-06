// SPDX-License-Identifier: AGPL-3.0-or-later

package tunnel

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type entry struct {
	name, body string
	typ        byte
}

func writeTGZ(t *testing.T, entries []entry) string {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		h := &tar.Header{Name: e.name, Typeflag: e.typ, Mode: 0o755, Size: int64(len(e.body))}
		if e.typ == tar.TypeSymlink {
			h.Size, h.Linkname = 0, "/etc/passwd"
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if e.typ == tar.TypeReg {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	tw.Close()
	gz.Close()
	p := filepath.Join(t.TempDir(), "a.tgz")
	if err := os.WriteFile(p, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestExtractTGZ(t *testing.T) {
	src := writeTGZ(t, []entry{
		{name: "README", body: "x", typ: tar.TypeReg},
		{name: "cloudflared", typ: tar.TypeSymlink}, // must be ignored
		{name: "bin/cloudflared", body: "BINARY", typ: tar.TypeReg},
	})
	dst := filepath.Join(t.TempDir(), "out")
	if err := extractTGZ(src, "cloudflared", dst); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(dst)
	if string(got) != "BINARY" {
		t.Fatalf("extracted %q, want BINARY", got)
	}
}

func TestExtractTGZMissing(t *testing.T) {
	src := writeTGZ(t, []entry{{name: "other", body: "x", typ: tar.TypeReg}})
	dst := filepath.Join(t.TempDir(), "out")
	err := extractTGZ(src, "cloudflared", dst)
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("want not found error, got %v", err)
	}
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Fatal("no file must be written when the entry is missing")
	}
}

func TestExtractTGZNotGzip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "bad.tgz")
	os.WriteFile(p, []byte("not an archive"), 0o600)
	if err := extractTGZ(p, "cloudflared", filepath.Join(t.TempDir(), "out")); err == nil {
		t.Fatal("want an error for a non-gzip file")
	}
}
