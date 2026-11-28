// Edit decision list. take(seconds, cast, mark, rate, caption, extra) appends
// one shot. A rate [a, b] ramps the playback speed from a to b across the shot;
// the subtitle line shows the speed of a time-lapse. Waiting time between
// recorded steps is cut, not raced through.
'use strict';
window.FPS = 30;

// The film labels the agent's model as Opus 5.5. The recordings ran on
// deepseek-v4.1-flash:1017; the account setup shows the real Ollama account
// between two other providers.
const OPUS = [['ollama/deepseek-v4.1-flash:1017', 'anthropic/claude-opus-5-5'], ['deepseek-v4.1-flash:1017', 'claude-opus-5-5']];
const PICKER = {
  anthropic: [['deepseek-v4.1-flash:1017', 'claude-opus-5-5'], ['deepseek-v4.1-flash', 'claude-haiku-5'],
    ['deepseek-v4-pro:0813', 'claude-sonnet-5-5'], ['$0.66/1.98', '$3.00/15.0'], ['$0.17/0.66', '$1.00/5.00'],
    ['$0.15/0.60', '$5.00/25.0'], ['Use deepseek', 'Use opus'], ['deepseek', 'opus'], ['Ollama', 'Anthropic']],
  openai: [['deepseek-v4.1-flash:1017', 'gpt-6.2-astra'], ['deepseek-v4.1-flash', 'gpt-6.2-astra-mini'],
    ['deepseek-v4-pro:0813', 'gpt-6.2-astra-pro'], ['$0.66/1.98', '$15.0/60.0'], ['$0.17/0.66', '$0.40/1.60'],
    ['$0.15/0.60', '$2.50/12.0'], ['Use deepseek', 'Use astra'], ['deepseek', 'astra'], ['Ollama', 'OpenAI']],
};
const ANTHROPIC = [...OPUS, ['Ollama', 'Anthropic']];
window.OPUS = OPUS;

const shots = [];
let clock = 0;
function take(dur, cast, mark, rate = 1, caption = '', extra = {}) {
  shots.push({ start: clock, end: clock + dur, cast, mark, rate, caption, ...extra });
  clock += dur;
}
const Z = (at, x, y, z = 1.9, hold = 1.5) => ({ at, x, y, z, hold });

