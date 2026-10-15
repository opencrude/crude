# Shell Frame Padding — Review Findings & Open Issues

> Status: IN PROGRESS — open hardening findings for the four-side configurable
> shell frame (Settings → Appearance → Shell frame). The feature itself is
> implemented and tested; the items below are follow-ups. Written 2026-08-28.

## Shipped summary (context)

- `types.ThemeSettings` carries `PadTop/PadBottom/PadLeft/PadRight *int`
  (`pad_top` / `pad_bottom` / `pad_left` / `pad_right`, `omitempty`). Nil =
  inherit default. Default is **0 on every side** — padding is opt-in, the
  shell renders edge to edge. Accessors `PadXOrDefault()` treat nil/negative
  as default.
- `theme.Spacing.Frame` (`theme.Insets`) is the runtime frame; `ui.Insets`
  aliases `theme.Insets` (type must live in `theme` — `ui` imports `theme`).
- Settings → Appearance has four number inputs (`frame-top/bottom/left/right`)
  that are **live**: every keystroke emits `form.changed` (new `Live` flag on
  `ui.FieldState` for text fields) → `settingsui.FramePreviewMsg` →
  `App.applyFramePreview` re-insets the shell immediately. Save persists
  (`collectAppearance`) and clears the overlay; Cancel clears it (restores).
- All shell geometry derives from the frame via `App.frameInsets()` and the
  accessor family in `tui/viewmodel/layout.go` (`frameTopRow`, `shellTopRow`,
  `chromeTopRow`, `chromeBottomRows`, `termDockTopRow`). Legacy constants
  `sidebarTopRow = 3` and `terminalDockTopRow = 3` were deleted; geometry,
  mouse regions, popup clamping, and the header projection width all flow
  from the frame now.
- Frame application points: `NewApp` (`applyWorkspaceFrame`) and
  `ApplyThemePreset` (`applyThemeFrame`, which loads effective workspace
  settings) — covers startup, settings save, cancel, workspace switch, and
  config.json hot reload.
- Tests: `types/theme_frame_test.go`, `tui/settings/frame_pad_test.go`,
  `tui/viewmodel/frame_insets_test.go`.

## Review findings — fixed during review (kept for the record)

1. **Preview overlay doubled untouched sides** — `frameInsets()` originally
   *added* the live overlay onto the persisted frame. The settings form seeds
   rows with the persisted values, so the preview message already carries the
   complete frame; adding re-doubled every untouched side on each edit.
   Fix: overlay **replaces** the persisted frame (see `frameInsets` doc
   comment). Regression test:
   `TestApplyFramePreviewReplacesNotAdds`.
2. **Blanked field previewed the persisted value, not what save writes** —
   preview must be save-faithful. A cleared row now previews as 0 (what
   `collectAppearance` persists and `PadXOrDefault` resolves). Test:
   `TestApplyFramePreviewBlankFieldMeansZero`.
3. **Header right-aligned runs overflowed the right frame** —
   `configureHeaderScreen` sent `Width: m.width` while the header slot is
   `m.width - frame.Left - frame.Right` wide. Fix: projection width mirrors
   the slot. Test: `TestHeaderRightRunStaysInsidePaddedSlot` (asserts
   `cell.IsBlank()` on frame columns — frame cells are bg-painted blanks,
   not zero chars; do not assert `Char == 0` there).

## Open issues (fix when stable)

### O1. Unbounded vertical frame degenerates on small terminals (medium)

`App.frameInsets()` only clamps each side to 0..20; it does **not** bound the
frame against the terminal size. Reproduce (probe verified):

- terminal 60×24 with frame Top=Bottom=20:
  - `chromeTopRow()=22` and `chromeBottomRows()=22` — top and bottom chrome
    bands overlap;
  - header slot paints at `Y=20..21`, status slot at `Y=2..3` (status is
    ABOVE the header), tab bar and activity bar at `Y=22` (last row);
  - input/deck/term-dock rects collapse or land off-geometry.

The horizontal side has the same theoretical failure (Left+Right ≥ width).
Nothing crashes (CellBuffer writes are clipped, `surface/buffer.go`), but
chrome overlaps and mouse-region math reads inverted geometry.

Suggested fix (mirrors the existing `boundedContentGeometry` philosophy):
bound the *effective* frame per axis against the usable terminal size, e.g.

```
maxTop    := max(0, (m.height - minChromeH) / 2)   // leave room for header+status+1 chat row
maxBottom := max(0, (m.height - minChromeH) / 2)
maxLeft   := max(0, (m.width  - minContentW) / 2)  // leave room for rail + innerW floor
maxRight  := max(0, (m.width  - minContentW) / 2)
```

