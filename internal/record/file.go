// SPDX-License-Identifier: AGPL-3.0-or-later

// Package record writes incoming media to disk as it arrives.
package record

import (
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

// File is an append-only recording file fsync'ed every second, so at most ~1 s
// is at risk even if the PC itself loses power. Losing the phone loses nothing
// that already reached the PC.
type File struct {
	f       *os.File
	started time.Time
	written atomic.Int64
	stop    chan struct{}
	done    chan struct{}
	once    sync.Once
}

func Create(dir, mode, ext string) (*File, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	name := "ghostcam-" + time.Now().Format("20060102-150405.000") + "-" + mode + ext
	f, err := os.OpenFile(filepath.Join(dir, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, err
	}
	rf := &File{f: f, started: time.Now(), stop: make(chan struct{}), done: make(chan struct{})}
	go rf.syncLoop()
	return rf, nil
}

func (r *File) syncLoop() {
	defer close(r.done)
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			_ = r.f.Sync()
		case <-r.stop:
			return
		}
	}
}

func (r *File) Write(p []byte) (int, error) {
	n, err := r.f.Write(p)
	r.written.Add(int64(n))
	return n, err
}

func (r *File) Close() error {
	var err error
	r.once.Do(func() {
		close(r.stop)
		<-r.done
		_ = r.f.Sync()
		err = r.f.Close()
	})
	return err
}

func (r *File) Path() string       { return r.f.Name() }
func (r *File) Written() int64     { return r.written.Load() }
func (r *File) Started() time.Time { return r.started }
