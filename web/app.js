// SPDX-License-Identifier: AGPL-3.0-or-later
'use strict';
// GhostCam mobile client. Nothing is stored on the phone: frames go straight
// to the PC (WebRTC, DTLS-SRTP), or as AES-GCM encrypted chunks over the
// WebSocket when no WebRTC path exists.
//
// One pairing = one connection kept alive (and re-established on network
// loss); each Rec/Stop makes a separate file on the PC.

const $ = (id) => document.getElementById(id);
const { t } = I18N; // texts: web/locales/*.json, language of the phone's browser
const enc = new TextEncoder();
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

// The secret lives in the URL fragment (never sent to any server). Move it to
// sessionStorage so the address bar/history don't keep it but a reload works.
const params = new URLSearchParams(location.hash.slice(1));
if (params.get('t')) {
  try { sessionStorage.setItem('gc', JSON.stringify({ t: params.get('t'), f: params.get('f') })); } catch {}
}
let saved = {};
try { saved = JSON.parse(sessionStorage.getItem('gc') || '{}'); } catch {}
const TOKEN = params.get('t') || saved.t;
const PINNED_FP = (params.get('f') || saved.f || '').toLowerCase();
history.replaceState(null, '', location.pathname);

const S = {
  stream: null,
  transport: null, // current connection to the PC
  want: false,     // user wants to record (survives reconnections)
  recStart: 0,
  busy: false,
  count: 0,
  quality: { id: 'standard', width: 1280, height: 720, fps: 30, bitrate: 2_000_000 }, // chosen on the PC
  qualities: [],      // all presets, to step down when the buffer overflows
  localQuality: null, // automatic downgrade, until the PC settings change
  buffer: 0,          // seconds of video the phone may hold in memory (0 = real time)
};
let kAuth, kEnc, wakeLock;

// ------------------------------------------------------------------- crypto

const b64urlDecode = (s) => Uint8Array.from(atob(s.replace(/-/g, '+').replace(/_/g, '/')), (c) => c.charCodeAt(0));
const b64urlEncode = (b) => btoa(String.fromCharCode(...new Uint8Array(b))).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
const concat = (...parts) => {
  const arrs = parts.map((p) => (p instanceof Uint8Array ? p : new Uint8Array(p)));
  const out = new Uint8Array(arrs.reduce((n, a) => n + a.length, 0));
  let o = 0;
  for (const a of arrs) { out.set(a, o); o += a.length; }
  return out;
};

async function deriveKeys() {
  const master = await crypto.subtle.importKey('raw', b64urlDecode(TOKEN), { name: 'HMAC', hash: 'SHA-256' }, false, ['sign']);
  const auth = await crypto.subtle.sign('HMAC', master, enc.encode('ghostcam/auth/v1'));
  const encRaw = await crypto.subtle.sign('HMAC', master, enc.encode('ghostcam/enc/v1'));
  kAuth = await crypto.subtle.importKey('raw', auth, { name: 'HMAC', hash: 'SHA-256' }, false, ['sign']);
  kEnc = await crypto.subtle.importKey('raw', encRaw, { name: 'AES-GCM' }, false, ['encrypt']);
}

// MAC = HMAC(kAuth, nonce || kind || 0x00 || payload), see internal/pairing.
async function mac(nonce, kind, payload) {
  const msg = concat(nonce, enc.encode(kind), new Uint8Array([0]), enc.encode(payload));
  return b64urlEncode(await crypto.subtle.sign('HMAC', kAuth, msg));
}

// ----------------------------------------------------------------------- UI

