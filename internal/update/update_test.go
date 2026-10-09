// SPDX-License-Identifier: AGPL-3.0-or-later

package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestNewer(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"1.3.0", "1.2.0", true},
		{"1.10.0", "1.9.9", true},
		{"2.0.0", "1.99.99", true},
		{"1.2.0", "1.2.0", false},
		{"1.2.0", "1.3.0", false},
		{"v1.3.0", "1.2.0", true},
		{"1.3.0", "dev", false}, // dev builds never update
		{"1.3", "1.2.0", false}, // malformed tag
		{"1.3.0-rc1", "1.2.0", false},
	}
	for _, c := range cases {
		if got := Newer(c.a, c.b); got != c.want {
			t.Errorf("Newer(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

// fakeGitHub serves a "latest release" with this platform's asset.
func fakeGitHub(t *testing.T, tag string, body []byte, digest string) (*httptest.Server, *Updater) {
	t.Helper()
	asset := AssetName(runtime.GOOS, runtime.GOARCH)
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	prefix := srv.URL + "/owner/repo/releases/download/"
	mux.HandleFunc("/api/latest", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tag_name": tag, "html_url": srv.URL + "/notes",
			"assets": []map[string]any{{"name": asset, "browser_download_url": prefix + tag + "/" + asset, "digest": "sha256:" + digest, "size": len(body)}},
		})
	})
	mux.HandleFunc("/owner/repo/releases/download/", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(body) })
	t.Cleanup(srv.Close)
	return srv, &Updater{API: srv.URL + "/api/latest", Prefix: prefix, Client: srv.Client()}
}

func sum(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

func TestCheckAndApply(t *testing.T) {
	newBin := []byte("NEW BINARY v1.3.0")
	_, u := fakeGitHub(t, "v1.3.0", newBin, sum(newBin))

	if rel, err := u.Check(context.Background(), "1.3.0"); err != nil || rel != nil {
		t.Fatalf("same version: want no update, got %v, %v", rel, err)
	}
	rel, err := u.Check(context.Background(), "1.2.0")
	if err != nil || rel == nil || rel.Version != "1.3.0" {
		t.Fatalf("want 1.3.0, got %+v, %v", rel, err)
	}

	exe := filepath.Join(t.TempDir(), "ghostcam")
	os.WriteFile(exe, []byte("OLD BINARY"), 0o755)
	var progressed bool
	if err := u.Apply(context.Background(), rel, exe, func(done, total int64) { progressed = true }); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(exe); string(got) != string(newBin) {
		t.Fatalf("exe not replaced: %q", got)
	}
	if got, _ := os.ReadFile(exe + ".old"); string(got) != "OLD BINARY" {
		t.Fatalf("previous binary not kept: %q", got)
	}
	if _, err := os.Stat(exe + ".new"); !os.IsNotExist(err) {
		t.Fatal("temporary download left behind")
	}
	_ = progressed // tiny file: progress may not fire, that's fine
	Cleanup(exe)
	if _, err := os.Stat(exe + ".old"); !os.IsNotExist(err) {
		t.Fatal("Cleanup must remove the previous binary")
	}
}

func TestApplyRejectsTamperedBinary(t *testing.T) {
	published := []byte("GENUINE")
	tampered := []byte("EVIL!!!") // same size, different content
	_, u := fakeGitHub(t, "v9.0.0", tampered, sum(published))
	rel, err := u.Check(context.Background(), "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(t.TempDir(), "ghostcam")
	os.WriteFile(exe, []byte("OLD BINARY"), 0o755)
	err = u.Apply(context.Background(), rel, exe, nil)
	if err == nil || !strings.Contains(err.Error(), "SHA-256 mismatch") {
		t.Fatalf("want SHA-256 mismatch, got %v", err)
	}
	if got, _ := os.ReadFile(exe); string(got) != "OLD BINARY" {
		t.Fatal("the current binary must be untouched when verification fails")
	}
	if _, err := os.Stat(exe + ".new"); !os.IsNotExist(err) {
		t.Fatal("rejected download must be deleted")
	}
}

func TestCheckRejectsForeignURL(t *testing.T) {
	body := []byte("X")
	_, u := fakeGitHub(t, "v9.0.0", body, sum(body))
	u.Prefix = "https://github.com/someone-else/repo/releases/download/"
	if _, err := u.Check(context.Background(), "1.0.0"); err == nil || !strings.Contains(err.Error(), "unexpected download URL") {
		t.Fatalf("want unexpected download URL, got %v", err)
	}
}

func TestForRepo(t *testing.T) {
	u := ForRepo("https://github.com/p3374/GhostCam")
	if u.API != "https://api.github.com/repos/p3374/GhostCam/releases/latest" || u.Prefix != "https://github.com/p3374/GhostCam/releases/download/" {
		t.Fatalf("%+v", u)
	}
}

func TestWaitExit(t *testing.T) {
	if !WaitExit(999999, time.Second) { // no such process
		t.Fatal("a missing process must count as exited")
	}
	if WaitExit(os.Getpid(), 300*time.Millisecond) {
		t.Fatal("the test process itself is alive")
	}
}
