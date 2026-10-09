// SPDX-License-Identifier: AGPL-3.0-or-later

// Package update finds a newer GhostCam release on GitHub, downloads the
// binary for this platform, verifies it and replaces the running executable.
//
// A download is accepted only if it comes from this repository's release
// download URL, has the announced size and matches the SHA-256 digest GitHub
// publishes for the asset. (A detached signature could be added later: Verify
// is the single place to extend.)
package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Release is a newer version available for this platform.
type Release struct {
	Version string `json:"version"` // without the leading "v"
	Page    string `json:"page"`    // release notes URL
	URL     string `json:"-"`
	SHA256  string `json:"-"`
	Size    int64  `json:"size"`
}

// Updater checks one GitHub repository.
type Updater struct {
	API    string // https://api.github.com/repos/<owner>/<repo>/releases/latest
	Prefix string // assets must be downloaded from here: https://github.com/<owner>/<repo>/releases/download/
	Client *http.Client
}

// ForRepo returns an Updater for a https://github.com/<owner>/<repo> URL.
func ForRepo(repo string) *Updater {
	path := strings.TrimPrefix(strings.TrimRight(repo, "/"), "https://github.com/")
	return &Updater{
		API:    "https://api.github.com/repos/" + path + "/releases/latest",
		Prefix: "https://github.com/" + path + "/releases/download/",
		Client: &http.Client{Timeout: 10 * time.Minute},
	}
}

// AssetName is the release file for this platform (see scripts/build-release.sh).
func AssetName(goos, goarch string) string {
	switch goos + "/" + goarch {
	case "windows/amd64":
		return "ghostcam.exe"
	case "darwin/arm64":
		return "ghostcam-macos-apple-silicon"
	case "darwin/amd64":
		return "ghostcam-macos-intel"
	case "linux/amd64":
		return "ghostcam-linux-x64"
	case "linux/arm64":
		return "ghostcam-linux-arm64"
	}
	return ""
}

// Check returns the latest release if it is newer than current, nil otherwise.
func (u *Updater) Check(ctx context.Context, current string) (*Release, error) {
	asset := AssetName(runtime.GOOS, runtime.GOARCH)
	if asset == "" {
		return nil, fmt.Errorf("no release binary for %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u.API, nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "GhostCam")
	resp, err := u.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API: %s", resp.Status)
	}
	var r struct {
		Tag    string `json:"tag_name"`
		Page   string `json:"html_url"`
		Draft  bool   `json:"draft"`
		Pre    bool   `json:"prerelease"`
		Assets []struct {
			Name   string `json:"name"`
			URL    string `json:"browser_download_url"`
			Digest string `json:"digest"`
			Size   int64  `json:"size"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&r); err != nil {
		return nil, err
	}
	latest := strings.TrimPrefix(r.Tag, "v")
	if r.Draft || r.Pre || !Newer(latest, current) {
		return nil, nil
	}
	for _, a := range r.Assets {
		if a.Name != asset {
			continue
		}
		sum := strings.ToLower(strings.TrimPrefix(a.Digest, "sha256:"))
		if len(sum) != 64 {
			return nil, fmt.Errorf("no SHA-256 digest published for %s", asset)
		}
		if !strings.HasPrefix(a.URL, u.Prefix) {
			return nil, fmt.Errorf("unexpected download URL %s", a.URL)
		}
		return &Release{Version: latest, Page: r.Page, URL: a.URL, SHA256: sum, Size: a.Size}, nil
	}
	return nil, fmt.Errorf("release %s has no %s", r.Tag, asset)
}

// Progress reports downloaded and total bytes.
type Progress func(done, total int64)

// Apply downloads rel, verifies it and replaces the executable at exe. The
// previous binary is kept as exe+".old" (removed by Cleanup at next start).
func (u *Updater) Apply(ctx context.Context, rel *Release, exe string, progress Progress) error {
	tmp := exe + ".new"
	defer os.Remove(tmp)
	if err := u.download(ctx, rel, tmp, progress); err != nil {
		return err
	}
	return replace(exe, tmp)
}

func (u *Updater) download(ctx context.Context, rel *Release, dst string, progress Progress) error {
	if !strings.HasPrefix(rel.URL, u.Prefix) {
		return fmt.Errorf("unexpected download URL %s", rel.URL)
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, rel.URL, nil)
	req.Header.Set("User-Agent", "GhostCam")
	resp, err := u.client().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download: %s", resp.Status)
	}
	f, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	h := sha256.New()
	var done int64
	last := time.Time{}
	buf := make([]byte, 64<<10)
	body := io.LimitReader(resp.Body, rel.Size+1)
	for {
		n, rerr := body.Read(buf)
		if n > 0 {
			if _, err := f.Write(buf[:n]); err != nil {
				f.Close()
				return err
			}
			h.Write(buf[:n])
			done += int64(n)
			if progress != nil && time.Since(last) > 250*time.Millisecond {
				last = time.Now()
				progress(done, rel.Size)
			}
		}
		if errors.Is(rerr, io.EOF) {
			break
		}
		if rerr != nil {
			f.Close()
			return rerr
		}
	}
	if err := f.Close(); err != nil {
		return err
	}
	return Verify(dst, done, h.Sum(nil), rel)
}

// Verify checks a downloaded file against the release metadata.
func Verify(path string, size int64, sum []byte, rel *Release) error {
	if size != rel.Size {
		return fmt.Errorf("size mismatch: got %d bytes, expected %d", size, rel.Size)
	}
	if got := hex.EncodeToString(sum); got != rel.SHA256 {
		return fmt.Errorf("SHA-256 mismatch: got %s, expected %s", got, rel.SHA256)
	}
	return nil
}

// replace swaps exe for next. Renaming a running executable is allowed on
// Windows, macOS and Linux; the running process keeps its open image.
func replace(exe, next string) error {
	old := exe + ".old"
	_ = os.Remove(old)
	if err := os.Rename(exe, old); err != nil {
		return fmt.Errorf("cannot move the current executable (folder not writable?): %w", err)
	}
	if err := os.Rename(next, exe); err != nil {
		_ = os.Rename(old, exe) // put the working version back
		return err
	}
	return nil
}

// Cleanup removes the previous binary left by a successful update.
func Cleanup(exe string) {
	_ = os.Remove(exe + ".old")
}

// Executable returns the path of the running binary, symlinks resolved.
func Executable() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(exe)
}

func (u *Updater) client() *http.Client {
	if u.Client != nil {
		return u.Client
	}
	return http.DefaultClient
}

// Newer reports whether version a (x.y.z) is greater than b. A non-release
// current version ("dev") never updates.
func Newer(a, b string) bool {
	pa, okA := parse(a)
	pb, okB := parse(b)
	if !okA || !okB {
		return false
	}
	for i := range pa {
		if pa[i] != pb[i] {
			return pa[i] > pb[i]
		}
	}
	return false
}

func parse(v string) ([3]int, bool) {
	var out [3]int
	parts := strings.Split(strings.TrimPrefix(v, "v"), ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}