let toastTimer;
function toast(text) {
  $('toast-text').textContent = text;
  $('toast').classList.add('show');
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => $('toast').classList.remove('show'), 3500);
}
function setConn(text, cls = '') {
  $('conn').textContent = text;
  $('conn').className = cls;
}
const fmtSize = (b) => (b < 1024 ** 3 ? t('common.mb', { n: I18N.num(b / 1024 ** 2, 1) }) : t('common.gb', { n: I18N.num(b / 1024 ** 3, 2) }));
const fmtTime = (s) => {
  const p = (n) => String(n).padStart(2, '0');
  return (s >= 3600 ? Math.floor(s / 3600) + ':' : '') + p(Math.floor(s / 60) % 60) + ':' + p(s % 60);
};

function render() {
  const rec = $('rec');
  const recording = S.want && S.recStart > 0;
  rec.classList.toggle('on', S.want);
  rec.disabled = S.busy || (!S.transport && !S.want);
  rec.setAttribute('aria-label', t(S.want ? 'phone.rec.stop' : 'phone.rec.start'));
  $('timer').hidden = !recording;
  if (!S.want) {
    $('info').textContent = S.count ? t('phone.info.count', { count: S.count }) : t('phone.info.tap');
  } else if (!recording) {
    $('info').textContent = t('phone.info.starting');
  } else {
    $('info').textContent = t('phone.info.recording');
  }
}
setInterval(() => {
  if (S.want && S.recStart) $('timer').textContent = fmtTime(Math.floor((Date.now() - S.recStart) / 1000));
}, 250);

// ----------------------------------------------------------- camera & power

async function ensureStream() {
  if (S.stream && S.stream.getTracks().every((t) => t.readyState === 'live')) return;
  if (S.stream) S.stream.getTracks().forEach((t) => t.stop());
  const video = { facingMode: { ideal: 'environment' }, ...videoConstraints(S.quality) };
  const basic = { facingMode: { ideal: 'environment' } };
  for (const v of [video, basic]) {
    try { S.stream = await navigator.mediaDevices.getUserMedia({ video: v, audio: true }); break; } catch {}
    try { S.stream = await navigator.mediaDevices.getUserMedia({ video: v }); break; } catch (e) { if (v === basic) throw e; }
  }
  $('preview').srcObject = S.stream;
}

// Quality is set on the PC and sent with each "started": it applies from the
// next video on, without reconnecting.
// "ideal" alone only moves the camera up; "max" (on the long side, so it
// works in portrait and landscape) lets the browser scale down too.
function videoConstraints(q) {
  const track = S.stream && S.stream.getVideoTracks()[0];
  const cur = track ? track.getSettings() : {};
  const portrait = cur.width && cur.height ? cur.width < cur.height : innerHeight > innerWidth;
  const [w, h] = portrait ? [q.height, q.width] : [q.width, q.height];
  const long = Math.max(q.width, q.height);
  return { width: { ideal: w, max: long }, height: { ideal: h, max: long }, frameRate: { ideal: q.fps, max: q.fps }, resizeMode: 'crop-and-scale' };
}

async function applyQuality(q, videoSender) {
  if (!q) return;
  S.quality = q;
  const track = S.stream.getVideoTracks()[0];
  if (track) {
    try { await track.applyConstraints(videoConstraints(q)); }
    catch (e) {
      console.warn('applyConstraints', e);
      await track.applyConstraints({ width: { ideal: q.width }, height: { ideal: q.height }, frameRate: { ideal: q.fps } }).catch(() => {});
    }
    const st = track.getSettings();
    console.info(`camera ${st.width}x${st.height}@${st.frameRate} for ${q.id}`);
  }
  if (videoSender) {
    const p = videoSender.getParameters();
    if (p.encodings && p.encodings[0]) {
      p.encodings[0].maxBitrate = q.bitrate;
      p.encodings[0].maxFramerate = q.fps;
      // Evidence video: on a weak uplink drop frames, keep the detail.
      p.degradationPreference = 'maintain-resolution';
      await videoSender.setParameters(p).catch((e) => console.warn('setParameters', e));
    }
  }
}

async function keepAwake() {
  try {
    if ('wakeLock' in navigator && document.visibilityState === 'visible') wakeLock = await navigator.wakeLock.request('screen');
  } catch (e) { console.warn('wake lock', e); }
}

