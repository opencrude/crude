# libghostty-vt (pinned)

Crude paints an embedded terminal from Ghostty's VT library, not a
hand-written parser. The C API is unstable, so this tree pins one Ghostty
commit and installs only `libghostty-vt` into a gitignored prefix.

## Decision

`go.mitchellh.com/libghostty` at `56450a2` requires **Go 1.26**. Crude's
toolchain is **Go 1.25**. That module also wraps the entire unstable C
surface (~80 files). Crude uses a narrow nonvisual cgo adapter in `terminalvt`
against the pinned headers instead.

## Bootstrap

Zig 0.16.x must be on `PATH` (install under `~/tools/zig-0.16.0` is enough).
On Linux, `tic` (Debian/Ubuntu package `ncurses-bin`) is also required.

### Linux / macOS

```bash
scripts/bootstrap_libghostty.sh
```

This clones Ghostty at the commit in `GHOSTTY_PIN` (default source dir
`$HOME/tools/src/ghostty`) and installs headers + `libghostty-vt.a` into
`deps/libghostty-vt/`.

### Windows

```powershell
pwsh scripts/bootstrap_libghostty.ps1
```

The Windows bootstrap builds only `libghostty-vt` (producing
`ghostty-vt-static.lib`, not `libghostty-vt.a`). The terminfo step is
skipped — the compiled `xterm-ghostty` entry is already committed under
`types/terminfo/` and embedded via `go:embed`, so Windows users don't
need `tic`.

The Windows build uses `zig cc` as the C compiler (`CC=zig cc`): it
produces MSVC COFF objects that link cleanly against `ghostty-vt-static.lib`
while accepting gcc-style cgo flags. `gcc`/MinGW produces ABI-incompatible
objects (`__chkstk` mismatch); `clang` targets the MSVC ABI but rejects
cgo's `-mthreads` flag. Source `scripts/libghostty_env.ps1` before building
to set `CC`, `CXX`, `PKG_CONFIG_PATH`, and add Zig to `PATH`.

## Pinned terminfo

The same script also generates the `xterm-ghostty` terminfo source from the
pinned commit (the upstream terminfo module is emitted by a tiny Zig runner),
compiles it with `tic -x`, and installs it twice:

- `deps/libghostty-vt/share/terminfo/` — local build prefix (gitignored)
- `types/terminfo/` — committed copy embedded into the binary via
  `types/ghostty_terminfo.go` (`go:embed`)

At runtime `workspace.TerminfoDir()` materializes the compiled entry under
`~/.crude/terminfo/x/xterm-ghostty` (override with `CRUDE_TERMINFO_DIR`) and
`PTYEnviron` sets `TERMINFO` for every PTY child, so `TERM=xterm-ghostty`
always resolves — including for installed binaries with no repository
checkout.

`go build` / `go test` then link via cgo `#cgo` paths relative to `terminalvt`.
`CGO_ENABLED=0` builds compile a stub that returns an error from
`terminalvt.New`; they do not silently keep the old parser or advertise
working `TERM=xterm-ghostty` emulation.
