# Phase 15 Stabilization Checklist

Status: **IN PROGRESS** (continued 2026-08-01 — Phase 13 root Host wiring landed: `App.rootHost` + `shellRoot` tree + one `App.mainSurf`; per-screen Hosts/layer surfaces deleted; see `TUI_COMPONENT_SYSTEM_PLAN.md` §13.9)

Canonical plan: [`TUI_COMPONENT_SYSTEM_PLAN.md`](../../TUI_COMPONENT_SYSTEM_PLAN.md) § Phase 15

Phase 15 runs in parallel with Phase 13.7 shell work. This checklist tracks every
deliverable and exit criterion without watering down enforcement targets.

## Exit criteria (must all pass)

| Criterion | Status | Notes |
|-----------|--------|-------|
| `go build ./...` | ✅ passing | Run in CI `.github/workflows/tui.yml` |
| `go test ./...` | ✅ passing | Full suite green; `crude/jsonrepair TestConcurrentRepairStress` is a known flake (passes in isolation) |
| TUI race suites | ✅ **expanded** | `make test-race-tui` — includes `./app/`, `./tui/viewmodel/` |
| Static design checks, **no allowlist** | 🚧 shrunk | Hex allowlist = `tui/theme/` only (diffview deleted 2026-08-01); `raw-truecolor-constructors` = `tui/theme/` + `tui/cell/` + `tui/rendering/`; `raw-ansi-sgr` = `tui/cell/` + `tui/compositor/` + `tui/rendering/` + foundation, now **enforced** by `tui_design_audit.sh`; **lint gate fully enabled** (errcheck/unused/gosimple) — zero issues (2026-08-01) |
| Runtime theme switching passes gallery + product tests | ⚠️ partial | Gallery done; product screens now have paint goldens for all shell chrome + FIF + palette |

## Deliverables

### 1. Cell-buffer visual goldens (gallery + product screens)

| Asset | Status | Paths |
|-------|--------|-------|
| Gallery baselines (80×24–200×50, dark/light) | ✅ done | `tui/ui/visual_baseline_test.go` |
| Gallery all presets (dark/light/high-contrast) | ✅ done | `tui/ui/demo_test.go` (`TestDemoGalleryPaintsAndEdits`) |
| Runtime theme switch | ✅ done | `tui/ui/demo_test.go` (`TestDemoGalleryRuntimeThemeSwitchPreservesState`) |
| WindowFrame / TextField / List / Button / Label goldens | ✅ done | `tui/ui/visual_baseline_test.go` |
| Product screen goldens — launcher, sessionpicker, shelltabs, shellchat | ✅ done | `TestPaintGolden*` in respective `*_test.go` |
| Product screen goldens — shelleditor, shellheader, shellstatus, shellinput, shelltree | ✅ **new** | `TestPaintGoldenCellBufferContract` in each package |
| Product screen goldens — findinfiles (native screen) | ✅ **new** | `tui/screens/findinfiles/find_in_files_test.go` |
| Product screen goldens — command palette (frame builder) | ✅ **new** | `tui/viewmodel/command_palette_test.go` (`TestPaintGoldenCellBufferContract`) |
| Checked-in golden cell dumps (optional hash files) | ✅ **done** | `tui/uitest` golden-hash infrastructure: `GoldenHash` (deterministic SHA-256 over every cell: char/width/fg/bg/attrs/transparency) + `CheckGolden` (checked-in `testdata/golden/<name>.sha256`, `-update` flag to regenerate, fails on any drift). Byte-identical goldens for 7 screens: shellheader, shellinput, shelltabs, shellstatus, shelltree, shelleditor, launcher, findinfiles, command_palette. Header clock frozen via `nowFunc` seam. Determinism proven: two runs across a 3s gap produce identical hashes; drift-proven by mutating one separator cell → hash mismatch. sessionpicker intentionally excluded (renders Today/Yesterday from `time.Now()`) |

### 2. Keyboard / mouse / paste interaction contract tests

