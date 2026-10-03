// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build !windows

package tunnel

// Linux binaries are not code-signed: the SHA-256 check in download is the
// only verification.
func verifySignature(string) error { return nil }
