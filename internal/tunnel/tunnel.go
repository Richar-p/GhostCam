// SPDX-License-Identifier: AGPL-3.0-or-later

package tunnel

import "sync"

// Tunnel makes the local server reachable at a public HTTPS URL.
type Tunnel interface {
	URL() string
	Done() <-chan struct{} // closed when the tunnel goes down
	Stop()
}

// Providers, as stored in the settings.
const (
	Cloudflare   = "cloudflare"   // Cloudflare Quick Tunnel (cloudflared, downloaded automatically)
	LocalhostRun = "localhostrun" // localhost.run, SSH tunnel built into GhostCam
	Custom       = "custom"       // the user's own HTTPS URL forwarding to the local server
)

// Static is a custom URL: GhostCam doesn't run anything, the user's own
// tunnel or reverse proxy forwards it to the local server.
type Static struct {
	url  string
	done chan struct{}
	once sync.Once
}

func NewStatic(url string) *Static { return &Static{url: url, done: make(chan struct{})} }

func (t *Static) URL() string           { return t.url }
func (t *Static) Done() <-chan struct{} { return t.done }
func (t *Static) Stop()                 { t.once.Do(func() { close(t.done) }) }
