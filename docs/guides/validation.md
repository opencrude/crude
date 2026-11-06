# Validation and Visual Capture

> Status: Current guide — build/test/lint/docs gates, test isolation and the Linux visual capture workflow.

A change is accepted on evidence, not on a narrow passing test. Run the gates
that cover what you touched, then the repository gates when the change is
complete. Report blocked or aborted gates explicitly; never present them as
passing.

## Gates

Command-only CI (`.github/workflows/tui.yml`, reusable
`.github/workflows/verify.yml`) runs
Linux workflow/installer checks, the mandatory shadcn website lint/build and
acceptance test, full-repo lint, design/docs/performance gates, build, full
tests and full race tests. A native Windows job builds/tests with cgo and
exercises both PowerShell 5.1 and 7 installers. Release jobs reuse these checks;
see [releases](releases.md). Native CI configuration is not proof that the
corresponding run has passed. Push, pull-request, tag and schedule triggers are
absent; a workflow starts only from an explicit dispatch. Local checks and
manual publication do not require a hosted workflow. The `instant` release mode
skips this chain by design and is not acceptance evidence for the skipped
gates. See the [release modes](releases.md#release-modes) and the
[website guide](website.md).
Locally:

```bash
. scripts/libghostty_env.sh          # once per shell; libghostty-vt for terminals
go run ./internal/wiregen -check      # nonmutating source-cohort freshness
go build ./...
go test ./<affected packages>/...     # focused first
make test-syntax                     # includes the nested native binding suite
make test-lsp                        # pure data/transport and nested LSP ownership
go test ./... -count=1 -timeout=180s  # repository suite
golangci-lint run ./...               # zero issues, whole repo
make design-audit                     # layer boundary, raw SGR, hard-coded hex
make docs-check                       # documentation acceptance gate
make bench-gate                       # allocation ceilings and work budgets
make test-race-tui                    # race-clean full repo (go test -race ./...)
make site-check                       # shadcn lint, Vite build and site acceptance
```

CI uses `python scripts/test_race.py` for the full race suite. It runs every
package; the large session/viewmodel packages are exhaustively partitioned by
discovered top-level test/example/fuzz name, each exactly once. The multi-megabyte
upload recovery stress case runs separately with the same five-minute limit.
No workload size, individual assertion or timeout is relaxed to fit the sum of
hundreds of cases into one package-wide budget.

### Windows race checks with Zig

Use a separate PowerShell session for race checks, after bootstrapping the
Windows libghostty dependency. For example:

```powershell
. .\scripts\libghostty_env.ps1
. .\scripts\race_env.ps1
go test -race -count=1 ./plan/ ./types/ ./terminalvt/
```

If the local PowerShell execution policy blocks scripts, start that session with
`powershell -ExecutionPolicy Bypass` under the applicable local policy. The race
setup creates Zig's missing synchronization import archive under `%LOCALAPPDATA%`
and adds race linker flags to that session's `GOFLAGS`. It sets a low image base
and disables dynamic-base relocation: Go's Windows race runtime needs a
compatible shadow address range, and Zig does not accept Go's GNU spelling for
that switch. The PE high-entropy bit may remain set, but dynamic-base is off.
The setup rejects an existing `GOFLAGS` linker override. Close the session before
normal builds; do not use these race-specific linker flags for production binaries.
For the whole repository in the same session, limit concurrent package binaries
so timing-sensitive tests are not starved by the run itself:
`go test -race -count=1 -p 2 -timeout 1800s ./...` (about 30 minutes on a
developer laptop).

### When a test fails only under `-race`

The race detector slows every goroutine several times, so it exposes ordering
assumptions that a fast run hides. Classify each failure before changing it:

- **A `WARNING: DATA RACE` report** is a product or test defect. Fix the
  unsynchronized access; never suppress the report.
- **A wrong readiness signal** (the test reads state after a proxy counter or an
  acknowledgement that precedes the state it asserts) is fixed by waiting on the
  real outcome. For example, a governor lease is counted before its client is
  constructed, so wait for the client to run, not for the counter.
- **A lost or refused user action** (a keystroke, Open or save that fails only
  because Crude's own background work moved under it) is a product bug. Fix
  the owner; do not make the test wait longer.
- **A fixed time budget** that a slow machine exceeds is replaced by waiting for
  the real completion signal with a generous safety deadline that fails loudly.
  Wall-clock performance ceilings belong in `make bench-gate`, not in tests
  that also run under `-race`.

Do not skip a test under `-race`, loosen its assertion, or add sleeps for
ordering. A failure that reproduces without `-race` is an ordinary failure, not
a race-only one.

### Gate details

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
- Native parser changes additionally run the ownership regressions in
  `third_party/go-tree-sitter/`; it is a replacement module and is not included
  by the root package glob. `make test-syntax` runs both that suite and the
  `syntax/` corpus. Use native sanitizers and the registered fuzz target for
  C-boundary work, and keep C heap/RSS evidence distinct from Go allocations.
- Language-server ownership changes also run `make test-lsp`. The replaced
  Powernap module is excluded from the root package glob. Its native tests
  cover process groups or Job Objects on the executing platform; cross-built
  tests do not attest Windows/macOS behavior. Pure-Go data/transport checks
  prevent a syntax result type from pulling the native parser into all clients.
- Checkpointed restart changes exercise the `^TestCoreRestart` and
  `^TestGoalHandover` test-name filters across `app`, `core`, `session`,
  `sessiondb` and `tui/viewmodel`, including race
  checks. Use an isolated short real `CRUDE_HOME` for native capture; never stop
  the developer's current core as a test. Keep schedule, natural checkpoint,
  predecessor settlement, successor activation and clean foreground exit evidence
  separate. See [the restart contract](../reference/core-restart.md).

### Protocol generation and v2 acceptance

After production-source edits, explicitly run `go run ./internal/wiregen` in the
canonical native cgo environment, inspect its generated diff, then run
`go run ./internal/wiregen -check`. The check is nonmutating; it does not regenerate
codecs or repair stale output. Make build/test/benchmark targets,
`scripts/build.sh` and `scripts/release.py` check freshness. An arbitrary direct
`go build` does not invoke generation or the check.

The full source cohort covers production platform/tag alternatives, local forks,
embedded assets and native pins, independent of native executable bytes. Keep the
LF and byte-preservation policy in `.gitattributes` intact; the wiregen checkout
test exercises `core.autocrlf=true` with both text and binary inputs. Equality of
that cohort is not authentication or proof of native-platform behavior. See the
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

### Streaming separator and thinking pane acceptance

`TestStreamSeparatorVisualAcceptance` (`CRUDE_VISUAL_STREAM=1`) replays the
event shape of a provider that streams late `\n` deltas between parallel tool
calls, over a long transcript. Precompile the viewmodel test binary and run it
through `scripts/tuishot.sh` with `CRUDE_BIN`/`CRUDE_ARGS`, at 160x40 and
80x24, one capture at a time. F2 streams a thought that overflows its pane: the
earlier replies must keep their wrapping. F3 and F4 send and settle three reads:
they must show as one `Read 3 files` group one blank row under the answer.

### Aeron and tab tooltip acceptance

`TestAeronAndTabTooltipVisualAcceptance` (`CRUDE_VISUAL_AERON=1`) drives the
connected shell with a fixture provider. Precompile the viewmodel test binary
and run it through `scripts/tuishot.sh` with `CRUDE_BIN`/`CRUDE_ARGS`, at
160x40 and 80x24, one capture at a time. Rest the pointer on the file tab and
press F4 (the painted tooltip shows the absolute path). F3 releases Aeron's
question; F4 checks that it is a modal over the unchanged tab and draft.
Click the modal's **Cancel**, then F4 checks that it closed. F5 shows the
Agents panel; Home, Down and Enter open Aeron's row; F4 checks its normal
agent tab. F9 exits. `CRUDE_AERON_VISUAL_RESULT` receives
`AERON_VISUAL_SUCCESS` or the failed phase and its state.

### Shared language diagnostic acceptance

The source-qualified language slice has independent tests in `insight/facts`,
`workspace/insight_diagnostics_test.go`, `core/insight_diagnostics_test.go` and
the connected three-shell UTF-16 journey in `tui/viewmodel/lsp_utf16_test.go`.
Exercise missing versus observed-empty, unversioned, limited and stale facts;
late callbacks, equal-text undo, source/producer/context replacement; actual
wire versions versus canonical revisions; and callback-before-send-receipt
ordering. Visibility/severity changes must preserve the original batch ID and
producer instance. Preserve negative-overlay evidence for those invariants.

`CRUDE_LSP_LIVE=1 go test ./tui/viewmodel -run '^TestLiveLSP(Go|Clangd)$'`
uses installed servers and real canonical unsaved edits. A skipped provider is
not acceptance. `TestLSPVisualHarnessHeadless` checks the mounted local framed
core journey at desktop and constrained sizes, without claiming native pixels.

For native Linux acceptance, compile the viewmodel test binary and select it
with `CRUDE_BIN`, `CRUDE_VISUAL_LSP=1` and
`CRUDE_ARGS='-test.run=^TestLSPVisualAcceptance$ -test.count=1 -test.timeout=180s'`.
Use `scripts/tuishot.sh` sequentially at 160x40 and 80x24. F2-F6 drive the actual
diagnostic, hover, references, definition and completion handlers against an
installed gopls. Inspect qualified Problems detail, underlines and visible
partial coverage; finish with `quit:F9`. The separate
`CRUDE_LSP_VISUAL_RESULT` must report each phase, an observed real gopls PID and
joined cleanup, and the capture must independently verify exit zero. This is an
isolated rendered fixture, not distinct-host/device, general project-context or
complete semantic-feature acceptance.

The manual code-action regression uses
`go test ./tui/viewmodel -run '^TestCodeActionVisualHeadless$'` with installed
gopls. It drives actual Alt+Enter and list-row selection, Escape and default Cancel,
fresh immutable review and explicit Apply against independently authored unused
import bytes. A supported row is required; unsupported is never a successful
mutation. The receipt, selected original safety content and exact provider join
are checked separately.

For its native captures select `CRUDE_VISUAL_CODE_ACTION=1`,
`CRUDE_ARGS='-test.run=^TestCodeActionVisualAcceptance$ -test.count=1 -test.timeout=600s'`
and `CRUDE_CODE_ACTION_VISUAL_RESULT` with the compiled viewmodel test binary.
F3 sets up only the fixture; F4 passively observes list/Escape,
fresh list/review/default-Cancel, and fresh list/review/Apply.
`CODE_ACTION_ROW_INDEX` records which literal row to reach with ordinary Down
keys. Use sequential `scripts/tuishot.sh` captures at 160x40 and 80x24, wait for
each original observer boundary and finish with `quit:F9`. No observer may
perform a scored action or patch chooser/editor state. This is local Linux
rendered evidence, not distinct-host or native Windows/macOS acceptance.

Qualified navigation uses `TestNavigationVisualHeadless` with installed real
gopls. It drives all four registry keys, a two-target implementation chooser,
Esc that closes without opening, and Enter on the second row that lands in its
code. Literal fixture positions, original provider response metadata, unchanged
source/private drafts and exact provider cleanup are independent assertions.
For sequential native captures select `CRUDE_VISUAL_NAVIGATION=1`,
`CRUDE_ARGS='-test.run=^TestNavigationVisualAcceptance$ -test.count=1 -test.timeout=600s'`
and `CRUDE_NAVIGATION_VISUAL_RESULT`. F3 prepares only the disposable fixture;
F4 is a side-effect-free observation boundary. Supply the ordinary keys listed
in `lsp_navigation_visual_test.go`, wait for each original `F4 VERIFIED` marker,
and finish with `quit:F9`. Use the same compiled image at 160x40 and 80x24,
inspect the actual native frames, and verify zero exit and joined cleanup.
Neither a headless pass nor a cross-build supplies native pixel or device evidence.

On Windows, `TestLSPInteractionNativeCapture` is an opt-in real-gopls fixture for
`scripts/tuishot.ps1`. Precompile the viewmodel test binary, select
`CRUDE_CAPTURE_LSP=references` or `actions`, and set
`CRUDE_ARGS='-test.run=^TestLSPInteractionNativeCapture$ -test.count=1 -test.timeout=90s -test.v'`.
Set `CRUDE_CAPTURE_LSP_RESULT` to a temporary result path. Ordinary keys or the
editor Code context menu perform the actions; F4 only observes the picker,
landing or edit review, and F9 exits. References require two observations
(list, chosen-code landing). Actions require three (list, exact import-removal
review, Cancel). Verify `passed=true`, the full phase count, unchanged source
and private draft, and inspect the screenshots at 160x40 and 80x24. A capture
script's exit alone does not establish that the fixture assertions passed.

Read-only inlay hints use `TestInlayVisualHeadless` with installed gopls v0.21.1
and explicit generic `gopls.hints` fixture settings. The test independently checks
three literal labels and UTF-16/source-byte anchors, the original provider
response, actual painted cells, source caret/pointer placement, view-local
Show/Hide/Refresh, original-row inspection and unchanged text/revision/private
draft/Trust. `TestConnectedInlay*` adds two framed shell attachments and a
tooltip-resolve provider, including original opaque-number spelling, native Back
cancellation, late-reader fences, default-Off and a coalesced settings epoch.
`TestInlayEveryAnswerWaitsForTheOriginalContextCursor` separately checks the
positive/null/empty/unsupported publication barrier.

For sequential native captures select `CRUDE_VISUAL_INLAY=1`,
`CRUDE_ARGS='-test.run=^TestInlayVisualAcceptance$ -test.count=1 -test.timeout=600s'`
and `CRUDE_INLAY_VISUAL_RESULT` with the compiled viewmodel test binary.
F3 prepares only the disposable fixture; F4 passively verifies the original
boundary after the ordinary keys listed in `lsp_inlay_visual_test.go`. Follow
the actually observed `RESOLVE_SUPPORTED` value, never treat unsupported tooltip
resolution as an executed read. Inspect native inline/list/detail and hidden
frames at 160x40 and 80x24, use the printed literal `POINTER_COUNT` cell for the
final click, and finish with `quit:F9`. Check all ten `F4 VERIFIED` markers,
`INLAY_VISUAL_SUCCESS`, exact provider join and clean product exit separately.
These deterministic local Linux regressions are not adaptive human-workflow,
distinct-host/device, native containment or complete-project acceptance.

Completion documentation uses `TestCompletionVisualHeadless` with the actual
installed gopls and framed core at 160x40 and 80x24. The independently authored
Alpha/Beta comments must appear only for the selected original row. Browsing,
Escape, original snippet acceptance, ordinary typing and two author Undos preserve
canonical source, private draft and disk invariants. Original initialize and RPC
traces qualify the result: gopls v0.21.1 supplies documentation eagerly and does
not advertise resolve. This is initial-documentation evidence, not a successful
real-gopls resolve. `TestConnectedCompletion*` and the framed `lspclient` and
nested Powernap tests separately exercise advertised exact-object resolve,
same-label row identity, inert documentation, refusal and joined cancellation.

For sequential native captures select `CRUDE_VISUAL_COMPLETION=1`,
`CRUDE_ARGS='-test.run=^TestCompletionVisualAcceptance$ -test.count=1 -test.timeout=600s'`
and `CRUDE_COMPLETION_VISUAL_RESULT` with the compiled viewmodel test binary.
F3 prepares only the disposable fixture; F4 passively observes each transition.
Supply the ordinary keys in `lsp_completion_visual_test.go`, inspect the actual
selected-documentation and original-insertion frames at both sizes, and finish
with `quit:F9`. Check all eight `F4 VERIFIED` markers, unchanged initial insertion
during documentation, actual capability/no-unadvertised-resolve evidence, exact
provider join and clean product exit separately. Native frame accounting includes
boot and non-sleep actions, not sleeps or a post-exit frame. These deterministic
local regressions do not close adaptive workflow, device or complete-context gates.

Diagnostic readers use `TestDiagnosticDetailsVisualHeadless` with the installed
gopls and framed core at 160x40 and 80x24. An independently authored anonymous
struct causes a long compiler message. Its original notification, canonical
source, literal message and producer/context identity are checked separately
from the mounted reader. Ordinary editor-menu input opens the source-anchored
read-only body. PageDown must paint both the original final-field token and
message tail in published cells, not merely retain them in `Value()`. Selection,
inert typing, selection-first Escape, passive hover and native wheel scrolling
must preserve source/caret/private draft, original facts and disk.

For sequential native captures select `CRUDE_VISUAL_DIAGNOSTIC_DETAILS=1`,
`CRUDE_ARGS='-test.run=^TestDiagnosticDetailsVisualAcceptance$ -test.count=1 -test.timeout=600s'`
and `CRUDE_DIAGNOSTIC_DETAILS_VISUAL_RESULT` with the compiled viewmodel test
binary. F3 prepares only the disposable fixture; F4 is a read-only observation
boundary. After Escape, move the pointer away from the diagnostic source and
back before testing hover: the context-menu click left it there, and moving to
the same coordinates is not a new native motion event. Use the ordinary input
in `diagnostic_details_visual_test.go`, wait
for each original phase/page observation, inspect the actual native frames and
finish with `quit:F9`. Require all eleven `F4 VERIFIED` phases, original-message
tail paint, `DIAGNOSTIC_DETAILS_VISUAL_SUCCESS`, independently joined provider
cleanup and clean product exit. These deterministic local Linux regressions
are not exploratory developer, device, containment or complete-context acceptance.

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