document.addEventListener('visibilitychange', () => {
  if (document.visibilityState !== 'visible') return;
  keepAwake();
  // iOS stops the camera in background: reconnect with a fresh stream.
  if (S.stream && S.stream.getTracks().some((t) => t.readyState !== 'live') && S.transport) S.transport.close();
});

// ---------------------------------------------------------------- signaling

// Settings changed on the PC: applied from the next video.
function onConfig(m) {
  if (m.quality) S.quality = m.quality;
  if (typeof m.buffer === 'number') S.buffer = m.buffer;
  S.localQuality = null; // the user chose again: drop our automatic downgrade
}

function openSignaling() {
  return new Promise((resolve, reject) => {
    const ws = new WebSocket(`${location.protocol === "https:" ? "wss" : "ws"}://${location.host}/ws`);
    ws.binaryType = 'arraybuffer';
    // Replies are matched by type, so a late "stopped" never answers a "start".
    const queue = [], waiters = [];
    const deliver = () => {
      for (let i = 0; i < waiters.length; i++) {
        const w = waiters[i];
        const j = queue.findIndex((m) => (!w.type || m.type === w.type) && (!w.match || w.match(m)));
        if (j >= 0) { waiters.splice(i--, 1); w.resolve(queue.splice(j, 1)[0]); }
      }
    };
    ws.onmessage = (e) => {
      const m = JSON.parse(e.data);
      if (m.type === 'config') { onConfig(m); return; }
      if (m.type === 'ack') { if (ws.onAck) ws.onAck(m.bytes); return; } // relay: media bytes received by the PC
      queue.push(m);
      deliver();
    };
    ws.onclose = (e) => { while (waiters.length) waiters.shift().reject(new Error(`${t('phone.err.closed')} (${e.code})`)); };
    ws.onerror = () => reject(new Error(t('phone.err.unreachable')));
    ws.next = (timeout = 15000, type = null, match = null) => new Promise((res, rej) => {
      const w = {
        type,
        match,
        resolve: (m) => { clearTimeout(timer); res(m); },
        reject: (e) => { clearTimeout(timer); rej(e); },
      };
      const timer = setTimeout(() => { waiters.splice(waiters.indexOf(w), 1); rej(new Error(t('phone.err.noReply'))); }, timeout);
      waiters.push(w);
      deliver();
      if (waiters.includes(w) && ws.readyState !== WebSocket.OPEN) { waiters.splice(waiters.indexOf(w), 1); w.reject(new Error(t('phone.err.closed'))); }
    });
    ws.onopen = () => resolve(ws);
  });
}

function waitIceGathering(pc, ms) {
  if (pc.iceGatheringState === 'complete') return Promise.resolve();
  return new Promise((r) => {
    const t = setTimeout(r, ms);
    pc.addEventListener('icegatheringstatechange', () => { if (pc.iceGatheringState === 'complete') { clearTimeout(t); r(); } });
  });
}

// The answer must carry the DTLS fingerprint from the QR code: a relay in the
// middle (tunnel, TURN) cannot substitute its own endpoint.
function checkFingerprint(sdp) {
  const fps = [...sdp.matchAll(/^a=fingerprint:sha-256 ([0-9A-Fa-f:]+)/gm)].map((m) => m[1].replace(/:/g, '').toLowerCase());
  if (!fps.length || fps.some((fp) => fp !== PINNED_FP)) throw new Error('FINGERPRINT');
}

// ------------------------------------------- buffered recording (RAM only)
//
// With a buffer (Settings on the PC), the phone records with MediaRecorder at
// full frame rate and sends the chunks over a reliable, ordered channel: a
// network drop delays the video instead of freezing it. Chunks wait in this
// page's memory, never in the phone's storage. The relay always works this
// way; with buffer = 0 it just doesn't adapt the quality.

