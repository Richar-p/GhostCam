// SPDX-License-Identifier: AGPL-3.0-or-later

package record

import (
	"errors"
	"sync"
	"time"

	"github.com/at-wat/ebml-go/webm"
	"github.com/pion/rtp"
	"github.com/pion/rtp/codecs"
	"github.com/pion/webrtc/v4/pkg/media/samplebuilder"
)

var ErrClosed = errors.New("recorder closed")

// WebM muxes VP8 + Opus RTP into a live WebM file. ebml-go writes the Segment
// and Clusters with "unknown size", so the file is valid at every byte
// boundary: a sudden cut only loses the frame being written.
type WebM struct {
	mu       sync.Mutex
	dir      string
	onCreate func(*File)

	file           *File
	audio, video   webm.BlockWriteCloser
	audioB, videoB *samplebuilder.SampleBuilder
	audioTS        time.Duration
	videoTS        time.Duration
	closed         bool
}

func NewWebM(dir string, onCreate func(*File)) *WebM {
	return &WebM{
		dir:      dir,
		onCreate: onCreate,
		audioB:   samplebuilder.New(10, &codecs.OpusPacket{}, 48000),
		videoB:   samplebuilder.New(50, &codecs.VP8Packet{}, 90000),
	}
}

func (w *WebM) PushVP8(p *rtp.Packet) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return ErrClosed
	}
	w.videoB.Push(p)
	for s := w.videoB.Pop(); s != nil; s = w.videoB.Pop() {
		if len(s.Data) == 0 {
			continue
		}
		key := s.Data[0]&0x1 == 0
		if w.video == nil {
			// The file starts on the first keyframe, which carries the size.
			if !key || len(s.Data) < 10 {
				continue
			}
			raw := uint(s.Data[6]) | uint(s.Data[7])<<8 | uint(s.Data[8])<<16 | uint(s.Data[9])<<24
			if err := w.open(int(raw&0x3FFF), int((raw>>16)&0x3FFF)); err != nil {
				return err
			}
		}
		w.videoTS += s.Duration
		if _, err := w.video.Write(key, int64(w.videoTS/time.Millisecond), s.Data); err != nil {
			return err
		}
	}
	return nil
}

func (w *WebM) PushOpus(p *rtp.Packet) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return ErrClosed
	}
	w.audioB.Push(p)
	for s := w.audioB.Pop(); s != nil; s = w.audioB.Pop() {
		if w.audio == nil {
			continue
		}
		w.audioTS += s.Duration
		if _, err := w.audio.Write(true, int64(w.audioTS/time.Millisecond), s.Data); err != nil {
			return err
		}
	}
	return nil
}

func (w *WebM) open(width, height int) error {
	f, err := Create(w.dir, "webrtc", ".webm")
	if err != nil {
		return err
	}
	ws, err := webm.NewSimpleBlockWriter(f, []webm.TrackEntry{
		{
			Name: "Audio", TrackNumber: 1, TrackUID: 1, CodecID: "A_OPUS", TrackType: 2,
			DefaultDuration: 20000000,
			Audio:           &webm.Audio{SamplingFrequency: 48000.0, Channels: 2},
		},
		{
			Name: "Video", TrackNumber: 2, TrackUID: 2, CodecID: "V_VP8", TrackType: 1,
			DefaultDuration: 33333333,
			Video:           &webm.Video{PixelWidth: uint64(width), PixelHeight: uint64(height)},
		},
	})
	if err != nil {
		_ = f.Close()
		return err
	}
	w.file, w.audio, w.video = f, ws[0], ws[1]
	if w.onCreate != nil {
		w.onCreate(f)
	}
	return nil
}

// Info returns the file path and size, or "" if no keyframe arrived yet.
func (w *WebM) Info() (string, int64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return "", 0
	}
	return w.file.Path(), w.file.Written()
}

// Close flushes buffered blocks; the underlying file is closed once both
// track writers are closed.
func (w *WebM) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	w.closed = true
	if w.video == nil {
		return nil
	}
	err := errors.Join(w.audio.Close(), w.video.Close())
	return errors.Join(err, w.file.Close())
}