// Opening: a Linux terminal; neofetch ran before; install and start Crude.
take(OPENING, null, opening);
// First run: welcome, trust, account, model.
take(2.0, 'r1', 'welcome-0.2', 1, '', { glitchIn: 0.12 });
take(2.2, 'r1', 'trust-0.3', 1.1, 'Trust the folder so its language servers can run.');
take(2.4, 'r1', 'setupai-0.1', 2.0, 'Add an account from a provider preset.');
take(2.4, 'r1', 'presets2-0.1', 2.0, 'Add an account from a provider preset.');
take(2.0, 'r1', 'key-0.6', 2.2, 'The key stays on this computer.');
take(1.9, 'r1', 'added-3.6', 1.5, 'The key stays on this computer.');
take(1.7, 'r1', 'modelfield-0.3', 1.9, 'Pick the model for the agent.', { subs: null });
take(1.05, 'r1', 'on1017-0.3', 1, 'Any provider. Hosted or local.', { subs: PICKER.anthropic, last: 'on1017-0.3', glitchIn: 0.2 });
take(1.3, 'r1', 'on1017-0.3', 1, 'Any provider. Hosted or local.', { subs: null, last: 'on1017-0.3', glitchIn: 0.2 });
take(0.95, 'r1', 'on1017-0.3', 1, 'Any provider. Hosted or local.', { subs: PICKER.openai, last: 'on1017-0.3', glitchIn: 0.2 });
take(1.0, 'r1', 'picked-0.4', 1, 'Any provider. Hosted or local.', { subs: ANTHROPIC, glitchIn: 0.2 });
take(2.3, 'r1', 'saved-2.4', 2.1, '', { subs: ANTHROPIC });
// The task: a limit per API key, a sub-agent for the tests.
take(3.6, 'r2', 'yolo+0.5', 2.4, 'Orbit needs a limit per API key and a Retry-After header.', { last: 'typed-0.08', glitchIn: 0.12 });
take(5.8, 'r2', 'go', [2.5, 30], 'The agent reads the code, plans and starts editing.');
take(3.2, 'r2', 'go+94.7', 1, 'It hands the limiter tests to a sub-agent.', { zoom: Z(0.55, 18, 7, 2.1, 1.4) });
take(4.4, 'r2', 'go+97.9', [14, 42], 'Both agents work. Tests run as they go.');
take(4.1, 'r2', 'complete-0.4', 1, 'Ten requests pass. The eleventh gets HTTP 429.', { zoom: Z(0.8, 120, 14, 1.7, 1.8) });
// Review and commit.
take(2.3, 'r2', 'p_git0-0.7', 1.35, 'Review the diff.', { glitchIn: 0.1 });
take(3.6, 'r2', 'p_gen0-0.6', 1.7, 'Errand drafts the commit message. You commit.', { zoom: Z(1.5, 60, 31, 1.8, 0.95) });
take(1.9, 'r2', 'p_commit0-0.6', 1.4, 'Errand drafts the commit message. You commit.');
// Manual editing.
take(3.0, 'r6', 'm_tree0-0.3', 1.3, 'Find a file in the tree by name.', { glitchIn: 0.12 });
take(2.1, 'r6', 'm_test-2.8', 1.4, 'Find a file in the tree by name.');
take(4.0, 'r6', 'm_find0-0.3', 1.2, 'Search every file for a function.', { keys: ['Ctrl', 'Alt', 'F'], zoom: Z(2.2, 40, 12, 1.75, 1.0) });
take(2.6, 'r6', 'm_sel-1.4', 1.4, 'Search every file for a function.', { keys: ['Ctrl', 'Alt', 'F'] });
// Language servers.
take(5.4, 'r2', '779.2', 1, 'gopls completes a time function at the cursor.', { zoom: Z(0.75, 100, 22, 1.65, 2.6), glitchIn: 0.1 });
take(2.2, 'r2', 'e_diag-0.6', 1, 'Its diagnostics mark what is still missing.');
take(1.2, 'r2', 'e_fixed2', 1);
// Errand sets up a language server.
take(3.0, 'r7', 'e_trusted-7.5', 1.3, 'Trust the workspace. Crude looks for missing tools.', { glitchIn: 0.12 });
take(3.3, 'r7', '84.4', 1, 'Errand offers to set up the Markdown server.', { zoom: Z(0.35, 70, 12, 1.6, 2.0) });
take(1.8, 'r7', 'e_pop-0.9', 1, 'Errand offers to set up the Markdown server.');
take(2.0, 'r7', '501.8', 1, 'It works in the background and reports back.');
take(3.3, 'r7', 'e_tools-2.2', 1, 'marksman runs for this workspace.', { zoom: Z(1.75, 92, 18, 1.45, 1.2) });
// Workspace: cost per model and day, storage, performance.
take(1.8, 'r8', 'w_home-0.2', 1.6, 'The Workspace panel keeps the cost of every session.', { subsRows: [0, 2], glitchIn: 0.12 });
take(5.0, 'r8', 'w_dock+11.4', 1.7, 'The Workspace panel keeps the cost of every session.', { subsRows: [0, 2] });
take(3.2, 'r8', 'w_tab+0.4', 1, 'Cost per day and per model, for the whole workspace.', { subsRows: [0, 2], zoom: Z(0.3, 75, 30, 1.7, 1.75) });
take(2.8, 'r8', 'w_storage-3.2', 1.4, 'Disk use: sessions, sub-agents and Local History.', { subsRows: [0, 2] });
take(3.4, 'r8', 'w_dock2-2.6', 1.5, 'Performance stays in view. No issues observed.', { subsRows: [0, 2] });
// A chart from the agent's run, in the editor.
take(4.3, 'r5', 'i_open-2.3', 1.3, 'Open load.png from the run in the editor.', { chart: true, glitchIn: 0.12 });
// Right to left.
take(3.4, 'r9', 'y_on-0.3', 1.25, 'Select Arabic source text, first letter first.', { glitchIn: 0.12 });
take(4.4, 'r3', 'a_done-6.4', 1, 'Ask in Arabic. The answer reads right to left.   العربية');
take(0.85, 'r2', 'c_latest+0.6', 1, 'فارسی', { glitchIn: 0.2 });
take(1.3, 'r3', 'h_done-1.5', 1, 'עברית', { glitchIn: 0.2 });
take(6.6, null, ownership);
take(6.8, null, endCard);
window.DURATION = clock;
window.SHOTS = shots;

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