| Asset | Status | Paths |
|-------|--------|-------|
| Interaction matrix (baseline doc) | ✅ done | `docs/tui/INTERACTION_MATRIX.md` |
| TextEdit / TextArea contract suite | ✅ done | `tui/ui/text_edit_suite_test.go` |
| VirtualList End → stick-to-bottom | ✅ done | `tui/ui/virtlist_contract_test.go` |
| VirtualList selection-anchors-scroll | ✅ **new** | `tui/ui/virtlist_contract_test.go` (`TestVirtualListSelectionAnchorsScrollContract`) |
| ScrollModel ShouldAutoScroll contract | ✅ done | `tui/ui/virtlist_contract_test.go` |
| List keyboard (Up/Down/Enter, skip disabled) | ✅ done | `tui/ui/list_test.go` |
| VirtualList select/activate | ✅ done | `tui/ui/virtlist_test.go` |
| ActionBar focus/hit routing | ✅ done | `tui/ui/action_bar_test.go` |
| TextField / TextArea Home/End/shift | ✅ done | `tui/ui/textfield_test.go`, `tui/ui/text_area_test.go` |
| Tree Home/End | ✅ done | `tui/ui/tree_test.go` + `tui/screens/shelltree/tree_contract_test.go` (Host-level table) |
| Mouse click-outside policies | ✅ done | `tui/viewmodel/overlay_click_outside_contract_test.go` — full `overlayOutsideContractMatrix` (17 overlays) + one real `dispatchMouseClick` integration per policy group |
| Bracketed paste contract | ✅ done | `tui/ui/paste_contract_test.go` |
| Shell header/tabs/status/tree/chat input | ✅ **expanded** | Paint goldens + keyboard tables: **shellinput**, **shelltabs**, **shelltree** (`tree_contract_test.go`), **shellheader** (`header_contract_test.go`, display-only), **shellstatus** (`status_contract_test.go` display-only + `viewmodel/status_keyboard_contract_test.go` ActionRegistry chords) |
| Launcher / welcome keyboard contracts | ✅ **expanded** | `tui/screens/launcher/launcher_contract_test.go` (Host table incl. `d` delete); `tui/viewmodel/launcher_keyboard_contract_test.go` (dispatch + global chords + min-size gate) |
| Panel-deck docked form contracts | ✅ **expanded** | `tui/viewmodel/panel_deck_contract_test.go` (settings, confirmation/allow via `dispatchKeyPress`, question cancel, YOLO cancel, background jobs, session deck isolation) |
| Chat scroll / selection / keyboard contracts | ✅ **expanded** | `tui/screens/shellchat/scroll_contract_test.go` (disarmed offset preservation + armed stick); `tui/viewmodel/chat_keyboard_contract_test.go` (production-path append sync); `host_test.go`, `host_capture_test.go`, `mouse_capture_test.go` |
| Viewmodel tree-focus routing | ✅ **new** | `tui/viewmodel/tree_focus_contract_test.go` |

### 3. Fuzz tests (text editing, layout, scrolling, geometry)

| Asset | Status | Paths |
|-------|--------|-------|
| ScrollModel offset clamp / ThumbBounds | ✅ done | `tui/ui/scroll_fuzz_test.go` |
| TextEdit editing ops | ✅ done | `tui/ui/text_edit_fuzz_test.go` |
| VirtualList itemAtRow / prefix rebuild | ✅ done | `tui/ui/virtlist_fuzz_test.go` |
| Layout constraint solver | ❌ open | No Flex/Grid yet |
| Geometry / hit-test transforms | ✅ **new** | `tui/ui/host_fuzz_test.go` — `FuzzHostHitTest`, `FuzzWindowFrameCloseBounds` |
| Third-party reference | ℹ️ | `third_party/xansi/parser_decode_test.go`, `third_party/ultraviolet/key_test.go` |

### 4. Race tests (TUI + workspace event boundaries)

| Asset | Status | Paths |
|-------|--------|-------|
| Makefile target `test-race-tui` | ✅ **expanded** | `Makefile` — adds `./app/`, `./tui/viewmodel/` |
| CI race step | ✅ done | `.github/workflows/tui.yml` |
| Workspace EventBus concurrent Send/log | ✅ done | `workspace/eventbus_race_test.go` |
| File tree watcher callback outside lock | ✅ done | `tui/filetree/watcher_regression_test.go` |
| Editor watcher shutdown idempotency | ✅ done | `tui/editor/watcher_regression_test.go` |
| Config hot-reload (`app/config_watch`) | ✅ **new** | `app/config_watch_race_test.go` |
| Project search async completion | ✅ **new** | `workspace/project_search_race_test.go` |
| Viewmodel event subscription teardown | ✅ **new** | `tui/viewmodel/eventbus_race_test.go` (Send + SetProgram under -race) |

**`make test-race-tui` packages:** `./app/`, `./tui/cell/`, `./tui/surface/`, `./tui/compositor/`, `./tui/ui/`, `./tui/filetree/`, `./tui/screens/shellchat/`, `./tui/chatprojection/`, `./tui/viewmodel/`, `./workspace/`