const SLICE = 16 * 1024;            // message size safe on every DataChannel
const IN_FLIGHT_SEC = 1;            // at most ~1 s of video handed to the channel at once:
                                    // what the channel holds can't be measured or reordered
const MAX_RAM = 150 * 1024 * 1024;  // hard cap: stop rather than exhaust memory
const STEP_EVERY = 5000;            // min ms between two automatic downgrades

function pickMime() {
  const types = ['video/webm;codecs=vp8,opus', 'video/webm', 'video/mp4'];
  return types.find((t) => window.MediaRecorder && MediaRecorder.isTypeSupported(t));
}

// Outbox: FIFO of {bin} media slices and {text} control items. Items stay in
// JS memory until the channel has room, which also keeps control messages
// ordered after the media they follow.
class Outbox {
  constructor(send, inFlight) {
    this.send = send;         // (item) => void
    this.inFlight = inFlight; // () => bytes still held by the channel
    this.items = [];
    this.bytes = 0;
    this.secs = 0; // duration of the queued media (item.sec), whatever its bitrate
    this.timer = setInterval(() => this.pump(), 50);
  }
  push(item) {
    if (this.closed) return;
    this.items.push(item);
    if (item.bin) this.bytes += item.bin.length;
    this.secs += item.sec || 0;
    this.pump();
  }
  pump() {
    const cap = Math.max(32 * 1024, (S.quality.bitrate / 8) * IN_FLIGHT_SEC);
    while (this.items.length && this.inFlight() < cap) {
      const it = this.items.shift();
      if (it.bin) this.bytes -= it.bin.length;
      this.secs -= it.sec || 0;
      this.send(it);
    }
  }
  pending() { return this.bytes + this.inFlight(); }
  // Seconds of video not yet on the network (what the channel holds is at most
  // ~1 s, estimated from the bitrate).
  pendingSeconds() { return Math.max(0, this.secs) + (this.inFlight() * 8) / S.quality.bitrate; }
  close() { this.closed = true; clearInterval(this.timer); this.items = []; this.bytes = 0; this.secs = 0; }
}

function startSegment(mime, onBytes) {
  const rec = new MediaRecorder(S.stream, { mimeType: mime, videoBitsPerSecond: S.quality.bitrate, audioBitsPerSecond: 128000 });
  let chain = Promise.resolve();
  rec.ondataavailable = (e) => {
    if (!e.data.size) return;
    chain = chain.then(async () => onBytes(new Uint8Array(await e.data.arrayBuffer())));
  };
  rec.start(1000);
  // Resolves once the last chunk has been handed to onBytes.
  return () => new Promise((r) => {
    if (rec.state === 'inactive') { chain.then(r); return; }
    rec.onstop = () => chain.then(r);
    rec.stop();
  });
}

function lowerQuality() {
  const list = (S.qualities || []).slice().sort((a, b) => a.bitrate - b.bitrate);
  const i = list.findIndex((q) => q.id === S.quality.id);
  return i > 0 ? list[i - 1] : null;
}

