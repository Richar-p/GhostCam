// SPDX-License-Identifier: AGPL-3.0-or-later

package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/pion/ice/v4"
	"github.com/pion/interceptor"
	"github.com/pion/interceptor/pkg/intervalpli"
	"github.com/pion/rtcp"
	"github.com/pion/webrtc/v4"

	"ghostcam/internal/record"
)

func newWebRTCAPI(rtcPort int) (*webrtc.API, error) {
	m := &webrtc.MediaEngine{}
	videoFB := []webrtc.RTCPFeedback{{Type: "goog-remb"}, {Type: "ccm", Parameter: "fir"}, {Type: "nack"}, {Type: "nack", Parameter: "pli"}}
	if err := m.RegisterCodec(webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeVP8, ClockRate: 90000, RTCPFeedback: videoFB},
		PayloadType:        96,
	}, webrtc.RTPCodecTypeVideo); err != nil {
		return nil, err
	}
	if err := m.RegisterCodec(webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2, SDPFmtpLine: "minptime=10;useinbandfec=1"},
		PayloadType:        111,
	}, webrtc.RTPCodecTypeAudio); err != nil {
		return nil, err
	}

	ir := &interceptor.Registry{}
	if err := webrtc.RegisterDefaultInterceptors(m, ir); err != nil { // NACK, RTCP reports
		return nil, err
	}
	pli, err := intervalpli.NewReceiverInterceptor() // periodic keyframes
	if err != nil {
		return nil, err
	}
	ir.Add(pli)

	se := webrtc.SettingEngine{}
	// All host candidates share one fixed UDP port (IPv4 + IPv6), so a single
	// UPnP mapping / firewall rule covers every session.
	mux, err := ice.NewMultiUDPMuxFromPort(rtcPort)
	if err != nil {
		return nil, fmt.Errorf("udp port %d: %w", rtcPort, err)
	}
	se.SetICEUDPMux(mux)
	// Tolerate 4G hiccups: the file is written as data arrives, so declaring
	// the phone dead late costs nothing.
	se.SetICETimeouts(5*time.Second, 20*time.Second, 2*time.Second)

	return webrtc.NewAPI(webrtc.WithMediaEngine(m), webrtc.WithInterceptorRegistry(ir), webrtc.WithSettingEngine(se)), nil
}

func (s *Server) pionICEServers() []webrtc.ICEServer {
	out := make([]webrtc.ICEServer, 0, len(s.cfg.ICEServers))
	for _, is := range s.cfg.ICEServers {
		out = append(out, webrtc.ICEServer{URLs: is.URLs, Username: is.Username, Credential: is.Credential})
	}
	return out
}

// extraCandidates advertises the UPnP-mapped public address. pion cannot learn
// it by itself (STUN would see an ephemeral socket, not the muxed port).
func (s *Server) extraCandidates() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.mapping == nil {
		return nil
	}
	return []string{fmt.Sprintf("candidate:3141592653 1 udp 1694498815 %s %d typ srflx raddr 0.0.0.0 rport 0", s.mapping.ExternalIP, s.mapping.Port)}
}

type answerMsg struct {
	Type       string   `json:"type"`
	SDP        string   `json:"sdp"`
	Candidates []string `json:"candidates,omitempty"`
}

// ctrlMsg travels on the "control" DataChannel. It is DTLS-protected end to
// end (pinned fingerprint + MAC'ed offer), so neither side can be spoofed.
type ctrlMsg struct {
	Type    string   `json:"type"` // phone: start | stop ; PC: started | stopped
	File    string   `json:"file,omitempty"`
	Bytes   int64    `json:"bytes,omitempty"`
	Quality *Quality `json:"quality,omitempty"` // with "started": applied by the phone
}

