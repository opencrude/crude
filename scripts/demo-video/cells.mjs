// node cells.mjs CAST SPEC ROW [COL0 COL1]: prints each cell of one row as "col:U+XXXX char".
import { readFileSync } from 'node:fs';
import { gunzipSync } from 'node:zlib';
import { replayTerminal } from './terminal.mjs';
const [file, spec, rowArg, c0 = '0', c1 = '9999'] = process.argv.slice(2);
const lines = (file.endsWith('.gz') ? gunzipSync(readFileSync(file)).toString('utf8') : readFileSync(file, 'utf8')).split('\n').filter(Boolean);
const hdr = JSON.parse(lines[0]);
const ev = lines.slice(1).map((l) => JSON.parse(l));
const marks = {}; for (const e of ev) if (e[1] === 'm') marks[e[2]] = e[0];
const m = spec.match(/^([A-Za-z_][\w-]*)([+-][\d.]+)?$/);
const ts = m ? marks[m[1]] + (m[2] ? parseFloat(m[2]) : 0) : parseFloat(spec);
const term = replayTerminal(hdr);
let c = ''; for (const e of ev) { if (e[0] > ts) break; if (e[1] === 'o') c += e[2]; }
await new Promise((r) => term.write(c, r));
const line = term.buffer.active.getLine(+rowArg);
const out = [];
for (let x = +c0; x < Math.min(hdr.width, +c1); x++) { const ch = line.getCell(x).getChars(); if (ch && ch !== ' ') out.push(`${x}:U+${ch.codePointAt(0).toString(16).toUpperCase()}`); }
console.log(out.join(' '));
