// Match the recorded terminal's two-cell emoji before applying relative cursor moves.
import xterm from '@xterm/headless';
import unicode from '@xterm/addon-unicode11';

export function replayTerminal(header) {
  const term = new xterm.Terminal({ cols: header.width, rows: header.height, allowProposedApi: true, scrollback: 0 });
  term.loadAddon(new unicode.Unicode11Addon());
  term.unicode.activeVersion = '11';
  return term;
}
