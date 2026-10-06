// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build integration

package tunnel

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Downloads the real macOS archives from Cloudflare's latest release, checks
// their SHA-256 and extracts a Mach-O binary, without needing a Mac.
//
//	go test -tags integration -run Darwin ./internal/tunnel
func TestDarwinAssetsIntegration(t *testing.T) {
	machO := [][]byte{{0xcf, 0xfa, 0xed, 0xfe}, {0xca, 0xfe, 0xba, 0xbe}} // 64-bit LE, universal
	for _, asset := range []string{"cloudflared-darwin-arm64.tgz", "cloudflared-darwin-amd64.tgz"} {
		t.Run(asset, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()
			rel, err := latestRelease(ctx, asset)
			if err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			tgz := filepath.Join(dir, "a.tgz")
			if err := download(ctx, rel, tgz, func(string, map[string]any) {}); err != nil {
				t.Fatal(err)
			}
			bin := filepath.Join(dir, "cloudflared")
			if err := extractTGZ(tgz, "cloudflared", bin); err != nil {
				t.Fatal(err)
			}
			if sum, _ := fileSHA256(bin); rel.InnerSHA256 == "" || sum != rel.InnerSHA256 {
				t.Fatalf("binary sha256 %s, release notes %q", sum, rel.InnerSHA256)
			}
			head := make([]byte, 4)
			f, _ := os.Open(bin)
			f.Read(head)
			f.Close()
			if !bytes.Equal(head, machO[0]) && !bytes.Equal(head, machO[1]) {
				t.Fatalf("not a Mach-O binary: % x", head)
			}
			st, _ := os.Stat(bin)
			t.Logf("%s %s: archive sha256 = GitHub digest, binary sha256 = release notes, %d MB Mach-O", rel.Tag, asset, st.Size()>>20)
		})
	}
}