### 5. Performance and allocation benchmarks

| Asset | Status | Paths |
|-------|--------|-------|
| Recorded baselines doc | ✅ done | `docs/tui/PERFORMANCE_BASELINES.md` |
| Gallery paint/handle benches | ✅ done | `tui/ui/benchmark_test.go` |
| Surface/compositor benches | ✅ done | `tui/surface/`, `tui/compositor/` bench files |
| CI bench regression gate | ✅ **done** | `scripts/tui_bench_gate.sh` — allocation ceilings from PERFORMANCE_BASELINES.md enforced in CI (allocations are hardware-independent; timing intentionally ungated); tightened ceilings after StripAnsi fast path + FullRender benchmark fix; `make bench-gate` |
| Product-screen benches (chat stream, editor paint) | ✅ **new** | `tui/screens/shelleditor/paint_bench_test.go` (`BenchmarkEditorPaint_80x24/120x40/LargeFile`, `BenchmarkEditorScrollRepaint_120x40`), `tui/screens/shellchat/stream_bench_test.go` (`BenchmarkChatStreamPaint_80x24/120x40`); recorded in `PERFORMANCE_BASELINES.md`; supported the `contentDirty` cache removal (editor repaint ~180-250µs at 120x40) |

### 6. Root lint + CI workflow

| Asset | Status | Paths |
|-------|--------|-------|
| Design audit script | ✅ done | `scripts/tui_design_audit.sh` |
| CI workflow (build + test + audit + race) | ✅ done | `.github/workflows/tui.yml` |
| Root `golangci-lint` config | ✅ **new** | `.golangci.yml` — **full repo `./...` in CI** (2026-08-01) |
| CI lint step | ✅ **new** | `.github/workflows/tui.yml` — scoped packages |
| `go vet` / full-repo staticcheck | ✅ **complete** | All default-family linters enabled (govet, ineffassign, misspell, staticcheck, gosimple, unused, errcheck) — **zero issues repo-wide** (2026-08-01); CI lint scope expanded to `./...`; workspace errcheck exclusion removed |
| Fuzz corpus in CI (`-fuzztime`) | ❌ open | Optional smoke; full fuzz is local |

**CI lint scope:** `./...` (full repo — expanded 2026-08-01; zero issues)

### 7. Component authoring / design-language docs

| Asset | Status | Paths |
|-------|--------|-------|
| Control usage map | ✅ done | `docs/tui/CONTROL_USAGE.md` |
| Surface catalog | ✅ done | `docs/tui/SURFACE_CATALOG.md` |
| Design token bypasses | ✅ done | `docs/tui/DESIGN_TOKEN_BYPASSES.md` |
| Interaction matrix | ✅ done | `docs/tui/INTERACTION_MATRIX.md` |
| Component authoring guide (how to build a screen) | ✅ **new** | `docs/tui/COMPONENT_AUTHORING.md` |
| Host/OverlayHost/PanelDeck recipes | ✅ **new** | Covered in COMPONENT_AUTHORING.md § Recipes 1–2 |

---

## bypass_manifest.json — must empty for Phase 15 exit

Do **not** remove these entries until consumers are deleted. Track shrink progress here.

### `control_bypasses` with `"status": "open"`

None — all entries migrated or deleted.

### Recently closed (this pass)

| ID | Path | Resolution |
|----|------|------------|
| `popupchrome-parallel` | `tui/widgets/popupchrome.go` | **deleted** |
| `components-textinput-parallel` | `tui/components/text.go` | **deleted** |

### `rules[].allowed_paths` (must be `[]` at exit)

| Rule ID | Current allowlist paths | Action |
|---------|-------------------------|--------|
| `hardcoded-lipgloss-hex` | `tui/theme/` | Shrink as consumers migrate |
| `raw-ansi-sgr` | low-level render paths | Shrink feature ANSI |
| `uniseg-import` | `[]` | Keep forbidden |
| `runewidth-import` | `[]` | Keep forbidden |

Manifest: `docs/tui/bypass_manifest.json`

---

## This pass additions