applied inside `frameInsets()` (keep the configured values untouched on the
theme; clamp only at geometry time). `minChromeH` ≈ 3 (2 header rows + 1 chat
row), `minContentW` ≈ activity bar width + minimum InnerW (20). Add a
stress-matrix test (small terminals × large frame values) asserting:
status Y > header Y, no empty input rect, all slot rects within the terminal.

Note: `clampFramePad`'s 0..20 band stays useful as a sanity guard; the
terminal-relative clamp is the real safety net.

### O2. `applyThemeFrame` double-reads settings and races the workspace layer (low)

`ApplyThemePreset` → `applyThemeFrame(th)` calls `m.Workspace.LoadAppSettings()`
on every preset application (settings save, cancel, hot reload, workspace
switch). Two concerns:

- `SaveAppSettings` (workspace layer) mutates `w.appDefaults` *before* the
  viewmodel applies the preset, so the re-read is usually consistent — but it
  is a second settings read per frame path that can diverge from
  `msg.AppSettings` if a config write lands in between (fsnotify hot-reload
  fires `ConfigReloadedMsg` with only `ThemePreset`; the frame comes from the
  fresh disk read, which is the desired behavior — just document it).
- Consider passing the frame through `ThemePreviewMsg`/`SaveMsg` payloads
  instead of re-reading, or caching the effective frame on the App at save
  time. Keep `LoadAppSettings` as the fallback for hot reload.

### O3. Live preview only reaches the shell while settings stay open (by design, but verify UX)

`FramePreviewMsg` updates `App.frameOverlay`; the settings window is modal.
While it is open the frame visibly changes behind it (verified in tests).
Closing via **Esc** routes `CancelMsg` → restore; closing the floating window
by other means (e.g. click-outside policy for modals suppresses it, but check
`wm` window-close paths) must also clear the overlay — the only closer wired
today is `update.go` (`SaveMsg`/`CancelMsg`). Audit all
`ui.OverlayWorkspaceSettings` close paths and route them through a shared
`closeSettings()` that calls `clearFramePreview()`. Candidates found:
`app.go:503` (openSettings close-before-open), `view.go:728` (window close
route), `popupmouse.go:90`.

### O4. `framePreview` reads the form, not the validated state (low)

`settings.Model.framePreview` maps blank → nil and invalid → nil, and
`applyFramePreview` resolves nil → 0 — consistent with what save persists.
But a *mid-typing* value like `"1"` then `"12"` triggers per-keystroke
previews of intermediate values (intended), while an invalid value (`"abc"`)
previews as 0 silently even though the row shows a validation error. Decide:
either suppress the preview while the edited field has errors (nicer), or
leave as-is (preview shows the 0-fallback that save would persist) and
document it. Currently the latter.

### O5. Header separator still paints `b.W`, content budget uses `proj.Width` (cosmetic, verify)

`shellheader.Screen.Paint` draws the separator with `b.W` (slot width —
correct) but `buildRuns` receives `innerW = projWidth - pad*2` and the model
budget uses `s.proj.Width` directly (`(s.proj.Width-4)/3`). After fix #3 these
agree (proj.Width == slot width), but if any caller ever passes a projection
width ≠ slot width the two budgets diverge silently. Consider dropping
`Projection.Width` entirely and deriving everything from `Bounds()` — the
slot is now the single geometry source.

### O6. Session-settings / composer hint rows assume legacy offsets (unverified)

`classifyShellRegion`'s `inputStartLine = ChatAreaHeight + chromeTopRow() +
sessionDeckRect.H` and the input `textAreaY()` mapping were updated, but the
composer hint / attachment chips row math (`input.Model.ShellHeight`,
`LayoutShell`) is slot-local and was only spot-checked. If any user report
mentions the caret or chips row being off by exactly the top-frame count,
start at `tui/input/input.go` `LayoutShell`/`PaintShell` (they use slot-local
Y, so they should be frame-agnostic — verify with a frame-heavy paint test).

### O7. Tests still assert legacy geometry in places (housekeeping)

`activity_test.go` uses `app.shellTopRow() + RowStride` (frame-aware), but a
few other fixtures still assume `ChatAreaTop == 3` implicitly by clicking
absolute coordinates (e.g. `host_capture_test.go:161` `{40, 3}` for the tab
bar band). They pass today because the default frame is zero (top chrome =
legacy 3), but they will break the moment a test app sets a non-zero frame.
Prefer deriving from `app.chromeTopRow()` in new tests; sweep existing ones
when touching them next.

## Verification state at time of writing

- `go build ./...` clean; `go test ./...` all 61 packages pass;
  `golangci-lint run ./types/... ./tui/...` → 0 issues; gofmt clean.
- Probe tests used during review were removed
  (`zz_header_probe_test.go`, `zz_stress_probe_test.go`).
