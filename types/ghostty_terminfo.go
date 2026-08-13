package types

import _ "embed"

// GhosttyTerminfoEntry is the compiled (tic -x) terminfo database entry for
// GhosttyTermName, generated from the exact Ghostty revision pinned in
// deps/GHOSTTY_PIN by scripts/bootstrap_libghostty.sh. The blob is the
// standard ncurses binary format; commit it so the binary can materialize the
// database on machines that never ran the bootstrap (users without tic/zig).
//
//go:embed terminfo/x/xterm-ghostty
var GhosttyTerminfoEntry []byte
