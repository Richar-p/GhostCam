#!/bin/sh
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# Builds every release binary into /out (dist/ on the host) and SHA256SUMS.txt.
# Runs in the "release" image: docker compose run --rm build-release
set -eu
cd "$(dirname "$0")/.."

# VERSION is the single source of the version: embedded in the binaries (the
# updater compares it with the latest release) and in the Windows resources.
V=$(tr -d '[:space:]' < VERSION)
grep -q "\"ProductVersion\": \"$V\"" cmd/ghostcam/winres/winres.json || {
  echo "cmd/ghostcam/winres/winres.json is not at $V: update it, then: docker compose run --rm icons" >&2
  exit 1
}
echo "GhostCam $V"
sh scripts/gen-notices.sh

build() { # GOOS GOARCH output [extra ldflags]
  echo "building $3"
  GOOS=$1 GOARCH=$2 CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=$V ${4:-}" -o "/out/$3" ./cmd/ghostcam
}
build windows amd64 ghostcam.exe -H=windowsgui
build darwin  arm64 ghostcam-macos-apple-silicon
build darwin  amd64 ghostcam-macos-intel
build linux   amd64 ghostcam-linux-x64
build linux   arm64 ghostcam-linux-arm64

cd /out
sha256sum ghostcam.exe ghostcam-macos-* ghostcam-linux-x64 ghostcam-linux-arm64 > SHA256SUMS.txt
ls -lh ghostcam* SHA256SUMS.txt
