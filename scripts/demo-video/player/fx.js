// Fixed compositions for the edit. All positions are in 1920 x 1080 pixels.
'use strict';
const W = 1920, H = 1080;
const PAPER = '#0d1117', MATTE = '#101419', INK = '#e6edf3', DIM = '#a8b2ba';
const AMBER = '#f0883e', GREEN = '#7ee787', BLUE = '#a5d6ff';
const WIN = 'irm https://opencrude.github.io/windows.ps1 | iex';
const LINUX = 'curl -fsSL https://opencrude.github.io/linux.sh | sh';
// Same brand glyphs as the install list on the Crude website.
const ICON = { windows: '\uf17a', linux: '\uf17c', apple: '\uf179', github: '\uf09b', branch: '\ue0a0', shield: '\uf132' };
function text(ctx, str, x, y, size, color = INK, weight = 400) {
  ctx.font = `${weight} ${size}px JBM`; ctx.fillStyle = color;
  ctx.textBaseline = 'alphabetic'; ctx.fillText(str, x, y);
  return ctx.measureText(str).width;
}
function fill(ctx, color, x = 0, y = 0, w = W, h = H) {
  ctx.fillStyle = color; ctx.fillRect(x, y, w, h);
}
// On quiet keys there is a little more time to see the full command.
function keyTimes(str, start, interval) {
  let t = start;
  return Array.from(str, (ch, i) => { t += interval * (1 + (i % 7 === 0 ? 0.65 : 0) + (ch === ' ' ? 0.5 : 0)); return t; });
}
const installKeys = keyTimes(WIN, 0.8, 0.065), launchKeys = keyTimes('crude', 7.35, 0.135);
function promptLine(ctx, y, command, times, t, active) {
  let x = 152;
  x += text(ctx, 'orbit ', x, y, 33, BLUE);
  x += text(ctx, ICON.branch + ' master ', x, y, 33, '#d2a8ff');
  x += text(ctx, '❯ ', x, y, 33, AMBER);
  const n = times.filter(at => at <= t).length;
  x += text(ctx, command.slice(0, n), x, y, 33);
  if (active && (n < command.length || Math.floor(t * 2) % 2 === 0)) fill(ctx, DIM, x + 2, y - 27, 13, 31);
}
function opening(ctx, t) {
  fill(ctx, PAPER);
  text(ctx, ICON.windows + '  PowerShell', 152, 216, 25, DIM);
  promptLine(ctx, 343, WIN, installKeys, t, t < 4.85);
  if (t >= 4.85) text(ctx, 'Version v0.9.0 · windows/amd64', 152, 415, 29, DIM);
  if (t >= 5.4) text(ctx, '✓  Download    SHA-256 matches', 152, 464, 29, GREEN);
  if (t >= 6.08) text(ctx, 'Crude v0.9.0 is ready.', 152, 526, 29);
  if (t >= 6.95) promptLine(ctx, 652, 'crude', launchKeys, t, true);
}
function ownership(ctx, t) {
  fill(ctx, PAPER);
  text(ctx, ICON.shield + '   Local workspace', 156, 235, 28, DIM);
  text(ctx, 'Your commits carry your name.', 152, 354, 59, INK, 700);
  text(ctx, 'Usage telemetry to Crude', 156, 486, 32, DIM); text(ctx, '0 B', 924, 486, 32, GREEN);
  text(ctx, 'Account settings and history', 156, 552, 32, DIM); text(ctx, 'On this machine', 924, 552, 32);
  text(ctx, 'Agent instructions', 156, 618, 32, DIM); text(ctx, 'Open to inspection', 924, 618, 32);
  text(ctx, 'Hosted models receive the requests you send them.', 156, 715, 28, DIM);
  const cmd = '$ export DO_NOT_TRACK=1';
  const n = Math.floor(clamp((t - 2.2) / 1.5) * cmd.length);
  const w = text(ctx, cmd.slice(0, n), 156, 850, 29, INK);
  if (t > 3.9) text(ctx, '   # not needed here', 156 + w, 850, 29, DIM);
}
function endCard(ctx) {
  fill(ctx, PAPER);
  ctx.drawImage(window.crudeIcon, 152, 185, 122, 122);
  text(ctx, 'Crude', 306, 282, 95, INK, 700);
  text(ctx, 'Agents and an editor in your terminal.', 154, 394, 42);
  const platforms = [[ICON.windows, 'Windows', WIN], [ICON.linux, 'Linux', LINUX], [ICON.apple, 'macOS', 'Build from source']];
  platforms.forEach(([icon, name, how], i) => {
    const y = 552 + i * 77;
    text(ctx, icon, 158, y, 37, INK); text(ctx, name, 229, y, 31, DIM); text(ctx, how, 490, y, 31, INK);
  });
  text(ctx, ICON.github, 156, 879, 37, INK); text(ctx, 'github.com/opencrude/crude', 229, 879, 33, AMBER);

}
async function poster(ctx) {
  fill(ctx, PAPER);
  ctx.drawImage(window.crudeIcon, 86, 65, 90, 90);
  text(ctx, 'Crude', 204, 135, 66, INK, 700);
  text(ctx, 'A rate limiter, from prompt to passing tests.', 500, 129, 33, DIM);
  text(ctx, '▶  Watch', 1650, 129, 35, AMBER);
  const g = await window.castGrid('r2', (await window.castMarks('r2')).p_split + 1);
  const s = 0.79;
  paintGrid(ctx, g, (W - g.cols * CELL_W * s) / 2, 184, s);
}
function coda(ctx, t) {
  fill(ctx, PAPER);
  eye(ctx, 1602, 238, 25, 0.82);
  text(ctx, '$ sudo open pod-bay-doors', 152, 452, 32);
  if (t >= 0.8) text(ctx, 'HAL is not in the sudoers file. This incident will be reported.', 152, 523, 32, DIM);
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
