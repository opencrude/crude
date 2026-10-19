# Validation and Visual Capture

> Status: Current guide — build/test/lint/docs gates, test isolation and the Linux visual capture workflow.

A change is accepted on evidence, not on a narrow passing test. Run the gates
that cover what you touched, then the repository gates when the change is
complete. Report blocked or aborted gates explicitly; never present them as
passing.

## Gates

CI (`.github/workflows/tui.yml`, reusable `.github/workflows/verify.yml`) runs
Linux workflow/installer checks, full-repo lint, design/docs/performance gates,
build, full tests and full race tests. A native Windows job builds/tests with
cgo and exercises both PowerShell 5.1 and 7 installers. Release jobs reuse these
checks; see [releases](releases.md). Native CI configuration is not proof that
the corresponding run has passed.
Locally:

```bash
. scripts/libghostty_env.sh          # once per shell; libghostty-vt for terminals
go build ./...
go test ./<affected packages>/...     # focused first
go test ./... -count=1 -timeout=180s  # repository suite
golangci-lint run ./...               # zero issues, whole repo
make design-audit                     # layer boundary, raw SGR, hard-coded hex
make docs-check                       # documentation acceptance gate
make bench-gate                       # allocation ceilings and work budgets
make test-race-tui                    # race-clean full repo (go test -race ./...)
```

- **Lint** (`.golangci.yml`, golangci-lint v2) enables `govet`, `ineffassign`,
  `misspell`, `staticcheck`, `unused` and `errcheck` plus `gofmt`, with zero
  issues allowed. Do not reintroduce dead code or unchecked errors.
- **Design audit** (`scripts/tui_design_audit.sh`) enforces that `workspace/`
  never imports `crude/tui`, raw ANSI SGR lives only in
  `tui/cell|compositor|rendering`, and hard-coded hex lives only in `tui/theme/`.
  Approved exceptions are frozen in
  [design token bypasses](../reference/design-token-bypasses.md).
- **Docs gate** (`scripts/docs_check.sh`) enforces naming, status headers, link
  resolution, code-to-doc references, the index and stale names; see
  [the documentation index](../README.md#documentation-rules).
- **Bench gate** (`scripts/tui_bench_gate.sh`) enforces leaf allocation ceilings
  (`scripts/perf_ceilings.txt`) and per-frame work budgets; numbers live in
  [performance baselines](../reference/performance-baselines.md).
- Cross-platform code also needs `GOOS=windows`/`GOOS=darwin` builds of the
  affected packages. Cross-compilation is not native-platform acceptance.

Never regenerate goldens, raise allocation ceilings or loosen a gate to land a
regression. Update golden output only for an intentional, visually verified
change.

## Test isolation

- Packages whose tests construct workspaces, sessions, history or a core use
  `TestMain` → `internal/testhome.Run`, which points HOME, USERPROFILE and the
  platform data directories at one temporary profile. Setting only `HOME` is not
  isolation on Windows (`os.UserHomeDir` reads USERPROFILE); it once wrote over
  a thousand test workspaces into a real profile. Add the `TestMain` to any new
  package that can reach `~/.crude`. It also sets `GIT_CONFIG_NOSYSTEM` and an
  empty `GIT_CONFIG_GLOBAL`, so Git fixtures behave identically on every
  machine (Git for Windows' system config sets `core.autocrlf=true`); tests of
  line-ending policy set it per repository.
- Windows fixtures normalize the inherited temporary root to its long name;
  Git and core attachment already return that spelling. CI places temporary
  databases on the runner work disk, keeping real durability checks enabled.
  Explicit 8.3-name regressions still exercise document open/edit/save and
  directory safety without treating short-name expansion as a symlink.
- `CRUDE_HOME` selects an isolated profile (own core, config, workspaces,
  sessions) for developing Crude inside Crude.
- `MODELS_DEV_URL` overrides the models.dev catalog endpoint for tests and
  air-gapped runs.
- Real-shell fixtures must run the same UI teardown (`App.StopAsyncIO`) as the
  product, and standalone editor fixtures register `Editor.Close`. Leaked
  watchers exhaust inotify and make unrelated later tests fail with EMFILE;
  never hide that with retries, sleeps or raised OS limits.

## Test helper ownership

- Go `_test.go` files in one package share package scope. A helper defined in a
  feature-specific test file may silently serve unrelated tests.
- Before deleting a test file, search the whole package for every helper it
  defines (functions, constants, fixtures, builders), not just tests named after
  that feature.
- Move genuinely shared helpers to the package's neutral `test_helpers_test.go`
  in the same change. Do not restore deleted production UI or add compatibility
  code merely to make tests compile.
- Run the complete package and `go test ./...` after deletion; targeted screen
  tests cannot detect missing package-wide helpers.

## Visual capture

Use the repository capture scripts for real UI acceptance, at both a readable
desktop size and a constrained terminal size. On Linux use `scripts/tuishot.sh`;
on Windows follow [Windows visual capture](windows-visual-capture.md). Never use
WSL to inspect a Windows product failure, and never capture or send input to an
unrelated user window.

Never run two captures concurrently on either platform: Alacritty and Windows
desktop input/focus are process-global, so concurrent runs can target the wrong
window and invalidate both results.

A discussion-sized Linux capture:

```bash
COLS=160 ROWS=40 FONT_SIZE=11 scripts/tuishot.sh /tmp/crude-screenshots \
  key:ctrl+alt+p 'type:application settings' key:Return
```

`Ctrl+Alt+P` opens the Command Palette; `type:application settings` filters it
and `Return` activates the highlighted Application Settings entry. The broader
`settings` query also matches command descriptions, so an extra `Down` can
select a different action.

Linux capture profiles need a real short `CRUDE_HOME`: the canonical
`<CRUDE_HOME>/.crude/corewire/core.sock` must fit 103 bytes, and a symlink to a
long scratch path does not help. Use `mktemp -d /tmp/crude-shot-XXXXXX`, keep
screenshots and logs in `CRUDE_TEMP_DIR`, and close only the isolated test core
afterward. `scripts/tuishot.sh` rejects an over-long socket path and fails
immediately after a product exit or screenshot error. Inspect boot evidence
before input: a capture driver finishing is not product success.

After every capture, close the dedicated Crude window. Keep `KEEP_OPEN=0` for
normal captures; after an explicit `KEEP_OPEN=1` review, close it and verify no
`CrudeShot`/`CrudeShot-*` window remains (`xdotool search --class CrudeShot`).

For stationary pointer acceptance, `down:col,row`, `move:col,row`, `sleep:1`,
`shot:held`, and `up:col,row` keep the same button down across steps. Compare the
held viewport with a later stationary shot and verify it stops after release;
a burst drag alone does not establish timer-driven edge scrolling. Cleanup
releases a held button even if capture fails.