// runWebRTC keeps one PeerConnection per pairing. Media flows only while the
// phone records; each start/stop pair on the control channel makes one file.
func (s *Server) runWebRTC(ctx context.Context, c *websocket.Conn, offer string) error {
	pc, err := s.api.NewPeerConnection(webrtc.Configuration{
		ICEServers:   s.pionICEServers(),
		Certificates: []webrtc.Certificate{s.cert},
	})
	if err != nil {
		return err
	}
	defer pc.Close()

	var (
		recMu     sync.Mutex
		rec       *record.WebM
		videoSSRC atomic.Uint32
	)
	current := func() *record.WebM {
		recMu.Lock()
		defer recMu.Unlock()
		return rec
	}
	keyframe := func() {
		if ssrc := videoSSRC.Load(); ssrc != 0 {
			_ = pc.WriteRTCP([]rtcp.Packet{&rtcp.PictureLossIndication{MediaSSRC: ssrc}})
		}
	}
	stopRec := func() (string, int64) {
		recMu.Lock()
		r := rec
		rec = nil
		recMu.Unlock()
		if r == nil {
			return "", 0
		}
		_ = r.Close()
		path, n := r.Info()
		s.recorded(path, n)
		return path, n
	}
	defer stopRec() // connection lost mid-video: keep what was received

	connected, failed := make(chan struct{}), make(chan struct{})
	var connOnce, failOnce sync.Once
	pc.OnConnectionStateChange(func(st webrtc.PeerConnectionState) {
		log.Printf("webrtc: %s", st)
		if st == webrtc.PeerConnectionStateFailed || st == webrtc.PeerConnectionStateClosed {
			failOnce.Do(func() { close(failed) })
		}
	})
	pc.OnDataChannel(func(dc *webrtc.DataChannel) {
		if dc.Label() != "control" {
			return
		}
		reply := func(m ctrlMsg) {
			b, _ := json.Marshal(m)
			_ = dc.SendText(string(b))
		}
		dc.OnOpen(func() { connOnce.Do(func() { close(connected) }) })
		dc.OnMessage(func(msg webrtc.DataChannelMessage) {
			var m ctrlMsg
			if json.Unmarshal(msg.Data, &m) != nil {
				return
			}
			switch m.Type {
			case "start":
				stopRec()
				r := record.NewWebM(s.outDir(), func(f *record.File) {
					log.Printf("recording to %s", f.Path())
					s.setStatus("recording", "webrtc", f)
				})
				recMu.Lock()
				rec = r
				recMu.Unlock()
				s.setStatus("recording", "webrtc", nil)
				keyframe() // start the file now, not at the next periodic keyframe
				q := s.quality()
				reply(ctrlMsg{Type: "started", Quality: &q})
			case "stop":
				path, n := stopRec()
				s.setStatus("connected", "webrtc", nil)
				reply(ctrlMsg{Type: "stopped", File: filepath.Base(path), Bytes: n})
			}
		})
	})
	pc.OnTrack(func(t *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		log.Printf("webrtc: track %s %s", t.Kind(), t.Codec().MimeType)
		video := t.Kind() == webrtc.RTPCodecTypeVideo
		if video {
			videoSSRC.Store(uint32(t.SSRC()))
			keyframe()
		}
		for {
			pkt, _, err := t.ReadRTP()
			if err != nil {
				return
			}
			r := current()
			if r == nil {
				continue // packets in flight around a stop
			}
			if video {
				err = r.PushVP8(pkt)
			} else {
				err = r.PushOpus(pkt)
			}
			if err != nil && !errors.Is(err, record.ErrClosed) {
				log.Printf("webrtc: write: %v", err)
				stopRec()
			}
		}
	})

	if err := pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: offer}); err != nil {
		return err
	}
	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		return err
	}
	gathered := webrtc.GatheringCompletePromise(pc)
	if err := pc.SetLocalDescription(answer); err != nil {
		return err
	}
	select {
	case <-gathered:
	case <-time.After(5 * time.Second):
	}
	if err := wsjson.Write(ctx, c, answerMsg{Type: "answer", SDP: pc.LocalDescription().SDP, Candidates: s.extraCandidates()}); err != nil {
		return err
	}

	// The WebSocket only matters until the control channel opens: after that,
	// a tunnel hiccup must not stop the recording.
	wsGone := make(chan struct{})
	go func() {
		for {
			if _, _, err := c.Read(context.Background()); err != nil {
				close(wsGone)
				return
			}
		}
	}()
	select {
	case <-connected:
	case <-failed:
		return errors.New("ICE failed")
	case <-wsGone:
		return errors.New("phone left before media connected")
	case <-time.After(30 * time.Second):
		return errors.New("ICE timeout")
	case <-ctx.Done():
		return ctx.Err()
	}
	s.setStatus("connected", "webrtc", nil)

	select {
	case <-failed:
		return errors.New("peer connection lost")
	case <-ctx.Done():
		return ctx.Err()
	}
}