// bufferedRecording drives one connection's buffered mode, for every video
// recorded on it. io provides:
//   begin(mime)  queue the "start" of a new file        end(id)  queue its "stop", tagged with id
//   data(bytes)  queue media bytes (one MediaRecorder chunk = 1 s)
//   lag(s)       report seconds pending (not queued)    pending() / pendingSeconds()
//   reply(type, ms, match)  wait for a PC reply
//
// Every "stop" carries an id echoed by the PC: on a slow network the stop of an
// automatic downgrade is answered long after it was queued, and must not be
// mistaken for the answer to the user's final stop.
//
// Stopping never drops queued video: the button is released at once and the
// remaining seconds keep flowing in the background for as long as the
// connection lives (the status line shows them).
function bufferedRecording(io) {
  const mime = pickMime();
  let stopSeg = null, recording = false, lastStep = 0, stepping = false, stopId = 0;

  const begin = (q) => {
    S.quality = q; // the recorder takes its bitrate from S.quality
    io.begin(mime);
    stopSeg = startSegment(mime, io.data);
  };

  const monitor = () => {
    const sec = io.pendingSeconds();
    io.lag(sec);
    if (sec >= 1) setConn(t('phone.conn.buffer', { n: Math.round(sec) }), sec > Math.max(S.buffer, 1) / 2 ? 'warn' : 'ok');
    else setConn(t('phone.conn.connected', { mode: io.label }), 'ok');
    if (!recording) return;
    if (io.pending() > MAX_RAM) { toast(t('phone.toast.memoryFull')); toggle(); return; }
    // Buffer over the limit: one quality step down, in a new file. The old
    // file keeps everything already recorded.
    if (S.buffer > 0 && sec > S.buffer && !stepping && Date.now() - lastStep > STEP_EVERY) {
      const lower = lowerQuality();
      if (!lower) return;
      stepping = true;
      lastStep = Date.now();
      S.localQuality = lower;
      (async () => {
        await stopSeg();
        io.end(++stopId);
        await applyQuality(lower);
        begin(lower);
        toast(t('phone.toast.qualityDown', { quality: t(`pc.quality.${lower.id}.label`) }));
      })().finally(() => { stepping = false; });
    }
  };
  const tick = setInterval(monitor, 1000);

  return {
    async start() {
      io.begin(mime);
      const m = await io.reply('started', 15000);
      const q = S.localQuality || m.quality || S.quality;
      await applyQuality(q);
      S.quality = q;
      stopSeg = startSegment(mime, io.data);
      recording = true;
    },
    // Resolves at once. If video is still queued, returns {flushing: seconds,
    // done: promise of the PC's "stopped"}; otherwise waits for "stopped".
    async stop() {
      recording = false;
      while (stepping) await sleep(50);
      await stopSeg();
      const id = ++stopId;
      io.end(id);
      const done = io.reply('stopped', 3600000, (m) => m.id === String(id));
      const sec = io.pendingSeconds();
      return sec >= 1 ? { flushing: sec, done } : done;
    },
    close() {
      clearInterval(tick);
      recording = false;
      if (stopSeg) stopSeg(); // connection lost: stop feeding a dead channel
    },
  };
}

// ---------------------------------------------------- transport: WebRTC

// Chrome starts WebRTC at ~300 kbit/s and ramps up, so the first video after
// connecting would be low-res. Start at the chosen quality instead (ignored by
// Safari). Only adds an fmtp parameter: the pinned fingerprint is untouched.
function withStartBitrate(sdp, bps) {
  const m = sdp.match(/^a=rtpmap:(\d+) VP8\/90000\r?$/m);
  if (!m) return sdp;
  const kbps = `x-google-start-bitrate=${Math.round(bps / 1000)}`;
  const fmtp = new RegExp(`^(a=fmtp:${m[1]} .*?)(\\r?)$`, 'm');
  return fmtp.test(sdp) ? sdp.replace(fmtp, `$1;${kbps}$2`) : sdp.replace(m[0], `${m[0].replace(/\r$/, '')}\r\na=fmtp:${m[1]} ${kbps}`);
}

