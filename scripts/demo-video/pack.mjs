// node pack.mjs NAME [--also TEXT=REPLACEMENT ...]
//
// Writes casts/NAME.cast.gz for the repository. Personal names are replaced
// with text of the same width, so the screen layout does not move:
//   - the chat header "HOST\USER" becomes "You" followed by the header line,
//   - the computer name and the user name everywhere else,
//   - any extra TEXT=REPLACEMENT pairs, applied first (a replacement shorter
//     than its text is padded with spaces).
// The pack stops when the cast still holds an API key or a personal name.
import { readFileSync, writeFileSync } from 'node:fs';
import { gzipSync } from 'node:zlib';
import { join, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';
import os from 'node:os';

const here = dirname(fileURLToPath(import.meta.url));
const [name, ...rest] = process.argv.slice(2);
if (!name) { console.error('usage: node pack.mjs NAME [--also TEXT=REPLACEMENT ...]'); process.exit(2); }
const user = os.userInfo().username, host = os.hostname();
const sameWidth = (repl, n) => (repl.length >= n ? repl.slice(0, n) : repl + ' '.repeat(n - repl.length));
const rules = [];
for (let i = 0; i < rest.length; i++) {
  if (rest[i] !== '--also') continue;
  const [from, to = ''] = rest[++i].split('=');
  rules.push([from, sameWidth(to, from.length)]);
}
const header = `${host}\\${user} `;
const border = '\x1b[38;2;48;54;61m';
rules.push([header + border, 'You ' + border + '\u2500'.repeat(header.length - 4)]);
rules.push([host, sameWidth('WORKSTATION-237', host.length)]);
rules.push([user, sameWidth('nobody', user.length)]);

const lines = readFileSync(join(here, 'casts', name + '.cast'), 'utf8').split('\n').filter(Boolean);
const out = [lines[0]];
for (const l of lines.slice(1)) {
  const e = JSON.parse(l);
  if (e[1] === 'o') for (const [from, to] of rules) e[2] = e[2].split(from).join(to);
  out.push(JSON.stringify(e));
}
const text = out.join('\n') + '\n';
const leaks = [/sk-[A-Za-z0-9_-]{20,}/, new RegExp(user.replace(/[.*+?^${}()|[\]\\]/g, '\\$&'), 'i'), new RegExp(host, 'i')];
for (const rx of leaks) {
  const m = text.match(rx);
  if (m) { console.error(`pack: ${name} still contains "${m[0].slice(0, 12)}..."; add an --also rule or cut that part.`); process.exit(1); }
}
writeFileSync(join(here, 'casts', name + '.cast.gz'), gzipSync(text, { level: 9 }));
console.log(`packed casts/${name}.cast.gz`);