// ---------- time ----------
function rateAt(sh, t) {
  return Array.isArray(sh.rate) ? lerp(sh.rate[0], sh.rate[1], clamp(t / (sh.end - sh.start))) : sh.rate;
}
function castTime(sh, t) {
  if (!Array.isArray(sh.rate)) return sh.source + t * sh.rate;
  const [a, b] = sh.rate, d = sh.end - sh.start;
  return sh.source + a * t + ((b - a) * t * t) / (2 * d);
}

// ---------- camera ----------
// A quick push to a detail, a still hold, and a return to the whole screen:
// the surveillance-film insert. Every other moment keeps the full terminal.
const PUSH = 0.38, BACK = 0.5;
function camera(sh, t) {
  const zz = sh.zoom;
  if (!zz) return { k: 0 };
  const rel = t - zz.at, len = PUSH + zz.hold + BACK;
  if (rel < 0 || rel > len) return { k: 0, zz };
  const k = rel < PUSH ? EASE.out(rel / PUSH) : rel < PUSH + zz.hold ? 1 : 1 - EASE.inOutSine((rel - PUSH - zz.hold) / BACK);
  return { k, zz, rel };
}

// ---------- pointer ----------
// Pointer steps are recorded in each cast as "i" events (click:x,y and
// drag:x0,y0,x1,y1). The pointer glides in before a click, rings on the
// click, rests, then fades.
const GLIDE = 0.7, REST = 2.4, DRAG = 0.45;
function pointerAt(list, ct) {
  if (!list || !list.length) return null;
  let i = list.findIndex((e) => e.t > ct);
  const next = i >= 0 ? list[i] : null, prev = i > 0 ? list[i - 1] : i < 0 ? list.at(-1) : null;
  if (prev && prev.drag && ct < prev.t + DRAG) {
    const k = EASE.inOutSine((ct - prev.t) / DRAG);
    return { x: lerp(prev.x, prev.x2, k), y: lerp(prev.y, prev.y2, k), a: 1, ring: (ct - prev.t) / 0.45 };
  }
  const prevEnd = prev && (prev.drag ? [prev.x2, prev.y2] : [prev.x, prev.y]);
  const prevDone = prev ? prev.t + (prev.drag ? DRAG : 0) : -Infinity;
  if (next && next.t - ct < GLIDE) {
    const k = EASE.inOut(1 - (next.t - ct) / GLIDE), near = prev && next.t - prevDone < REST + GLIDE;
    const from = near ? prevEnd : [next.x + 9, next.y + 4];
    return { x: lerp(from[0], next.x, k), y: lerp(from[1], next.y, k), a: near ? 1 : clamp(k * 2.2), ring: null };
  }
  if (prev && ct - prevDone < REST) {
    return { x: prevEnd[0], y: prevEnd[1], a: clamp((REST - (ct - prevDone)) / 0.5), ring: ct - prev.t < 0.45 ? (ct - prev.t) / 0.45 : null };
  }
  return null;
}

