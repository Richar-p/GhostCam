#!/bin/sh
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# Regenerates third_party_licenses.txt (embedded in the binary) from the
# modules actually linked into the Windows build, plus the Go standard library.
# Runs in the "release" image (needs go-licenses).
set -eu
cd "$(dirname "$0")/.."
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

GOOS=windows go-licenses save ./cmd/ghostcam --save_path="$tmp/l" --ignore github.com/p3374/GhostCam 2>/dev/null

{
  echo "==== Third-party licenses ===="
  echo
  echo "GhostCam includes the following components, under their own licenses."
  ( cd "$tmp/l" && find . -type f | sort ) | while read -r f; do
    printf '\n---- %s ----\n\n' "$(dirname "${f#./}")"
    cat "$tmp/l/$f"
  done
  printf '\n---- Go standard library (%s) ----\n\n' "$(go env GOVERSION)"
  cat "$(go env GOROOT)/LICENSE"
} > third_party_licenses.txt

echo "third_party_licenses.txt: $(grep -c '^---- ' third_party_licenses.txt) components"
