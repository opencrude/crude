// node scan.mjs CAST STEP PATTERN...
// Replays the cast and samples the screen every STEP seconds. For each
// regular expression it prints the time ranges in which it is on screen and
// the row where it first appears.
import { readFileSync } from 'node:fs';
import { gunzipSync } from 'node:zlib';
import { replayTerminal } from './terminal.mjs';

const [file, stepArg, ...pats] = process.argv.slice(2);
const step = parseFloat(stepArg);
const lines = (file.endsWith('.gz') ? gunzipSync(readFileSync(file)).toString('utf8') : readFileSync(file, 'utf8')).split('\n').filter(Boolean);
const hdr = JSON.parse(lines[0]);
const ev = lines.slice(1).map((l) => JSON.parse(l));
const end = ev[ev.length - 1][0];
const term = replayTerminal(hdr);
const write = (d) => new Promise((r) => term.write(d, r));
const res = pats.map((p) => ({ p, rx: new RegExp(p), ranges: [], row: -1, open: null }));
let i = 0;
for (let t = 0; t <= end + step; t += step) {
  let chunk = '';
  while (i < ev.length && ev[i][0] <= t) { if (ev[i][1] === 'o') chunk += ev[i][2]; i++; }
  if (chunk) await write(chunk);
  const b = term.buffer.active;
  const rows = [];
  for (let y = 0; y < hdr.height; y++) rows.push(b.getLine(y).translateToString(true));
  for (const r of res) {
    const y = rows.findIndex((s) => r.rx.test(s));
    if (y >= 0) {
      if (r.open === null) { r.open = t; if (r.row < 0) r.row = y; }
    } else if (r.open !== null) { r.ranges.push([r.open, t]); r.open = null; }
  }
}
for (const r of res) {
  if (r.open !== null) r.ranges.push([r.open, end]);
  const s = r.ranges.map(([a, b]) => `${a.toFixed(1)}-${b.toFixed(1)}`).join(' ');
  console.log(`${r.p}  row ${r.row}  ${s || 'never'}`);
}
