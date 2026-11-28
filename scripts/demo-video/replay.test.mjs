import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import { gunzipSync } from 'node:zlib';
import { replayTerminal } from './terminal.mjs';

test('the New workspace card and its adjacent divider stay straight', async () => {
  const data = gunzipSync(readFileSync(new URL('casts/r1.cast.gz', import.meta.url)));
  const [header, ...events] = data.toString('utf8').trimEnd().split('\n').map(s => JSON.parse(s));
  const trust = events.find(e => e[1] === 'm' && e[2] === 'trust')[0];
  const bytes = events.filter(e => e[0] <= trust && e[1] === 'o').map(e => e[2]).join('');
  const term = replayTerminal(header);
  try {
    await new Promise(done => term.write(bytes, done));
    const row = y => term.buffer.active.getLine(y);
    assert.match(row(9).translateToString(), /📁 New workspace/);
    assert.deepEqual([8, 9, 10, 11].map(y => row(y).getCell(79).getChars()), ['╮', '│', '│', '╯']);
    assert.deepEqual([8, 9, 10, 11].map(y => row(y).getCell(81).getChars()), ['│', '│', '│', '│']);
  } finally {
    term.dispose();
  }
});

// The Arabic selection in the film must start at the first letter, and each
// letter must appear once. A combining mark (tanween) in the source made the
// recorder's console and the replay disagree about cell positions, which
// doubled a letter next to the selection.
test('the Arabic selection starts at the first letter', async () => {
  const data = gunzipSync(readFileSync(new URL('casts/r9.cast.gz', import.meta.url)));
  const [header, ...events] = data.toString('utf8').trimEnd().split('\n').map(s => JSON.parse(s));
  const at = events.find(e => e[1] === 'm' && e[2] === 'y_sel')[0];
  const term = replayTerminal(header);
  try {
    await new Promise(done => term.write(events.filter(e => e[0] <= at && e[1] === 'o').map(e => e[2]).join(''), done));
    const line = term.buffer.active.getLine(18);
    const cells = Array.from({ length: header.width }, (_, x) => line.getCell(x));
    const quote = cells.findLastIndex(c => c.getChars() === '"');
    const first = cells[quote - 1];
    assert.equal(first.getChars(), '\ufec3', 'the cell before the closing quote holds the first letter');
    assert.equal(cells.filter(c => c.getChars() === '\ufec3').length, 1);
    const selected = (c) => c.isBgRGB() && c.getBgColor() !== 0x0d1117;
    assert.ok(selected(first), 'the first letter is selected');
    assert.ok(!selected(cells[quote]), 'the closing quote is not selected');
  } finally {
    term.dispose();
  }
});
