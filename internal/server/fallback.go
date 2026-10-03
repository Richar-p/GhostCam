// SPDX-License-Identifier: AGPL-3.0-or-later

package server

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"ghostcam/internal/record"
)

var allowedMimes = map[string]string{
	"video/webm;codecs=vp8,opus": ".webm",
	"video/webm;codecs=vp9,opus": ".webm",
	"video/webm":                 ".webm",
	"video/mp4":                  ".mp4",
}

// Relay frame types, inside the AES-GCM plaintext (first byte). Control
// messages are encrypted too, so the tunnel cannot start or stop a video.
const (
	frameStart = 1 // payload: MIME type of the MediaRecorder stream
	frameData  = 2 // payload: MediaRecorder chunk
	frameStop  = 3
)

// runRelay is used when no WebRTC path exists (UDP blocked, symmetric NAT on
// both sides, no TURN). The phone's MediaRecorder sends 1 s chunks over the
// WebSocket, end-to-end encrypted: the tunnel only sees ciphertext. Like
// WebRTC, one connection carries any number of start/stop videos.
func (s *Server) runRelay(ctx context.Context, c *websocket.Conn, nonce []byte) error {
	opener, err := s.secret.NewOpener(nonce)
	if err != nil {
		return err
	}
	var f *record.File
	stop := func() (string, int64) {
		if f == nil {
			return "", 0
		}
		_ = f.Close()
		path, n := f.Path(), f.Written()
		s.recorded(path, n)
		f = nil
		return path, n
	}
	defer stop() // connection lost mid-video: keep what was received

	if err := wsjson.Write(ctx, c, map[string]string{"type": "ready"}); err != nil {
		return err
	}
	s.setStatus("connected", "relay", nil)
	for {
		// The phone sends a chunk every second while recording and a ping
		// every 10 s otherwise; silence means it is gone.
		rctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		typ, data, err := c.Read(rctx)
		cancel()
		if err != nil {
			return err
		}
		if typ != websocket.MessageBinary {
			continue // ping
		}
		pt, err := opener.Open(data)
		if err != nil {
			return fmt.Errorf("decrypt: %w", err)
		}
		if len(pt) == 0 {
			continue
		}
		switch pt[0] {
		case frameStart:
			stop()
			ext, ok := allowedMimes[strings.ReplaceAll(strings.ToLower(string(pt[1:])), " ", "")]
			if !ok {
				return fmt.Errorf("unsupported mime %q", pt[1:])
			}
			if f, err = record.Create(s.outDir(), "relay", ext); err != nil {
				return err
			}
			log.Printf("recording to %s", f.Path())
			s.setStatus("recording", "relay", f)
			_ = wsjson.Write(ctx, c, map[string]any{"type": "started", "quality": s.quality()})
		case frameData:
			if f == nil {
				continue
			}
			if _, err := f.Write(pt[1:]); err != nil {
				return err
			}
		case frameStop:
			path, n := stop()
			s.setStatus("connected", "relay", nil)
			_ = wsjson.Write(ctx, c, map[string]any{"type": "stopped", "file": filepath.Base(path), "bytes": n})
		}
	}
}