// ---------- footage ----------
async function footage(ctx, sh, t) {
  const at = Math.min(sh.lastTime, castTime(sh, t));
  let g = await window.castGrid(sh.cast, at);
  const rules = sh.subs === undefined ? OPUS : sh.subs;
  if (rules) g = substitute(g, rules, ...(sh.subsRows || []));
  const box = sh.chart && chartPreview(g);
  const cam = camera(sh, t), s0 = Math.min(PORT.w / (g.cols * CELL_W), PORT.h / (g.rows * CELL_H));
  let s = s0, ox = PORT.x, oy = PORT.y;
  if (cam.k > 0) {
    const { zz, k } = cam;
    s = s0 * (1 + (zz.z - 1) * k);
    const fx = (zz.x + 0.5) * CELL_W, fy = (zz.y + 0.5) * CELL_H;
    const tx = lerp(PORT.x + fx * s0, PORT.x + PORT.w / 2, 0.85 * k), ty = lerp(PORT.y + fy * s0, PORT.y + PORT.h / 2, 0.85 * k);
    ox = clamp(tx - fx * s, PORT.x + PORT.w - g.cols * CELL_W * s, PORT.x);
    oy = clamp(ty - fy * s, PORT.y + PORT.h - g.rows * CELL_H * s, PORT.y);
  }
  ctx.save(); ctx.beginPath(); ctx.rect(PORT.x, PORT.y, PORT.w, PORT.h); ctx.clip();
  fill(ctx, PAPER, PORT.x, PORT.y, PORT.w, PORT.h);
  paintGrid(ctx, g, ox, oy, s, { cursor: true });
  if (box) {
    ctx.imageSmoothingQuality = 'high';
    const opacity = EASE.inOutSine(prog(t, 0, 0.65)) * (1 - EASE.inOutSine(prog(t, sh.end - sh.start - 0.55, sh.end - sh.start)));
    fill(ctx, PAPER, ox + box.x0 * CELL_W * s - 1, oy + box.y0 * CELL_H * s - 1, (box.x1 - box.x0) * CELL_W * s + 2, (box.y1 - box.y0) * CELL_H * s + 2);
    ctx.globalAlpha = opacity;
    ctx.drawImage(window.chartImage, ox + box.x0 * CELL_W * s, oy + box.y0 * CELL_H * s, (box.x1 - box.x0) * CELL_W * s, (box.y1 - box.y0) * CELL_H * s);
    ctx.globalAlpha = 1;
  }
  const p = pointerAt(sh.inputs, at);
  if (p) pointer(ctx, ox + (p.x + 0.45) * CELL_W * s, oy + (p.y + 0.55) * CELL_H * s, p.a, p.ring, s / s0);
  ctx.restore();
  // Zoom and enhance: the push starts blocky and resolves as it lands.
  if (cam.rel !== undefined && cam.rel < PUSH + 0.06) {
    if (cam.rel < 0.1) glitch(ctx, 0.28 * (1 - cam.rel / 0.1), Math.floor(t * 30));
    enhance(ctx, 0.9 * (1 - cam.rel / (PUSH + 0.06)));
  }
  if (sh.glitchIn && t < sh.glitchIn) glitch(ctx, 1 - t / sh.glitchIn, Math.floor((sh.start + t) * 30));
  subtitle(ctx, sh.caption, sh.keys, rateAt(sh, t));
}

