# Contributing to GhostCam

Thanks for helping. GhostCam exists to protect recordings when the phone cannot
be trusted to keep them, so **reliability and security come before features**.

## Ways to help

- **Field reports.** Tell us whether it worked on your network: carrier, router
  (UPnP on/off), phone model, OS and browser, and the connection mode shown
  (direct / relay). Attach `%LOCALAPPDATA%\GhostCam\ghostcam.log` after
  checking it contains nothing private.
- **Bugs.** Steps to reproduce, what you expected, what happened, logs.
- **Code.** Pick an item from *Known issues and roadmap* in the README, or open
  an issue first for anything larger than a small fix.
- **Docs and translations.** The UI is French only today.

## Development setup

Only Docker and Docker Compose are needed on the host. Do not install Go,
Node or other runtimes locally; everything runs in containers.

```sh
docker compose up dev                         # run in a container, web/ served live from disk
docker compose run --rm --no-deps dev go vet ./...
docker compose run --rm --no-deps dev sh -c "GOOS=windows go vet ./..."
docker compose run --rm build-windows         # dist/ghostcam.exe
```

The phone page and the PC window are plain HTML/JS in `web/`, with no framework
and no build step. With `docker compose up dev` they are served from disk, so a
browser refresh picks up your changes.

## Before opening a pull request

1. `gofmt` clean and `go vet` clean for Linux **and** Windows (`GOOS=windows`).
2. The end-to-end tests pass in both modes:
   ```sh
   docker compose run --rm build-linux
   MODE=webrtc docker compose run --rm e2e
   MODE=relay  docker compose run --rm e2e
   ```
3. If you change the recording path, the protocol or the crypto, explain in the
   description how a hard cut (phone killed mid-video) is still safe, and how
   the change keeps the guarantees of the security model in the README.
4. Small, focused commits with clear messages, in English.

## License of contributions

GhostCam is licensed under AGPL-3.0-or-later. By submitting a contribution you
agree that it is licensed under the same terms (inbound = outbound), and you
confirm you have the right to submit it. There is no CLA.

- New source files start with `// SPDX-License-Identifier: AGPL-3.0-or-later`
  (`<!-- ... -->` in HTML/SVG).
- New dependencies must have a license compatible with AGPL-3.0 (MIT, BSD, ISC,
  Apache-2.0, MPL-2.0, LGPL/GPL-3.0…). `docker compose run --rm build-windows`
  regenerates `third_party_licenses.txt` (embedded in the binary): commit it, and
  mention new licenses in the README.

## Code style

- Go: standard library first. Every new dependency needs a reason, because the
  goal is a small, auditable binary.
- Comments explain *why*, not *what*.
- JavaScript: vanilla, no bundler, works on current Safari iOS and Chrome Android.
- Keep the phone page CSP strict: no inline scripts, no third-party origins.

## Security issues

Do not report vulnerabilities in a public issue. Use GitHub's private
vulnerability reporting:
<https://github.com/p3374/GhostCam/security/advisories/new>, so a fix can
ship before the details are public.

## Code of conduct

Be respectful and constructive. Assume good faith and focus on the work.
