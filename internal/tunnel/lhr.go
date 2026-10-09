// SPDX-License-Identifier: AGPL-3.0-or-later

package tunnel

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

// LocalhostRunAddr is the SSH endpoint of localhost.run.
const LocalhostRunAddr = "localhost.run:22"

var lhrURL = regexp.MustCompile(`https://[a-z0-9-]+\.lhr\.life`)

// SSHTunnel is a reverse tunnel through localhost.run, built into GhostCam:
// no binary to download, no account. The service terminates HTTPS and
// forwards each connection over SSH to the local server.
type SSHTunnel struct {
	url    string
	client *ssh.Client
	done   chan struct{}
	once   sync.Once
}

func (t *SSHTunnel) URL() string           { return t.url }
func (t *SSHTunnel) Done() <-chan struct{} { return t.done }
func (t *SSHTunnel) Stop()                 { t.once.Do(func() { _ = t.client.Close() }) }

// StartLocalhostRun opens the tunnel and returns once the public URL is known.
// knownHosts stores the server's host key on first use; a different key later
// is refused (someone in the middle of the SSH connection).
func StartLocalhostRun(ctx context.Context, addr, local, knownHosts string) (*SSHTunnel, error) {
	cfg := &ssh.ClientConfig{
		User: "nokey",
		// localhost.run accepts anonymous sessions through keyboard-interactive.
		Auth:            []ssh.AuthMethod{ssh.KeyboardInteractive(func(string, string, []string, []bool) ([]string, error) { return nil, nil })},
		HostKeyCallback: tofu(knownHosts, addr),
		Timeout:         15 * time.Second,
	}
	d := net.Dialer{Timeout: 15 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	c, chans, reqs, err := ssh.NewClientConn(conn, addr, cfg)
	if err != nil {
		conn.Close()
		return nil, err
	}
	client := ssh.NewClient(c, chans, reqs)
	t := &SSHTunnel{client: client, done: make(chan struct{})}
	go func() { _ = client.Wait(); close(t.done) }()
	fail := func(err error) (*SSHTunnel, error) { t.Stop(); return nil, err }

	ln, err := client.Listen("tcp", "localhost:80")
	if err != nil {
		return fail(fmt.Errorf("remote forward: %w", err))
	}
	go serveForward(ln, local)

	// The service reports the URL as JSON events on a session. It doesn't
	// answer channel requests, so the exec request must not wait for a reply.
	ch, chReqs, err := client.OpenChannel("session", nil)
	if err != nil {
		return fail(fmt.Errorf("session: %w", err))
	}
	go ssh.DiscardRequests(chReqs)
	if _, err := ch.SendRequest("exec", false, ssh.Marshal(struct{ Command string }{"--output json"})); err != nil {
		return fail(err)
	}
	found := make(chan string, 1)
	go readEvents(io.MultiReader(ch), found)

	select {
	case u := <-found:
		t.url = u
		return t, nil
	case <-t.done:
		return nil, errors.New("localhost.run closed the connection")
	case <-time.After(20 * time.Second):
		return fail(errors.New("timeout waiting for the localhost.run address"))
	case <-ctx.Done():
		return fail(ctx.Err())
	}
}

// readEvents parses the service's JSON events and reports the first public
// URL; later events (warnings, domain changes) are logged.
func readEvents(r io.Reader, found chan<- string) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	sent := false
	for sc.Scan() {
		var ev struct {
			Type string `json:"type"`
			Data string `json:"data"`
		}
		line := bytes.TrimSpace(sc.Bytes())
		if json.Unmarshal(line, &ev) != nil {
			continue
		}
		if u := lhrURL.FindString(ev.Data); u != "" && !sent && strings.Contains(ev.Type, "accepted") {
			found <- u
			sent = true
			continue
		}
		if ev.Type != "" {
			log.Printf("localhost.run: %s", ev.Type)
		}
	}
}

// serveForward copies every tunnelled connection to the local server.
func serveForward(ln net.Listener, local string) {
	for {
		remote, err := ln.Accept()
		if err != nil {
			return
		}
		go func() {
			defer remote.Close()
			up, err := net.DialTimeout("tcp", local, 5*time.Second)
			if err != nil {
				return
			}
			defer up.Close()
			go func() { _, _ = io.Copy(up, remote); _ = up.(*net.TCPConn).CloseWrite() }()
			_, _ = io.Copy(remote, up)
		}()
	}
}

// tofu trusts the host key seen on first connection (stored in path) and
// refuses any other key afterwards.
func tofu(path, addr string) ssh.HostKeyCallback {
	return func(_ string, _ net.Addr, key ssh.PublicKey) error {
		line := addr + " " + key.Type() + " " + base64.StdEncoding.EncodeToString(key.Marshal())
		known, err := os.ReadFile(path)
		if err == nil {
			for _, l := range strings.Split(string(known), "\n") {
				if !strings.HasPrefix(l, addr+" ") {
					continue
				}
				if strings.TrimSpace(l) == line {
					return nil
				}
				return fmt.Errorf("the %s host key changed (expected %s): refusing to connect", addr, ssh.FingerprintSHA256(key))
			}
		}
		f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = fmt.Fprintln(f, line)
		log.Printf("localhost.run: trusting host key %s", ssh.FingerprintSHA256(key))
		return err
	}
}
