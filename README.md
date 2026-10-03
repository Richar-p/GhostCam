<p align="center">
  <img src="web/icon.png" width="96" height="96" alt="GhostCam icon">
</p>

<h1 align="center">GhostCam</h1>

<p align="center">
  <b>English</b> · <a href="README.fr.md">Français</a>
</p>

<p align="center">
  <sub>Made with ❤️ in France 🇫🇷</sub>
</p>

<p align="center">
  <b>Your phone films, your PC keeps the proof.</b><br>
  Stream a phone camera to your own computer from anywhere (4G/5G, any Wi-Fi) and write it to disk in real time.
  Nothing is ever stored on the phone.
</p>

<p align="center">
  <img src="docs/screenshots/phone-recording.png" height="420" alt="Phone recording">
  &nbsp;&nbsp;
  <img src="docs/screenshots/pc-recording.png" height="420" alt="PC window while recording">
</p>

---

## Why GhostCam?

If a phone is seized, broken or switched off, whatever it recorded locally can be
lost. GhostCam turns the phone into a **remote camera with no local storage**:
every frame goes straight to a PC you control, and every second received is
already on its disk.

- **No install on the phone.** Scan a QR code and the camera page opens in Safari (iOS) or Chrome (Android).
- **Works across networks.** Phone on 4G/5G outside, PC at home behind its router: no port forwarding, no account, no server to rent.
- **Survives a hard cut.** Files are written as data arrives and stay playable if the phone disappears mid-video.
- **End-to-end encrypted.** Neither the tunnel nor any relay can read the video, inject frames or start or stop a recording.
- **Single file, lightweight.** One ~13 MB Windows executable written in Go. `cloudflared` is downloaded, verified and kept up to date automatically. The UI is a native WebView2 window.
- **Open source**, so you can audit it, which a tool like this needs.

## Features

| On the phone | On the PC |
|---|---|
| One button to start and stop, as many videos as you want | One file per video, live recording timer, received bitrate |
| Live preview, nothing sent between videos (saves data and battery) | Quality presets with latency warnings |
| Automatic reconnection: if the network drops mid-video, recording resumes in a new file | Choice of output folder (native folder picker) |
| Screen kept awake (Wake Lock) | QR code or "copy link" to pair |
| Clear status: direct connection, encrypted relay, slow network | Works headless too (`-headless`, QR code in the terminal) |

<p align="center">
  <img src="docs/screenshots/pc-pairing.png" height="380" alt="Pairing">
  &nbsp;
  <img src="docs/screenshots/pc-settings.png" height="380" alt="Settings">
  &nbsp;
  <img src="docs/screenshots/phone-intro.png" height="380" alt="Phone intro">
</p>

## Quick start (Windows)

