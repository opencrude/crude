// Fixed compositions and effects for the edit. Positions are 1920 x 1080 pixels.
'use strict';
const W = 1920, H = 1080;
const PAPER = '#0d1117', MATTE = '#101419', INK = '#e6edf3', DIM = '#a8b2ba', MUTED = '#6e7681';
const AMBER = '#f0883e', GREEN = '#7ee787', BLUE = '#a5d6ff', RED = '#ff7b72';
const WIN = 'irm https://opencrude.github.io/windows.ps1 | iex';
const LINUX = 'curl -fsSL https://opencrude.github.io/linux.sh | sh';
// Nerd Font glyphs, as on the Crude website's install list.
const ICON = { windows: '\uf17a', linux: '\uf17c', apple: '\uf179', github: '\uf09b', branch: '\ue0a0', shield: '\uf132',
  debian: '\uf306', folder: '\uf07c', lock: '\uf023', eye: '\uf06e', home: '\uf015', check: '\uf00c', terminal: '\uf120' };
// The area the recorded terminal fills. Subtitles sit below it.
const PORT = { x: 48, y: 8, w: 1824, h: 1026 };

function text(ctx, str, x, y, size, color = INK, weight = 400) {
  ctx.font = `${weight} ${size}px JBM`; ctx.fillStyle = color;
  ctx.textBaseline = 'alphabetic'; ctx.fillText(str, x, y);
  return ctx.measureText(str).width;
}
function fill(ctx, color, x = 0, y = 0, w = W, h = H) {
  ctx.fillStyle = color; ctx.fillRect(x, y, w, h);
}
function rnd(seed) {
  let a = seed >>> 0;
  return () => { a = (a + 0x6d2b79f5) >>> 0; let t = Math.imul(a ^ (a >>> 15), 1 | a); t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t; return ((t ^ (t >>> 14)) >>> 0) / 4294967296; };
}

