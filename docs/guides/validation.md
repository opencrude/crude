# Validation and Visual Capture

> Status: Current guide — build/test/lint/docs gates, test isolation and the Linux visual capture workflow.

A change is accepted on evidence, not on a narrow passing test. Run the gates
that cover what you touched, then the repository gates when the change is
complete. Report blocked or aborted gates explicitly; never present them as
passing.

## Gates

Optional manual CI (`.github/workflows/tui.yml`, reusable
`.github/workflows/verify.yml`) runs
Linux workflow/installer checks, full-repo lint, design/docs/performance gates,
build, full tests and full race tests. A native Windows job builds/tests with
cgo and exercises both PowerShell 5.1 and 7 installers. Release jobs reuse these
checks; see [releases](releases.md). Native CI configuration is not proof that
the corresponding run has passed. Push, pull-request and tag triggers are removed;
repository Actions is disabled. Local checks and manual publication do not require
a hosted workflow. Explicitly re-enable Actions before deliberately dispatching
one; see the [manual release path](releases.md#manual-local-publication).
Locally:

```bash
. scripts/libghostty_env.sh          # once per shell; libghostty-vt for terminals
go run ./internal/wiregen -check      # nonmutating source-cohort freshness
go build ./...
go test ./<affected packages>/...     # focused first
go test ./... -count=1 -timeout=180s  # repository suite
golangci-lint run ./...               # zero issues, whole repo
make design-audit                     # layer boundary, raw SGR, hard-coded hex
make docs-check                       # documentation acceptance gate
make bench-gate                       # allocation ceilings and work budgets
make test-race-tui                    # race-clean full repo (go test -race ./...)
```

CI uses `python scripts/test_race.py` for the full race suite. It runs every
package; the large session/viewmodel packages are exhaustively partitioned by
discovered top-level test/example/fuzz name, each exactly once. The multi-megabyte
upload recovery stress case runs separately with the same five-minute limit.
No workload size, individual assertion or timeout is relaxed to fit the sum of
hundreds of cases into one package-wide budget.

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

### Protocol generation and v2 acceptance

After production-source edits, explicitly run `go run ./internal/wiregen` in the
canonical native cgo environment, inspect its generated diff, then run
`go run ./internal/wiregen -check`. The check is nonmutating; it does not regenerate
codecs or repair stale output. Make build/test/benchmark targets,
`scripts/build.sh` and `scripts/release.py` check freshness. An arbitrary direct
`go build` does not invoke generation or the check.

The full source cohort covers production platform/tag alternatives, local forks,
embedded assets and native pins, independent of native executable bytes. Keep the
LF policy in `.gitattributes` intact; equality of that cohort is not authentication
or proof of native-platform behavior. See the
[wire identity contract](../reference/core-client-protocol.md#wire-format-and-bounded-delivery).

Use the [active v2 acceptance matrix](../plans/core-protocol-v2.md#acceptance-gates)
without raising thresholds. Record the exact measured stage: PTY-to-IPC reaction
is not key-to-PTY-write latency, SDK carrier survival is not a native two-host
journey, and internal grid fps is not presented outer-terminal fps. A successful
fuzz run for one boundary does not mark every feature or native path accepted.
Historical v1 gates do not attest the current refactor. Keep the plan open until
every performance, per-feature visual and fuzz row has evidence.

Run the bounded native protocol campaigns after the ordinary and race suites:

```sh
FUZZ_SECONDS=2 GOMAXPROCS=2 bash scripts/protocol_fuzz.sh
# For a longer focused campaign:
FUZZ_SECONDS=30 FUZZ_TARGETS='FuzzCoreSessionOwnership FuzzRemoteStateTopicFences' \
  bash scripts/protocol_fuzz.sh
```

`CRUDE_TEMP_DIR` must name an evidence directory. The runner compiles instrumented
test binaries, including the nested renderer forks, checks exact target names and
runs from private working directories. Binaries, logs, caches and crash corpora
stay beneath its printed evidence path; it never writes unreviewed fuzz failures
into the checkout. Its generated short private `CRUDE_HOME` is removed on exit.
Native terminal dependencies are required, and missing targets or invalid bounds
fail closed. Linux Verify includes a two-second-per-target smoke campaign; wiring
is not proof of a hosted run. Runtime-backed feature fuzzers retain real durable
owners and are substantially slower than pure decoder targets, so report their
actual execution counts rather than treating the same wall time as equal coverage.

Run the network-impaired QUIC gate on Linux with sudo:

```sh
CRUDE_TEMP_DIR=/path/to/evidence bash scripts/protocol_netem.sh
# Profiles are per-direction delay/loss; the default covers the 50 ms RTT gate
# and a 160 ms RTT link above mosh's prediction trigger.
CRUDE_NETEM_PROFILES='25ms/1% 80ms/1%' CRUDE_TEMP_DIR=... bash scripts/protocol_netem.sh
```

It creates two private network namespaces joined by one veth pair, applies netem
to both ends, and runs the core role and the real shell-port client role as the
invoking user. No host interface, route or qdisc changes. The client measures
key → applied echo idle and under a terminal flood, a bulk download and both,
plus mosh-policy prediction. Namespaces share one kernel and CPU; this is not a
physically separate-host result.

### Startup process acceptance

Build a current binary before this Linux check:

```sh
go build -o "$CRUDE_TEMP_DIR/startup-check" .
PYTHONDONTWRITEBYTECODE=1 python3 scripts/startup_acceptance.py \
  --binary "$CRUDE_TEMP_DIR/startup-check" \
  --out "$CRUDE_TEMP_DIR/startup-process-evidence"
```

Use a new output directory for each run.
The script uses private short profiles, a private tmux socket, and a loopback catalog.
It checks first use, reminder suppression, path errors, malformed configuration, and exact-process crash recovery.
It retains evidence and stops its owned processes, including after failure.
It sends no inference request and uses no external credentials.
Its text captures do not replace sequential native visual captures.
Native Windows and distinct-host SSH checks remain separate.

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

### Live agent-wait acceptance

`app/live_agent_wait_test.go` is opt-in actual-model acceptance with
`deepseek/deepseek-v4.1-flash` for both parent and child, no fallback. It creates
a private App/core with framed clients, never attaches the user's retained core,
and retains only the explicitly selected API-key account in memory. Ordinary
tests exercise only its offline fixture/config checks.

```sh
. scripts/libghostty_env.sh
CRUDE_LIVE_AGENT_WAIT=1 CRUDE_WAIT_CONFIG=/explicit/config.json \
  CRUDE_WAIT_RESULT="$CRUDE_TEMP_DIR/wait-result.txt" \
  go test ./app -run '^TestLiveAgentWaitDirectedEventJourney$' -count=1 -v -timeout=10m
```

Optional `CRUDE_WAIT_ACCOUNT` selects an exact account. The child sends a blocker
while the parent waits, the parent replies before the child can finish, then
reads the exact final report and separately observes the terminal outcome.
Assertions check the nonterminal `directed_event`, normal exactly-once context
admission/receipts, selected model usage and an ordinary follow-up.

For native visual acceptance, precompile the app test binary and run it through
`scripts/tuishot.sh` with `CRUDE_VISUAL_WAIT=1` at desktop and constrained sizes,
sequentially. `CRUDE_WAIT_VISUAL_HOLD_SECONDS` is capped at 120; inspect captures,
the sanitized result marker and a clean test exit independently. The connected
TUI paints the same private core's event log; this is not distinct-host or
cross-process-core acceptance.

### Live diagnostic acceptance

`app/live_diagnostics_test.go`, `app/live_diagnostics_visual_test.go` and
`app/live_diagnostics_remote_test.go` exercise production diagnostics through a
distinct server process, multiple connected ports and the actual composer.
Ordinary tests need no provider account; `TestDiagnosticsHarnessDistinctCoreSmoke`
covers exact process provenance, bounded continuation/range recovery and real
shell handlers. Inference is opt-in, never part of the ordinary suite:

```sh
. scripts/libghostty_env.sh
CRUDE_LIVE_DIAGNOSTICS=1 CRUDE_DIAGNOSTICS_CONFIG=/explicit/account-config.json \
  CRUDE_DIAGNOSTICS_PHASE=all go test ./app \
  -run '^TestLiveDiagnosticsActualModel$' -v -count=1 -timeout=26m
```

The requested model is exactly `deepseek/deepseek-v4.1-flash`, with no fallback.
The private core restricts tools, disables memory/delegation/full-turn dumps and
retains only the selected connection in a disposable 0600 configuration. `basic`
(default) tests discovery, both process roles, link/runtime/workspace evidence and
ordinary file work without diagnostic polling. `dense` adds more than 200 KiB of
real pasted context, immutable continuation and an intentional range error with
model recovery. `restart` adds completed-turn core restart/passive resume and a
fresh-PID followup; `all` combines them. `CRUDE_DIAGNOSTICS_SOAK=1` additionally
exercises eight real followups, Stop during streaming, normal work after Stop,
actual context compaction using the same model, accepted transcript preservation
and post-compaction diagnostics. This is not proof of an uninterrupted active turn
across process death or arbitrary-length conversation reliability.

`CRUDE_VISUAL_DIAGNOSTICS=1` uses `tui.Start` in a dedicated capture terminal.
Precompile the app test binary, then use `scripts/tuishot.sh` sequentially at
desktop and constrained sizes with its `CRUDE_BIN`/`CRUDE_ARGS` interface. A bounded
`CRUDE_DIAGNOSTICS_VISUAL_HOLD_SECONDS` (at most 120) permits final screenshot
inspection; the independent `CRUDE_DIAGNOSTICS_RESULT` marker and clean exit must
also be checked. The harness waits for same-shell `composer.inspect` readiness
before exactly one Enter; a fixed delay is not paste persistence evidence.

Distinct-host runs require explicit `CRUDE_DIAGNOSTICS_REMOTE`,
`CRUDE_DIAGNOSTICS_REMOTE_USER`, `CRUDE_DIAGNOSTICS_REMOTE_IDENTITY`,
`CRUDE_DIAGNOSTICS_REMOTE_HOME`, `CRUDE_DIAGNOSTICS_REMOTE_IMAGE`,
`CRUDE_DIAGNOSTICS_REMOTE_TEST_IMAGE`, `CRUDE_DIAGNOSTICS_REMOTE_QUIC` and
`CRUDE_DIAGNOSTICS_REMOTE_TLS`. Provision a disposable **non-root** SSH user/home
whose basename begins `crdiag`, marker `.diagnostics-test-owned` containing
`crude-diagnostics-2026-10-26`, digest-verified matching production/test images and
0600 `input.json` first. The harness does not install keys, bypass host verification,
alter firewalls or attach a real user profile. It checks the SSH-observed home,
exact process image/PID and server mode; its parent must remove the temporary user,
key and credential copies after exact-process shutdown. Network access is explicit
test setup, not an agent grant.

`TestLiveDiagnosticsRemoteCarriers` verifies SSH/QUIC/TLS switching and retained
owner provenance without inference. `TestLiveDiagnosticsRemoteInspectExisting`
uses offered commands to diagnose an already-running harness without restarting
it. Remote transcript assertions use a bounded compressed SQLite backup (including
live WAL state), not a main-file copy or a product diagnostic data export.

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

`TERMINAL=alacritty` (default), `TERMINAL=kitty` and `TERMINAL=tilix` select the
actual native emulator through this same workflow, not a changed `TERM` label.
Each launch is an independent borderless capture process; Tilix uses
`--new-process`, never an existing user terminal. The window is selected by the
exact capture process and visible geometry, not the latest global class match.
Keyboard steps activate and fence that window before XTEST input: GTK/VTE ignores
XSendEvent-based `--window` keystrokes. Verify the reported geometry
and actual application rows before relying on pointer cell coordinates. A final
`quit:F9` (or the journey's real exit chord) waits for a verified exit code zero
without taking a post-exit screenshot; `key:F9` still requires the product to
remain running and therefore deliberately rejects an unexpected exit.

Autonomous terminal journeys may finish with `await-exit` instead of an exit
chord. `record:8` records eight seconds of this exact dedicated window at a 60 Hz
sampling rate into lossless `native-window.mkv` (`FFMPEG_BIN` selects ffmpeg).
This is pixel evidence, not automatically a presented-FPS pass: inspect the
actual timestamps and distinct terminal-content frames, excluding chrome/blink
and idle output. Recording and cleanup own only that exact capture process.

For video acceptance, put an independent frame number in several source-image
bands and decode their rendered pixels. Inspect the recording's actual frame PTS,
and use `ffmpeg -fps_mode passthrough` when exporting raw samples: default output
rate conversion can duplicate frames to a nominal 60 Hz even when the recording
contains fewer frames. Count distinct agreeing tags per actual timestamp window;
exclude and report mixed/ambiguous tags. This establishes observed frame identities
at the sampled bands, not atomicity or independent equality of every source pixel.

The opt-in `scripts/terminal_native_gate.py` uses a separate core process, a
real shell App/Program and a 200×50 PTY. Its strace correlation measures key
enqueue to a successful PTY-master write; shell runtime metrics exclude the
core's GC. In-kernel seccomp filtering traces only writes, avoiding ptrace stops
on unrelated reads/futexes; write tracing overhead remains included and every
one-byte PTY-master syscall must still match one admitted key. Content-bearing
writer flushes are a necessary throughput check, not
proof that an emulator displayed them. `--no-strace` is diagnostic only and
deliberately leaves latency unverified with a nonzero result. Use the repository
capture workflow and `--native-output` for outer-emulator evidence; record each
boundary separately and do not equate a typed `mpv` key with application response.

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
