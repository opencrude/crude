// Synthesizes the soundtrack for the demo video from a cue sheet.
//
//   node music.mjs cues.json out.wav
//
// cues.json: { bpm, duration, sections: [{start, end, kind}],
//   ticks: [t], spectro, stinger }
// Section kinds: intro, build, work, break, outro. Everything is generated
// sample by sample, so the result is deterministic.

import { readFileSync, writeFileSync } from 'node:fs';

const [cuePath, outPath] = process.argv.slice(2);
const cue = JSON.parse(readFileSync(cuePath, 'utf8'));
const SR = 48000;
const N = Math.round(cue.duration * SR);
const L = new Float32Array(N), R = new Float32Array(N);
const busDrum = new Float32Array(N), busBass = new Float32Array(N), busSynth = [new Float32Array(N), new Float32Array(N)];
const busFx = [new Float32Array(N), new Float32Array(N)];
const busDry = new Float32Array(N); // no reverb, no ducking
const duck = new Float32Array(N).fill(1);

const beat = 60 / cue.bpm, bar = beat * 4, s16 = beat / 4;
let seed = 12345;
const rand = () => ((seed = (seed * 1664525 + 1013904223) >>> 0) / 4294967296) * 2 - 1;
const midi = (n) => 440 * Math.pow(2, (n - 69) / 12);
const sectionAt = (t) => cue.sections.find((s) => t >= s.start && t < s.end);

// ---------- instruments ----------
function kick(t0, gain = 1) {
  const i0 = Math.round(t0 * SR), len = Math.round(0.45 * SR);
  let ph = 0;
  for (let i = 0; i < len && i0 + i < N; i++) {
    const t = i / SR;
    const f = 42 + 110 * Math.exp(-t * 28);
    ph += (2 * Math.PI * f) / SR;
    const env = Math.exp(-t * 7.5);
    const click = i < 90 ? (rand() * 0.5) * (1 - i / 90) : 0;
    busDrum[i0 + i] += gain * (Math.tanh(Math.sin(ph) * 2.2) * env * 0.9 + click * 0.4);
  }
  // sidechain: duck pads and bass after each kick
  const dl = Math.round(0.32 * SR);
  for (let i = 0; i < dl && i0 + i < N; i++) duck[i0 + i] = Math.min(duck[i0 + i], 0.25 + 0.75 * Math.pow(i / dl, 0.7));
}

function clap(t0, gain = 1) {
  const i0 = Math.round(t0 * SR), len = Math.round(0.3 * SR);
  let bp1 = 0, bp2 = 0;
  for (let i = 0; i < len && i0 + i < N; i++) {
    const t = i / SR;
    // three quick bursts then a tail
    const bursts = [0, 0.011, 0.022].reduce((a, d) => a + (t >= d ? Math.exp(-(t - d) * 180) : 0), 0);
    const env = bursts * 0.6 + Math.exp(-t * 14) * 0.6;
    const n = rand();
    bp1 += 0.35 * (n - bp1); bp2 += 0.35 * (bp1 - bp2);
    const band = n - bp2;
    busDrum[i0 + i] += gain * band * env * 0.55;
    const tone = Math.sin(2 * Math.PI * 190 * t) * Math.exp(-t * 40) * 0.25;
    busDrum[i0 + i] += gain * tone;
  }
}

function hat(t0, gain = 1, open = false) {
  const i0 = Math.round(t0 * SR), len = Math.round((open ? 0.22 : 0.05) * SR);
  let lp = 0;
  for (let i = 0; i < len && i0 + i < N; i++) {
    const t = i / SR;
    const n = rand();
    lp += 0.6 * (n - lp);
    const hp = n - lp;
    busDrum[i0 + i] += gain * hp * Math.exp(-t * (open ? 18 : 90)) * 0.28;
  }
}

