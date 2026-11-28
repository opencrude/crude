// node find.mjs CAST SPEC TEXT...  -> cell positions of each TEXT on the screen at SPEC
// SPEC is seconds or MARK[+-offset]. Prints "text: row col0-col1" for every match.
import { readFileSync } from 'node:fs';
import { gunzipSync } from 'node:zlib';
import { replayTerminal } from './terminal.mjs';

const [file, spec, ...texts] = process.argv.slice(2);
const lines = (file.endsWith('.gz') ? gunzipSync(readFileSync(file)).toString('utf8') : readFileSync(file, 'utf8')).split('\n').filter(Boolean);
const hdr = JSON.parse(lines[0]);
const ev = lines.slice(1).map((l) => JSON.parse(l));
const marks = {};
for (const e of ev) if (e[1] === 'm') marks[e[2]] = e[0];
const m = spec.match(/^([A-Za-z_][\w-]*)([+-][\d.]+)?$/);
const ts = m ? marks[m[1]] + (m[2] ? parseFloat(m[2]) : 0) : parseFloat(spec);
const term = replayTerminal(hdr);
{ let c = ''; for (const e of ev) { if (e[0] > ts) break; if (e[1] === 'o') c += e[2]; } await new Promise((r) => term.write(c, r)); }
const b = term.buffer.active;
for (let y = 0; y < hdr.height; y++) {
  const line = b.getLine(y);
  // map string index to cell column
  let s = '', cols = [];
  for (let x = 0; x < hdr.width; x++) { const c = line.getCell(x); const ch = c.getChars(); if (c.getWidth() === 0) continue; const t = ch || ' '; for (let k = 0; k < t.length; k++) cols.push(x); s += t; }
  for (const t of texts) {
    let i = s.indexOf(t);
    while (i >= 0) { console.log(`${t}: row ${y} col ${cols[i]}-${cols[i + t.length - 1] + 1}`); i = s.indexOf(t, i + 1); }
  }
}
console.log('t=', ts.toFixed(2));
