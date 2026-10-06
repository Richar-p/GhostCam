// SPDX-License-Identifier: AGPL-3.0-or-later

package tunnel

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
)

// maxBinary bounds the extracted size (cloudflared is ~40 MB).
const maxBinary = 256 << 20

// extractTGZ copies the regular file whose base name is name from the .tgz
// archive src to dst (mode 0700). Only that entry is read: no path from the
// archive is ever used to write on disk.
func extractTGZ(src, name, dst string) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("gzip: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return fmt.Errorf("%s not found in archive", name)
		}
		if err != nil {
			return fmt.Errorf("tar: %w", err)
		}
		if h.Typeflag != tar.TypeReg || path.Base(h.Name) != name {
			continue
		}
		if h.Size > maxBinary {
			return fmt.Errorf("%s too large in archive (%d bytes)", name, h.Size)
		}
		out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o700)
		if err != nil {
			return err
		}
		n, err := io.Copy(out, io.LimitReader(tr, maxBinary))
		if cerr := out.Close(); err == nil {
			err = cerr
		}
		if err == nil && n != h.Size {
			err = fmt.Errorf("short extract: %d of %d bytes", n, h.Size)
		}
		if err != nil {
			_ = os.Remove(dst)
		}
		return err
	}
}