// one-pole lowpassed detuned saw voice
function sawVoice(buf, t0, dur, freq, gain, cutoff, opts = {}) {
  const i0 = Math.round(t0 * SR), len = Math.round((dur + (opts.release || 0.05)) * SR);
  const det = opts.detune || [0];
  const ph = det.map(() => (rand() + 1) / 2);
  let lp1 = 0, lp2 = 0;
  for (let i = 0; i < len && i0 + i < N; i++) {
    const t = i / SR;
    let v = 0;
    for (let k = 0; k < det.length; k++) {
      ph[k] += (freq * Math.pow(2, det[k] / 1200)) / SR;
      ph[k] -= Math.floor(ph[k]);
      v += 2 * ph[k] - 1;
    }
    v /= det.length;
    const a = opts.attack || 0.005, rel = opts.release || 0.05;
    let env = t < a ? t / a : t < dur ? 1 : Math.max(0, 1 - (t - dur) / rel);
    if (opts.decay) env *= Math.exp(-t * opts.decay);
    const c = typeof cutoff === 'function' ? cutoff(t0 + t, t) : cutoff;
    const k1 = 1 - Math.exp((-2 * Math.PI * c) / SR);
    lp1 += k1 * (v - lp1); lp2 += k1 * (lp1 - lp2);
    const out = lp2 * env * gain;
    if (Array.isArray(buf)) {
      const pan = opts.pan || 0;
      buf[0][i0 + i] += out * (1 - pan) ; buf[1][i0 + i] += out * (1 + pan);
    } else buf[i0 + i] += out;
  }
}

function sine(buf, t0, dur, f0, f1, gain, decay) {
  const i0 = Math.round(t0 * SR), len = Math.round(dur * SR);
  let ph = 0;
  for (let i = 0; i < len && i0 + i < N; i++) {
    const t = i / SR, f = f0 * Math.pow(f1 / f0, t / dur);
    ph += (2 * Math.PI * f) / SR;
    const v = Math.sin(ph) * Math.exp(-t * decay) * gain * Math.min(1, i / 64);
    buf[0][i0 + i] += v; buf[1][i0 + i] += v;
  }
}

function tick(t0, gain = 1) {
  const i0 = Math.round(t0 * SR), len = Math.round(0.018 * SR);
  let lp = 0;
  const pitch = 2400 + rand() * 900;
  for (let i = 0; i < len && i0 + i < N; i++) {
    const t = i / SR, n = rand();
    lp += 0.5 * (n - lp);
    const v = ((n - lp) * 0.6 + Math.sin(2 * Math.PI * pitch * t) * 0.4) * Math.exp(-t * 300) * gain * 0.22;
    busFx[0][i0 + i] += v; busFx[1][i0 + i] += v;
  }
}

// ---------- arrangement ----------
// i - VI - III - VII in A minor: Am F C G
const prog = [
  { root: 45, chord: [57, 60, 64, 67] }, // Am(7)
  { root: 41, chord: [53, 57, 60, 64] }, // Fmaj7
  { root: 48, chord: [55, 60, 64, 67] }, // C
  { root: 43, chord: [55, 59, 62, 67] }, // G
];
// No downbeat over the install: the only rhythm is the user's keys. Bring
// in a soft bass pulse at the task, then a half-time beat for the agent.
for (let b = 0; b * bar < cue.duration; b++) {
  const tb = b * bar, sec = sectionAt(tb + 0.001);
  if (!sec) continue;
  const p = prog[b % 4], work = sec.kind === 'work';
  for (const n of p.chord) sawVoice(busSynth, tb, bar * 0.91, midi(n), 0.068, 760,
    { detune: [-5, 5], attack: 0.38, release: 0.48, pan: ((n % 3) - 1) * 0.24 });
  if (sec.kind === 'build' || work || sec.kind === 'break') {
    for (const q of work ? [0, 6, 8, 14] : [0, 8]) {
      const t = tb + q * s16;
      sawVoice(busBass, t, 0.32, midi(p.root - 12), 0.32, 440,
        { attack: 0.02, release: 0.18, decay: 3.5 });
    }
  }
  if (work) { kick(tb, 0.40); clap(tb + 2 * beat, 0.22); hat(tb + 1.5 * beat, 0.35); hat(tb + 3.5 * beat, 0.28); }
  if (sec.kind !== 'outro') {
    const notes = work ? [2, 5, 10, 14] : [2, 10];
    notes.forEach((q, i) => sawVoice(busSynth, tb + q * s16, 0.13,
      midi(p.chord[(b + i) % 4] + 12), 0.10, 1150, { attack: 0.014, decay: 6, release: 0.19, pan: i % 2 ? 0.2 : -0.2 }));
  }
}
for (const t of cue.ticks) tick(t, 0.5);

