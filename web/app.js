// SPDX-License-Identifier: AGPL-3.0-or-later
'use strict';
// GhostCam mobile client. Nothing is stored on the phone: frames go straight
// to the PC (WebRTC, DTLS-SRTP), or as AES-GCM encrypted chunks over the
// WebSocket when no WebRTC path exists.
//
// One pairing = one connection kept alive (and re-established on network
// loss); each Rec/Stop makes a separate file on the PC.

const $ = (id) => document.getElementById(id);
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
  quality: { width: 1280, height: 720, fps: 30, bitrate: 2_000_000 }, // chosen on the PC
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
const fmtSize = (b) => (b < 1024 ** 3 ? (b / 1024 ** 2).toFixed(1).replace('.', ',') + ' Mo' : (b / 1024 ** 3).toFixed(2).replace('.', ',') + ' Go');
const fmtTime = (s) => {
  const p = (n) => String(n).padStart(2, '0');
  return (s >= 3600 ? Math.floor(s / 3600) + ':' : '') + p(Math.floor(s / 60) % 60) + ':' + p(s % 60);
};

function render() {
  const rec = $('rec');
  const recording = S.want && S.recStart > 0;
  rec.classList.toggle('on', S.want);
  rec.disabled = S.busy || (!S.transport && !S.want);
  rec.setAttribute('aria-label', S.want ? "Arrêter l'enregistrement" : "Démarrer l'enregistrement");
  $('timer').hidden = !recording;
  if (!S.want) {
    $('info').textContent = S.count ? `${S.count} vidéo${S.count > 1 ? 's' : ''} enregistrée${S.count > 1 ? 's' : ''} sur le PC` : 'Appuyez pour filmer';
  } else if (!recording) {
    $('info').textContent = 'Démarrage…';
  } else {
    $('info').textContent = "Enregistrement sur le PC, rien n'est gardé ici";
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

function openSignaling() {
  return new Promise((resolve, reject) => {
    const ws = new WebSocket(`${location.protocol === "https:" ? "wss" : "ws"}://${location.host}/ws`);
    ws.binaryType = 'arraybuffer';
    const queue = [], waiters = [];
    ws.onmessage = (e) => {
      const m = JSON.parse(e.data);
      waiters.length ? waiters.shift().resolve(m) : queue.push(m);
    };
    ws.onclose = (e) => { while (waiters.length) waiters.shift().reject(new Error(`connexion fermée (${e.code})`)); };
    ws.onerror = () => reject(new Error('serveur injoignable'));
    ws.next = (timeout = 15000) => {
      if (queue.length) return Promise.resolve(queue.shift());
      if (ws.readyState !== WebSocket.OPEN) return Promise.reject(new Error('connexion fermée'));
      return new Promise((res, rej) => {
        const w = {
          resolve: (m) => { clearTimeout(t); res(m); },
          reject: (e) => { clearTimeout(t); rej(e); },
        };
        const t = setTimeout(() => { waiters.splice(waiters.indexOf(w), 1); rej(new Error('pas de réponse du PC')); }, timeout);
        waiters.push(w);
      });
    };
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
    pc.setConfiguration({ iceServers: hello.iceServers || [] });
    const senders = S.stream.getTracks().map((track) => ({ track, sender: pc.addTransceiver(track, { direction: 'sendonly', streams: [S.stream] }).sender }));
    const dc = pc.createDataChannel('control');

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
      const t = setTimeout(() => reject(new Error('timeout WebRTC')), 12000);
      dc.onopen = () => { clearTimeout(t); resolve(); };
      pc.onconnectionstatechange = () => { if (pc.connectionState === 'failed') { clearTimeout(t); reject(new Error('ICE failed')); } };
    });
    ws.close(); // signaling no longer needed
    ok = true;

    const pending = {};
    dc.onmessage = (e) => { const m = JSON.parse(e.data); if (pending[m.type]) pending[m.type](m); };
    const request = (type, expect) => new Promise((res, rej) => {
      const t = setTimeout(() => { delete pending[expect]; rej(new Error('pas de réponse du PC')); }, 8000);
      pending[expect] = (m) => { clearTimeout(t); delete pending[expect]; res(m); };
      dc.send(JSON.stringify({ type }));
    });

    const closed = new Promise((resolve) => {
      let timer;
      const check = () => {
        const s = pc.connectionState;
        if (s === 'connected') { clearTimeout(timer); timer = null; setConn('Connecté au PC · direct', 'ok'); }
        if (s === 'disconnected' && !timer) { setConn('Réseau instable…', 'warn'); timer = setTimeout(resolve, 8000); }
        if (s === 'failed' || s === 'closed') resolve();
      };
      pc.onconnectionstatechange = check;
      dc.onclose = resolve;
    });

    return {
      kind: 'direct',
      closed,
      async start() {
        const m = await request('start', 'started');
        await applyQuality(m.quality, videoSender);
        for (const s of senders) await s.sender.replaceTrack(s.track);
      },
      async stop() {
        for (const s of senders) await s.sender.replaceTrack(null);
        await sleep(400); // let in-flight packets reach the PC
        return request('stop', 'stopped');
      },
      close() { pc.close(); },
    };
  } finally {
    if (!ok) pc.close();
    ws.close();
  }
}

// ------------------------------------------- transport: encrypted relay

function pickMime() {
  const types = ['video/webm;codecs=vp8,opus', 'video/webm', 'video/mp4'];
  return types.find((t) => window.MediaRecorder && MediaRecorder.isTypeSupported(t));
}

const FRAME_START = 1, FRAME_DATA = 2, FRAME_STOP = 3;

async function connectRelay() {
  const mime = pickMime();
  if (!mime) throw new Error('MediaRecorder non supporté');
  const ws = await openSignaling();
  let nonce;
  try {
    const hello = await ws.next();
    nonce = b64urlDecode(hello.nonce);
    if (hello.quality) S.quality = hello.quality;
    ws.send(JSON.stringify({ type: 'relay', mac: await mac(nonce, 'relay', '') }));
    if ((await ws.next()).type !== 'ready') throw new Error('refusé par le PC');
  } catch (e) { ws.close(); throw e; }

  // Every frame, control included, is encrypted: seq(8) || iv(12) || AES-GCM(type || payload).
  let seq = 0n, chain = Promise.resolve(), rec = null;
  const sendFrame = (type, payload) => {
    chain = chain.then(async () => {
      const body = payload instanceof Blob ? new Uint8Array(await payload.arrayBuffer()) : payload;
      const seqBytes = new Uint8Array(8);
      new DataView(seqBytes.buffer).setBigUint64(0, seq++);
      const iv = crypto.getRandomValues(new Uint8Array(12));
      const ct = await crypto.subtle.encrypt({ name: 'AES-GCM', iv, additionalData: concat(nonce, seqBytes) }, kEnc, concat(new Uint8Array([type]), body));
      if (ws.readyState === WebSocket.OPEN) ws.send(concat(seqBytes, iv, new Uint8Array(ct)));
      const lag = Math.round(ws.bufferedAmount / 1024);
      if (lag > 512) setConn(`Réseau lent · ${lag} Ko en attente`, 'warn');
      else setConn('Connecté au PC · relais chiffré', 'ok');
    });
    return chain;
  };
  const ping = setInterval(() => { if (ws.readyState === WebSocket.OPEN) ws.send('{"type":"ping"}'); }, 10000);
  const closed = new Promise((r) => ws.addEventListener('close', r));
  closed.then(() => clearInterval(ping));

  return {
    kind: 'relais chiffré',
    closed,
    async start() {
      sendFrame(FRAME_START, enc.encode(mime));
      const ack = await ws.next(8000);
      if (ack.type !== 'started') throw new Error('refusé par le PC');
      await applyQuality(ack.quality);
      rec = new MediaRecorder(S.stream, { mimeType: mime, videoBitsPerSecond: S.quality.bitrate });
      rec.ondataavailable = (e) => { if (e.data.size) sendFrame(FRAME_DATA, e.data); };
      rec.start(1000);
    },
    async stop() {
      if (rec && rec.state !== 'inactive') await new Promise((r) => { rec.onstop = r; rec.stop(); });
      rec = null;
      await sendFrame(FRAME_STOP, new Uint8Array());
      return ws.next(8000);
    },
    close() {
      if (rec && rec.state !== 'inactive') rec.stop();
      ws.close();
    },
  };
}

// ---------------------------------------------------------- main loop

async function startRecording(t) {
  await t.start();
  S.recStart = Date.now();
  render();
}

async function connectionLoop() {
  let preferWebRTC = true;
  for (;;) {
    let t;
    try {
      await ensureStream();
      setConn('Connexion au PC…');
      t = preferWebRTC ? await connectWebRTC() : await connectRelay();
    } catch (e) {
      console.warn(e);
      if (e.message === 'FINGERPRINT') {
        setConn('Connexion refusée : ce n\'est pas votre PC', 'warn');
        return;
      }
      setConn(preferWebRTC ? 'Pas de chemin direct, essai du relais…' : `Reconnexion… (${e.message})`, 'warn');
      preferWebRTC = !preferWebRTC;
      await sleep(1500);
      continue;
    }

    S.transport = t;
    setConn(`Connecté au PC · ${t.kind}`, 'ok');
    render();
    if (S.want) {
      // Connection came back during a video: resume in a new file.
      try { await startRecording(t); toast('Enregistrement repris (nouveau fichier)'); } catch (e) { t.close(); }
    }

    await t.closed;
    t.close();
    S.transport = null;
    S.recStart = 0;
    render();
    setConn('Connexion perdue, reconnexion…', 'warn');
    if (S.want) toast('Connexion perdue : ce qui a été filmé est déjà sur le PC');
    await sleep(1000);
  }
}

async function toggle() {
  if (S.busy) return;
  S.busy = true;
  render();
  const t = S.transport;
  try {
    if (!S.want) {
      S.want = true;
      render();
      if (t) await startRecording(t);
    } else {
      S.want = false;
      S.recStart = 0;
      render();
      if (t) {
        const r = await t.stop();
        S.count++;
        toast(r && r.bytes ? `Vidéo enregistrée sur le PC · ${fmtSize(r.bytes)}` : 'Vidéo enregistrée sur le PC');
      }
    }
  } catch (e) {
    console.warn(e);
    toast(`Erreur : ${e.message}`);
    if (t && S.want) t.close(); // reconnect; the loop resumes recording
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
    $('intro-text').textContent = `Caméra indisponible : ${e.message}`;
    $('go').disabled = false;
    return;
  }
  $('intro').hidden = true;
  render();
  connectionLoop();
}

if (!TOKEN || !PINNED_FP || !window.isSecureContext) {
  $('intro-text').textContent = "Lien d'appairage invalide : scannez le QR code affiché sur le PC.";
  $('go').hidden = true;
} else {
  $('go').addEventListener('click', activate);
  $('rec').addEventListener('click', toggle);
}