// ---------- effects ----------
const scratch = {};
function layer(name) {
  if (!scratch[name]) { const c = document.createElement('canvas'); c.width = W; c.height = H; scratch[name] = c; }
  return scratch[name];
}
// glitch splits the color channels and shifts a few horizontal bands. It is
// used for a handful of frames at a cut, never as a running texture.
function glitch(ctx, amount, seed) {
  if (amount <= 0.02) return;
  const r = rnd(seed), src = layer('src'), red = layer('red'), cyan = layer('cyan');
  src.getContext('2d').drawImage(ctx.canvas, 0, 0);
  for (const [c, tint] of [[red, '#ff0000'], [cyan, '#00ffff']]) {
    const x = c.getContext('2d');
    x.globalCompositeOperation = 'source-over'; x.drawImage(src, 0, 0);
    x.globalCompositeOperation = 'multiply'; x.fillStyle = tint; x.fillRect(0, 0, W, H);
    x.globalCompositeOperation = 'source-over';
  }
  const dx = Math.round(3 + 12 * amount);
  ctx.save();
  fill(ctx, '#000');
  ctx.globalCompositeOperation = 'lighter';
  ctx.drawImage(red, dx, 0); ctx.drawImage(cyan, -dx, 0);
  ctx.globalCompositeOperation = 'source-over';
  const bands = Math.round(2 + 7 * amount);
  for (let i = 0; i < bands; i++) {
    const h = 4 + r() * 60 * amount, y = r() * H, off = (r() - 0.5) * 180 * amount;
    ctx.drawImage(src, 0, y, W, h, off, y, W, h);
  }
  ctx.fillStyle = `rgba(165,214,255,${0.16 * amount})`;
  ctx.fillRect(0, r() * H, W, 2);
  ctx.restore();
}
// enhance pixelates the port and resolves it: the film's "zoom and enhance".
function enhance(ctx, amount) {
  if (amount <= 0.02) return;
  const block = Math.max(2, Math.round(2 + 7 * amount)), small = layer('small'), s = small.getContext('2d');
  const w = Math.ceil(PORT.w / block), h = Math.ceil(PORT.h / block);
  s.imageSmoothingEnabled = true; s.clearRect(0, 0, w, h);
  s.drawImage(ctx.canvas, PORT.x, PORT.y, PORT.w, PORT.h, 0, 0, w, h);
  ctx.save();
  ctx.imageSmoothingEnabled = false; ctx.globalAlpha = 0.85 * amount;
  ctx.drawImage(small, 0, 0, w, h, PORT.x, PORT.y, w * block, h * block);
  ctx.restore();
}
// A mouse pointer with a dark outline; ring is the click, 0..1.
function pointer(ctx, x, y, alpha, ring, scale = 1) {
  if (alpha <= 0.01) return;
  ctx.save();
  if (ring !== null && ring !== undefined && ring < 1) {
    ctx.globalAlpha = alpha * (1 - ring) * 0.9;
    ctx.strokeStyle = AMBER; ctx.lineWidth = 2.5;
    ctx.beginPath(); ctx.arc(x, y, 6 + 24 * EASE.out(ring), 0, Math.PI * 2); ctx.stroke();
  }
  ctx.globalAlpha = alpha;
  ctx.translate(x, y); ctx.scale(1.3 * scale, 1.3 * scale);
  ctx.shadowColor = 'rgba(0,0,0,0.55)'; ctx.shadowBlur = 6; ctx.shadowOffsetY = 2;
  ctx.beginPath();
  ctx.moveTo(0, 0); ctx.lineTo(0, 17); ctx.lineTo(4.2, 13.2); ctx.lineTo(7.2, 20); ctx.lineTo(10, 18.8);
  ctx.lineTo(7.1, 12.2); ctx.lineTo(12.4, 12.2); ctx.closePath();
  ctx.fillStyle = '#ffffff'; ctx.fill();
  ctx.shadowColor = 'transparent'; ctx.lineWidth = 1.2; ctx.strokeStyle = '#0d1117'; ctx.stroke();
  ctx.restore();
}
// Subtitle line with key caps, and the speed of a time-lapse on the right.
function subtitle(ctx, caption, keys = [], speed = 0) {
  const y = 1066, size = 25;
  ctx.font = `400 ${size}px JBM`;
  const parts = [];
  let width = caption ? ctx.measureText(caption).width : 0;
  ctx.font = `400 20px JBM`;
  for (const k of keys) { const w = ctx.measureText(k).width + 20; parts.push([k, w]); width += w + 8; }
  if (caption && keys.length) width += 10;
  let x = (W - width) / 2;
  if (caption) x += text(ctx, caption, x, y, size) + (keys.length ? 18 : 0);
  for (const [k, w] of parts) {
    ctx.fillStyle = '#161b22'; ctx.strokeStyle = '#3d444d'; ctx.lineWidth = 1.5;
    ctx.beginPath(); ctx.roundRect(x, y - 24, w, 32, 6); ctx.fill(); ctx.stroke();
    text(ctx, k, x + 10, y - 1, 20, INK);
    x += w + 8;
  }
  if (speed >= 1.95) {
    ctx.textAlign = 'right';
    text(ctx, `\u25b8\u25b8 ${Math.round(speed)}\u00d7`, PORT.x + PORT.w, y, 21, MUTED);
    ctx.textAlign = 'left';
  }
}

// ---------- the opening: a zsh terminal on Linux ----------
// The previous command was neofetch. HAL's eye is its logo; the rest are
// small jokes in plain sight.
const C = { fg: 0xe6edf3, dim: 0x8b949e, faint: 0x6e7681, green: 0x7ee787, blue: 0x79c0ff, cyan: 0x56d4dd,
  purple: 0xd2a8ff, red: 0xff7b72, orange: 0xf0883e, yellow: 0xe3b341 };