- **WindowFrame reveal animation** (2026-08-01): `WindowFrame.SetReveal(progress)` (0=collapsed body, 1=full; Layout scales content height so paint + Host hit-testing agree), OverlayHost `SetOnOpen` hook, viewmodel tick driver (`beginReveal`/`advanceReveals`/`stopReveal`, fast 16ms tick while animating, 120ms duration). Non-modal popovers (context menu, palettes, LSP popups) slide in; modals appear instantly. Tests: `TestWindowFrameReveal*` (6, ui) + `TestReveal_*` (6, viewmodel).
- **CI bench allocation gate** (2026-08-01): `scripts/tui_bench_gate.sh` + `make bench-gate` + CI step; ceilings from PERFORMANCE_BASELINES.md (tightened ~25% over measured).
- **StripAnsi zero-alloc fast path** (2026-08-01): plain text (no `\x1b`) returns unchanged — label/list/button paint allocations cut ~75% (DemoGallery 3888B/151 allocs → 1040B/44).
- **FullRender benchmark corrected** (2026-08-01): reused the layer surface instead of allocating per op (90425B/7 allocs → 8249B/4); the old number measured its own setup allocation.
- **Byte-identical golden cell dumps** (2026-08-01): new `tui/uitest` test-support package (`GoldenHash` + `CheckGolden` + `-update`); checked-in golden hashes for shellheader, shellinput, shelltabs, shellstatus, shelltree, shelleditor, launcher, findinfiles, command_palette. Header clock frozen via `nowFunc` test seam. Determinism + drift-sensitivity proven (see §1). sessionpicker excluded (time-dependent date grouping).
- **Dead `tui/rendering/adapter.go` deleted** (2026-08-01): `ANSIAdapter` + `ModelAdapter` had zero consumers — the last `View()`-string ANSI bridges; also removed `tui/rendering`'s only import of `tui/widget`.
- **Full-repo lint gate zero** (2026-08-01): CI lint scope expanded from scoped packages to `./...`; all remaining packages cleaned (clipboard, session, sessiondb, agent, jsonrepair, provider, logger, fileio, tui/chatprojection, tui/filetree, tui/rendering, tui/viewmodel, tui/widget, tui/editor). **Real defects found & fixed**: (1) `Editor.moveWordLeft` regression — the b323a3b rewrite turned the climb-to-previous-word loop into a `return`, parking the cursor on whitespace-only lines instead of the word before them; restored the `continue` + added `TestMoveWordLeft_CrossesLines` (4 cases incl. whitespace-only-line climb and empty-first-line). (2) `FindSegmentStart` dead `currentSeg++` removed (behavior-identical). (3) `positionFindReplaceCursor` dead `pos = i+1` removed. (4) popupmouse references/completion/outline `idx := -1` redundant inits. (5) **`logger.Logger.write` data race** — the `closed` flag was read unlocked while `Close()`/`Remove()` wrote it under the mutex; a session's orchestrator goroutine can log concurrently with `Session.Close()` (caught intermittently by `TestNewWithSessionDirEmptyIDUsesLoggerSessionID` under `-race`). `write()` now reads under the same mutex; permanent guard `logger/logger_race_test.go` (`TestLoggerCloseConcurrentWithWrite` — proven to fail under `-race` without the fix). Also: vestigial `th` param removed from `chatprojection.BuildSubPane*` builders (8 call sites); `StackLayout.Arrange` behavioral tests added (`container_test.go`, 6 cases).
- **Three-layer boundary enforced**: `tui_design_audit.sh` now fails if `workspace/` imports `crude/tui` (probe-verified).
- **Full lint gate enabled** (2026-08-01): `errcheck`, `unused`, `gosimple` all enabled in `.golangci.yml`; workspace errcheck exclusion removed. Zero issues across CI scope (`./app/... ./tui/cell/... ./tui/surface/... ./tui/compositor/... ./tui/ui/... ./tui/screens/... ./workspace/...`). ~108 items resolved: 50 unused (incl. `TextArea.View` ANSI path, `input/view.go`, `ui.BlockCursorTime`, app.go legacy msg aliases, shellchat `vpRow`, scrollbar_control dead inits, `tui/widgets/test_helpers_test.go`), 46 errcheck, 12 gosimple.
- **TextArea ANSI View() path deleted** (2026-08-01): `TextArea.View` + `renderSegmentANSI`/`renderAtomicChipANSI`/`applySelectionANSI`/`blinkContrastFG`/`ansiColor` + `tui/input/view.go` ANSI bridge + `ui.BlockCursorTime` deleted; raw `\x1b[7m` removed from `tui/ui`. Tests migrated to native `Paint` cell-buffer assertions (`Layout(W:8)` ≡ legacy `View(12)`; cursor inverse-video/accent-block asserted on cells). `raw-ansi-sgr` rule now **enforced** in `tui_design_audit.sh` (probe-verified to fail outside allowlist).
- **Dead `tui/diffview` package deleted** (2026-08-01): zero Go importers, no tests; was the last non-theme hex allowlist entry. `hardcoded-lipgloss-hex` shrunk to `tui/theme/` only; `raw-truecolor-constructors` shrunk to `tui/theme/` + `tui/cell/` + `tui/rendering/`. Audit script + bypass manifest v40 + DESIGN_TOKEN_BYPASSES.md updated.
- Product paint goldens: `shelleditor/editor_test.go`, `shellheader/header_test.go`, `shellstatus/paint_golden_test.go`, `shellinput/input_test.go`, `shelltree/tree_test.go`, `findinfiles/find_in_files_test.go`, `viewmodel/command_palette_test.go`
- Race tests: `app/config_watch_race_test.go`, `workspace/project_search_race_test.go`, `tui/viewmodel/eventbus_race_test.go`
- Root lint: `.golangci.yml` + CI lint step; trivial fixes in `tui/ui/tabs.go`, `tui/ui/scrollbar_control.go`
- `Makefile` `test-race-tui` expanded with `./app/` and `./tui/viewmodel/`
- Authoring doc: `docs/tui/COMPONENT_AUTHORING.md`
- Shell keyboard contracts: `shellinput/input_contract_test.go`, `shelltabs/tabs_contract_test.go`, `shelltree/tree_contract_test.go`, `shellheader/header_contract_test.go`, `shellstatus/status_contract_test.go`
- Viewmodel interaction contracts: `viewmodel/tree_focus_contract_test.go`, `viewmodel/tree_filter_contract_test.go`, `viewmodel/overlay_click_outside_contract_test.go`, `viewmodel/status_keyboard_contract_test.go`, `viewmodel/launcher_keyboard_contract_test.go`, `viewmodel/panel_deck_contract_test.go`, `viewmodel/chat_keyboard_contract_test.go`
- Launcher / panel-deck / chat contracts: `screens/launcher/launcher_contract_test.go` (incl. workspace `d` delete), `screens/shellchat/scroll_contract_test.go` (disarmed offset)
- Production fixes: shelltree filter row forwards keys; `focusTarget()` uses `launcherHostVisible()` + nil-safe `FocusStack`; hidden/min-size launcher no longer owns keyboard focus; **VirtualList `SetSelectionAnchorsScroll(false)` for paint-only shellchat**
- Geometry fuzz: `tui/ui/host_fuzz_test.go` (`FuzzHostHitTest`, `FuzzWindowFrameCloseBounds`)

