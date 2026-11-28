// Edit decision list. Each entry ends at a film time in seconds. Recorded
// excerpts are straight cuts: do not race through the idle time between them.
'use strict';
window.FPS = 30;
const shots = [];
function take(end, cast, mark, rate = 1, caption = '', extra = {}) {
  shots.push({ start: shots.at(-1)?.end || 0, end, cast, mark, rate, caption, ...extra });
}
take(9, null, opening);
take(11.2, 'r1', 'welcome');
take(13.4, 'r1', 'folder-0.7');
take(16, 'r1', 'trust-0.45');
take(18.8, 'r1', 'addaccount+0.1', 1, 'Use a provider preset to add an account.');
take(21.4, 'r1', 'key-0.55', 1, 'Use a provider preset to add an account.');
take(24.8, 'r1', 'list+0.05', 1.55, 'Search the catalog by model name.');
take(29.6, 'r2', 'yolo+1', 1.65, 'Orbit needs a limit per API key and a Retry-After header.', { last: 'typed-0.08' });
take(32.6, 'r2', 'go+6', 1.9, 'Hand the bucket tests to a second agent.');
take(36.2, 'r2', '140.5', 2.5, 'Hand the bucket tests to a second agent.');
take(40, 'r2', 'complete', 1, 'Ten requests pass. The eleventh gets a 429.');
take(43, 'r2', '352.2', 1.75, 'Review the diff before committing.');
take(46.8, 'r2', 'p_float', 1, 'This run cost four cents.');
take(49.5, 'r2', '451.8', 1, 'Float the performance panel to watch the live timings.');
take(52.3, 'r2', '471.3', 1, 'Float the performance panel to watch the live timings.');
take(56, 'r2', '581.6', 1, 'Arrange the panes so the test run stays in view.');
take(63, 'r2', '778.5', 1, 'Look up a time function at the cursor.', { detail: true });
take(65.7, 'r2', 'e_diag-0.7', 1, 'gopls marks the unfinished timestamp on line 15.');
take(67.4, 'r2', 'e_fixed2', 1, 'The diagnostic clears.');
take(72.2, 'r5', 'i_open-2.3', 1.25, 'Open load.png in the editor.', { chart: true });
take(75.3, 'r4', 'x_sel0-0.4', 1.75, 'Select the localized error message in the source.');
take(80, 'r3', 'a_done-7', 1, 'Ask about the same limiter in Arabic.   العربية');
take(80.9, 'r2', 'c_latest+0.6', 1, 'فارسی', { signal: true });
take(82.2, 'r3', 'h_done-1.5', 1, 'עברית', { signal: true });
take(90, null, ownership);
take(96.5, null, (ctx) => endCard(ctx));
take(100.5, null, coda);
window.DURATION = shots.at(-1).end;

function resolveMark(marks, spec) {
  if (Number.isFinite(Number(spec))) return Number(spec);
  const m = /^([A-Za-z_]\w*)([+-][\d.]+)?$/.exec(spec);
  if (!m || marks[m[1]] === undefined) throw new Error(`Unknown cast time: ${spec}`);
  return marks[m[1]] + Number(m[2] || 0);
}
// chartPreview finds the image viewer's cell preview and returns its cell box.
// A Kitty or Sixel terminal shows the PNG's own pixels there; the recorder's
// console has no image protocol, so the player draws the PNG in that box.
// The status text about the reduced preview is cleared for the same reason.
function chartPreview(g) {
  let x0 = g.cols, y0 = g.rows, x1 = -1, y1 = -1;
  for (let y = 3; y < g.rows - 1; y++) {
    for (let x = 0; x < g.cols; x++) {
      const c = g.ch[y * g.cols + x];
      if (c === '\u2580' || c === '\u2584') { if (x < x0) x0 = x; if (x > x1) x1 = x; if (y < y0) y0 = y; if (y > y1) y1 = y; }
    }
  }
  for (let y = 0; y < 3; y++) {
    let row = '';
    for (let x = 0; x < g.cols; x++) row += g.ch[y * g.cols + x] || ' ';
    const i = row.indexOf('\u00b7 Reduced detail');
    if (i >= 0) for (let k = i - 1; k < i + 16 && k < g.cols; k++) g.ch[y * g.cols + k] = ' ';
  }
  return x1 > x0 + 10 && y1 > y0 + 5 ? { x0, y0, x1: x1 + 1, y1: y1 + 1 } : null;
}