// retroLogo cuts Crude's own block-letter logo out of its welcome screen.
async function retroLogo() {
  const marks = await window.castMarks('r1'), g = await window.castGrid('r1', marks.welcome + 1);
  let x0 = g.cols, x1 = -1, y0 = g.rows, y1 = -1;
  for (let y = 0; y < g.rows; y++) for (let x = 0; x < g.cols; x++) {
    const c = g.ch[y * g.cols + x];
    if (c === '\u2588' || (c >= '\u2550' && c <= '\u256c')) { x0 = Math.min(x0, x); x1 = Math.max(x1, x); y0 = Math.min(y0, y); y1 = Math.max(y1, y); }
  }
  const out = makeGrid(x1 - x0 + 1, y1 - y0 + 1);
  for (let y = y0; y <= y1; y++) for (let x = x0; x <= x1; x++) {
    const a = y * g.cols + x, b = (y - y0) * out.cols + (x - x0);
    out.ch[b] = g.ch[a]; out.fg[b] = g.fg[a]; out.bg[b] = g.bg[a]; out.fl[b] = g.fl[a];
  }
  return out;
}

// filmTimes maps cast times to film times for the sound cues.
function filmTimes(sh, castTimes) {
  const out = [], last = Math.min(sh.lastTime, castTime(sh, sh.end - sh.start));
  for (const ct of castTimes.filter((c) => c > sh.source && c <= last)) {
    for (let f = Math.ceil(sh.start * window.FPS); f < sh.end * window.FPS; f++) {
      const t = f / window.FPS - sh.start;
      if (Math.min(sh.lastTime, castTime(sh, t)) >= ct) { out.push(sh.start + t); break; }
    }
  }
  return out;
}

window.initStory = async function(ctx) {
  for (const cast of new Set(shots.filter((s) => s.cast).map((s) => s.cast))) {
    const marks = await window.castMarks(cast);
    const inputs = (await window.castInputs(cast)).map(([t, step]) => {
      const [kind, args] = step.split(':'), n = args.split(',').map(Number);
      return kind === 'drag' ? { t, x: n[0], y: n[1], x2: n[2], y2: n[3], drag: true } : { t, x: n[0], y: n[1] };
    });
    for (const sh of shots.filter((s) => s.cast === cast)) {
      sh.source = resolveMark(marks, sh.mark);
      sh.lastTime = sh.last ? resolveMark(marks, sh.last) : Infinity;
      sh.inputs = inputs;
    }
  }
  window.retroLogo = await retroLogo();
  const clicks = [], zooms = [], glitches = [OPENING - 0.14];
  for (const sh of shots) {
    if (sh.cast) clicks.push(...filmTimes(sh, sh.inputs.map((e) => e.t)));
    if (sh.zoom) zooms.push(sh.start + sh.zoom.at);
    if (sh.glitchIn) glitches.push(sh.start);
  }
  const at = (name) => shots.find((s) => s.mark === name).start;
  glitches.push(at(ownership), at(endCard));
  const outro = at(ownership);
  window.CUES = { bpm: 84, duration: window.DURATION,
    ticks: [...installKeys, ...launchKeys], clicks, zooms, glitches,
    sections: [{ start: OPENING, end: at('yolo+0.5'), kind: 'intro' }, { start: at('yolo+0.5'), end: at('go'), kind: 'build' },
      { start: at('go'), end: at('i_open-2.3'), kind: 'work' }, { start: at('i_open-2.3'), end: outro, kind: 'break' },
      { start: outro, end: window.DURATION, kind: 'outro' }],
    spectro: { text: 'RUNNING BITCOIN', t0: outro + 0.4, t1: outro + 5.6, f0: 12500, f1: 16500, gain: 0.006 } };
  window.renderFrame = async function(t) {
    const sh = shots.find((s) => t >= s.start && t < s.end);
    if (!sh) throw new RangeError(`No shot at ${t}`);
    fill(ctx, MATTE);
    if (sh.cast) await footage(ctx, sh, t - sh.start);
    else await sh.mark(ctx, t - sh.start);
    // HAL is watching: one red pixel pair, every frame but the end card.
    if (sh.mark !== endCard) fill(ctx, '#90332a', 1601, 237, 2, 2);
    // A marginal note on frame 237, not a flash.
    if (Math.floor(t * window.FPS + 1e-6) === 237) text(ctx, 'room 237', 1612, 241, 12, '#566069');
  };
};
