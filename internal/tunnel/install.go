// SPDX-License-Identifier: AGPL-3.0-or-later

package tunnel

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

const releasesAPI = "https://api.github.com/repos/cloudflare/cloudflared/releases/latest"

type release struct {
	Tag    string
	URL    string
	SHA256 string // of the downloaded asset
	Size   int64
	// For .tgz assets, Cloudflare's release notes list the checksum of the
	// binary inside the archive (GitHub's digest covers the archive itself).
	InnerSHA256 string
}

type installedInfo struct {
	Tag    string `json:"tag"`
	SHA256 string `json:"sha256"`
}

// Progress reports a startup step as a translation key (web/locales) + variables.
type Progress func(key string, vars map[string]any)

// Ensure returns a verified, up-to-date cloudflared kept in dir, downloading
// it from Cloudflare's official GitHub releases when missing or outdated.
// Offline, an already installed copy is used as is.
//
// A download is accepted only if its SHA-256 matches the digest GitHub
// publishes for the asset (and the checksum Cloudflare lists in the release
// notes, when present) and, on Windows, if it carries a valid Authenticode
// signature from "Cloudflare, Inc.".
func Ensure(ctx context.Context, dir string, progress Progress) (string, error) {
	asset, exe, err := assetName()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	bin := filepath.Join(dir, exe)
	cur, haveCur := readInstalled(dir, bin)

	rctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	rel, err := latestRelease(rctx, asset)
	cancel()
	if err != nil {
		if haveCur {
			log.Printf("cloudflared: update check failed (%v), using installed %s", err, cur.Tag)
			return bin, nil
		}
		return "", fmt.Errorf("cannot reach GitHub releases: %w", err)
	}
	if haveCur && cur.Tag == rel.Tag {
		return bin, nil
	}

	if haveCur {
		progress("progress.cfUpdate", map[string]any{"from": cur.Tag, "to": rel.Tag})
	} else {
		progress("progress.cfFirst", map[string]any{"version": rel.Tag})
	}
	tmp := bin + ".download"
	staged := tmp
	err = download(ctx, rel, tmp, progress)
	if err == nil && strings.HasSuffix(asset, ".tgz") {
		// macOS assets are archives: the SHA-256 above covered the archive,
		// extract the binary from it.
		staged = bin + ".extracted"
		err = extractTGZ(tmp, "cloudflared", staged)
	}
	if err == nil {
		progress("progress.cfSignature", nil)
		err = verifySignature(staged)
	}
	var sum string
	if err == nil {
		sum, err = fileSHA256(staged)
	}
	if err == nil && rel.InnerSHA256 != "" && sum != rel.InnerSHA256 {
		err = fmt.Errorf("extracted binary SHA-256 %s does not match release notes %s", sum, rel.InnerSHA256)
	}
	if err == nil {
		err = os.Rename(staged, bin)
	}
	_ = os.Remove(tmp)
	if err != nil {
		_ = os.Remove(staged)
		if haveCur {
			log.Printf("cloudflared: update to %s failed (%v), keeping %s", rel.Tag, err, cur.Tag)
			return bin, nil
		}
		return "", fmt.Errorf("install: %w", err)
	}
	// Hash of the installed binary (not of the archive), re-checked at each launch.
	b, _ := json.Marshal(installedInfo{Tag: rel.Tag, SHA256: sum})
	if err := os.WriteFile(filepath.Join(dir, "cloudflared.json"), b, 0o600); err != nil {
		return "", err
	}
	log.Printf("cloudflared: installed %s", rel.Tag)
	return bin, nil
}

func assetName() (asset, exe string, err error) {
	switch runtime.GOOS + "/" + runtime.GOARCH {
	case "windows/amd64":
		return "cloudflared-windows-amd64.exe", "cloudflared.exe", nil
	case "windows/386":
		return "cloudflared-windows-386.exe", "cloudflared.exe", nil
	case "linux/amd64":
		return "cloudflared-linux-amd64", "cloudflared", nil
	case "linux/arm64":
		return "cloudflared-linux-arm64", "cloudflared", nil
	case "darwin/amd64":
		return "cloudflared-darwin-amd64.tgz", "cloudflared", nil
	case "darwin/arm64":
		return "cloudflared-darwin-arm64.tgz", "cloudflared", nil
	}
	return "", "", fmt.Errorf("no automatic cloudflared download for %s/%s", runtime.GOOS, runtime.GOARCH)
}

