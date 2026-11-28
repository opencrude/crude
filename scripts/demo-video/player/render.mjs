// Plays recorded Crude sessions (asciicast v2) frame by frame and encodes video.
//
//   node player/render.mjs --out video.mp4 [--from S --to S] [--fps 60] [--cues cues.json]
//   node player/render.mjs --stills 3,12.5 --dir stills
//   node player/render.mjs --cast r2 --at mark+1,40 --dir stills   (raw cast frames)
//
// The terminal state comes from @xterm/headless, a real terminal emulator.
// The page paints the cells with the same fonts Crude uses in Windows
// Terminal and adds camera moves, captions and transitions from story.js.

import { createServer } from 'node:http';
import { readFileSync, existsSync, mkdirSync, writeFileSync } from 'node:fs';
import { gunzipSync } from 'node:zlib';
import { join, dirname, extname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { spawn } from 'node:child_process';
import puppeteer from 'puppeteer-core';
import { replayTerminal } from '../terminal.mjs';

const here = dirname(fileURLToPath(import.meta.url));
const args = {};
for (let i = 2; i < process.argv.length; i++) {
  const a = process.argv[i];
  if (a.startsWith('--')) { const n = process.argv[i + 1]; args[a.slice(2)] = n && !n.startsWith('--') ? (i++, n) : '1'; }
}
const castDir = resolve(args.casts || join(here, '..', 'casts'));
const assetDir = resolve(join(here, '..', 'testdata', 'orbit-archive'));

// readCast reads NAME.cast, or the gzip copy NAME.cast.gz kept in the repository.
function readCast(name) {
  const plain = join(castDir, name + '.cast');
  if (existsSync(plain)) return readFileSync(plain, 'utf8');
  return gunzipSync(readFileSync(plain + '.gz')).toString('utf8');
}

// ---------- casts ----------
const PALETTE = [0x0d1117, 0xff7b72, 0x7ee787, 0xd29922, 0x79c0ff, 0xd2a8ff, 0xa5d6ff, 0xc9d1d9,
  0x6e7681, 0xffa198, 0x56d364, 0xe3b341, 0xa5d6ff, 0xe2c5ff, 0xb3f0ff, 0xffffff];
function paletteColor(i) {
  if (i < 16) return PALETTE[i];
  if (i < 232) { const n = i - 16, r = Math.floor(n / 36), g = Math.floor(n / 6) % 6, b = n % 6; const v = (x) => (x ? 55 + x * 40 : 0); return (v(r) << 16) | (v(g) << 8) | v(b); }
  const g = 8 + (i - 232) * 10; return (g << 16) | (g << 8) | g;
}

class Clip {
  constructor(name) {
    const lines = readCast(name).split('\n').filter(Boolean);
    this.hdr = JSON.parse(lines[0]);
    this.ev = lines.slice(1).map((l) => JSON.parse(l)).filter((e) => e[1] === 'o');
    this.marks = {};
    for (const l of lines.slice(1)) { const e = JSON.parse(l); if (e[1] === 'm') this.marks[e[2]] = e[0]; }
    this.reset();
  }
  reset() {
    this.term = replayTerminal(this.hdr);
    this.i = 0; this.t = -1; this.cache = null;
  }
  async at(t) {
    if (t < this.t) this.reset();
    let wrote = false;
    let chunk = '';
    while (this.i < this.ev.length && this.ev[this.i][0] <= t) {
      chunk += this.ev[this.i][2];
      this.i++; wrote = true;
    }
    if (chunk) await new Promise((r) => this.term.write(chunk, r));
    this.t = t;
    if (!wrote && this.cache) return this.cache;
    this.cache = this.snapshot();
    return this.cache;
  }
  snapshot() {
    const { width: cols, height: rows } = this.hdr;
    const b = this.term.buffer.active;
    const ch = new Array(cols * rows), fg = new Array(cols * rows), bg = new Array(cols * rows), fl = new Array(cols * rows);
    let cell;
    for (let y = 0; y < rows; y++) {
      const line = b.getLine(b.viewportY + y);
      for (let x = 0; x < cols; x++) {
        const k = y * cols + x;
        cell = line ? line.getCell(x, cell) : null;
        if (!cell) { ch[k] = ' '; fg[k] = -1; bg[k] = -1; fl[k] = 0; continue; }
        const w = cell.getWidth();
        ch[k] = w === 0 ? '' : (cell.getChars() || ' ');
        let f = cell.isFgRGB() ? cell.getFgColor() : cell.isFgPalette() ? paletteColor(cell.getFgColor()) : -1;
        let g = cell.isBgRGB() ? cell.getBgColor() : cell.isBgPalette() ? paletteColor(cell.getBgColor()) : -1;
        if (cell.isInverse()) { const tf = f; f = g === -1 ? 0x0d1117 : g; g = tf === -1 ? 0xe6edf3 : tf; }
        fg[k] = f; bg[k] = g;
        fl[k] = (cell.isBold() ? 1 : 0) | (cell.isItalic() ? 2 : 0) | (cell.isUnderline() ? 4 : 0) | (cell.isDim() ? 8 : 0)
          | (cell.isStrikethrough() ? 16 : 0) | (cell.isInvisible() ? 32 : 0) | (w === 2 ? 64 : 0);
      }
    }
    const core = this.term._core;
    const hidden = core && core.coreService ? core.coreService.isCursorHidden : false;
    return { cols, rows, ch, fg, bg, fl, cx: b.cursorX, cy: b.cursorY, cv: !hidden };
  }
}

const clips = {};
function clip(name) { return clips[name] || (clips[name] = new Clip(name)); }

// ---------- page ----------
const fontDirs = [process.env.LOCALAPPDATA && join(process.env.LOCALAPPDATA, 'Microsoft', 'Windows', 'Fonts'), 'C:/Windows/Fonts'].filter(Boolean);
function findFont(name) { for (const d of fontDirs) { const p = join(d, name); if (existsSync(p)) return p; } return null; }
const types = { '.html': 'text/html', '.js': 'text/javascript', '.ttf': 'font/ttf', '.json': 'application/json', '.png': 'image/png' };
const server = createServer((req, res) => {
  const url = decodeURIComponent(req.url.split('?')[0]);
  const file = url === '/crude-icon.png' ? join(here, '../../../assets/crude-icon-512.png')
    : /^\/fonts\/CrudeRTL-(Regular|Bold)\.ttf$/.test(url) ? join(here, '../../../assets', url.slice(1))
    : url.startsWith('/fonts/') ? findFont(url.slice(7)) : url.startsWith('/assets/') ? join(assetDir, url.slice(8)) : join(here, url === '/' ? 'index.html' : url.slice(1));
  if (!file || !existsSync(file)) { res.writeHead(404); res.end('missing ' + url); return; }
  res.writeHead(200, { 'content-type': types[extname(file)] || 'application/octet-stream' });
  res.end(readFileSync(file));
});
await new Promise((r) => server.listen(0, '127.0.0.1', r));
const port = server.address().port;

const chromes = [process.env.CHROME, 'C:/Program Files/Google/Chrome/Application/chrome.exe', 'C:/Program Files (x86)/Microsoft/Edge/Application/msedge.exe'];
const browser = await puppeteer.launch({ executablePath: chromes.find((p) => p && existsSync(p)), headless: true,
  args: ['--force-color-profile=srgb', '--hide-scrollbars', '--font-render-hinting=none', '--disable-lcd-text'] });
const page = await browser.newPage();
await page.setViewport({ width: 1920, height: 1080, deviceScaleFactor: 1 });
page.on('console', (m) => console.error('page:', m.text()));
page.on('pageerror', (e) => console.error('page error:', e.message));
await page.exposeFunction('castGrid', (name, t) => clip(name).at(t));
await page.exposeFunction('castMarks', (name) => clip(name).marks);
await page.goto(`http://127.0.0.1:${port}/index.html?render=1`);
await page.waitForFunction('window.videoReady === true', { timeout: 60000 });
const { duration, fps } = await page.evaluate(() => ({ duration: window.DURATION, fps: window.FPS }));
if (args.cues) { writeFileSync(resolve(args.cues), JSON.stringify(await page.evaluate(() => window.CUES), null, 1)); console.log('wrote cues', args.cues); }

async function frame(t) {
  const data = await page.evaluate(async (tt) => { await window.renderFrame(tt); return document.getElementById('out').toDataURL('image/png'); }, t);
  return Buffer.from(data.split(',')[1], 'base64');
}
async function castFrame(name, t) {
  const data = await page.evaluate(async (n, tt) => { await window.renderCast(n, tt); return document.getElementById('out').toDataURL('image/png'); }, name, t);
  return Buffer.from(data.split(',')[1], 'base64');
}

const dir = resolve(args.dir || join(here, 'stills'));
if (args.poster) {
  await page.evaluate(() => window.renderPoster());
  const data = await page.evaluate(() => document.getElementById('out').toDataURL('image/png'));
  writeFileSync(resolve(args.poster), Buffer.from(data.split(',')[1], 'base64'));
}
if (args.cast) {
  mkdirSync(dir, { recursive: true });
  const marks = clip(args.cast).marks;
  for (const s of args.at.split(',')) {
    const m = s.match(/^([A-Za-z_][\w-]*)([+-][\d.]+)?$/);
    const t = m ? marks[m[1]] + (m[2] ? parseFloat(m[2]) : 0) : parseFloat(s);
    writeFileSync(join(dir, `${args.cast}-${s.replace(/[^\w.+-]/g, '_')}.png`), await castFrame(args.cast, t));
  }
  console.log('stills in', dir);
} else if (args.stills) {
  mkdirSync(dir, { recursive: true });
  for (const s of args.stills.split(',')) {
    const t = parseFloat(s);
    writeFileSync(join(dir, `still-${t.toFixed(2).padStart(6, '0')}.png`), await frame(t));
  }
  console.log('stills in', dir);
} else {
  const rate = parseFloat(args.fps || fps);
  const from = parseFloat(args.from || '0'), to = parseFloat(args.to || String(duration));
  const out = resolve(args.out || 'video-silent.mp4');
  const ff = spawn('ffmpeg', ['-y', '-loglevel', 'error', '-f', 'image2pipe', '-framerate', String(rate), '-i', '-',
    '-c:v', 'libx264', '-preset', args.preset || 'slow', '-crf', args.crf || '14', '-tune', 'animation', '-pix_fmt', 'yuv420p',
    '-colorspace', 'bt709', '-color_primaries', 'bt709', '-color_trc', 'bt709', '-movflags', '+faststart', out], { stdio: ['pipe', 'inherit', 'inherit'] });
  const n = Math.round((to - from) * rate);
  const t0 = Date.now();
  for (let i = 0; i < n; i++) {
    const buf = await frame(from + i / rate);
    if (!ff.stdin.write(buf)) await new Promise((r) => ff.stdin.once('drain', r));
    if (i % 120 === 0) console.log(`frame ${i}/${n} ${((Date.now() - t0) / 1000).toFixed(0)}s`);
  }
  ff.stdin.end();
  const code = await new Promise((r) => ff.on('close', r));
  if (code !== 0) throw new Error(`ffmpeg exited with code ${code}`);
  console.log('wrote', out);
}
await browser.close();
server.close();
