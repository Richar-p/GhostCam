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

## Get started in 3 steps

**1. On your computer:** download GhostCam and open it.

- **Windows 10/11:** [`ghostcam.exe`](https://github.com/p3374/GhostCam/releases/latest/download/ghostcam.exe) (or [`ghostcam-cli.exe`](https://github.com/p3374/GhostCam/releases/latest/download/ghostcam-cli.exe) for the [command line](#command-line)). If SmartScreen says the app is unrecognized, click *More info → Run anyway*. When the firewall asks, click *Allow*.
- **macOS** *(experimental)*: [Apple Silicon (M1 and later)](https://github.com/p3374/GhostCam/releases/latest/download/ghostcam-macos-apple-silicon) or [Intel](https://github.com/p3374/GhostCam/releases/latest/download/ghostcam-macos-intel). Then, in Terminal:
  ```sh
  cd ~/Downloads && chmod +x ghostcam-macos-* && xattr -c ghostcam-macos-* && ./ghostcam-macos-apple-silicon
  ```
  (use `./ghostcam-macos-intel` on an Intel Mac). The app opens in your browser.
- **Linux** *(experimental)*: [x64](https://github.com/p3374/GhostCam/releases/latest/download/ghostcam-linux-x64) or [ARM64](https://github.com/p3374/GhostCam/releases/latest/download/ghostcam-linux-arm64). Then `chmod +x ghostcam-linux-x64 && ./ghostcam-linux-x64`. The app opens in your browser.

**2. On your phone:** scan the QR code shown on the PC with the camera app, then tap **Enable camera**.
It works on iPhone (Safari) and Android (Chrome), on 4G/5G or any Wi-Fi. Nothing to install.

**3. Record:** tap the red button to start, and tap it again to stop. Do it as many times as you like.
Each video is saved on the computer, in `Videos/GhostCam` (`Movies/GhostCam` on a Mac). The **Open videos** button shows them.

<p align="center">
  <img src="docs/screenshots/phone-recording.png" height="420" alt="Phone recording">
  &nbsp;&nbsp;
  <img src="docs/screenshots/pc-recording.png" height="420" alt="PC window while recording">
</p>

### Good to know

- **Nothing is stored on the phone.** Every second filmed is already on the PC. If the phone is lost, broken or switched off mid-video, the file stays readable.
- **Slow mobile network?** Choose **Economy** in the settings (top right button), or turn on the **network buffer** so short drops don't freeze the video.
- **Keep the camera page open** on the phone: on iPhone, switching to another app stops the camera.
- **First launch:** the app downloads the Cloudflare connector (~50 MB), so the PC needs an Internet connection once.
- **Updates:** when a new version is out, a banner offers to install it in one click. GhostCam replaces itself and restarts, and the phone then scans the new QR code. Versions before 1.3.0 can't update themselves: download 1.3.0 once.

### Troubleshooting

- **The QR code doesn't appear:** the app shows the reason. Check the computer's Internet connection. Logs: `%LOCALAPPDATA%\GhostCam\ghostcam.log` (Windows), `~/Library/Application Support/GhostCam/ghostcam.log` (macOS), `~/.config/GhostCam/ghostcam.log` (Linux).
- **The phone says "encrypted relay" instead of "direct":** this is normal and works fine. The direct mode is lighter; it needs UPnP enabled on your router.
- **"Invalid pairing link" on the phone:** scan the QR code again. A new code is created each time the app starts.
- **Something else?** [Open an issue](https://github.com/p3374/GhostCam/issues) with your phone model, browser and computer OS.

---

# Under the hood

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
- The QR code appears only once the phone page actually loads through the
  tunnel (resolved with 1.1.1.1, so the router's DNS cache isn't polluted).

`-cloudflared <path>` skips all of this and uses your own binary.

## Command line

The command line works on Windows, macOS and Linux:

| System | File to use |
|---|---|
| Windows | [`ghostcam-cli.exe`](https://github.com/p3374/GhostCam/releases/latest/download/ghostcam-cli.exe), a separate download: `ghostcam.exe` is a window app and prints nothing in a terminal |
| macOS | The usual binary (`ghostcam-macos-apple-silicon` or `ghostcam-macos-intel`) |
| Linux | The usual binary (`ghostcam-linux-x64` or `ghostcam-linux-arm64`) |

On macOS and Linux, the same binary does both: without arguments it opens the
interface, with a command it stays in the terminal.

Commands:

| Command | What it does |
|---|---|
| `ghostcam run` | Start in the terminal: QR code and live status (waiting, connected, ● REC, size, delay) |
| `ghostcam config` | Show the settings |
| `ghostcam config set KEY VALUE` | Change a setting, applied at once if GhostCam is running. Keys: `quality`, `buffer`, `out`, `tunnel`, `tunnel-url`, `language`, `updates` |
| `ghostcam config get KEY` | Show one setting |
| `ghostcam update [--check]` | Install the latest version (`--check`: only look) |
| `ghostcam version` | Show the version |

The terminal speaks the same languages as the window (same translation
files). With redirected output (logs, Docker), it prints one line per change.

## Internet link (tunnel)

The phone reaches the computer through a public HTTPS address. In Settings →
*Internet link* (or `ghostcam config set tunnel …`):

| Choice | How it works |
|---|---|
| **Cloudflare** (default) | Cloudflare Quick Tunnel: no account; `cloudflared` is downloaded, verified and updated automatically |
| **localhost.run** | SSH tunnel built into GhostCam: nothing to download, no account. The free address changes more often, and the server's SSH key is pinned on first use |
| **My own address** | Any HTTPS URL you control that forwards to `127.0.0.1:8080` on this computer: ngrok, Tailscale Funnel, a Cloudflare named tunnel, Caddy or nginx on your own server… |

Changing the link restarts it at once, with a new QR code. If the tunnel drops,
GhostCam reconnects by itself.

## Updates

GhostCam checks the latest GitHub release at startup and then every 6 hours.
You can turn this off in Settings, or check manually with *Check now*. When a
newer version exists, a banner offers to install it:

1. The binary for this platform is downloaded next to the executable.
2. It is accepted only if it comes from this repository's release download URL,
   has the announced size and matches the **SHA-256 digest published by GitHub**.
3. The running executable is replaced. The previous one is kept as `.old` until
   the new version has run for a minute.
4. GhostCam restarts. The new instance waits for the old one to close its
   recordings and release its ports.

An update is refused while a video is recording.

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
| Economy | 480p 24 fps | ≈ 0.8 Mbit/s | ≈ 0.3 GB/h |
| Standard (default) | 720p 30 fps | ≈ 2 Mbit/s | ≈ 0.8 GB/h |
| High | 1080p 30 fps | ≈ 4 Mbit/s | ≈ 1.7 GB/h |
| Maximum | 1080p 30 fps | ≈ 8 Mbit/s | ≈ 3.4 GB/h |

If the phone's uplink is slower than the preset needs:
- **Direct:** the video stays real time but drops frames (`degradationPreference: maintain-resolution`).
- **Relay:** data queues on the phone, which means those seconds are **not yet safe on the PC**.

The PC window shows the received bitrate and warns when it falls below half the
target.

### Network buffer

Off by default (real time). With a buffer of 5, 15 or 30 s (Settings), the phone
records with `MediaRecorder` at full frame rate and sends the chunks over a
reliable channel (the WebRTC DataChannel, or the encrypted relay):

- A network drop **delays** the video instead of freezing it.
- Waiting chunks stay in the page's **memory only**, never in the phone's storage.
  They would be lost if the phone were destroyed, which is why the buffer is opt-in.
- The phone and the PC show the current delay. In relay mode it is measured
  from the PC's acknowledgements, because the OS's TCP buffers are invisible to the page.
- When the delay exceeds the buffer, the phone lowers the quality one step,
  in a new file. If even Economy is too much for the network, the delay keeps
  growing, and the display says so.
- Stopping is immediate: the remaining seconds keep flowing in the background,
  and the phone confirms when the PC has everything.

---

# For developers

## Build

Everything builds in Docker. Only Docker and Docker Compose are needed on the host.

```sh
git clone https://github.com/p3374/GhostCam.git && cd GhostCam
docker compose run --rm build-windows   # -> dist/ghostcam.exe (single file, license texts embedded)
docker compose run --rm build-release   # -> every release binary (Windows, macOS, Linux) + SHA256SUMS.txt
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
| `-tunnel <provider>` | saved setting | `cloudflare` or `localhostrun` for this run |
| `-public-url <https-url>` | saved setting | Your own address for this run (see [Internet link](#internet-link-tunnel)) |
| `-cloudflared <path>` | auto-downloaded and updated | Use this cloudflared binary instead |
| `-stun <urls>` | Cloudflare + Google | Comma-separated STUN servers; empty disables STUN |
| `-turn <url> -turn-user <u>` | none | TURN server; password via `GHOSTCAM_TURN_PASS` |
| `-headless` | off | No window: QR code and status in the terminal (same as `ghostcam run`) |
| `-web-dir <dir>` | embedded | Serve `web/` from disk (live editing) |

Logs are written to `%LOCALAPPDATA%\GhostCam\ghostcam.log`.

## Languages

The interface is available in **English** and **French**:
- The phone page follows the phone's browser language.
- The PC window follows the system language, or the choice made in Settings → *Language*.
- Unknown languages fall back to English.

Translations live in [`web/locales/`](web/locales), one file per language in
**i18next JSON v4** format (nested keys, `{{variables}}`, CLDR plural forms such
as `_one` / `_many` / `_other`). `en.json` is the source. The format works as is
with Weblate, Crowdin, Lokalise and similar tools. To add a language, see
[CONTRIBUTING.md](CONTRIBUTING.md#translations).

## Testing

An end-to-end test runs a headless Chromium with a fake camera as the phone. It
records 3 videos, changes quality and folder mid-session, then kills the browser
mid-video (phone "destroyed"). It passes only if ffmpeg can decode every file.

```sh
docker compose run --rm build-linux
MODE=webrtc docker compose run --rm e2e                   # direct path
MODE=relay  docker compose run --rm e2e                   # UDP blocked: encrypted relay
MODE=webrtc docker compose run --rm -e QUALITY2=high e2e  # quality going up instead of down
LOCALE=en-US docker compose run --rm e2e                  # phone and PC window in English (default: fr-FR)
BUFFER=5 docker compose run --rm e2e                      # network buffer on
BUFFER=5 RATE=300kbit QUALITY1=high docker compose run --rm e2e  # throttled network: delay, quality steps down, nothing lost
docker compose run --rm i18n                              # translation files: keys, plurals, variables
docker compose run --rm --no-deps dev go test ./internal/... # unit tests (updater, archive extraction)
# self-update end to end: a fake GitHub serves 1.3.1 to a running 1.3.0
VERSION_OVERRIDE=1.3.0 OUT_NAME=ghostcam-old docker compose run --rm build-linux
VERSION_OVERRIDE=1.3.1 OUT_NAME=ghostcam-new docker compose run --rm build-linux
docker compose run --rm selfupdate
```

The test prints, for each file, the frame count and the resolution of the first
and last frames.

## Project layout

```
cmd/ghostcam/        entry point: subcommands (cli.go), terminal UI (term.go), tunnels, UPnP, updates; native window (gui_windows.go)
internal/server/     HTTP (public + admin), WebSocket signaling, WebRTC session, encrypted relay, settings
internal/record/     fsync'ed append-only file, live WebM muxer
internal/pairing/    pairing secret, HMAC auth, AES-GCM frame decryption
internal/tunnel/     tunnels: cloudflared Quick Tunnel, localhost.run (SSH, built in), custom URL
internal/upnp/       router port mapping + lease renewal
internal/update/     self-update: release check, verified download, binary swap, restart
web/                 phone page (index.html, app.js), PC window (admin.html), i18n.js, icon (embedded in the binary)
web/locales/         translations, i18next JSON v4 (en.json = source)
notices.go           embeds LICENSE + third_party_licenses.txt (shown in the app)
scripts/             gen-notices.sh: regenerates third_party_licenses.txt
test/e2e/            Playwright end-to-end test
test/i18n/           translation consistency check
test/update/         self-update end-to-end test
VERSION              version embedded in the binaries (compared with the latest release)
```

## Known issues and roadmap

Good places to start contributing:

- **Occasional direct-connection miss.** In tests with many rapid reconnections, about 1 in 8 attempts did not connect directly and fell back to the relay after ~12 s. Cause not identified yet.
- **iOS background.** iOS stops the camera when Safari goes to the background. The page reconnects with a fresh camera when it comes back, but this is not yet tested on a real iPhone.
- **Relay resolution.** Lowering the quality is confirmed in direct mode. In relay mode, with Chromium's fake camera, MediaRecorder kept the native resolution. This needs checking on real phones.
- **Self-hosted phone page**, to remove the trust in the tunnel for the JavaScript (see Security model).
- **macOS and Linux are experimental.** They use the browser instead of a native window. The macOS download path (cloudflared `.tgz`) is tested against Cloudflare's real archives, but GhostCam itself hasn't run on a real Mac yet: reports are welcome. The macOS binary isn't notarized, hence the `xattr` step.
- **Unit tests and CI.** Today there is only the end-to-end test.
- **More languages.** English and French are available: translations are welcome (see [Languages](#languages)).
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
