// Grid painter for recorded terminal frames.
//
// A grid is the cell state of the terminal at one moment: characters, 24-bit
// colors and attributes for every cell, as @xterm/headless parsed them from
// Crude's real output. paintGrid draws it at any scale with the fonts Crude
// uses in Windows Terminal (JetBrainsMono Nerd Font, Crude RTL fallback).
// Box drawing and block elements are drawn as vector shapes so lines join
// exactly, as a terminal does.

'use strict';

const THEME = { fg: '#e6edf3', bg: '#0d1117', accent: '#f0883e', accent2: '#a5d6ff', muted: '#8b949e', border: '#30363d' };
const FAMILY = "'JBM', 'RTL', 'Segoe UI Emoji', 'Segoe UI Symbol'";
const CELL_W = 12, CELL_H = 24, FONT_PX = 20, BASELINE = 19;

const EASE = {
  linear: (x) => x,
  inOut: (x) => (x < 0.5 ? 4 * x * x * x : 1 - Math.pow(-2 * x + 2, 3) / 2),
  inOutSine: (x) => -(Math.cos(Math.PI * x) - 1) / 2,
  out: (x) => 1 - Math.pow(1 - x, 3),
  outExpo: (x) => (x === 1 ? 1 : 1 - Math.pow(2, -10 * x)),
  outBack: (x) => { const c1 = 1.4, c3 = c1 + 1; return 1 + c3 * Math.pow(x - 1, 3) + c1 * Math.pow(x - 1, 2); },
  in: (x) => x * x * x,
};
const clamp = (x, a = 0, b = 1) => Math.max(a, Math.min(b, x));
const prog = (t, a, b) => clamp((t - a) / (b - a));
const lerp = (a, b, x) => a + (b - a) * x;

const hexCache = new Map();
function hex(c, dflt) {
  if (c < 0) return dflt;
  let s = hexCache.get(c);
  if (!s) { s = '#' + c.toString(16).padStart(6, '0'); hexCache.set(c, s); }
  return s;
}
function mix(c, d, a) {
  const r = ((c >> 16) & 255) * (1 - a) + ((d >> 16) & 255) * a, g = ((c >> 8) & 255) * (1 - a) + ((d >> 8) & 255) * a, b = (c & 255) * (1 - a) + (d & 255) * a;
  return (Math.round(r) << 16) | (Math.round(g) << 8) | Math.round(b);
}

// Box drawing: [up, right, down, left] with 0 none, 1 light, 2 heavy, 3 double.
const BOX = {
  '\u2500': [0, 1, 0, 1], '\u2502': [1, 0, 1, 0], '\u250c': [0, 1, 1, 0], '\u2510': [0, 0, 1, 1],
  '\u2514': [1, 1, 0, 0], '\u2518': [1, 0, 0, 1], '\u251c': [1, 1, 1, 0], '\u2524': [1, 0, 1, 1],
  '\u252c': [0, 1, 1, 1], '\u2534': [1, 1, 0, 1], '\u253c': [1, 1, 1, 1], '\u2501': [0, 2, 0, 2],
  '\u2503': [2, 0, 2, 0], '\u250f': [0, 2, 2, 0], '\u2513': [0, 0, 2, 2], '\u2517': [2, 2, 0, 0], '\u251b': [2, 0, 0, 2],
  '\u2523': [2, 2, 2, 0], '\u252b': [2, 0, 2, 2], '\u2533': [0, 2, 2, 2], '\u253b': [2, 2, 0, 2], '\u254b': [2, 2, 2, 2],
  '\u2550': [0, 3, 0, 3], '\u2551': [3, 0, 3, 0], '\u2554': [0, 3, 3, 0], '\u2557': [0, 0, 3, 3], '\u255a': [3, 3, 0, 0],
  '\u255d': [3, 0, 0, 3], '\u2560': [3, 3, 3, 0], '\u2563': [3, 0, 3, 3], '\u2566': [0, 3, 3, 3], '\u2569': [3, 3, 0, 3],
  '\u256c': [3, 3, 3, 3], '\u2574': [0, 0, 0, 1], '\u2575': [1, 0, 0, 0], '\u2576': [0, 1, 0, 0], '\u2577': [0, 0, 1, 0],
  '\u2578': [0, 0, 0, 2], '\u2579': [2, 0, 0, 0], '\u257a': [0, 2, 0, 0], '\u257b': [0, 0, 2, 0],
  '\u251d': [1, 2, 1, 0], '\u2525': [1, 0, 1, 2], '\u2520': [2, 1, 2, 0], '\u2528': [2, 0, 2, 1],
};
const ROUND = { '\u256d': [0, 1, 1, 0], '\u256e': [0, 0, 1, 1], '\u2570': [1, 1, 0, 0], '\u256f': [1, 0, 0, 1] };