async function connectWebRTC() {
  const ws = await openSignaling();
  const pc = new RTCPeerConnection();
  let ok = false;
  try {
    const hello = await ws.next();
    const nonce = b64urlDecode(hello.nonce);
    if (hello.quality) S.quality = hello.quality;
    S.buffer = hello.buffer || 0;
    S.qualities = hello.qualities || [];
    pc.setConfiguration({ iceServers: hello.iceServers || [] });
    const senders = S.stream.getTracks().map((track) => ({ track, sender: pc.addTransceiver(track, { direction: 'sendonly', streams: [S.stream] }).sender }));
    const dc = pc.createDataChannel('control');
    dc.binaryType = 'arraybuffer';

    await pc.setLocalDescription(await pc.createOffer());
    await waitIceGathering(pc, 3000);
    const sdp = pc.localDescription.sdp;
    ws.send(JSON.stringify({ type: 'offer', sdp, mac: await mac(nonce, 'offer', sdp) }));
    const answer = await ws.next();
    checkFingerprint(answer.sdp);
    await pc.setRemoteDescription({ type: 'answer', sdp: withStartBitrate(answer.sdp, S.quality.bitrate) });
    for (const c of answer.candidates || []) await pc.addIceCandidate({ candidate: c, sdpMLineIndex: 0 });

    // No media until Rec: saves data and battery while paired.
    for (const s of senders) await s.sender.replaceTrack(null);
    const videoSender = (senders.find((s) => s.track.kind === 'video') || {}).sender;

    await new Promise((resolve, reject) => {
      const timer = setTimeout(() => reject(new Error('timeout WebRTC')), 12000);
      dc.onopen = () => { clearTimeout(timer); resolve(); };
      pc.onconnectionstatechange = () => { if (pc.connectionState === 'failed') { clearTimeout(timer); reject(new Error('ICE failed')); } };
    });
    ws.close(); // signaling no longer needed
    ok = true;

    // Text = real-time control (JSON). Buffered mode uses binary frames only
    // (start/data/stop, as the relay): mixing text and binary messages did not
    // keep their relative order end to end.
    const pending = new Set(); // waiters: {type, match, res, rej}
    dc.onmessage = (e) => {
      const m = JSON.parse(e.data);
      if (m.type === 'config') { onConfig(m); return; }
      for (const w of pending) {
        // match skips e.g. the "stopped" of an earlier automatic downgrade
        if (w.type === m.type && (!w.match || w.match(m))) { pending.delete(w); w.res(m); return; }
      }
    };
    dc.addEventListener('close', () => { for (const w of pending) w.rej(new Error(t('phone.err.closed'))); pending.clear(); });
    const reply = (type, ms, match = null) => new Promise((res, rej) => {
      const w = {
        type, match,
        res: (m) => { clearTimeout(timer); res(m); },
        rej: (e) => { clearTimeout(timer); rej(e); },
      };
      const timer = setTimeout(() => { pending.delete(w); rej(new Error(t('phone.err.noReply'))); }, ms);
      pending.add(w);
    });
    const request = (type, expect) => { const r = reply(expect, 8000); dc.send(JSON.stringify({ type })); return r; };

    const closed = new Promise((resolve) => {
      let timer;
      const check = () => {
        const s = pc.connectionState;
        if (s === 'connected') { clearTimeout(timer); timer = null; setConn(t('phone.conn.connected', { mode: t('phone.mode.direct') }), 'ok'); }
        if (s === 'disconnected' && !timer) { setConn(t('phone.conn.unstable'), 'warn'); timer = setTimeout(resolve, 8000); }
        if (s === 'failed' || s === 'closed') resolve();
      };
      pc.onconnectionstatechange = check;
      dc.onclose = resolve;
    });

    // One outbox and one controller per connection, created on first use:
    // a new video always queues behind the end of the previous one.
    let outbox = null, buffered = null, mode = null;
    const getBuffered = () => {
      if (buffered) return buffered;
      outbox = new Outbox((it) => dc.send(it.ctrl || it.bin), () => dc.bufferedAmount);
      buffered = bufferedRecording({
        label: t('phone.mode.direct'),
        begin: (mime) => outbox.push({ ctrl: concat(new Uint8Array([FRAME_START]), enc.encode(mime)) }),
        end: (id) => outbox.push({ ctrl: concat(new Uint8Array([FRAME_STOP]), enc.encode(String(id))) }),
        data: (bytes) => {
          for (let o = 0; o < bytes.length; o += SLICE) {
            const part = bytes.subarray(o, o + SLICE);
            outbox.push({ bin: concat(new Uint8Array([FRAME_DATA]), part), sec: part.length / bytes.length });
          }
        },
        lag: (sec) => { try { dc.send(JSON.stringify({ type: 'lag', seconds: Math.round(sec * 10) / 10 })); } catch {} },
        pending: () => outbox.pending(),
        pendingSeconds: () => outbox.pendingSeconds(),
        reply,
      });
      return buffered;
    };

    return {
      kind: 'direct', // phone.mode.<kind>
      closed,
      async start() {
        if (S.buffer > 0 && pickMime()) {
          mode = 'buffered';
          return getBuffered().start();
        }
        mode = 'rtp';
        const m = await request('start', 'started');
        await applyQuality(S.localQuality || m.quality, videoSender);
        for (const s of senders) await s.sender.replaceTrack(s.track);
      },
      async stop() {
        if (mode === 'buffered') return buffered.stop();
        for (const s of senders) await s.sender.replaceTrack(null);
        await sleep(400); // let in-flight packets reach the PC
        return request('stop', 'stopped');
      },
      close() {
        if (buffered) buffered.close();
        if (outbox) outbox.close();
        pc.close();
      },
    };
  } finally {
    if (!ok) pc.close();
    ws.close();
  }
}

