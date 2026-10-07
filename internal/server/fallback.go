// SPDX-License-Identifier: AGPL-3.0-or-later

package server

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
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
	frameLag   = 4 // payload: seconds still queued on the phone (decimal text)
)

// createForMime creates the recording file for a phone MediaRecorder stream
// (relay or buffered WebRTC), after checking its MIME type.
func createForMime(dir, mode, mime string) (*record.File, error) {
	ext, ok := allowedMimes[strings.ReplaceAll(strings.ToLower(mime), " ", "")]
	if !ok {
		return nil, fmt.Errorf("unsupported mime %q", mime)
	}
	return record.Create(dir, mode, ext)
}

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
	s.setOnSettings(func() {
		_ = wsjson.Write(ctx, c, map[string]any{"type": "config", "quality": s.quality(), "buffer": s.settings().Buffer})
	})
	defer s.setOnSettings(nil)
	s.setStatus("connected", "relay", nil)

	// Acknowledge the media bytes received, every second. The phone can't see
	// what its OS still holds in TCP buffers (WebSocket.bufferedAmount stops
	// at the socket), so it measures its real backlog as sent - acknowledged.
	var received atomic.Int64
	ackCtx, stopAcks := context.WithCancel(ctx)
	defer stopAcks()
	go func() {
		t := time.NewTicker(time.Second)
		defer t.Stop()
		last := int64(-1)
		for {
			select {
			case <-ackCtx.Done():
				return
			case <-t.C:
				if n := received.Load(); n != last {
					last = n
					_ = wsjson.Write(ackCtx, c, map[string]any{"type": "ack", "bytes": n})
				}
			}
		}
	}()

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
			if f, err = createForMime(s.outDir(), "relay", string(pt[1:])); err != nil {
				return err
			}
			log.Printf("recording to %s", f.Path())
			s.setStatus("recording", "relay", f)
			s.setBuffered(s.settings().Buffer > 0)
			_ = wsjson.Write(ctx, c, map[string]any{"type": "started", "quality": s.quality(), "buffer": s.settings().Buffer})
		case frameData:
			received.Add(int64(len(pt) - 1)) // counted like the phone counts what it sent
			if f == nil {
				continue
			}
			if _, err := f.Write(pt[1:]); err != nil {
				return err
			}
		case frameLag:
			if v, err := strconv.ParseFloat(string(pt[1:]), 64); err == nil {
				s.setLag(v)
			}
		case frameStop:
			path, n := stop()
			s.setStatus("connected", "relay", nil)
			_ = wsjson.Write(ctx, c, map[string]any{"type": "stopped", "file": filepath.Base(path), "bytes": n, "id": string(pt[1:])})
		}
	}
}