// readInstalled reports the installed version, after checking the binary still
// has the hash recorded at install time.
func readInstalled(dir, bin string) (installedInfo, bool) {
	var cur installedInfo
	b, err := os.ReadFile(filepath.Join(dir, "cloudflared.json"))
	if err != nil || json.Unmarshal(b, &cur) != nil || cur.Tag == "" {
		return cur, false
	}
	sum, err := fileSHA256(bin)
	if err != nil || sum != cur.SHA256 {
		log.Printf("cloudflared: installed binary missing or modified, reinstalling")
		return cur, false
	}
	return cur, true
}

func latestRelease(ctx context.Context, asset string) (release, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, releasesAPI, nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "GhostCam")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return release{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return release{}, fmt.Errorf("GitHub API: %s", resp.Status)
	}
	var r struct {
		TagName string `json:"tag_name"`
		Body    string `json:"body"`
		Assets  []struct {
			Name   string `json:"name"`
			URL    string `json:"browser_download_url"`
			Digest string `json:"digest"`
			Size   int64  `json:"size"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&r); err != nil {
		return release{}, err
	}
	for _, a := range r.Assets {
		if a.Name != asset {
			continue
		}
		digest := strings.ToLower(strings.TrimPrefix(a.Digest, "sha256:"))
		notes := ""
		if m := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(asset) + `:\s*([0-9a-fA-F]{64})\s*$`).FindStringSubmatch(r.Body); m != nil {
			notes = strings.ToLower(m[1])
		}
		inner := ""
		if strings.HasSuffix(asset, ".tgz") {
			// Two independent checks: archive vs GitHub, binary vs release notes.
			if digest == "" {
				return release{}, errors.New("no published checksum for " + asset)
			}
			inner = notes
		} else {
			switch {
			case digest == "" && notes == "":
				return release{}, errors.New("no published checksum for " + asset)
			case digest != "" && notes != "" && digest != notes:
				return release{}, errors.New("GitHub digest and release-notes checksum disagree for " + asset)
			case digest == "":
				digest = notes
			}
		}
		if !strings.HasPrefix(a.URL, "https://github.com/cloudflare/cloudflared/releases/download/") {
			return release{}, errors.New("unexpected download URL " + a.URL)
		}
		return release{Tag: r.TagName, URL: a.URL, SHA256: digest, Size: a.Size, InnerSHA256: inner}, nil
	}
	return release{}, fmt.Errorf("asset %s not found in release %s", asset, r.TagName)
}

func download(ctx context.Context, rel release, dst string, progress Progress) error {
	dctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	req, _ := http.NewRequestWithContext(dctx, http.MethodGet, rel.URL, nil)
	req.Header.Set("User-Agent", "GhostCam")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download: %s", resp.Status)
	}
	f, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o700)
	if err != nil {
		return err
	}
	h := sha256.New()
	pw := &progressWriter{total: rel.Size, tag: rel.Tag, report: progress}
	// Read at most the announced size (+1 byte to detect an oversized body).
	n, err := io.Copy(io.MultiWriter(f, h, pw), io.LimitReader(resp.Body, rel.Size+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if n != rel.Size {
		return fmt.Errorf("size mismatch: got %d bytes, expected %d", n, rel.Size)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != rel.SHA256 {
		return fmt.Errorf("SHA-256 mismatch: got %s, expected %s", got, rel.SHA256)
	}
	return nil
}

type progressWriter struct {
	total, done int64
	last        time.Time
	tag         string
	report      Progress
}

func (p *progressWriter) Write(b []byte) (int, error) {
	p.done += int64(len(b))
	if time.Since(p.last) > 300*time.Millisecond {
		p.last = time.Now()
		p.report("progress.cfDownload", map[string]any{"version": p.tag, "done": p.done >> 20, "total": p.total >> 20})
	}
	return len(b), nil
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