// ------------------------------------------- transport: encrypted relay

const FRAME_START = 1, FRAME_DATA = 2, FRAME_STOP = 3, FRAME_LAG = 4;

async function connectRelay() {
  if (!pickMime()) throw new Error(t('phone.err.noRecorder'));
  const ws = await openSignaling();
  let nonce;
  try {
    const hello = await ws.next();
    nonce = b64urlDecode(hello.nonce);
    if (hello.quality) S.quality = hello.quality;
    S.buffer = hello.buffer || 0;
    S.qualities = hello.qualities || [];
    ws.send(JSON.stringify({ type: 'relay', mac: await mac(nonce, 'relay', '') }));
    if ((await ws.next()).type !== 'ready') throw new Error(t('phone.err.refused'));
  } catch (e) { ws.close(); throw e; }

  // Every frame, control included, is encrypted: seq(8) || iv(12) || AES-GCM(type || payload).
  // Frames are encrypted and sent strictly in order.
  let seq = 0n, chain = Promise.resolve(), encrypting = 0;
  // The OS keeps TCP data that WebSocket.bufferedAmount no longer counts: the
  // real backlog is the media sent minus what the PC acknowledged.
  let sentMedia = 0, acked = 0;
  ws.onAck = (n) => { acked = n; };
  const sendFrame = (type, body) => {
    encrypting += body.length;
    chain = chain.then(async () => {
      const seqBytes = new Uint8Array(8);
      new DataView(seqBytes.buffer).setBigUint64(0, seq++);
      const iv = crypto.getRandomValues(new Uint8Array(12));
      const ct = await crypto.subtle.encrypt({ name: 'AES-GCM', iv, additionalData: concat(nonce, seqBytes) }, kEnc, concat(new Uint8Array([type]), body));
      encrypting -= body.length;
      if (ws.readyState === WebSocket.OPEN) ws.send(concat(seqBytes, iv, new Uint8Array(ct)));
      if (type === FRAME_DATA) sentMedia += body.length;
    });
    return chain;
  };
  const ping = setInterval(() => { if (ws.readyState === WebSocket.OPEN) ws.send('{"type":"ping"}'); }, 10000);
  const closed = new Promise((r) => ws.addEventListener('close', r));
  closed.then(() => clearInterval(ping));

  // Control frames carry their payload in "ctrl" (not counted as pending media).
  const outbox = new Outbox((it) => sendFrame(it.frame, it.ctrl || it.bin || new Uint8Array()), () => Math.max(0, sentMedia - acked) + encrypting);
  const rec = bufferedRecording({
    label: t('phone.mode.relay'),
    begin: (mime) => outbox.push({ frame: FRAME_START, ctrl: enc.encode(mime) }),
    end: (id) => outbox.push({ frame: FRAME_STOP, ctrl: enc.encode(String(id)) }),
    data: (bytes) => outbox.push({ frame: FRAME_DATA, bin: bytes, sec: 1 }),
    lag: (sec) => sendFrame(FRAME_LAG, enc.encode(String(Math.round(sec * 10) / 10))),
    pending: () => outbox.pending(),
    pendingSeconds: () => outbox.pendingSeconds(),
    reply: (type, ms, match) => ws.next(ms, type, match),
  });

  return {
    kind: 'relay',
    closed,
    start: () => rec.start(),
    stop: () => rec.stop(),
    close() {
      rec.close();
      outbox.close();
      ws.close();
    },
  };
}