// Text drawn into the spectrogram: each lit pixel of a 5x7 font is a sine
// tone in a high band, too quiet to notice under the mix.
const FONT = {
  A: ['01110', '10001', '10001', '11111', '10001', '10001', '10001'], B: ['11110', '10001', '10001', '11110', '10001', '10001', '11110'],
  C: ['01111', '10000', '10000', '10000', '10000', '10000', '01111'], G: ['01111', '10000', '10000', '10011', '10001', '10001', '01111'],
  I: ['11111', '00100', '00100', '00100', '00100', '00100', '11111'], N: ['10001', '11001', '10101', '10011', '10001', '10001', '10001'],
  O: ['01110', '10001', '10001', '10001', '10001', '10001', '01110'], R: ['11110', '10001', '10001', '11110', '10100', '10010', '10001'],
  T: ['11111', '00100', '00100', '00100', '00100', '00100', '00100'], U: ['10001', '10001', '10001', '10001', '10001', '10001', '01110'],
  ' ': ['00000', '00000', '00000', '00000', '00000', '00000', '00000'],
};
function spectro({ text, t0, t1, f0, f1, gain }) {
  const cols = [];
  for (const ch of text) { const g = FONT[ch] || FONT[' ']; for (let x = 0; x < 5; x++) cols.push(g.map((r) => r[x] === '1')); cols.push(Array(7).fill(false)); }
  const rows = 7, colDur = (t1 - t0) / cols.length;
  const freqs = Array.from({ length: rows }, (_, r) => f1 - (r * (f1 - f0)) / (rows - 1));
  const i0 = Math.round(t0 * SR), i1 = Math.min(N, Math.round(t1 * SR));
  const ph = new Array(rows * 3).fill(0);
  for (let i = i0; i < i1; i++) {
    const tt = i / SR - t0, c = Math.min(cols.length - 1, Math.floor(tt / colDur));
    const x = (tt - c * colDur) / colDur, w = Math.sin(Math.PI * x) ** 0.5;
    let v = 0;
    for (let r = 0; r < rows; r++) {
      for (let k = 0; k < 3; k++) {
        ph[r * 3 + k] += (2 * Math.PI * (freqs[r] + (k - 1) * 70)) / SR;
        if (cols[c][r]) v += Math.sin(ph[r * 3 + k]) / 1.6;
      }
    }
    busDry[i] += v * gain * w;
  }
}
if (cue.spectro) spectro(cue.spectro);

// After the credits: a low drone, two quiet computer beeps and one thud.
if (cue.stinger) {
  const { t0, t1, beep } = cue.stinger;
  const i0 = Math.round((t0 + 0.2) * SR), i1 = Math.min(N, Math.round(t1 * SR));
  for (let i = i0; i < i1; i++) {
    const t = i / SR - t0 - 0.2, len = t1 - t0 - 0.2;
    const env = Math.min(1, t / 1.2) * Math.min(1, (len - t) / 0.6);
    const v = (Math.sin(2 * Math.PI * 55 * t) * 0.22 + Math.sin(2 * Math.PI * 82.4 * t + 0.3) * 0.1 + Math.sin(2 * Math.PI * 110.5 * t) * 0.05) * env;
    busDry[i] += v;
  }
  for (const b of [beep, beep + 0.22]) sine(busFx, b, 0.16, 1046.5, 1046.5, 0.12, 18);
  sine(busFx, t1 - 0.9, 0.8, 90, 40, 0.2, 5);
}

