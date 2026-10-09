// SPDX-License-Identifier: AGPL-3.0-or-later

// Package tunnel exposes the local signaling server on a public HTTPS URL
// through a Cloudflare Quick Tunnel (no account, no router configuration:
// cloudflared only makes outbound connections).
package tunnel

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

var urlRe = regexp.MustCompile(`https://[a-z0-9-]+\.trycloudflare\.com`)

// CloudflaredTunnel is a Cloudflare Quick Tunnel run by a cloudflared child process.
type CloudflaredTunnel struct {
	url  string
	cmd  *exec.Cmd
	done chan struct{}
}

func (t *CloudflaredTunnel) URL() string           { return t.url }
func (t *CloudflaredTunnel) Done() <-chan struct{} { return t.done }

// FindBinary looks for cloudflared next to our executable, then in PATH.
func FindBinary() (string, error) {
	name := "cloudflared"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if exe, err := os.Executable(); err == nil {
		p := filepath.Join(filepath.Dir(exe), name)
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return exec.LookPath(name)
}

// StartQuick runs `cloudflared tunnel --url <local>` and waits until the tunnel
// is both allocated (URL printed) and registered with the edge.
func StartQuick(ctx context.Context, bin, local string) (*CloudflaredTunnel, error) {
	cmd := exec.CommandContext(ctx, bin, "tunnel", "--no-autoupdate", "--url", local)
	hideWindow(cmd)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start cloudflared: %w", err)
	}
	bindToParent(cmd)

	type result struct {
		url string
		err error
	}
	ready := make(chan result, 1)
	go func() {
		sc := bufio.NewScanner(stderr)
		var url string
		sent := false
		for sc.Scan() {
			line := sc.Text()
			if u := urlRe.FindString(line); u != "" && !strings.HasPrefix(u, "https://api.") && url == "" {
				url = u
			}
			if !sent && url != "" && strings.Contains(line, "Registered tunnel connection") {
				ready <- result{url: url}
				sent = true
			}
		}
		if !sent {
			ready <- result{err: errors.New("cloudflared exited before the tunnel was ready")}
		}
		_, _ = io.Copy(io.Discard, stderr)
	}()

	select {
	case r := <-ready:
		if r.err != nil {
			_ = cmd.Wait()
			return nil, r.err
		}
		t := &CloudflaredTunnel{url: r.url, cmd: cmd, done: make(chan struct{})}
		go func() { _ = cmd.Wait(); close(t.done) }() // cloudflared exited: tunnel down
		return t, nil
	case <-time.After(45 * time.Second):
		_ = cmd.Process.Kill()
		return nil, errors.New("timeout waiting for cloudflared tunnel")
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (t *CloudflaredTunnel) Stop() {
	if t.cmd.Process != nil {
		_ = t.cmd.Process.Kill()
	}
	<-t.done
}
