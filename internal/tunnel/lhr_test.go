// SPDX-License-Identifier: AGPL-3.0-or-later

package tunnel

import (
	"crypto/ed25519"
	"crypto/rand"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

func newKey(t *testing.T) ssh.PublicKey {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	k, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func TestTOFU(t *testing.T) {
	path := filepath.Join(t.TempDir(), "known_hosts")
	check := tofu(path, "localhost.run:22")
	first, other := newKey(t), newKey(t)

	if err := check("", nil, first); err != nil {
		t.Fatalf("first key must be trusted: %v", err)
	}
	if err := check("", nil, first); err != nil {
		t.Fatalf("same key must be accepted again: %v", err)
	}
	err := check("", nil, other)
	if err == nil || !strings.Contains(err.Error(), "host key changed") {
		t.Fatalf("a different key must be refused, got %v", err)
	}
	// Another server's entry doesn't interfere.
	if err := tofu(path, "other.example:22")("", nil, other); err != nil {
		t.Fatalf("other host: %v", err)
	}
}

func TestReadEvents(t *testing.T) {
	events := `{"type":"v1.tcpip_forward.register.accepted","data":"abc tunneled with tls termination, https://1e2f3a.lhr.life\r\ncreate an account"}
{"type":"v1.notice","data":"see https://admin.localhost.run"}
`
	found := make(chan string, 1)
	readEvents(strings.NewReader(events), found)
	if got := <-found; got != "https://1e2f3a.lhr.life" {
		t.Fatalf("got %q", got)
	}
}