function drawBox(ctx, ch, x, y, cw, chh, color, s) {
  const cx = Math.round(x + cw / 2), cy = Math.round(y + chh / 2);
  const light = Math.max(1, Math.round(1.1 * s)), heavy = Math.max(2, Math.round(2.4 * s)), gap = Math.max(2, Math.round(1.8 * s));
  ctx.fillStyle = color; ctx.strokeStyle = color;
  const r = ROUND[ch];
  if (r) {
    const [u, , d, l] = r;
    const rad = Math.min(cw, chh) * 0.5;
    ctx.lineWidth = light; ctx.beginPath();
    const vy = u ? y : y + chh, hx = l ? x : x + cw;
    ctx.moveTo(cx, vy); ctx.lineTo(cx, cy + (u ? -rad : rad));
    ctx.quadraticCurveTo(cx, cy, cx + (l ? -rad : rad), cy); ctx.lineTo(hx, cy);
    ctx.stroke();
    return true;
  }
  const seg = BOX[ch];
  if (!seg) return false;
  const [u, rr, d, l] = seg;
  const half = (k) => (k === 2 ? heavy : k === 3 ? gap + light : light) / 2;
  const vext = Math.max(half(l), half(rr)), hext = Math.max(half(u), half(d));
  const vbar = (y0, y1, k) => {
    if (!k) return;
    if (k === 3) { ctx.fillRect(cx - gap - light / 2, y0, light, y1 - y0); ctx.fillRect(cx + gap - light / 2, y0, light, y1 - y0); return; }
    const w = k === 2 ? heavy : light; ctx.fillRect(cx - w / 2, y0, w, y1 - y0);
  };
  const hbar = (x0, x1, k) => {
    if (!k) return;
    if (k === 3) { ctx.fillRect(x0, cy - gap - light / 2, x1 - x0, light); ctx.fillRect(x0, cy + gap - light / 2, x1 - x0, light); return; }
    const w = k === 2 ? heavy : light; ctx.fillRect(x0, cy - w / 2, x1 - x0, w);
  };
  vbar(y, cy + (u && (l || rr) ? vext : 0), u);
  vbar(cy - (d && (l || rr) ? vext : 0), y + chh, d);
  hbar(x, cx + (l && (u || d) ? hext : 0), l);
  hbar(cx - (rr && (u || d) ? hext : 0), x + cw, rr);
  if (!u && !d && l && rr) hbar(x, x + cw, Math.max(l, rr));
  return true;
}

function drawBlock(ctx, ch, x, y, cw, chh, color) {
  // Edges snap to whole pixels, so neighbouring blocks meet without seams.
  const f = (a, b, w, h) => {
    const x0 = Math.round(x + a * cw), y0 = Math.round(y + b * chh), x1 = Math.round(x + (a + w) * cw), y1 = Math.round(y + (b + h) * chh);
    ctx.fillStyle = color; ctx.fillRect(x0, y0, Math.max(1, x1 - x0), Math.max(1, y1 - y0));
  };
  switch (ch) {
    case '\u2588': f(0, 0, 1, 1); return true;
    case '\u2580': f(0, 0, 1, 0.5); return true;
    case '\u2584': f(0, 0.5, 1, 0.5); return true;
    case '\u258c': f(0, 0, 0.5, 1); return true;
    case '\u2590': f(0.5, 0, 0.5, 1); return true;
    case '\u2589': f(0, 0, 7 / 8, 1); return true;
    case '\u258a': f(0, 0, 6 / 8, 1); return true;
    case '\u258b': f(0, 0, 5 / 8, 1); return true;
    case '\u258d': f(0, 0, 3 / 8, 1); return true;
    case '\u258e': f(0, 0, 2 / 8, 1); return true;
    case '\u258f': f(0, 0, 1 / 8, 1); return true;
    case '\u2581': f(0, 7 / 8, 1, 1 / 8); return true;
    case '\u2582': f(0, 6 / 8, 1, 2 / 8); return true;
    case '\u2583': f(0, 5 / 8, 1, 3 / 8); return true;
    case '\u2585': f(0, 3 / 8, 1, 5 / 8); return true;
    case '\u2586': f(0, 2 / 8, 1, 6 / 8); return true;
    case '\u2587': f(0, 1 / 8, 1, 7 / 8); return true;
    case '\u2594': f(0, 0, 1, 1 / 8); return true;
    case '\u2595': f(7 / 8, 0, 1 / 8, 1); return true;
    case '\u2591': ctx.globalAlpha = 0.25; f(0, 0, 1, 1); ctx.globalAlpha = 1; return true;
    case '\u2592': ctx.globalAlpha = 0.5; f(0, 0, 1, 1); ctx.globalAlpha = 1; return true;
    case '\u2593': ctx.globalAlpha = 0.75; f(0, 0, 1, 1); ctx.globalAlpha = 1; return true;
    default: return false;
  }
}