// ---------- mix ----------
// simple stereo reverb (Schroeder) on synth + fx
function reverb(inL, inR, wet) {
  const combs = [1557, 1617, 1491, 1422, 1277, 1356, 1188, 1116].map((d) => Math.round((d * SR) / 44100));
  const aps = [225, 556, 441, 341].map((d) => Math.round((d * SR) / 44100));
  const outL = new Float32Array(N), outR = new Float32Array(N);
  for (const [src, dst, spread] of [[inL, outL, 0], [inR, outR, 23]]) {
    const bufs = combs.map((d) => ({ b: new Float32Array(d + spread), i: 0, lp: 0 }));
    for (let i = 0; i < N; i++) {
      let acc = 0;
      for (const c of bufs) {
        const y = c.b[c.i];
        c.lp = y * 0.7 + c.lp * 0.3;
        c.b[c.i] = src[i] * 0.015 + c.lp * 0.86;
        c.i = (c.i + 1) % c.b.length;
        acc += y;
      }
      dst[i] = acc;
    }
    let x = dst;
    for (const d of aps) {
      const b = new Float32Array(d + spread); let j = 0; const y = new Float32Array(N);
      for (let i = 0; i < N; i++) {
        const bo = b[j];
        const out = -0.5 * x[i] + bo;
        b[j] = x[i] + 0.5 * out;
        y[i] = out;
        j = (j + 1) % b.length;
      }
      x = y;
    }
    dst.set(x);
  }
  return [outL.map((v) => v * wet), outR.map((v) => v * wet)];
}

// stereo delay on the arp bus (dotted eighth)
const dly = Math.round(beat * 0.75 * SR);
for (let i = dly; i < N; i++) {
  busSynth[0][i] += busSynth[1][i - dly] * 0.28;
  busSynth[1][i] += busSynth[0][i - dly] * 0.28;
}
const [rvL, rvR] = reverb(busSynth[0].map((v, i) => v + busFx[0][i] * 0.6), busSynth[1].map((v, i) => v + busFx[1][i] * 0.6), 1.0);

for (let i = 0; i < N; i++) {
  const d = duck[i];
  const bass = Math.tanh(busBass[i] * 1.4) * d;
  const drum = busDrum[i];
  L[i] = drum * 0.95 + bass * 0.8 + (busSynth[0][i] * d + rvL[i] * 0.9) + busFx[0][i] + busDry[i];
  R[i] = drum * 0.95 + bass * 0.8 + (busSynth[1][i] * d + rvR[i] * 0.9) + busFx[1][i] + busDry[i];
}
// fade in/out
const fin = Math.round(0.02 * SR), fout = Math.round(1.2 * SR), end = Math.round(cue.duration * SR);
for (let i = 0; i < N; i++) {
  let g = i < fin ? i / fin : 1;
  if (i > end - fout) g *= Math.max(0, (end - i) / fout);
  L[i] *= g; R[i] *= g;
}
let peak = 0;
for (let i = 0; i < N; i++) peak = Math.max(peak, Math.abs(L[i]), Math.abs(R[i]));
const norm = 0.89 / peak;

// ---------- wav ----------
const out = Buffer.alloc(44 + N * 4);
out.write('RIFF', 0); out.writeUInt32LE(36 + N * 4, 4); out.write('WAVE', 8); out.write('fmt ', 12);
out.writeUInt32LE(16, 16); out.writeUInt16LE(1, 20); out.writeUInt16LE(2, 22); out.writeUInt32LE(SR, 24);
out.writeUInt32LE(SR * 4, 28); out.writeUInt16LE(4, 32); out.writeUInt16LE(16, 34); out.write('data', 36); out.writeUInt32LE(N * 4, 40);
for (let i = 0; i < N; i++) {
  out.writeInt16LE(Math.round(Math.max(-1, Math.min(1, L[i] * norm)) * 32767), 44 + i * 4);
  out.writeInt16LE(Math.round(Math.max(-1, Math.min(1, R[i] * norm)) * 32767), 46 + i * 4);
}
writeFileSync(outPath, out);
console.log('wrote', outPath, (N / SR).toFixed(1) + 's', 'peak', peak.toFixed(2));
