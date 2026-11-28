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