function fontFor(fl, px) { return `${fl & 2 ? 'italic ' : ''}${fl & 1 ? 'bold ' : ''}${px}px ${FAMILY}`; }

// paintGrid draws grid g with its top-left at (ox, oy) and scale s.
// opts.cursor draws the terminal cursor; opts.transparent skips cells with
// the terminal's default background.
function paintGrid(ctx, g, ox, oy, s, opts = {}) {
  const cw = CELL_W * s, chh = CELL_H * s, px = FONT_PX * s, base = BASELINE * s;
  const { cols, rows, ch, fg, bg, fl } = g;
  const defBg = 0x0d1117, defFg = 0xe6edf3;
  const vx0 = Math.max(0, Math.floor(-ox / cw)), vx1 = Math.min(cols, Math.ceil((ctx.canvas.width - ox) / cw));
  const vy0 = Math.max(0, Math.floor(-oy / chh)), vy1 = Math.min(rows, Math.ceil((ctx.canvas.height - oy) / chh));
  for (let y = vy0; y < vy1; y++) {
    let x = vx0;
    while (x < vx1) {
      const k = y * cols + x, b = bg[k] < 0 ? defBg : bg[k];
      let e = x + 1;
      while (e < vx1 && (bg[y * cols + e] < 0 ? defBg : bg[y * cols + e]) === b) e++;
      // opts.transparent leaves the terminal's own background unpainted.
      if (!(opts.transparent && b === defBg)) {
        ctx.fillStyle = hex(b);
        ctx.fillRect(Math.floor(ox + x * cw), Math.floor(oy + y * chh), Math.ceil((e - x) * cw) + 1, Math.ceil(chh) + 1);
      }
      x = e;
    }
  }
  ctx.textBaseline = 'alphabetic';
  for (let y = vy0; y < vy1; y++) {
    const py = oy + y * chh;
    let x = vx0;
    while (x < vx1) {
      const k = y * cols + x, c = ch[k], f = fl[k];
      if (!c || c === ' ' || (f & 32)) { if (f & 4) underline(ctx, g, k, ox + x * cw, py, cw, chh, s); x++; continue; }
      let col = fg[k] < 0 ? defFg : fg[k];
      if (f & 8) col = mix(col, bg[k] < 0 ? defBg : bg[k], 0.45);
      const color = hex(col), pxx = ox + x * cw, code = c.codePointAt(0);
      if (code >= 0x2500 && code <= 0x259f && c.length === 1) {
        if (drawBox(ctx, c, pxx, py, cw, chh, color, s) || drawBlock(ctx, c, pxx, py, cw, chh, color)) { x++; continue; }
      }
      if (code < 0x7f) {
        let e = x + 1, str = c;
        while (e < vx1) {
          const j = y * cols + e, d = ch[j];
          if (!d || d.codePointAt(0) >= 0x7f || fg[j] !== fg[k] || fl[j] !== f) break;
          str += d; e++;
        }
        ctx.font = fontFor(f, px); ctx.fillStyle = color;
        ctx.fillText(str, pxx, py + base);
        if (f & 4) for (let i = x; i < e; i++) underline(ctx, g, y * cols + i, ox + i * cw, py, cw, chh, s);
        if (f & 16) { ctx.fillRect(pxx, py + chh * 0.52, (e - x) * cw, Math.max(1, s * 1.2)); }
        x = e; continue;
      }
      ctx.font = fontFor(f, px); ctx.fillStyle = color;
      const span = (f & 64) ? 2 : 1;
      const w = ctx.measureText(c).width;
      const dx = (span * cw - w) / 2;
      ctx.fillText(c, pxx + (w > cw * 0.9 || span === 2 ? dx : 0), py + base);
      if (f & 4) underline(ctx, g, k, pxx, py, cw * span, chh, s);
      x += span;
    }
  }
  if (opts.cursor && g.cv && g.cy < rows && g.cx < cols) {
    const k = g.cy * cols + g.cx;
    ctx.fillStyle = hex(fg[k] < 0 ? defFg : fg[k]);
    ctx.globalAlpha = 0.85;
    ctx.fillRect(ox + g.cx * cw, oy + g.cy * chh, cw, chh);
    ctx.globalAlpha = 1;
    const c = ch[k];
    if (c && c !== ' ') { ctx.font = fontFor(fl[k], px); ctx.fillStyle = hex(bg[k] < 0 ? defBg : bg[k]); ctx.fillText(c, ox + g.cx * cw, oy + g.cy * chh + base); }
  }
}