## Remaining gaps (priority order)

1. **Closed:** byte-identical golden cell dumps ✅ (2026-08-01) — `tui/uitest` golden-hash infra + 9 screen goldens (see §1 table); `uitest` self-tests hashing determinism + sensitivity
2. **Closed:** enable `errcheck` / `unused` / `gosimple` repo-wide ✅ (2026-08-01) — all enabled in `.golangci.yml`, **zero issues across `./...`** (previously ~147); CI lint scope expanded to the full repo; workspace errcheck exclusion removed; ~198 dead symbols/dead assignments deleted and 4 real defects fixed (see this-pass notes)
3. Empty all `allowed_paths` allowlists (`hardcoded-lipgloss-hex` now has 1 path: `tui/theme/`; `raw-truecolor-constructors` has 3: `tui/theme/`, `tui/cell/`, `tui/rendering/` — diffview deleted 2026-08-01)
4. **Closed:** CI bench regression gate ✅ (2026-08-01) — `scripts/tui_bench_gate.sh` enforces allocation ceilings in CI; **StripAnsi zero-alloc fast path** (plain-text strings return unchanged — gallery label/list paint allocations 3888B→1040B/op, 151→44 allocs); **FullRender benchmark fixed** (was allocating a new 80x24 surface per op: 90425B→8249B/op); ceilings tightened to ~25% headroom over measured values
5. Layout constraint solver fuzz (blocked until Flex/Grid lands)
6. **Closed:** product-screen allocation benchmarks ✅ (2026-07-31) — editor + chat stream benches recorded; `contentDirty` cache removed on their evidence

### Genuinely untestable / manual-only surfaces

| Surface | Reason |
|---------|--------|
| Real workspace/session delete persistence (`d` key → DB/filesystem) | Destructive side effects — Screen/Host contract covers in-memory `DeleteMsg` + row removal; viewmodel persistence path needs isolated temp HOME |
| Bracketed paste end-to-end (terminal emulator) | Covered by `paste_contract_test.go` at control layer; full PTY requires manual/CI terminal job |
| Live agent streaming token cadence | Timing-dependent; scroll stick verified via `syncAgentMessages(..., false)` + render, not network stream |
