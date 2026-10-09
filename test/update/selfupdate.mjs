// SPDX-License-Identifier: AGPL-3.0-or-later
// Self-update end-to-end test (Linux). A fake GitHub serves a "v1.3.1"
// release; a running 1.3.0 must find it, download and verify it, replace its
// own binary, restart and come back as 1.3.1. A second run serves a tampered
// binary, which must be refused without touching the installed one.
//
//   VERSION_OVERRIDE=1.3.0 OUT_NAME=ghostcam-old docker compose run --rm build-linux
//   VERSION_OVERRIDE=1.3.1 OUT_NAME=ghostcam-new docker compose run --rm build-linux
//   docker compose run --rm selfupdate
import http from 'node:http';
import { spawn } from 'node:child_process';
import { createHash } from 'node:crypto';
import { readFileSync, copyFileSync, mkdirSync, rmSync, existsSync, chmodSync } from 'node:fs';

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const sha = (b) => createHash('sha256').update(b).digest('hex');
const ASSET = 'ghostcam-linux-x64';
const NEW = readFileSync('/src/dist/ghostcam-new');
const OLD = readFileSync('/src/dist/ghostcam-old');

let served = { body: NEW, digest: sha(NEW) };
const gh = http.createServer((req, res) => {
  if (req.url === '/api/latest') {
    res.setHeader('Content-Type', 'application/json');
    res.end(JSON.stringify({
      tag_name: 'v1.3.1', html_url: 'http://127.0.0.1:9999/notes', draft: false, prerelease: false,
      assets: [{ name: ASSET, size: served.body.length, digest: 'sha256:' + served.digest,
        browser_download_url: `http://127.0.0.1:9999/owner/repo/releases/download/v1.3.1/${ASSET}` }],
    }));
  } else if (req.url.startsWith('/owner/repo/releases/download/')) {
    res.end(served.body);
  } else { res.statusCode = 404; res.end(); }
}).listen(9999, '127.0.0.1');

const status = async () => (await fetch('http://127.0.0.1:8081/api/status')).json();
const post = (path) => fetch('http://127.0.0.1:8081' + path, { method: 'POST', headers: { 'X-GhostCam': '1' } });
async function waitFor(pred, ms, what) {
  const t0 = Date.now();
  while (Date.now() - t0 < ms) {
    try { const s = await status(); if (pred(s)) return s; } catch {}
    await sleep(500);
  }
  throw new Error('timeout waiting for ' + what);
}

function install() {
  rmSync('/app', { recursive: true, force: true });
  mkdirSync('/app');
  copyFileSync('/src/dist/ghostcam-old', '/app/ghostcam');
  chmodSync('/app/ghostcam', 0o755);
}
function launch() {
  return spawn('/app/ghostcam', ['-headless', '-no-upnp', '-stun', '', '-public-url', 'http://127.0.0.1:8080', '-out', '/tmp/rec',
    '-update-api', 'http://127.0.0.1:9999/api/latest', '-update-prefix', 'http://127.0.0.1:9999/owner/repo/releases/download/'],
  { stdio: ['ignore', 'ignore', 'pipe'] });
}

let ok = true;
const check = (cond, msg) => { console.log((cond ? 'ok   ' : 'FAIL ') + msg); if (!cond) ok = false; };

// --- 1. genuine update -------------------------------------------------------
install();
let app = launch();
let s = await waitFor((x) => x.update && x.update.current, 15000, 'start');
check(s.update.current === '1.3.0', `running version ${s.update.current}`);
await post('/api/update/check');
s = await waitFor((x) => x.update.version, 15000, 'update detection');
check(s.update.version === '1.3.1', `update found: ${s.update.version}`);
const r = await post('/api/update');
check(r.status === 202, `update accepted (HTTP ${r.status})`);
const t0 = Date.now();
s = await waitFor((x) => x.update.current === '1.3.1', 60000, 'restart as 1.3.1');
check(true, `restarted as ${s.update.current} after ${((Date.now() - t0) / 1000).toFixed(1)}s`);
check(sha(readFileSync('/app/ghostcam')) === sha(NEW), 'installed binary is the published one');
check(existsSync('/app/ghostcam.old') && sha(readFileSync('/app/ghostcam.old')) === sha(OLD), 'previous binary kept as ghostcam.old');
check(!existsSync('/app/ghostcam.new'), 'no temporary file left');
await post('/api/update/check');
s = await waitFor((x) => x.update.state, 15000, 'up-to-date check');
check(s.update.state.key === 'pc.update.upToDate' && !s.update.version, `after update: ${s.update.state.key}`);
spawn('pkill', ['-f', '/app/ghostcam']);
await sleep(2000);

// --- 2. tampered binary --------------------------------------------------------
install();
served = { body: Buffer.from(NEW.toString('latin1').replace('GhostCam', 'EvilCam!'), 'latin1'), digest: sha(NEW) };
app = launch();
await waitFor((x) => x.update && x.update.current === '1.3.0', 15000, 'start (tampered run)');
await post('/api/update/check');
await waitFor((x) => x.update.version, 15000, 'update detection (tampered run)');
await post('/api/update');
s = await waitFor((x) => x.update.state && /failed/.test(x.update.state.key), 30000, 'tampered binary rejected');
check(/SHA-256 mismatch/.test(s.update.state.vars.detail), `rejected: ${s.update.state.vars.detail.slice(0, 40)}…`);
check(sha(readFileSync('/app/ghostcam')) === sha(OLD), 'installed binary untouched');
check(s.update.current === '1.3.0', 'still running 1.3.0');
spawn('pkill', ['-f', '/app/ghostcam']);
await sleep(1000);

gh.close();
console.log(ok ? 'SELF-UPDATE OK' : 'SELF-UPDATE FAILED');
process.exit(ok ? 0 : 1);