function underline(ctx, g, k, x, y, w, h, s) {
  const col = g.ul && g.ul[k] >= 0 ? g.ul[k] : (g.fg[k] < 0 ? 0xe6edf3 : g.fg[k]);
  ctx.fillStyle = hex(col);
  const style = g.us ? g.us[k] : 1;
  if (style === 3) { // curly
    ctx.strokeStyle = hex(col); ctx.lineWidth = Math.max(1, s * 1.2); ctx.beginPath();
    const yy = y + h - 3 * s, amp = 1.6 * s;
    for (let i = 0; i <= 8; i++) { const xx = x + (w * i) / 8; const v = yy + (i % 2 ? amp : -amp); if (i) ctx.lineTo(xx, v); else ctx.moveTo(xx, v); }
    ctx.stroke(); return;
  }
  ctx.fillRect(x, y + h - 2.5 * s, w, Math.max(1, s * 1.1));
}

// ---------- text substitution ----------
// The film may relabel a few words on screen (for example the model name).
// Each cell maps to one character of rowText, so indexes are columns.
function rowText(g, y) {
  let s = '';
  for (let x = 0; x < g.cols; x++) {
    const c = g.ch[y * g.cols + x];
    s += c === '' ? '\0' : c.length === 1 ? c : '\u0001';
  }
  return s;
}
function cloneGrid(g) {
  return { ...g, ch: g.ch.slice(), fg: g.fg.slice(), bg: g.bg.slice(), fl: g.fl.slice() };
}
// replaceAt writes text over the n cells at (x, y) and keeps the layout
// around it: right-aligned text keeps its right edge, text followed by a gap
// is padded, and text followed by more text pushes the words on its left
// into the nearest gap.
function replaceAt(g, y, x, n, text) {
  const cols = g.cols, row = y * cols, end = x + n, d = n - text.length;
  const k0 = row + x, style = [g.fg[k0], g.bg[k0], g.fl[k0] & ~64];
  const blank = (i) => (!g.ch[row + i] || g.ch[row + i] === ' ');
  const clear = (i) => { g.ch[row + i] = ' '; g.fl[row + i] &= ~(64 | 4); };
  const put = (at) => {
    for (let i = 0; i < text.length && at + i < cols; i++) {
      const k = row + at + i;
      g.ch[k] = text[i]; [g.fg[k], g.bg[k], g.fl[k]] = style;
    }
  };
  let tail = true;
  for (let i = end; i < cols; i++) if (!blank(i)) { tail = false; break; }
  if (d < 0) { put(x); return; }
  if (tail && x > cols / 2) { for (let i = x; i < x + d; i++) clear(i); put(x + d); return; }
  if (end >= cols || (blank(end) && (end + 1 >= cols || blank(end + 1)))) {
    put(x); for (let i = x + text.length; i < end; i++) clear(i); return;
  }
  let gap = -1;
  for (let i = x - 1; i > 0; i--) if (blank(i) && blank(i - 1)) { gap = i; break; }
  if (gap < 0 || gap + d >= x) { put(x); for (let i = x + text.length; i < end; i++) clear(i); return; }
  for (let i = x - 1; i > gap; i--) {
    const a = row + i, b = row + i + d;
    g.ch[b] = g.ch[a]; g.fg[b] = g.fg[a]; g.bg[b] = g.bg[a]; g.fl[b] = g.fl[a];
  }
  for (let i = gap + 1; i <= gap + d; i++) clear(i);
  put(x + d);
}
// substitute applies [from, to] rules to rows y0..y1 and returns a new grid
// when anything changed. The cached grid of a cast is never modified.
function substitute(g, rules, y0 = 0, y1 = g.rows - 1) {
  let out = null;
  for (let y = y0; y <= Math.min(y1, g.rows - 1); y++) {
    for (const [from, to] of rules) {
      let line = rowText(out || g, y), at = line.indexOf(from);
      while (at >= 0) {
        out = out || cloneGrid(g);
        replaceAt(out, y, at, from.length, to);
        line = rowText(out, y);
        at = line.indexOf(from, at + Math.max(1, to.length));
      }
    }
  }
  return out || g;
}

// ---------- synthetic terminals ----------
// makeGrid and put build a terminal screen for the staged shell scenes.
function makeGrid(cols, rows) {
  const n = cols * rows;
  return { cols, rows, ch: new Array(n).fill(' '), fg: new Array(n).fill(-1), bg: new Array(n).fill(-1), fl: new Array(n).fill(0), cx: 0, cy: 0, cv: false };
}
function put(g, x, y, str, fg = -1, fl = 0) {
  if (y < 0 || y >= g.rows) return x;
  for (const c of str) {
    if (x >= g.cols) break;
    const k = y * g.cols + x;
    g.ch[k] = c; g.fg[k] = fg; g.fl[k] = fl;
    x++;
  }
  return x;
}

if (typeof window !== 'undefined') Object.assign(window, { THEME, FAMILY, CELL_W, CELL_H, EASE, clamp, prog, lerp, hex, paintGrid, substitute, makeGrid, put });
