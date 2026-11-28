// node dump.mjs file.cast SPEC... > out.txt
// SPEC is a time in seconds, or MARK or MARK+SECONDS / MARK-SECONDS.
// Prints the screen text at each requested time, with marks listed first.
import { readFileSync } from 'node:fs';
import { gunzipSync } from 'node:zlib';
import { replayTerminal } from './terminal.mjs';

const [file, ...specs] = process.argv.slice(2);
const lines = (file.endsWith('.gz') ? gunzipSync(readFileSync(file)).toString('utf8') : readFileSync(file, 'utf8')).split('\n').filter(Boolean);
const hdr = JSON.parse(lines[0]);
const ev = lines.slice(1).map((l) => JSON.parse(l));
const marks = {};
for (const e of ev) if (e[1] === 'm') marks[e[2]] = e[0];
console.log('marks', JSON.stringify(marks), 'end', ev[ev.length - 1][0].toFixed(2));
const times = specs.map((s) => {
  const m = s.match(/^([A-Za-z_][\w-]*)([+-][\d.]+)?$/);
  if (m) return [s, (marks[m[1]] ?? NaN) + (m[2] ? parseFloat(m[2]) : 0)];
  return [s, parseFloat(s)];
}).sort((a, b) => a[1] - b[1]);
const term = replayTerminal(hdr);
const write = (d) => new Promise((r) => term.write(d, r));
let i = 0;
for (const [label, ts] of times) {
  let chunk = '';
  while (i < ev.length && ev[i][0] <= ts) {
    if (ev[i][1] === 'o') chunk += ev[i][2];
    i++;
  }
  if (chunk) await write(chunk);
  console.log(`===== ${label} t=${ts.toFixed(2)}`);
  const b = term.buffer.active;
  for (let y = 0; y < hdr.height; y++) console.log(String(y).padStart(2) + '|' + b.getLine(y).translateToString(true).replace(/\u00a0/g, ' '));
}
