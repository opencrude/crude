# Phase 15 Stabilization Checklist

Status: **IN PROGRESS** (continued 2026-07-29)

Canonical plan: [`TUI_COMPONENT_SYSTEM_PLAN.md`](../../TUI_COMPONENT_SYSTEM_PLAN.md) § Phase 15

Phase 15 runs in parallel with Phase 13.7 shell work. This checklist tracks every
deliverable and exit criterion without watering down enforcement targets.

## Exit criteria (must all pass)

| Criterion | Status | Notes |
|-----------|--------|-------|
| `go build ./...` | ✅ passing | Run in CI `.github/workflows/tui.yml` |
| `go test ./...` | ✅ passing | Full suite green; `crude/jsonrepair TestConcurrentRepairStress` is a known flake (passes in isolation) |
| TUI race suites | ✅ **expanded** | `make test-race-tui` — includes `./app/`, `./tui/viewmodel/` |
| Static design checks, **no allowlist** | 🚧 shrunk | Hex allowlist = `tui/theme/` + `tui/diffview/` only; `raw-ansi-sgr` lists only low-level render paths (`tui/editor/render.go` + foundation) — chat markdown/thinking is a render-time ANSI→PaintLine bridge (`BridgeAnsiLine`), not a manifest bypass |
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
| Checked-in golden cell dumps (optional hash files) | ❌ open | Current tests assert properties + labels, not byte-identical buffers |

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
| CI bench regression gate | ❌ open | No automated threshold check in workflow |
| Product-screen benches (chat stream, editor paint) | ✅ **new** | `tui/screens/shelleditor/paint_bench_test.go` (`BenchmarkEditorPaint_80x24/120x40/LargeFile`, `BenchmarkEditorScrollRepaint_120x40`), `tui/screens/shellchat/stream_bench_test.go` (`BenchmarkChatStreamPaint_80x24/120x40`); recorded in `PERFORMANCE_BASELINES.md`; supported the `contentDirty` cache removal (editor repaint ~180-250µs at 120x40) |

### 6. Root lint + CI workflow

| Asset | Status | Paths |
|-------|--------|-------|
| Design audit script | ✅ done | `scripts/tui_design_audit.sh` |
| CI workflow (build + test + audit + race) | ✅ done | `.github/workflows/tui.yml` |
| Root `golangci-lint` config | ✅ **new** | `.golangci.yml` — Phase 15 scope in CI |
| CI lint step | ✅ **new** | `.github/workflows/tui.yml` — scoped packages |
| `go vet` / full-repo staticcheck | ❌ open | `errcheck`, `unused`, `gosimple` disabled until repo clean |
| Fuzz corpus in CI (`-fuzztime`) | ❌ open | Optional smoke; full fuzz is local |

**CI lint scope:** `./app/... ./tui/cell/... ./tui/surface/... ./tui/compositor/... ./tui/ui/... ./tui/screens/... ./workspace/...`

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
| `hardcoded-lipgloss-hex` | `tui/theme/`, `tui/diffview/` | Shrink as consumers migrate |
| `raw-ansi-sgr` | low-level render paths | Shrink feature ANSI |
| `uniseg-import` | `[]` | Keep forbidden |
| `runewidth-import` | `[]` | Keep forbidden |

Manifest: `docs/tui/bypass_manifest.json`

---

## This pass additions

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

1. Byte-identical golden cell dumps (optional hash files) for regression sensitivity
2. Enable `errcheck` / `unused` / `gosimple` repo-wide after cleanup (~147 issues today)
3. Empty all `allowed_paths` allowlists (`hardcoded-lipgloss-hex` still has 2 paths: `tui/theme/`, `tui/diffview/`)
4. CI bench regression gate (optional `-benchtime` smoke)
5. Layout constraint solver fuzz (blocked until Flex/Grid lands)
6. **Closed:** product-screen allocation benchmarks ✅ (2026-07-31) — editor + chat stream benches recorded; `contentDirty` cache removed on their evidence

### Genuinely untestable / manual-only surfaces

| Surface | Reason |
|---------|--------|
| Real workspace/session delete persistence (`d` key → DB/filesystem) | Destructive side effects — Screen/Host contract covers in-memory `DeleteMsg` + row removal; viewmodel persistence path needs isolated temp HOME |
| Bracketed paste end-to-end (terminal emulator) | Covered by `paste_contract_test.go` at control layer; full PTY requires manual/CI terminal job |
| Live agent streaming token cadence | Timing-dependent; scroll stick verified via `syncAgentMessages(..., false)` + render, not network stream |