const NEO = [
  ['', 'dave@discovery-one'], ['', '------------------'],
  ['OS', 'Debian GNU/Linux 13 (trixie) x86_64'], ['Host', 'HAL 9000 (Urbana, IL)'],
  ['Kernel', '6.17.0-237-generic'], ['Uptime', '2001 days, 3 hours, 14 mins'],
  ['Packages', '1969 (dpkg), 42 (flatpak)'], ['Shell', 'zsh 5.9'], ['Terminal', 'kitty'],
  ['CPU', 'Heuristic ALgorithmic (12) @ 3.14GHz'], ['GPU', 'Weyland-Yutani MU/TH/UR 6000'],
  ['Memory', '2038MiB / 65536MiB'], ['Disk (/)', '237G / 2.0T (12%)'],
];
const PALETTE16 = [0x484f58, 0xff7b72, 0x3fb950, 0xd29922, 0x58a6ff, 0xbc8cff, 0x39c5cf, 0xb1bac4,
  0x6e7681, 0xffa198, 0x56d364, 0xe3b341, 0x79c0ff, 0xd2a8ff, 0x56d4dd, 0xffffff];
function keyTimes(str, start, interval) {
  let t = start;
  return Array.from(str, (ch, i) => { t += interval * (1 + (i % 7 === 0 ? 0.6 : 0) + (ch === ' ' ? 0.45 : 0)); return t; });
}
const installKeys = keyTimes(LINUX, 1.5, 0.05), launchKeys = keyTimes('crude', 7.55, 0.12);
const INSTALL_DONE = installKeys.at(-1) + 0.35, LAUNCH_DONE = launchKeys.at(-1) + 0.3;
const OPENING = LAUNCH_DONE + 0.35;
function promptLines(cmd, cursor) {
  const a = [[`\u256d\u2500 ${ICON.debian} `, C.faint], ['dave', C.green, 1], ['@discovery-one ', C.faint],
    [`${ICON.folder} ~/dev/orbit `, C.cyan, 1], [`${ICON.branch} master`, C.purple]];
  const b = [['\u2570\u2500\u276f ', C.green, 1], [cmd, C.fg]];
  if (cursor) b.push(['\u2588', C.dim]);
  return [a, b];
}
function shellLines(t) {
  const L = [[['Last login: Sun Jan 12 03:14:07 1992 from 10.0.2.37', C.faint]], []];
  L.push(...promptLines('neofetch', false));
  const neo = L.length;
  for (let i = 0; i < 17; i++) {
    const row = [['', C.fg, 0, 36]];
    if (i < NEO.length) {
      const [k, v] = NEO[i];
      if (!k) row.push([v, i === 0 ? C.red : C.dim, i === 0 ? 1 : 0]);
      else row.push([k, C.red, 1], [': ' + v, C.fg]);
    }
    if (i === 14 || i === 15) row.push(['', 0, 0, undefined, i === 14 ? 0 : 8]);
    L.push(row);
  }
  L.push([]);
  const typed = installKeys.filter((at) => at <= t).length;
  L.push(...promptLines(LINUX.slice(0, typed), t < INSTALL_DONE && (typed < LINUX.length || Math.floor(t * 2) % 2 === 0)));
  const out = [
    [0.35, [[`${ICON.linux} Installing Crude`, C.fg, 1], ['  linux/amd64', C.dim]]],
    [0.8, [[`  ${ICON.check} `, C.green], ['Downloaded       ', C.dim], ['crude_linux_amd64.tar.gz', C.fg]]],
    [1.25, [[`  ${ICON.check} `, C.green], ['SHA-256 matches  ', C.dim], ['7fffffff\u2026e1c0', C.fg]]],
    [1.6, [[`  ${ICON.check} `, C.green], ['Installed        ', C.dim], ['~/.local/bin/crude', C.fg]]],
    [2.0, [['Run ', C.dim], ['crude', C.orange, 1], [' in a project folder to start.', C.dim]]],
  ];
  for (const [at, row] of out) if (t >= INSTALL_DONE + at) L.push(row);
  if (t >= INSTALL_DONE + 2.35) {
    L.push([]);
    const n = launchKeys.filter((at) => at <= t).length;
    L.push(...promptLines('crude'.slice(0, n), t < LAUNCH_DONE + 0.2));
  }
  return { lines: L, neo };
}
const SHELL = { cols: 100, rows: 28 };
function opening(ctx, t) {
  fill(ctx, MATTE);
  const g = makeGrid(SHELL.cols, SHELL.rows), { lines, neo } = shellLines(t);
  const top = Math.max(0, lines.length - SHELL.rows);
  lines.slice(top).forEach((row, y) => {
    let x = 1;
    for (const [s, color, flags = 0, to, block] of row) {
      if (to !== undefined) x = to;
      if (block !== undefined) {
        for (let i = 0; i < 24; i++) g.bg[y * g.cols + x + i] = PALETTE16[block + Math.floor(i / 3)];
        continue;
      }
      x = put(g, x, y, s, color, flags);
    }
  });
  const s = PORT.w / (SHELL.cols * CELL_W), oy = PORT.y + (PORT.h - SHELL.rows * CELL_H * s) / 2;
  fill(ctx, PAPER, PORT.x, PORT.y, PORT.w, PORT.h);
  paintGrid(ctx, g, PORT.x, oy, s);
  // neofetch draws its logo as an image (kitty backend): HAL's eye.
  const cy = oy + (neo - top + 7.5) * CELL_H * s, cx = PORT.x + 18 * CELL_W * s;
  if (cy > PORT.y - 200) {
    ctx.save(); ctx.beginPath(); ctx.rect(PORT.x, PORT.y, PORT.w, PORT.h); ctx.clip();
    eye(ctx, cx, cy, 128, 0.78 + 0.12 * Math.sin(t * 1.7));
    ctx.restore();
  }
  // The terminal hands over to Crude with a short signal break.
  if (t > OPENING - 0.14) glitch(ctx, (t - (OPENING - 0.14)) / 0.14, Math.floor(t * 30));
}