// One optical insert. Hold the popup still for three seconds, then restore
// the establishing view before the cut. No motion on any other camera.
function lens(sh, elapsed) {
  if (!sh.detail) return 0;
  return EASE.inOutSine(prog(elapsed, 1.35, 2.55)) * (1 - EASE.inOutSine(prog(elapsed, 5.3, 6.5)));
}
const PORT = { x: 48, y: 8, w: 1824, h: 1026 };
async function footage(ctx, sh, t) {
  const at = Math.min(sh.lastTime, sh.source + t * sh.rate);
  const g = await window.castGrid(sh.cast, at);
  const box = sh.chart && chartPreview(g);
  const p = lens(sh, t), z = 1 + 0.33 * p;
  const { x, y, w, h } = PORT, s = Math.min(w / (g.cols * CELL_W), h / (g.rows * CELL_H)) * z;
  // Anchor the lens at the menu, rather than panning it through the screen.
  const ox = x + (w - g.cols * CELL_W * s) * 0.72, oy = y + (h - g.rows * CELL_H * s) * 0.52;
  ctx.save(); ctx.beginPath(); ctx.rect(x, y, w, h); ctx.clip();
  paintGrid(ctx, g, ox, oy, s, { cursor: true });
  if (box) {
    ctx.imageSmoothingQuality = 'high';
    const opacity = EASE.inOutSine(prog(t, 0, 0.65)) * (1 - EASE.inOutSine(prog(t, sh.end - sh.start - 0.55, sh.end - sh.start)));
    fill(ctx, PAPER, ox + box.x0 * CELL_W * s - 1, oy + box.y0 * CELL_H * s - 1,
      (box.x1 - box.x0) * CELL_W * s + 2, (box.y1 - box.y0) * CELL_H * s + 2);
    ctx.globalAlpha = opacity;
    ctx.drawImage(window.chartImage, ox + box.x0 * CELL_W * s, oy + box.y0 * CELL_H * s,
      (box.x1 - box.x0) * CELL_W * s, (box.y1 - box.y0) * CELL_H * s);
  }
  ctx.restore();
  // Subtitles get their own space; no ink is laid over a product control.
  ctx.textAlign = 'center';
  if (sh.signal && t < 0.12) {
    text(ctx, sh.caption, W / 2 + 2, 1066, 25, BLUE);
    text(ctx, sh.caption, W / 2 - 2, 1066, 25, AMBER);
  }
  text(ctx, sh.caption, W / 2, 1066, 25); ctx.textAlign = 'left';
}
window.initStory = async function(ctx) {
  for (const cast of new Set(shots.filter(s => s.cast).map(s => s.cast))) {
    const marks = await window.castMarks(cast);
    for (const sh of shots.filter(s => s.cast === cast)) {
      sh.source = resolveMark(marks, sh.mark);
      sh.lastTime = sh.last ? resolveMark(marks, sh.last) : Infinity;
    }
  }
  window.CUES = { bpm: 80, duration: window.DURATION,
    ticks: [...installKeys, ...launchKeys],
    sections: [{ start: 9, end: 24, kind: 'intro' }, { start: 24, end: 30, kind: 'build' },
      { start: 30, end: 54, kind: 'work' }, { start: 54, end: 82.2, kind: 'break' }, { start: 82.2, end: 96.5, kind: 'outro' }],
    spectro: { text: 'RUNNING BITCOIN', t0: 90.9, t1: 95.6, f0: 12500, f1: 16500, gain: 0.006 },
    stinger: { t0: 96.5, t1: window.DURATION, beep: 97.25 } };
  window.renderFrame = async function(t) {
    const sh = shots.find(s => t >= s.start && t < s.end);
    if (!sh) throw new RangeError(`No shot at ${t}`);
    fill(ctx, MATTE);
    if (sh.cast) await footage(ctx, sh, t - sh.start);
    else sh.mark(ctx, t - sh.start);
    if (t < 96.5) fill(ctx, '#90332a', 1601, 237, 2, 2);
    // A marginal note, not a single-frame flash.
    if (Math.floor(t * window.FPS + 1e-6) === 237) text(ctx, 'room 237', 1612, 241, 12, '#566069');
  };
};