1. Build `dist/ghostcam.exe` (see [Build](#build)). It is the only file you need.
2. Run it and allow it when the Windows firewall asks: this lets direct UDP connections in.
   On first launch it downloads `cloudflared` (~50 MB, see [Tunnel binary](#tunnel-binary-cloudflared)), so an Internet connection is required once.
3. Scan the QR code with the phone and tap **Activer la caméra**, then the red button.
4. Videos are saved to `Videos\GhostCam` by default. Change the folder and quality with the settings button (top right).

WebView2 is preinstalled on Windows 10/11. If it is missing, GhostCam opens the
same page in your default browser.

## How it works

```
 Phone (4G, CGNAT)                         Internet                       Home PC (behind router)
 ┌──────────────┐  HTTPS/WSS  ┌──────────────────────────┐  outbound QUIC  ┌─────────────────────┐
 │ Browser page │────────────▶│ Cloudflare Quick Tunnel  │◀───────────────│ cloudflared (child) │
 │ getUserMedia │ signaling   │ https://xxx.trycloudflare│                 │ ghostcam            │
 │ RTCPeerConn. │             └──────────────────────────┘                 │  :8080 public (lo)  │
 │              │                                                          │  :8081 admin  (lo)  │
 │              │════════ WebRTC media, DTLS-SRTP, UDP ═══════════════════▶│  :50000/udp (ICE)   │
 └──────────────┘   1. UPnP-mapped public IP:50000 (direct)                │  → *.webm on disk   │
                    2. STUN hole punching (direct)                         └─────────────────────┘
                    3. TURN relay (optional, your server)
                    4. Fallback: MediaRecorder chunks, AES-GCM encrypted, over the WSS tunnel
```

- **Signaling and the phone page** go through a Cloudflare Quick Tunnel that the
  app starts itself. `cloudflared` only makes outbound connections, and the phone
  gets a valid HTTPS URL, which browsers require for camera access.
- **Media** uses WebRTC on one fixed UDP port. The app asks the router to open
  that port via **UPnP** and sends its public address to the phone as an extra
  ICE candidate, so the phone can reach the PC directly even from a carrier NAT.
  Without UPnP, STUN hole punching often works. An optional TURN server covers
  the remaining cases.
- **Fallback**: if no WebRTC path connects within ~12 s, the phone switches to
  `MediaRecorder` chunks sent every second over the same WebSocket, end-to-end
  encrypted. This path works whenever the page loads.
- **Pairing vs. recording**: one pairing keeps a connection open. Each Rec/Stop
  produces one file. Start and stop commands travel on a WebRTC DataChannel or
  inside the encrypted relay frames.

## Tunnel binary (cloudflared)

GhostCam doesn't ship `cloudflared`. At each launch it checks Cloudflare's
official GitHub releases and installs or updates its own copy in
`%LOCALAPPDATA%\GhostCam\bin\`:

- A download is accepted only if its **SHA-256** matches the digest GitHub
  publishes for the asset (and the checksum Cloudflare lists in the release
  notes, when present), and if it carries a **valid Authenticode signature from
  "Cloudflare, Inc."**. The signature check doesn't depend on GitHub.
- At each launch the installed binary is re-hashed. If it was modified, it is
  reinstalled.
- **Offline**, the installed copy is used as is. If the update fails, the
  previous version is kept.
- `cloudflared` runs inside a Windows job object, so it dies with GhostCam,
  even on a crash or a forced kill.
- The QR code appears only once the tunnel hostname resolves on public DNS
  (checked against 1.1.1.1, so the router's DNS cache isn't polluted).

`-cloudflared <path>` skips all of this and uses your own binary.

## Security model

| Threat | Mitigation |
|---|---|
| Someone else connects | 256-bit secret in the QR URL **fragment** (`#t=`). Browsers never send the fragment over the network. The phone proves it knows the secret with HMAC(server nonce, SDP offer). |
| Tunnel or TURN swaps the PC endpoint | The PC's DTLS certificate fingerprint is pinned in the QR code (`&f=`), and the phone rejects any other answer. |
| Tunnel reads or alters the video | WebRTC media is DTLS-SRTP end to end. Relay frames, control frames included, are AES-256-GCM with AAD = nonce‖seq, so they cannot be replayed, reordered or dropped silently. |
| A website reads the secret from localhost | The admin UI listens on loopback only, is never tunneled, checks the Host header (DNS rebinding) and requires a custom header on state-changing calls. |
| Cross-site WebSocket or script injection | Origin must match Host, and the phone page has a strict CSP. |

**Known limit:** the tunnel serves the phone page itself, so a malicious tunnel
operator could serve modified JavaScript. To remove that trust, host `web/` on a
static origin you control and use the tunnel for `/ws` only. Contributions are
welcome.

## Recording format

- **Direct (WebRTC):** VP8 + Opus in **WebM with unknown-size Segment and
  Clusters**, written as packets arrive and fsync'ed every second. A sudden cut
  leaves a playable file (VLC, mpv, ffmpeg, browsers).
- **Relay:** the phone's own MediaRecorder stream: WebM on Chrome/Android, WebM
  or MP4 on Safari depending on its version. WebM tolerates cuts. Safari MP4 may
  need `ffmpeg -i in.mp4 -c copy out.mp4` after a hard cut.
- Files are written live, so they have no seek index. To get easy seeking:
  `ffmpeg -i in.webm -c copy fixed.webm`.
- Names: `ghostcam-<date>-<webrtc|relay>.<ext>`.

### Quality presets

Set in the settings panel, saved to `%LOCALAPPDATA%\GhostCam\settings.json`, and
applied from the next video without reconnecting the phone.

| Preset | Camera | Phone uplink needed | Disk |
|---|---|---|---|
| Économie | 480p 24 fps | ≈ 0.8 Mbit/s | ≈ 0.3 GB/h |
| Standard (default) | 720p 30 fps | ≈ 2 Mbit/s | ≈ 0.8 GB/h |
| Haute | 1080p 30 fps | ≈ 4 Mbit/s | ≈ 1.7 GB/h |
| Maximale | 1080p 30 fps | ≈ 8 Mbit/s | ≈ 3.4 GB/h |

If the phone's uplink is slower than the preset needs:
- **Direct:** the video stays real time but drops frames (`degradationPreference: maintain-resolution`).
- **Relay:** data queues on the phone, which means those seconds are **not yet safe on the PC**.

The PC window shows the received bitrate and warns when it falls below half the
target.

## Build

Everything builds in Docker. Only Docker and Docker Compose are needed on the host.

```sh
git clone https://github.com/p3374/GhostCam.git && cd GhostCam
docker compose run --rm build-windows   # -> dist/ghostcam.exe (single file, license texts embedded)
docker compose up dev                   # dev run in a container: QR code in the logs, admin UI on http://localhost:8081
docker compose run --rm icons           # regenerate icons from web/icon.svg (PNG + Windows .syso resource)
```

Run the `.exe` natively: UPnP and direct UDP need the host's network. Inside
Docker, only the encrypted relay (and sometimes STUN) works.

### Command-line flags

| Flag | Default | Purpose |
|---|---|---|
| `-out <dir>` | saved setting | Recordings folder (overrides the setting) |
| `-rtc-port <n>` | `50000` | UDP port for WebRTC (mapped with UPnP) |
| `-no-upnp` | off | Do not touch the router |
| `-public-url <https-url>` | Quick Tunnel | Use your own reverse proxy or named tunnel instead |
| `-cloudflared <path>` | auto-downloaded and updated | Use this cloudflared binary instead |
| `-stun <urls>` | Cloudflare + Google | Comma-separated STUN servers; empty disables STUN |
| `-turn <url> -turn-user <u>` | none | TURN server; password via `GHOSTCAM_TURN_PASS` |
| `-headless` | off | No window: print the QR code in the terminal |
| `-web-dir <dir>` | embedded | Serve `web/` from disk (live editing) |

Logs are written to `%LOCALAPPDATA%\GhostCam\ghostcam.log`.

## Testing

An end-to-end test runs a headless Chromium with a fake camera as the phone. It
records 3 videos, changes quality and folder mid-session, then kills the browser
mid-video (phone "destroyed"). It passes only if ffmpeg can decode every file.

```sh
docker compose run --rm build-linux
MODE=webrtc docker compose run --rm e2e                   # direct path
MODE=relay  docker compose run --rm e2e                   # UDP blocked: encrypted relay
MODE=webrtc docker compose run --rm -e QUALITY2=high e2e  # quality going up instead of down
```

The test prints, for each file, the frame count and the resolution of the first
and last frames.

## Project layout

```
cmd/ghostcam/        entry point: flags, tunnel, UPnP, settings; native window + folder picker (gui_windows.go)
internal/server/     HTTP (public + admin), WebSocket signaling, WebRTC session, encrypted relay, settings
internal/record/     fsync'ed append-only file, live WebM muxer
internal/pairing/    pairing secret, HMAC auth, AES-GCM frame decryption
internal/tunnel/     cloudflared Quick Tunnel
internal/upnp/       router port mapping + lease renewal
web/                 phone page (index.html, app.js), PC window (admin.html), icon (embedded in the binary)
notices.go           embeds LICENSE + third_party_licenses.txt (shown in the app)
scripts/             gen-notices.sh: regenerates third_party_licenses.txt
test/e2e/            Playwright end-to-end test
```

## Known issues and roadmap

Good places to start contributing:

- **Occasional direct-connection miss.** In tests with many rapid reconnections, about 1 in 8 attempts did not connect directly and fell back to the relay after ~12 s. Cause not identified yet.
- **iOS background.** iOS stops the camera when Safari goes to the background. The page reconnects with a fresh camera when it comes back, but this is not yet tested on a real iPhone.
- **Relay resolution.** Lowering the quality is confirmed in direct mode. In relay mode, with Chromium's fake camera, MediaRecorder kept the native resolution. This needs checking on real phones.
- **Self-hosted phone page**, to remove the trust in the tunnel for the JavaScript (see Security model).
- **Native window on macOS and Linux.** These platforms currently fall back to the browser.
- **Unit tests and CI.** Today there is only the end-to-end test.
- **i18n.** The UI is in French only.
- **ICE restart** instead of a new file on short network drops.
- **Seek index on stop**, by remuxing to add Cues so players can seek.

## Contributing

Contributions are very welcome: bug reports from real-world networks (carrier,
router model, phone and browser) are as valuable as code. Open an
[issue](https://github.com/p3374/GhostCam/issues) or a pull request, and see
[CONTRIBUTING.md](CONTRIBUTING.md).

## Responsible use

GhostCam records video and audio. Laws on filming people, recording audio and
using recordings as evidence differ by country. You are responsible for using it
lawfully and respecting others' privacy.

## License

GhostCam is free software, licensed under the
[GNU Affero General Public License v3.0 or later](LICENSE) (`AGPL-3.0-or-later`).

In short:
- You may use, study, modify and share it, commercially or not.
- If you **distribute** a modified version (binaries or source), you must make
  its complete source available under the same license.
- If you let **other people use a modified version over a network**, for example
  by hosting it as a service, you must also offer them its source (AGPL section 13).
- Private modifications that you neither distribute nor expose to others carry
  no obligation.

This summary is not legal advice; the [LICENSE](LICENSE) text is what applies.

Third-party components keep their own licenses, all permissive and compatible
with AGPL-3.0: MIT (pion, go-qrcode, go-webview2), BSD (Go standard library,
golang.org/x, goupnp, uuid, anet), ISC (coder/websocket, go-winloader) and
Apache-2.0 (ebml-go). Their full texts are embedded in the executable and shown
in the app (Settings → *Licences open source*). `cloudflared` is not
distributed with GhostCam: it is downloaded from Cloudflare at runtime and is
licensed under Apache-2.0.
