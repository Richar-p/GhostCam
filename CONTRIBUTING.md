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
- **Translations.** See [Translations](#translations) below.

## Translations

All interface texts live in `web/locales/<language>.json`, in **i18next JSON
v4** format. `en.json` is the source language. The Go code never sends display
text, only keys (for example `progress.cfDownload`), which the page translates.

To add a language, for example German:

1. Copy `web/locales/en.json` to `web/locales/de.json`, using a BCP 47 code:
   `de`, `pt-BR`, `zh-Hans`…
2. Translate the values. Keep the keys and the `{{variables}}` unchanged.
3. Plurals: provide every form your language needs, as `key_one`, `key_few`,
   `key_many`, `key_other`… The check below tells you which ones are missing.
4. Run `docker compose run --rm i18n`. It reports missing or stale keys, missing
   plural forms and variable mismatches.
5. Rebuild: the new language is picked up automatically. Nothing else to change.

**Weblate / Crowdin / Lokalise:** use the file format *i18next JSON v4*, the
file mask `web/locales/*.json` and the monolingual base file
`web/locales/en.json`.

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
2. The end-to-end tests pass in both modes, and the translations are consistent:
   ```sh
   docker compose run --rm build-linux
   MODE=webrtc docker compose run --rm e2e
   MODE=relay  docker compose run --rm e2e
   docker compose run --rm i18n
   ```
   New user-facing text goes into `web/locales/en.json` (and `fr.json`), never
   hard-coded in HTML, JS or Go.
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

## Releasing

Installed copies update themselves from the latest GitHub release, so a
release must follow these rules:

1. Bump the version in `VERSION` **and** in `cmd/ghostcam/winres/winres.json`,
   then run `docker compose run --rm icons`. `build-release` refuses to build if
   the two disagree.
2. `docker compose run --rm build-release` builds every binary and
   `SHA256SUMS.txt` in `dist/`.
3. Publish a GitHub release tagged `vX.Y.Z` (not a draft, not a pre-release),
   with the files **named exactly** `ghostcam.exe`, `ghostcam-cli.exe`,
   `ghostcam-macos-apple-silicon`, `ghostcam-macos-intel`, `ghostcam-linux-x64`
   and `ghostcam-linux-arm64`: installed versions look for these names.
4. Once published, the update is offered to every running copy within 6 hours.
   Test it before publishing (see the self-update test in the README).

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
