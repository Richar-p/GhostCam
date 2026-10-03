// SPDX-License-Identifier: AGPL-3.0-or-later

package tunnel

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// verifySignature requires a valid Authenticode signature whose signer is
// Cloudflare, Inc. This check does not depend on GitHub: the certificate
// chains to a public code-signing CA trusted by Windows.
func verifySignature(path string) error {
	const script = `$s = Get-AuthenticodeSignature -LiteralPath $env:GHOSTCAM_FILE
"$($s.Status)|$($s.SignerCertificate.Subject)"`
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script)
	cmd.Env = append(os.Environ(), "GHOSTCAM_FILE="+path)
	hideWindow(cmd)
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("signature check: %w", err)
	}
	status, subject, _ := strings.Cut(strings.TrimSpace(string(out)), "|")
	if status != "Valid" {
		return fmt.Errorf("invalid Authenticode signature (%s)", status)
	}
	if !strings.Contains(subject, `O="Cloudflare, Inc."`) {
		return fmt.Errorf("unexpected signer: %s", subject)
	}
	return nil
}