// ---------- privacy ----------
const OWN = [
  ['Telemetry sent to Crude', '0 B', GREEN],
  ['Keys, settings and history', 'on this machine', INK],
  ['Notes hidden in your prompts', 'none', GREEN],
  ['Commit author', 'you', INK],
  ['Agent instructions', 'open source', INK],
];
function ownership(ctx, t) {
  fill(ctx, PAPER);
  text(ctx, ICON.shield + '   Your workspace', 156, 222, 28, DIM);
  const head = 'Your code, your history, your name on the commits.';
  const n = Math.floor(clamp(t / 1.1) * head.length);
  text(ctx, head.slice(0, n), 152, 330, 50, INK, 700);
  OWN.forEach(([k, v, color], i) => {
    const at = 1.3 + i * 0.42;
    if (t < at) return;
    const y = 450 + i * 64;
    const w = text(ctx, k, 156, y, 31, DIM);
    ctx.font = '400 31px JBM';
    const dot = ctx.measureText('.').width;
    text(ctx, ' ' + '.'.repeat(Math.max(2, Math.floor((800 - w) / dot))), 156 + w, y, 31, '#30363d');
    text(ctx, v, 990, y, 31, color);
  });
  if (t > 3.8) {
    text(ctx, 'Hosted models receive the requests you send them.', 156, 806, 25, MUTED);
  }
  if (t > 4.4) {
    const cmd = '$ export DO_NOT_TRACK=1', k = Math.floor(clamp((t - 4.4) / 0.9) * cmd.length);
    const w = text(ctx, cmd.slice(0, k), 156, 900, 28, INK);
    if (t > 5.45) text(ctx, '   # already true', 156 + w, 900, 28, MUTED);
  }
  if (t < 0.16) glitch(ctx, 1 - t / 0.16, 77);
}

