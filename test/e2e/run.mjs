// SPDX-License-Identifier: AGPL-3.0-or-later
﻿// End-to-end test: a headless Chromium with a fake camera plays the phone,
// records several videos, then is killed mid-video (phone destroyed).
// Every file on the "PC" side must decode with ffmpeg.
//
//   MODE=webrtc|relay docker compose run --rm e2e
import { chromium } from 'playwright';
import { spawn, execFileSync } from 'node:child_process';
import { readdirSync, statSync, rmSync } from 'node:fs';

const MODE = process.env.MODE || 'webrtc';
const LOCALE = process.env.LOCALE || 'fr-FR'; // browser language of the phone and the PC window
const EXPECT = { fr: { go: 'Activer la caméra', pill: 'En attente' }, en: { go: 'Enable camera', pill: 'Waiting' } }[LOCALE.split('-')[0]];
const OUT = `/tmp/rec-${MODE}`;
const OUT2 = `/tmp/rec-${MODE}-moved`; // folder chosen in settings after video 1
const VIDEOS = 3;
const QUALITY2 = process.env.QUALITY2 || 'eco'; // quality set after video 1
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const status = async () => (await fetch('http://localhost:8081/api/status')).json();

rmSync(OUT, { recursive: true, force: true });
rmSync(OUT2, { recursive: true, force: true });
rmSync('/root/.cache/GhostCam', { recursive: true, force: true });
const srv = spawn('/src/dist/ghostcam-linux', ['-headless', '-no-upnp', '-stun', '', '-public-url', 'http://localhost:8080',
  '-out', OUT, '-admin', '127.0.0.1:8081'], { stdio: ['ignore', 'ignore', 'inherit'] });

let url;
for (let i = 0; i < 50 && !url; i++) { try { url = (await status()).url; } catch {} await sleep(200); }
if (!url) throw new Error('server did not start');

const args = ['--use-fake-ui-for-media-stream', '--use-fake-device-for-media-stream'];
// No UDP at all: WebRTC cannot connect, the page must fall back to the relay.
if (MODE === 'relay') args.push('--force-webrtc-ip-handling-policy=disable_non_proxied_udp');
const browser = await chromium.launch({ args });
const page = await (await browser.newContext({ bypassCSP: true, locale: LOCALE })).newPage(); // bypassCSP: test-only, lets Playwright evaluate

// PC window in the same language.
const pcPage = await (await browser.newContext({ bypassCSP: true, locale: LOCALE })).newPage();
await pcPage.goto('http://localhost:8081/');
await pcPage.waitForFunction((want) => document.querySelector('#pill').textContent === want, EXPECT.pill, { timeout: 10000 });
console.log(`PC window [${await pcPage.evaluate(() => document.documentElement.lang)}]: "${await pcPage.textContent('#pill')}"`);
await pcPage.close();
page.on('console', (m) => console.log('  [phone]', m.text()));

await page.goto(url);
await page.waitForFunction((want) => document.querySelector('#go').textContent === want, EXPECT.go, { timeout: 10000 });
console.log(`phone [${await page.evaluate(() => document.documentElement.lang)}]: "${await page.textContent('#go')}"`);
await page.click('#go');
await page.waitForFunction(() => document.querySelector('#conn').className === 'ok' && !document.querySelector('#rec').disabled, null, { timeout: 60000 });
console.log('connected:', await page.textContent('#conn'));

for (let i = 1; i <= VIDEOS; i++) {
  await page.click('#rec');
  await page.waitForFunction(() => !document.querySelector('#timer').hidden, null, { timeout: 15000 });
  await sleep(4000);
  await page.click('#rec');
  await page.waitForFunction(() => !document.querySelector('#rec').classList.contains('on') && !document.querySelector('#rec').disabled, null, { timeout: 15000 });
  console.log(`video ${i}:`, await page.textContent('#toast-text'));
  if (i === 1) {
    // Settings changed on the PC while the phone stays connected.
    const r = await fetch('http://localhost:8081/api/settings', { method: 'POST', headers: { 'X-GhostCam': '1' },
      body: JSON.stringify({ quality: QUALITY2, outDir: OUT2 }) });
    console.log(`settings -> ${QUALITY2} + ${OUT2}`, r.status);
  }
  await sleep(1500);
}

// Phone destroyed mid-video: kill the browser without any goodbye.
await page.click('#rec');
await page.waitForFunction(() => !document.querySelector('#timer').hidden, null, { timeout: 15000 });
await sleep(3000);
execFileSync('pkill', ['-9', '-f', 'chrom']);
console.log('browser killed while recording');
for (let i = 0; i < 60; i++) { if ((await status()).state === 'waiting') break; await sleep(500); }
const st = await status();
console.log(`PC side: state=${st.state} count=${st.count}`);
srv.kill('SIGINT');
await sleep(1000);

let ok = true;
const probe = (p) => execFileSync('ffprobe', ['-v', 'error', '-count_frames', '-select_streams', 'v:0',
  '-show_entries', 'stream=width,height,nb_read_frames', '-of', 'csv=p=0', p]).toString().trim().split(',').map(Number);
const listed = [OUT, OUT2].flatMap((d) => { try { return readdirSync(d).filter((f) => !f.startsWith('.')).sort().map((f) => `${d}/${f}`); } catch { return []; } });
for (const p of listed) {
  let w = 0, h = 0, frames = 0;
  try { [w, h, frames] = probe(p); } catch { ok = false; }
  if (!(frames > 0)) ok = false;
  // Resolution timeline: first frame size and when the stream reached its final size.
  const sizes = execFileSync('ffprobe', ['-v', 'error', '-select_streams', 'v:0', '-show_entries', 'frame=width,height',
    '-of', 'csv=p=0', p]).toString().trim().split('\n');
  const final = sizes[sizes.length - 1], firstFull = sizes.indexOf(final);
  console.log(`${p}  ${(statSync(p).size / 1024).toFixed(0)} KiB  ${frames} frames  first=${sizes[0]} final=${final} (from frame ${firstFull})`);
}
const n1 = listed.filter((p) => p.startsWith(OUT + '/')).length, n2 = listed.length - n1;
if (n1 !== 1 || n2 !== VIDEOS) { ok = false; console.log(`expected 1 file in ${OUT} and ${VIDEOS} in ${OUT2}, got ${n1} / ${n2}`); }
console.log(ok ? 'E2E OK' : 'E2E FAILED');
process.exit(ok ? 0 : 1);
