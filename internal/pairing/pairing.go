// SPDX-License-Identifier: AGPL-3.0-or-later

// Package pairing holds the per-run pairing secret shared with the phone via
// the QR code fragment. The secret itself never crosses the network: the phone
// only sends MACs and AES-GCM ciphertexts derived from it.
package pairing

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
)

const (
	labelAuth = "ghostcam/auth/v1"
	labelEnc  = "ghostcam/enc/v1"

	seqLen = 8
	ivLen  = 12
	tagLen = 16
)

type Secret struct {
	token   []byte
	authKey []byte
	encKey  []byte
}

func New() (*Secret, error) {
	t := make([]byte, 32)
	if _, err := rand.Read(t); err != nil {
		return nil, err
	}
	return &Secret{token: t, authKey: derive(t, labelAuth), encKey: derive(t, labelEnc)}, nil
}

func derive(token []byte, label string) []byte {
	m := hmac.New(sha256.New, token)
	m.Write([]byte(label))
	return m.Sum(nil)
}

// Token is the base64url value put in the QR code fragment (#t=...).
func (s *Secret) Token() string { return base64.RawURLEncoding.EncodeToString(s.token) }

// Verify checks mac == HMAC(authKey, nonce || kind || 0x00 || payload).
// Binding the server nonce prevents replay; binding the payload (SDP offer or
// MIME type) prevents a relay in the middle from swapping it.
func (s *Secret) Verify(nonce []byte, kind, payload, macB64 string) bool {
	got, err := base64.RawURLEncoding.DecodeString(macB64)
	if err != nil {
		return false
	}
	m := hmac.New(sha256.New, s.authKey)
	m.Write(nonce)
	m.Write([]byte(kind))
	m.Write([]byte{0})
	m.Write([]byte(payload))
	return hmac.Equal(got, m.Sum(nil))
}

// Opener decrypts the fallback media frames: seq(8, BE) || iv(12) || ct+tag.
// AAD = nonce || seq, and seq must be strictly sequential, so frames cannot be
// replayed, reordered or dropped silently.
type Opener struct {
	gcm   cipher.AEAD
	nonce []byte
	next  uint64
}

func (s *Secret) NewOpener(nonce []byte) (*Opener, error) {
	block, err := aes.NewCipher(s.encKey)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Opener{gcm: gcm, nonce: nonce}, nil
}

func (o *Opener) Open(frame []byte) ([]byte, error) {
	if len(frame) < seqLen+ivLen+tagLen {
		return nil, errors.New("frame too short")
	}
	seq := binary.BigEndian.Uint64(frame[:seqLen])
	if seq != o.next {
		return nil, fmt.Errorf("unexpected seq %d, want %d", seq, o.next)
	}
	aad := make([]byte, 0, len(o.nonce)+seqLen)
	aad = append(append(aad, o.nonce...), frame[:seqLen]...)
	pt, err := o.gcm.Open(nil, frame[seqLen:seqLen+ivLen], frame[seqLen+ivLen:], aad)
	if err != nil {
		return nil, err
	}
	o.next++
	return pt, nil
}