// ---------- end card ----------
function endCard(ctx, t) {
  fill(ctx, PAPER);
  const logo = window.retroLogo;
  const s = 1.55, lw = logo.cols * CELL_W * s, lh = logo.rows * CELL_H * s;
  const total = 150 + 40 + lw, x0 = (W - total) / 2, y0 = 170;
  ctx.drawImage(window.crudeIcon, x0, y0 + (lh - 150) / 2, 150, 150);
  paintGrid(ctx, logo, x0 + 190, y0, s, { transparent: true });
  ctx.textAlign = 'center';
  text(ctx, 'Agents and an editor in your terminal.', W / 2, y0 + lh + 92, 40, INK);
  text(ctx, 'Open source  \u00b7  any model, hosted or local  \u00b7  your data stays yours', W / 2, y0 + lh + 146, 25, MUTED);
  ctx.textAlign = 'left';
  const rows = [[ICON.linux, 'Linux', LINUX], [ICON.windows, 'Windows', WIN], [ICON.apple, 'macOS', 'build from source']];
  rows.forEach(([icon, name, how], i) => {
    const at = 0.35 + i * 0.18;
    if (t < at) return;
    const y = y0 + lh + 262 + i * 64;
    text(ctx, icon, 470, y, 34, INK); text(ctx, name, 530, y, 28, DIM); text(ctx, how, 720, y, 28, INK);
  });
  if (t > 1.0) {
    ctx.textAlign = 'center';
    text(ctx, ICON.github + '  github.com/opencrude/crude', W / 2, y0 + lh + 500, 32, AMBER);
    ctx.textAlign = 'left';
  }
  if (t < 0.22) glitch(ctx, 1 - t / 0.22, 2001);
}
async function poster(ctx) {
  fill(ctx, PAPER);
  ctx.drawImage(window.crudeIcon, 86, 62, 92, 92);
  paintGrid(ctx, window.retroLogo, 204, 66, 0.62, { transparent: true });
  text(ctx, '\u25b6  Watch', 1650, 129, 35, AMBER);
  const marks = await window.castMarks('r8');
  const g = substitute(await window.castGrid('r8', marks.w_tab + 0.5), window.OPUS, 0, 2);
  const s = 0.79;
  paintGrid(ctx, g, (W - g.cols * CELL_W * s) / 2, 184, s);
}
// A camera eye: a dark steel ring, a black lens and a red light in it.
function eye(ctx, x, y, r, glow) {
  ctx.save();
  if (r > 4) {
    const ring = ctx.createRadialGradient(x - r * 0.3, y - r * 0.3, r * 0.2, x, y, r * 1.18);
    ring.addColorStop(0, '#5b6068'); ring.addColorStop(0.6, '#24272c'); ring.addColorStop(1, '#0b0c0e');
    ctx.fillStyle = ring; ctx.beginPath(); ctx.arc(x, y, r * 1.18, 0, Math.PI * 2); ctx.fill();
    ctx.fillStyle = '#050505'; ctx.beginPath(); ctx.arc(x, y, r, 0, Math.PI * 2); ctx.fill();
  }
  const halo = ctx.createRadialGradient(x, y, 0, x, y, r * 2.6 + 8);
  halo.addColorStop(0, `rgba(255,30,20,${0.35 * glow})`); halo.addColorStop(1, 'rgba(255,30,20,0)');
  ctx.fillStyle = halo; ctx.beginPath(); ctx.arc(x, y, r * 2.6 + 8, 0, Math.PI * 2); ctx.fill();
  const lens = ctx.createRadialGradient(x, y, 0, x, y, Math.max(2, r * 0.72));
  lens.addColorStop(0, '#fff3b0'); lens.addColorStop(0.08, '#ffd54a'); lens.addColorStop(0.22, '#ff3a1a');
  lens.addColorStop(0.6, `rgba(170,0,0,${0.9 * glow + 0.1})`); lens.addColorStop(1, 'rgba(40,0,0,0)');
  ctx.fillStyle = lens; ctx.beginPath(); ctx.arc(x, y, Math.max(2, r * 0.72), 0, Math.PI * 2); ctx.fill();
  if (r > 4) {
    ctx.globalAlpha = 0.35;
    ctx.fillStyle = '#ffffff';
    ctx.beginPath(); ctx.ellipse(x - r * 0.38, y - r * 0.42, r * 0.22, r * 0.12, -0.6, 0, Math.PI * 2); ctx.fill();
  }
  ctx.restore();
}