// ---------------------------------------------------------- main loop

async function startRecording(tr) {
  await tr.start();
  S.recStart = Date.now();
  render();
}

async function connectionLoop() {
  let preferWebRTC = true;
  for (;;) {
    let tr;
    try {
      await ensureStream();
      setConn(t('phone.conn.connecting'));
      tr = preferWebRTC ? await connectWebRTC() : await connectRelay();
    } catch (e) {
      console.warn(e);
      if (e.message === 'FINGERPRINT') {
        setConn(t('phone.conn.refused'), 'warn');
        return;
      }
      setConn(preferWebRTC ? t('phone.conn.noDirect') : t('phone.conn.retry', { detail: e.message }), 'warn');
      preferWebRTC = !preferWebRTC;
      await sleep(1500);
      continue;
    }

    S.transport = tr;
    setConn(t('phone.conn.connected', { mode: t(`phone.mode.${tr.kind}`) }), 'ok');
    render();
    if (S.want) {
      // Connection came back during a video: resume in a new file.
      try { await startRecording(tr); toast(t('phone.toast.resumed')); } catch (e) { tr.close(); }
    }

    await tr.closed;
    tr.close();
    S.transport = null;
    S.recStart = 0;
    render();
    setConn(t('phone.conn.lost'), 'warn');
    if (S.want) toast(t('phone.toast.lost'));
    await sleep(1000);
  }
}

async function toggle() {
  if (S.busy) return;
  S.busy = true;
  render();
  const tr = S.transport;
  try {
    if (!S.want) {
      S.want = true;
      render();
      if (tr) await startRecording(tr);
    } else {
      S.want = false;
      S.recStart = 0;
      render();
      if (tr) {
        const r = await tr.stop();
        S.count++;
        const saved = (m) => toast(m && m.bytes ? t('phone.toast.savedSize', { size: fmtSize(m.bytes) }) : t('phone.toast.saved'));
        if (r && r.done) {
          // Buffered: the last seconds are still on their way to the PC.
          toast(t('phone.toast.flushing', { n: Math.round(r.flushing) }));
          r.done.then(saved, () => toast(t('phone.toast.flushFailed')));
        } else {
          saved(r);
        }
      }
    }
  } catch (e) {
    console.warn(e);
    toast(t('phone.toast.error', { detail: e.message }));
    if (tr && S.want) tr.close(); // reconnect; the loop resumes recording
  } finally {
    S.busy = false;
    render();
  }
}

async function activate() {
  $('go').disabled = true;
  try {
    await deriveKeys();
    await ensureStream();
    await keepAwake();
  } catch (e) {
    $('intro-text').textContent = t('phone.intro.cameraError', { detail: e.message });
    $('go').disabled = false;
    return;
  }
  $('intro').hidden = true;
  render();
  connectionLoop();
}

I18N.init().catch((e) => console.warn('i18n', e)).finally(() => {
  if (!TOKEN || !PINNED_FP || !window.isSecureContext) {
    $('intro-text').textContent = t('phone.intro.invalidLink');
    $('go').hidden = true;
  } else {
    $('go').addEventListener('click', activate);
    $('rec').addEventListener('click', toggle);
  }
  render();
});
