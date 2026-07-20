# TUI Component System and Design Language Plan

Status: Implementation in progress — aligned to this document as of 2026-07-17

Date: 2026-07-17 (progress last reconciled after performance baselines + agent lifecycle + live Conversation seam)

Scope: The complete `tui/` layer and every program boundary that supplies data to,
or receives actions from, the TUI.

This is the canonical plan for replacing Crude's parallel widget, form, popup,
input, scrolling, styling, and mouse implementations with one coherent component
system. It complements `PLAN.md`, `COMPOSITING_ENGINE_ARCHITECTURE.md`, and
`TERMINAL_WIDTH.md`.

**Reconciliation rule:** Code changes that advance migration must update
(1) this file's phase status, (2) Section 20 ledger, (3) Section 26 next steps,
and (4) `docs/tui/bypass_manifest.json` in the same change. Status labels mean:

| Label | Meaning |
|-------|---------|
| DONE | Exit criteria met (or residual gaps explicitly listed as non-blocking) |
| PARTIAL | Foundations and/or partial product cutover; exit criteria not met |
| OPEN | Not started or only planned |

### Snapshot (honest progress)

| Phase | Status | Notes |
|------|--------|-------|
| 0 Evidence freeze | DONE | Docs + audit + manifest; canonical dark/light visual baselines at 80x24 through 200x50; checked-in render/allocation/interaction benchmarks with recorded numeric targets |
| 1 Rendering foundations | DONE | cell/surface/compositor/scrollbar tests + benches |
| 2 Component host | DONE | `tui/ui` Host + controls; not yet wired as viewmodel root |
| 3 Theme engine | PARTIAL | TokenSet + state precedence; dark/light themes; two-theme state gallery + runtime switch; no HC/hot reload |
| 4 Layout / hit tree | PARTIAL | Constraints, Stack, Host layout/hit; no Flex/Grid/Split/Viewport |
| 5 Focus / actions / keymaps | PARTIAL | Keyboard dispatch wired to registry.MatchGlobal; palette entries from DefaultRegistry().All(); launcher shortcuts from registry; status hints from registry.HintFor; old global switch deleted; paletteCommands slice deleted; welcomeShortcuts() deleted |
| 6 Input engine | PARTIAL | TextEdit + RenderFilterLine on palette/launcher/settings/filepicker/list/FIF; tree prompt TextField |
| 7 Collections / scrollbar | PARTIAL | List (header/sep/disabled), VirtualList, ScrollModel, ScrollbarControl + ANSI bridge |
| 8 Form system | PARTIAL | form.TextItem uses TextEdit; form shell still legacy lipgloss |
| 9 Window/overlay | PARTIAL | WindowFrame is the only product chrome; WM decoration paint deleted; OverlayHost owns all product popup lifecycle; reveal-only PopupChrome bridge remains |
| 10 Domain seams | PARTIAL | ProjectSearchService and FileOpsService own domain work; direct workspace agent lifecycle routing; persisted hydration uses `ChatMessageView`; live agent messages now use workspace-owned `Conversation`/`Message` data projected into retained TUI lists by stable ID/version |
| 11 Compact migrations | PARTIAL | all 11 surfaces migrated; ConfirmPrompt/3Button DELETED; ContextMenuLines static tests migrated to BuildContextMenuFrame (reveal animation tests retained pending WindowFrame reveal) |
| 12 Structured migrations | PARTIAL | Filter engines + shared filter paint; Find in Files domain search moved to workspace; full surfaces still legacy |
| 13 Shell migrations | OPEN | |
| 14 Deletion | OPEN | Dual systems still present by design |
| 15 Stabilization | OPEN | |

### Intentional bridges (not drift — temporary)

These are allowed only as shrinking adapters listed in `bypass_manifest.json`:

- `ui.RenderFilterLine` — ANSI paint for legacy `View()` paths using TextEdit.
- `ui.ScrollbarCellANSI` — ANSI thumb/track from ScrollModel for legacy lists.
- `ui.ComponentWidget` / `ui.WindowFrameWidget` — mount Component trees and unified chrome inside existing FloatingWindow.
- `ui.LegacyWidget` / `ANSIViewFunc` — old widget → Component adapters.
- Flat `tui/ui/` package (plan target subdirs deferred until APIs stabilize).

**Not done yet (do not claim as complete):** high-contrast/hot-reloaded themes; PopupChrome deletion (reveal animation consumers remain);
workspace free of `tui/` imports; full TextField surface paint everywhere.

## 1. Objective

Build a retained, runtime-themeable terminal component system with the consistency,
state completeness, and reuse discipline expected from a mature desktop widget
toolkit.

The end state is not a collection of helper functions. It is a UI platform with:

- One component tree.
- One measurement and layout system.
- One focus and event-routing system.
- One action and keymap registry.
- One text-input engine.
- One collection, selection, viewport, and scrollbar system.
- One form system.
- One window, dialog, popover, and chrome system.
- One runtime theme resolver.
- One semantic model for tests, accessibility, and future frontends.
- Enforced package boundaries between `app/`, `workspace/`, and `tui/`.

Feature code will describe content and composition. It will not invent primitive
painting, input editing, scrolling, hit testing, padding, focus behavior, or visual
states.

## 2. Relationship to Existing Architecture

The target three-layer architecture remains unchanged:

```text
app/       IDE shell, app config, workspace lifecycle
workspace/ Per-project business state and services
tui/       Rendering, component state, and input routing
```

Communication remains:

```text
TUI -> workspace: method calls
workspace -> TUI: event bus
```

The component system lives entirely in `tui/`. Components must not know about
sessions, providers, SQLite, LSP connections, filesystem services, or workspace
objects. Screens translate component actions into workspace method calls. Workspace
events update screen data.

The existing low-level compositor remains the foundation:

```text
cell -> surface -> compositor -> UI component tree -> screens -> viewmodel host
```

The component system extends the current rendering architecture. It does not replace
the terminal-width pipeline, Surface compositing, or Bubble Tea event loop.

## 3. Audit Baseline

The initial parallel audit covered repository architecture, rendering foundations,
existing widgets, forms and text inputs, collection controls, mouse and geometry,
visual language, windows and overlays, visible product surfaces, test coverage, and
business-state boundaries.

The baseline found the following categories of duplication.

| Area | Baseline finding |
|------|------------------|
| Editable and filter text | At least eight independent behavior paths |
| Collection surfaces | Eighteen list, tree, menu, tab, or row-based surfaces catalogued |
| Selection behavior | Multiple wrap, clamp, click-only, and selection-as-hover models |
| Scroll behavior | Five distinct scroll models plus repeated scrollbar integration |
| Buttons | At least four render and interaction implementations |
| Dialog and popup chrome | Three independent chrome systems |
| Style production | Theme styles, inline Lipgloss, cells, raw ANSI, Chroma, and Glamour |
| Mouse geometry | Repeated screen-to-local transforms and render-time hit ranges |
| Focus | Focus stack, WM focus, popup cursors, and feature-local focus state |
| Architecture | Workspace owns TUI objects while some widgets own domain work |
| Tests | Strong WM/widget tests, but low-level rendering packages have critical gaps |

These counts are a planning baseline. Phase 0 will make them reproducible through a
checked-in manifest and static audit tooling.

## 4. Surface Coverage Catalog

No migration phase may claim completeness without accounting for every surface below.

### 4.1 Persistent shell

| Surface | Current primary files |
|---------|-----------------------|
| Header | `tui/widgets/header.go`, `tui/viewmodel/view.go` |
| File-tree sidebar | `tui/filetree/`, `tui/viewmodel/view.go` |
| Sidebar divider and resize handle | `tui/viewmodel/mouse.go` |
| Main tab bar | `tui/window/window.go`, `tui/viewmodel/view.go` |
| Overlay tab bar | `tui/window/window.go`, `tui/viewmodel/view.go` |
| Status bar | `tui/widgets/statusbar.go`, `tui/viewmodel/view.go` |
| Contextual shortcut help | `tui/viewmodel/view.go`, feature `Hint()` methods |
| Main content compositor surface | `tui/viewmodel/view.go` |

### 4.2 Primary views

| Surface | Current primary files |
|---------|-----------------------|
| Welcome launcher | `tui/widgets/launcher.go`, `tui/viewmodel/launcher.go` |
| Editor | `tui/editor/`, `tui/viewmodel/editor_tabs.go` |
| Agent chat | `tui/widgets/messagelist.go`, `tui/widgets/chatmessage.go` |
| Chat input | `tui/input/`, `tui/components/text.go` |
| Agent and thinking subpanes | `workspace/subpane.go`, `tui/widgets/messagelist.go` |
| Session picker | `tui/widgets/sessionpicker.go` |
| Async command rows | `tui/widgets/asyncbar.go` |
| Reference chips | `tui/input/references.go`, `tui/viewmodel/view.go` |

### 4.3 Floating windows and popups

| Surface | Current primary files |
|---------|-----------------------|
| Command palette | `tui/viewmodel/command_palette.go` |
| Find in Files | `tui/widgets/find_in_files.go`, `tui/viewmodel/find_in_files.go` |
| Find and replace | `tui/widgets/findreplace.go`, `tui/viewmodel/findreplace_handler.go` |
| Workspace settings | `tui/settings/settings.go`, `tui/widgets/workspace_settings.go` |
| File picker and workspace picker | `tui/widgets/filepicker.go` |
| Composer | `tui/widgets/composer.go` |
| Text/reference editor | `tui/widgets/textoverlay.go` |
| Context menu | `tui/widgets/contextmenu.go`, context-menu builders |
| Dirty-close prompt | `tui/viewmodel/dirty_prompt.go` |
| Tree create/rename prompt | `tui/widgets/textprompt.go`, tree operations |
| Tree delete confirmation | `tui/viewmodel/tree_operations.go` |
| LSP references | `tui/viewmodel/lsp_handlers.go`, popup handlers |
| LSP completion | `tui/viewmodel/lsp_handlers.go`, popup handlers |
| LSP outline | `tui/viewmodel/lsp_handlers.go`, popup handlers |
| Diagnostic popup | `tui/viewmodel/lsp_handlers.go` |

### 4.4 Inline and blocking forms

| Surface | Current primary files |
|---------|-----------------------|
| Session settings | `tui/widgets/session_settings.go`, `tui/form/` |
| Tool confirmation | `tui/widgets/confirmdialog.go` |
| Agent question form | `tui/widgets/questionform.go` |
| YOLO confirmation | `tui/widgets/yolodialog.go` |
| Generic confirmation prompt | `tui/widgets/confirmprompt.go` |

### 4.5 Secondary interactive content

The migration also covers slash-command lists, model/provider selectors, settings
category navigation, breadcrumbs, tree connectors, action cards, toggles, diagnostic
indicators, tab close buttons, scroll gutters, empty/loading/error rows, status
indicators, progress animation, key hints, section labels, dividers, and selectable
plain-text rows.

Nothing is exempt because it appears to be only text. If text has visual hierarchy,
state, alignment, truncation, a click target, a shortcut, or focus behavior, it is a
component or is rendered by one.

## 5. Current Duplication Map

### 5.1 Text and search

The mature input implementation is currently `components.TextInput` in
`tui/components/text.go`, but several surfaces bypass it or reimplement its paint
path.

Current categories include:

| Category | Examples |
|----------|----------|
| Full input engine | Chat input, composer, text overlay, find/replace |
| Input engine with custom renderer | Find in Files query |
| Independent form editor | `form.TextItem` |
| Hand-built prompt editor | Tree create/rename prompt |
| Append/backspace filter strings | Palette, launcher, settings, file picker, generic list |
| Specialized document editor | `tui/editor/` |

The target keeps one reusable input engine plus the specialized code editor. Search,
filter, masked, multiline, prompt, and form inputs become configured variants of the
same engine.

### 5.2 Collections and scrolling

Current list-like surfaces independently manage combinations of:

- Cursor index.
- Selected index or ID.
- Hover index.
- Filtered item arrays.
- Item-to-line maps.
- Scroll offsets.
- Page sizes.
- Scrollbar visibility and fade timers.
- Mouse-to-row calculations.
- Double-click tracking.

The target separates these responsibilities into reusable models:

```text
CollectionData
SelectionModel
HoverModel
ScrollModel
Viewport
Scrollbar
RowDelegate
```

Lists, trees, tables, menus, palettes, tabs, pickers, and virtual message collections
compose those models instead of reproducing them.

### 5.3 Chrome and windows

The original TUI had:

- `widgets.PopupChromeLines` and related helpers.
- WM decoration in `tui/wm/decoration.go` (deleted after WindowFrame cutover).
- Hand-drawn borders in feature code.

They disagreed on title placement, close buttons, background, inner padding, footer
space, content bounds, and hit testing. Product windows now use `WindowFrame`; the
remaining PopupChrome code is a shrinking reveal-animation bridge.

### 5.4 Visual styling

The current visual output can be produced by:

- Prebuilt styles in `tui/theme`.
- Inline `lipgloss.NewStyle()` calls.
- Direct `cell.Color` and `cell.Attrs` construction.
- Raw ANSI sequences.
- Chroma token styles.
- Glamour Markdown styles.
- Hard-coded glyph and icon choices.

The target permits feature code to request semantic styles only. Conversion to cells,
ANSI compatibility, Chroma, and Markdown happens through controlled paint adapters.

### 5.5 Mouse and geometry

Current hit testing often depends on coordinates recorded during rendering. Similar
screen-to-local translation appears in tab, tree, launcher, popup, editor, and
scrollbar paths.

The target layout pass generates component geometry and a hit tree. Painting and
input dispatch consume the same immutable geometry snapshot. A control does not
independently guess where it was drawn.

## 6. Design Principles

### 6.1 Retained component tree

Components are mounted objects with stable identity and retained presentation state.
They are measured, laid out, painted, and invalidated independently.

The Surface tree remains the rendering substrate. Component surfaces are retained or
recycled by component ID. Scrolling should reposition unchanged item surfaces rather
than repainting every row.

### 6.2 Explicit state

Every interactive control supports the states relevant to its role:

```text
default
hovered
focused
pressed
active
selected
disabled
read-only
invalid
warning
success
loading
dragging
```

State combination and precedence are resolved centrally. Current behavior such as
focused over selected over hovered becomes a framework invariant.

### 6.3 Composition over feature variants

A search field is an input plus leading icon, placeholder, clear action, and optional
toggle actions. A settings row is a form field plus label, description, validation,
and a control. A file picker is a dialog plus breadcrumb, search field, tree/list,
scroll viewport, and action bar.

Feature-specific mega-widgets should be decomposed into those parts without exposing
their internals to the viewmodel.

### 6.4 Data in, actions out

Screens receive immutable or explicitly versioned view data. Controls emit typed UI
actions. Screens translate actions into workspace method calls.

No component owns:

- Filesystem or SQLite handles.
- LSP clients.
- Provider clients.
- Workspace or session pointers.
- Domain goroutines.
- Persistence callbacks.

### 6.5 Terminal correctness

All measurement, truncation, cursor placement, selection, and hit testing use the
canonical grapheme-width path from `TERMINAL_WIDTH.md`.

Editing boundaries must not split a grapheme cluster. Data APIs may expose rune or
byte offsets where required by LSP or files, but visual editing converts through one
tested indexing layer.

## 7. Target Package Structure

**Current (transitional):** all new foundations live in a flat `tui/ui/` package
until public APIs stabilize. This is intentional — not a permanent deviation.

**Target (when APIs freeze):**

```text
tui/
  cell/                 existing low-level cells
  surface/              existing surfaces and compositing operations
  compositor/           existing frame compositor
  rendering/            terminal adapters and canonical width path

  ui/                    component host and base contracts
    component.go
    host.go
    event.go
    geometry.go
    invalidate.go
    semantics.go

    theme/               tokens, state styles, runtime resolver
    layout/              constraints and layout algorithms
    paint/               cell-native primitives and controlled bridges
    control/             interactive leaf controls
    collection/          list/tree/table/viewport infrastructure
    form/                fields, validation, forms, action bars
    overlay/             windows, dialogs, popovers, menus, tooltips
    testing/             component harness and cell-buffer assertions

  screens/               product surfaces composed from ui components
    launcher/
    settings/
    editor/
    chat/
    search/
    picker/

  viewmodel/             Bubble Tea host and workspace action routing
```

Existing packages are migration sources:

| Existing package | Target disposition |
|------------------|--------------------|
| `tui/widget` | Replaced by `tui/ui` host contracts |
| `tui/components` | Split into `control`, `collection`, and passive paint primitives |
| `tui/form` | Replaced by `tui/ui/form` |
| `tui/scrollbar` | Replaced by collection viewport/scrollbar controls |
| `tui/virtlist` | Retained algorithms moved behind collection APIs |
| `tui/widgets` | Reusable parts move to UI packages; features move to `screens` |
| `tui/window` | Tab state and rendering move to a standard Tabs control |
| `tui/wm` | Window placement and z-order retained; chrome and content use UI components |
| `tui/theme` | Replaced by versioned semantic theme registry |
| `tui/settings` | Screen composition moves to `screens/settings`; domain data stays outside TUI |

## 8. Core Contracts

The exact API is finalized during Phase 2, after low-level tests exist. Its required
capabilities are fixed by this plan.

```go
type Component interface {
	ID() ComponentID
	Measure(LayoutContext, Constraints) Size
	Layout(LayoutContext, Rect)
	Paint(*Canvas, PaintContext)
	Handle(EventContext, Event) EventResult
	Semantics() Semantics
}

type EventResult struct {
	Handled    bool
	Action     *Action
	Cmd        tea.Cmd
	Invalidate Invalidation
}
```

Rules:

- `Measure` and `Layout` are deterministic for the same inputs.
- `Paint` does not mutate domain or component behavior state.
- `Handle` does not paint.
- `Action` is typed and namespaced.
- `Cmd` is limited to UI timing or clipboard work inside controls. Domain async work
  begins only after the screen/viewmodel routes the action to workspace.
- Geometry is relative to the component parent until the host builds screen bounds.
- Component IDs are stable across frames and unique among mounted components.

## 9. Theme Architecture

### 9.1 Three token levels

```text
Palette tokens
  gray.950, orange.500, red.500

Semantic tokens
  surface.base, surface.raised, text.primary, border.focus, status.error

Component-state tokens
  input.focused.border, list.selected.bg, button.danger.pressed.fg
```

Feature code may use only semantic and component tokens. Palette values are theme
implementation details.

### 9.2 Token categories

The registry covers:

- Foreground and background colors.
- Border colors and border glyph sets.
- Typography attributes such as bold, italic, faint, underline, and reverse.
- Horizontal and vertical spacing.
- Control heights and minimum widths.
- Divider and connector glyphs.
- Cursor style.
- Scrollbar track and thumb style.
- Icons and fallback glyphs.
- Animation timing and reduced-motion behavior.
- Syntax, diff, diagnostics, Markdown, and terminal-code roles.

### 9.3 Runtime switching

The theme registry has a monotonically increasing version. A theme change:

1. Replaces the active resolved token set.
2. Increments the theme version.
3. Invalidates layout only if metric tokens changed.
4. Invalidates paint for the complete mounted tree.
5. Preserves control state, text, selections, scroll, and focus.

Dark, light, and high-contrast reference themes are required to prove that no control
depends on the current dark palette.

### 9.4 Visual state gallery

Every primitive and control is rendered in a gallery containing all supported states,
variants, widths, and reference themes. The gallery is both documentation and a
cell-buffer golden-test input.

## 10. Layout and Geometry

### 10.1 Constraints

```go
type Constraints struct {
	MinW, MaxW int
	MinH, MaxH int
}
```

Every component returns a size inside those bounds. Layout algorithms include:

- Row and column Flex.
- Grid.
- Stack/overlay.
- Inset for padding and border allocation.
- SplitPane with a draggable divider.
- Scroll viewport.
- Alignment and distribution.
- Intrinsic and constrained text sizing.

### 10.2 Hit tree

The host builds a hit tree from final layout geometry. Each hit node includes:

- Component ID.
- Screen bounds.
- Content-local transform.
- Semantic hit region such as content, action, thumb, divider, title, or resize edge.
- Z-order.
- Pointer acceptance and disabled state.

Mouse routing queries this tree top-to-bottom. Features no longer save custom X ranges
or repeat sidebar/padding offsets.

### 10.3 Responsive behavior

Responsive decisions belong to screen layouts and reusable responsive containers.
Controls do not silently switch to bespoke narrow renderers.

Every migrated screen has documented behavior for:

- Minimum supported terminal size.
- Narrow layout.
- Standard layout.
- Wide layout.
- Resized floating windows.
- Content overflow.

## 11. Events, Focus, and Actions

### 11.1 Normalized events

Bubble Tea messages are normalized once into UI events:

```text
KeyEvent
TextEvent
PasteEvent
PointerMove
PointerDown
PointerUp
PointerWheel
ResizeEvent
FocusEvent
TickEvent
```

Controls do not parse arbitrary Bubble Tea messages independently unless they are a
temporary legacy adapter.

### 11.2 Focus

The focus manager owns:

- Focus scopes.
- Tab and reverse-tab traversal.
- Directional navigation where declared.
- Programmatic focus.
- Focus restoration after popup close.
- Modal focus trapping.
- Visible focus state.

WM focus and component focus become coordinated. A floating window is a focus scope;
the focused control inside it is managed by the same focus system.

### 11.3 Mouse capture

Dragging a scrollbar, divider, selection, or floating window uses one mouse-capture
service. Capture has one owner, receives movement outside original bounds, and ends on
release or cancellation.

### 11.4 Actions and keymaps

Actions define ID, label, description, icon, enabled state, and bindings. Examples:

```text
palette.open
search.project.open
file.save
tab.close
sidebar.toggle
composer.toggle
```

The following consume the same registry:

- Global key dispatch.
- Component key handling.
- Command palette.
- Context menus.
- Launcher shortcuts.
- Status-bar hints.
- Help surfaces.

Displayed shortcuts can therefore never drift from active bindings.

## 12. Text and Input System

### 12.1 Shared editing engine

One editing model supports:

- Single-line and multiline modes.
- Grapheme-safe cursor movement.
- Word movement.
- Home, End, document start, and document end.
- Selection by keyboard and mouse.
- Clipboard copy, cut, and paste.
- Bracketed paste.
- Undo and redo.
- Horizontal and vertical viewport tracking.
- Placeholder and hint text.
- Prefix, suffix, leading icon, and trailing actions.
- Masked values.
- Read-only and disabled states.
- Validation and error presentation.
- Atomic ranges for references where needed.

### 12.2 Canonical cursor behavior

The cursor style is controlled by theme tokens. The default contract is:

- On an occupied grapheme, render inverse foreground/background over that grapheme.
- At end-of-line, render a themed block or caret cell.
- Placeholder text remains visibly placeholder text when empty.
- Focused placeholder/cursor behavior is identical across search, prompt, and form
  fields.
- Selection uses the same selection tokens across all inputs.

### 12.3 Input variants

The toolkit exposes configured controls rather than separate editing implementations:

| Control | Additional behavior |
|---------|---------------------|
| `TextField` | Single-line submit and validation |
| `SearchField` | Search icon, clear action, optional debounce action |
| `TextArea` | Multiline viewport and submit policy |
| `PasswordField` | Masked paint with real-value editing |
| `PromptField` | Label/prefix and submit/cancel actions |
| `FilterField` | Immediate filter action and collection linkage |

The code editor remains a specialized `DocumentEditor`, but it reuses theme, focus,
scrollbar, action, selection paint, and cursor contracts where applicable.

## 13. Collection System

### 13.1 Models

```text
SelectionModel
HoverModel
ScrollModel
Viewport
RowMetrics
CollectionData
```

Selection and hover are separate. Mouse hover never silently replaces keyboard focus
or committed selection.

### 13.2 Controls

The system provides:

- Uniform List.
- VirtualList for variable-height content.
- Tree.
- Table and aligned rows.
- Menu and ContextMenu.
- Tabs.
- Breadcrumbs.
- Select/dropdown list.
- ScrollView.
- Standard Scrollbar.

### 13.3 Navigation contract

Controls declare whether navigation wraps or clamps. The implementation of Up, Down,
Page Up, Page Down, Home, End, mouse selection, wheel scrolling, and ensuring the
cursor is visible is shared.

### 13.4 Scrollbar contract

The scrollbar owns:

- Thumb measurement.
- Track and thumb paint.
- Visibility and fade policy.
- Click-to-page or click-to-position policy.
- Mouse capture and drag.
- Mapping between content and track coordinates.
- Vertical and horizontal orientation.

No feature renders scrollbar glyphs or computes thumb bounds.

## 14. Form System

The form system separates values from controls.

```text
FormModel
FieldState
ValidationRule
FormLayout
ActionBar
```

Each field tracks:

- Current and initial value.
- Dirty state.
- Touched state.
- Validation errors.
- Disabled and read-only state.
- Label, description, and semantic name.

Supported fields include text, password, select, cycle, toggle, checkbox, list,
navigation row, and custom composite field.

Save, Cancel, Apply, Reset, and destructive actions use standard Button controls and
ActionBar layout. Forms do not render bracketed button strings themselves.

## 15. Window and Overlay System

### 15.1 Roles

| Role | Behavior |
|------|----------|
| Window | Movable, optionally resizable and closable |
| Dialog | Modal focus scope with explicit completion actions |
| Popover | Anchored, non-modal, closes according to outside policy |
| ContextMenu | Pointer/keyboard menu anchored to invocation position |
| Tooltip | Passive, non-focusable informational surface |
| Palette | Search field plus command collection |
| Panel | Inline content region without floating chrome |

### 15.2 One frame implementation

`WindowFrame` owns:

- Border and background.
- Title and optional icon.
- Close action.
- Content padding.
- Optional toolbar.
- Optional footer divider and action bar.
- Content rectangle.
- Focused and inactive visual state.
- Resize edges and title drag region.

Features provide content. They cannot draw a second border or compensate for frame
dimensions.

### 15.3 Outside-click policy

Every overlay declares one policy:

```text
CloseAndRoute
CloseAndConsume
KeepAndRoute
IgnoreAndConsume
```

Modal dialogs use `IgnoreAndConsume`. Menus generally use `CloseAndRoute` or
`CloseAndConsume`. Persistent non-modal windows use `KeepAndRoute` where appropriate.

## 16. Architecture Boundary Corrections

The component migration must also remove current layer leaks.

### 16.1 Workspace must stop owning TUI objects

Presentation objects currently referenced from workspace code are split into domain
state and TUI state. The completed boundary permits no imports from `workspace/` to
`tui/`.

Examples of required separation include:

- Agent message data versus `widgets.MessageList`.
- Subpane data versus subpane rendering and scrolling.
- File-tree data versus Tree control state and renderer.
- Input history/reference data versus text-input cursor and selection.
- Tab data versus Tab control rendering.
- Status data versus StatusBar control state.
- Theme preference data versus resolved TUI theme objects.

### 16.2 Widgets must stop owning domain work

Project search, filesystem mutation, settings persistence, provider model fetching,
and similar work move behind workspace methods or services. Components may debounce a
UI action, but they do not execute project search themselves.

### 16.3 Communication

No callbacks are introduced to make components reusable. Components return typed UI
actions. Screens and the viewmodel call workspace methods. Workspace publishes typed
events.

## 17. Rendering and Invalidation

### 17.1 Transition rule

The current editor `contentDirty` cache remains unchanged until component-level
invalidation proves equivalent behavior and performance. Its documented restrictions
continue to apply during migration.

### 17.2 Target invalidation

| Trigger | Target invalidation |
|---------|---------------------|
| Input cursor movement | Input paint only |
| Input value change | Input measure if wrapping changed, otherwise paint |
| Hover change | Old and new hovered components only |
| Selection change | Affected rows or text ranges |
| Scroll | Viewport child positions and scrollbar only |
| Streaming token | Active message item and dependent row metrics |
| Editor edit | Editor document surface and status data |
| Window move | Old and new window bounds |
| Resize | Affected layout subtree |
| Theme color change | Full paint invalidation |
| Theme metric change | Full layout and paint invalidation |

### 17.3 Legacy bridges

ANSI and Bubble Tea model adapters may exist during migration only at controlled
boundaries. New feature code cannot depend on them. Each adapter use is recorded in
the migration manifest and removed when its final consumer migrates.

Chroma and Glamour may initially use controlled ANSI-to-cell bridges. Their colors and
metrics still come from theme adapters, not feature literals.

## 18. Hard Source Rules

At completion, automated checks enforce:

1. No `lipgloss.NewStyle` outside theme/paint implementation and approved adapters.
2. No hard-coded color literals outside theme definitions and tests.
3. No raw ANSI outside compositor, parser, terminal adapters, and focused tests.
4. No direct cell painting from feature screens.
5. No custom scrollbar glyphs or thumb calculations.
6. No ad-hoc editable/filter strings with feature-level key handling.
7. No feature-level popup borders, padding compensation, or close-button hit testing.
8. No direct shortcut strings in launcher, status hints, menus, or help.
9. No noncanonical display-width library in layout or hit testing.
10. No `workspace/` import of `tui/`.
11. Every overlay has explicit focus, modal, close, and outside-click policy.
12. Every interactive component has a stable ID and semantic role.

During migration these rules use a checked-in allowlist. CI permits deletion from the
allowlist but rejects additions unless the plan itself is explicitly revised.

## 19. Detailed Phases

### Phase 0: Evidence freeze and no-new-debt gate ✅ DONE

Landed:
- `docs/tui/SURFACE_CATALOG.md`, `CONTROL_USAGE.md`, `INTERACTION_MATRIX.md`, `DESIGN_TOKEN_BYPASSES.md`
- `docs/tui/bypass_manifest.json` (v2; control_bypasses shrink only)
- `scripts/tui_design_audit.sh` (no-new-debt gate; currently passes)
- **Canonical visual baselines** (`tui/ui/visual_baseline_test.go`): 80x24, 80x30, 120x40, 200x50 under dark and light themes; runtime theme switch verification; WindowFrame close glyph, border cells, content fill; TextField cursor/placeholder; List selected row; Button disabled; Label color roles; ellipsis truncation; continuation cell integrity; render/key benchmarks
- **Checked-in numeric performance baseline** (`docs/tui/PERFORMANCE_BASELINES.md`): reproducible environment/command, five-sample observed ranges, allocation counts, expected review ceilings, and interpretation rules
- Real Go benchmarks in `tui/ui/benchmark_test.go` cover gallery paint at 80x24 and 120x40 plus the TextEdit key cycle; existing Surface/compositor hot paths are recorded alongside them

Objectives:

- Convert the initial audit into checked-in, reviewable artifacts.
- Prove that every current UI path is known before API implementation starts.
- Prevent further ad-hoc controls while migration is underway.

Deliverables:

- `docs/tui/SURFACE_CATALOG.md`.
- `docs/tui/CONTROL_USAGE.md`.
- `docs/tui/INTERACTION_MATRIX.md`.
- `docs/tui/DESIGN_TOKEN_BYPASSES.md`.
- A machine-readable component and bypass manifest.
- Canonical screenshots at minimum, `80x24`, `120x40`, and wide dimensions.
- A static audit command and CI no-new-bypass check.
- Baseline render, allocation, and interaction benchmarks.

Exit criteria:

- Every surface in Section 4 appears in the manifest.
- Every direct style, raw ANSI, custom input, custom scrollbar, and feature hit-test
  implementation is listed.
- CI fails when a new unregistered bypass is added.
- No component API implementation begins before the manifest is reviewed.

### Phase 1: Rendering-foundation verification ✅ DONE

Landed:
- `tui/cell/*_test.go` — Color, Attrs, Cell, pools
- `tui/surface/*_test.go` — Rect, ClipStack, Buffer, Blit/Merge/Blend, Surface tree
- `tui/compositor/*_test.go` — frame pipeline, damage, diff, ANSI output
- `tui/scrollbar/*_test.go` — thumb geometry, drag, fade, jump-click
- Benchmarks in surface and compositor packages

Objectives:

- Make the existing low-level renderer safe enough to support a retained component
  tree.
- Lock terminal-width and wide-cell behavior before higher-level migration.

Deliverables:

- Unit and property tests for `tui/cell`.
- Unit and property tests for `tui/surface`.
- Compositor frame, damage, z-order, transparency, and ANSI-output tests.
- Clip-stack and coordinate-transform tests.
- Scrollbar geometry tests for the existing implementation.
- Wide grapheme, continuation cell, clipping, and edge truncation tests.
- Baseline benchmarks for full frame, window overlap, and large lists.

Exit criteria:

- Transparent and wide-cell composition is directly tested.
- Layout width still matches the patched ultraviolet path.
- Compositor behavior has deterministic cell-buffer assertions.
- No unexplained rendering regression exists at the baseline screenshots.

### Phase 2: Component host and lifecycle ✅ DONE (host + first controls)

Landed:
- `tui/ui/` — ComponentID, Constraints, Rect, EventResult, Action, Semantics
- `Host` with layout, paint, hit-test (depth-preferring), focus, hover, SetTheme
- `TokenSet` semantic color resolution from `theme.Theme` + control-state matrix
- `Label`, `Button`, `Stack`, `TextField`, `List`
- `TextEdit` grapheme/rune-safe shared editing model
- `SelectionModel`, `ScrollModel` shared collection models
- `LegacyWidget` + `ANSIViewFunc` temporary adapters
- Tests: unit + integration (`go test -race ./tui/ui/`)
- `DemoGallery` non-product Host composition smoke test

Note (not Phase 2 blockers; tracked under later phases):
- `viewmodel.App` is **not** yet rooted on Host (product still hybrid)
- VirtualList, WindowFrame, ScrollbarControl, ComponentWidget landed early under
  Phases 7/9 foundations while keeping Phase 2 exit criteria satisfied

Objectives:

- Establish stable component identity, mounting, geometry, event results, and
  invalidation.

Deliverables:

- `ComponentID`, `Component`, `EventResult`, `Semantics`, and invalidation types.
- Root Host integrated into `viewmodel.App` without migrating product screens.
- Persistent surface ownership keyed by component ID.
- Controlled legacy-widget adapter.
- Component test harness.

Exit criteria:

- A nested test tree can mount, measure, lay out, paint, handle events, and unmount.
- Invalidating one leaf does not repaint unrelated siblings.
- Resize and theme-version changes invalidate the correct scopes.

### Phase 3: Theme engine and design specification 🔄 PARTIAL

Landed early:
- `TokenSet` with semantic roles and `ButtonColors` / `InputColors` / `ListRowColors`
- `ResolveControlState` precedence (disabled > focused > selected > pressed > hovered)
- Host `SetTheme` rebuilds tokens and invalidates
- **State gallery** (`tui/ui/demo.go`): all controls (TextField, Button, List,
  Label, WindowFrame dialog/popover/window) rendered in all visual states
  (default, focused, placeholder, disabled, selected, accent, muted, error,
  success) at 80x30 under dark and light themes.
- Complete light reference palette using the same semantic-style factory as dark
- Runtime Host theme switch rebuilds component tokens without losing input state

Remaining:
- High-contrast reference theme
- Glyph/spacing/typography token tables
- Live config hot-reload

Objectives:

- Replace color/style convention with a formal design language.
- Prove runtime switching before feature migration.

Deliverables:

- Palette, semantic, and component-state token schemas.
- Spacing, control metric, glyph, border, typography, and animation tokens.
- Versioned runtime resolver.
- Dark, light, and high-contrast reference themes.
- Primitive state gallery.
- Bridges for syntax, diagnostics, diff, and Markdown styles.

Exit criteria:

- Theme switching preserves all control state.
- Gallery snapshots update completely after a theme switch.
- No gallery control reads raw palette values from feature code.
- Metric token changes trigger layout; color-only changes trigger paint.

### Phase 4: Layout, geometry, and hit tree 🔄 PARTIAL

Landed early:
- Constraints, Rect, Size, Stack (row/column)
- Host layout + screen-bounds map + depth-preferring HitTest
- WindowFrame content rect vs chrome rect

Remaining:
- Flex, Grid, Inset, SplitPane, Viewport layouts
- Property tests for nested transforms
- Product mouse paths still use hand-translated coordinates in viewmodel

Objectives:

- Make measurement, painting, and hit testing share one geometry source.

Deliverables:

- Constraints, Rect, Size, Insets, and alignment types.
- Flex, Grid, Stack, Inset, SplitPane, and Viewport layouts.
- Screen-space transform propagation.
- Z-ordered hit tree.
- Responsive test fixtures.

Exit criteria:

- Nested local-to-screen and screen-to-local transforms are property tested.
- Pointer hit results match painted bounds at all canonical sizes.
- Layout tests cover zero, minimum, narrow, standard, and wide constraints.

### Phase 5: Event, focus, mouse capture, and action systems 🔄 PARTIAL

Landed early:
- Host focus + hover + pointer dispatch to hit component
- Typed `Action` on EventResult (button/window close)
- **ActionRegistry:** `ActionID`, `ActionDef`, `KeyBinding`, `KeyMap`,
  `MatchGlobal`, `MatchContext`, `HintFor`, `FormatShortcut`,
  `DefaultRegistry` with all global actions (palette, search, composer,
  sidebar, settings, quit) and command-palette metadata
- OverlayHost `RegisterContentHandler` / `RegisterHoverHandler` typed
  dispatch replacing bespoke popupmouse switches

Remaining:
- Wire product keyboard dispatch (`keyboard.go` global switch) to call
  `registry.MatchGlobal` and route the matched action to its handler
- Derive command palette entries from `DefaultRegistry().All()` instead
  of the parallel `paletteCommands` slice
- Derive launcher shortcut list from `registry` HintKey/Label
- Derive status-bar help hints from `registry.HintFor` for the active
  focus context
- Normalized UI event types (still raw tea.Msg in places)
- Focus scopes, modal trap/restore beyond WM
- Mouse capture service
- Shortcut formatter component

Objectives:

- Replace scattered keyboard and mouse interpretation with one dispatch model.

Deliverables:

- Normalized UI events.
- Focus scopes and traversal.
- Modal focus trapping and restoration.
- Hover and pressed-state dispatch.
- Mouse capture service.
- Action registry and layered keymaps.
- Shortcut formatting component.

Exit criteria:

- Keyboard and mouse activation emit the same action IDs.
- Closing nested overlays restores the exact prior focus path.
- Drag continues outside original component bounds and ends reliably on release.
- Palette, help, launcher, and status hints can render from action metadata.

### Phase 6: Text primitives and input engine 🔄 PARTIAL

Landed:
- `TextEdit` (rune-safe cursor, selection, word motion, line ops, paste filter)
- `TextField` (prompt, placeholder, password, read-only, disabled, submit action, mouse cursor, horizontal viewport)
- Inverse/block cursor paint path (TextField surface + `RenderFilterLine` ANSI bridge)
- `HandleTextEditKey` / `HandleFilterKey` shared key routers
- **Engine cutover (keys + value):** palette, launcher, settings search, file picker,
  `components.List` filter, form `TextItem`, tree prompt, Find in Files query

Remaining:
- TextArea multiline, SearchField debounce variant, FilterField collection link
- Paint cutover: many filters still use ANSI bridges / ad-hoc View paint, not TextField.Paint
- Chat input / composer / find-replace still `components.TextInput`
- File tree model `filter string` still open
- Delete ANSI filter bridge and hand-rolled editors

Objectives:

- Replace every non-editor editable text path with one engine.

Deliverables:

- Text, RichText, Label, Hint, KeyChord, and selectable text primitives.
- Shared grapheme-safe editing model.
- TextField, SearchField, TextArea, PasswordField, PromptField, and FilterField.
- Canonical cursor, selection, placeholder, clipboard, paste, and undo behavior.
- Input state gallery and interaction suite.

Exit criteria:

- Tree prompts, list filters, form text values, launcher search, settings search,
  palette search, and file-picker search can all use the same editing engine.
- Unicode, emoji, ZWJ, combining mark, and wide-character tests pass.
- No feature implementation slices strings using cursor byte offsets.

### Phase 7: Collections, viewport, and scrollbar 🔄 PARTIAL

Landed:
- `SelectionModel` (clamp/wrap, page, home/end)
- `ScrollModel` (offset, ensure-visible, thumb bounds)
- `List` (keyboard, mouse click, hover, wheel, activate action)
- `VirtualList` (variable-height foundation)
- `ScrollbarControl` (cell paint from ScrollModel)
- `ScrollbarCellANSI` bridge; **launcher + Find in Files** no longer use bespoke `┃` glyphs

Remaining:
- Tree, Table, Menu, Tabs collection controls
- Product lists still mostly custom (palette, message list, file tree, etc.)
- Delete ANSI scrollbar bridge when all consumers use ScrollbarControl

Objectives:

- Centralize selection, navigation, scrolling, virtualization, and row painting.

Deliverables:

- SelectionModel, HoverModel, ScrollModel, Viewport, and Scrollbar.
- List, VirtualList, Tree, Table, Menu, Tabs, Breadcrumbs, and Select controls.
- Standard empty, loading, error, disabled, and no-results rows.
- Shared row alignment and column layout.

Exit criteria:

- All collection controls share Page/Home/End and ensure-visible behavior.
- Scrollbar mouse math exists in one implementation.
- Hover, focus, selection, and activation remain distinct states.
- Variable-height virtualization retains current message-list capability.

### Phase 8: Form system 🔄 PARTIAL

Landed early:
- `form.TextItem` uses `ui.TextEdit` + `HandleTextEditKey` (selection/copy/cut/paste path)

Remaining:
- FormModel/FieldState redesign under `tui/ui/form` (or evolved `tui/form`)
- ActionBar standardization
- Session/workspace settings still legacy form shell + lipgloss View
- Question form still specialized widget

Objectives:

- Make settings, questions, confirmations, and structured editors use one field and
  action model.

Deliverables:

- FormModel, FieldState, typed values, validation, dirty/touched state.
- Field label/description/error layout.
- Text, password, select, cycle, toggle, checkbox, navigation, and custom fields.
- ActionBar with primary, secondary, danger, and ghost button variants.
- Nested form pages and inherited-value presentation.

Exit criteria:

- Form controls have keyboard and mouse parity.
- Save, Cancel, Apply, Reset, and destructive actions share controls and layout.
- Field errors, disabled state, inherited state, and read-only state are represented
  consistently.

### Phase 9: Window and overlay unification 🔄 PARTIAL

Landed:
- `WindowFrame` (FrameDialog / FramePopover / FrameWindow roles)
- `ComponentWidget` bridge into FloatingWindow
- `PaintTree` for nested Parent paint without Host
- **First product surface:** tree create/rename prompt via
  `widgets.BuildTextPromptFrame` + `viewmodel` open/refresh paths
- **OverlayHost:** typed `OverlayID` constants, `OverlayPolicy`,
  `OverlayEntry`, `OverlayStack` with `Open`/`Close`/`CloseAll`/`Get`/
  `IsOpen`/`Top`/`All`, `HandleClickOutside` (replaces bespoke
  popupmouse switch), `RegisterContentHandler`/`RegisterHoverHandler`
  typed dispatch, `DispatchContentClick`/`DispatchContentHover`
- Policy helpers: `DefaultOverlayPolicy`, `ModalDialogPolicy`,
  `PopoverPolicy`
- Tests: open/close/replace, click-outside (ignore/close/keep),
  content/hover dispatch, close-all, nil-safety
- **All product popup opens routed through OverlayHost.Open** (dirty-close,
  tree-delete, tree-prompt, context-menu, find-replace, command-palette,
  find-in-files, textoverlay, filepicker, workspace-settings, composer,
  references, completion, outline, diag-context, diag-hover)
- **closePopupWindowByID switch deleted** — now delegates to
  `overlayHost.Close(OverlayID(id))`. All cleanup logic lives in
  OnClose callbacks registered at open time.
- **State gallery** (`tui/ui/demo.go`): renders TextField, Button, List,
  Label, WindowFrame in all visual states at 80x30 under dark/light themes.
- **WM chrome unified:** FloatingWindow content fills the complete WM surface;
  `ui.WindowFrameWidget` mounts structured legacy bodies inside WindowFrame
- WM retains focus, z-order, drag, resize, close/title/edge hit testing only
- `wm/decoration.go`, border/title/close paint tests, BorderStyle, and TextAlign deleted
- All product close paths route through reentrant-safe `OverlayHost.Close`/`CloseAll`

Remaining:
- Tree prompt still rebuilds frame on each keystroke (acceptable interim; prefer mutate field)
- WindowFrame reveal animation before deleting the final ContextMenuLines/Reveal bridge

Objectives:

- Replace PopupChrome, WM decoration, and hand-drawn feature borders with one frame.

Deliverables:

- WindowFrame, Dialog, Popover, ContextMenu, Tooltip, Palette, and Panel roles.
- OverlayHost and focus-scope integration.
- Standard title, close action, content padding, footer, and action bar.
- Anchored, centered, and absolute placement policies.
- Explicit outside-click policy.

Exit criteria:

- A window's content rectangle is calculated once by WindowFrame.
- No migrated feature draws borders or stores close-button coordinates.
- Modal, non-modal, anchored, movable, and resizable examples pass interaction tests.

### Phase 10: Domain seam corrections 🔄 PARTIAL

Landed:
- `workspace.ProjectSearchService` owns recursive project-search request IDs,
  cancellation contexts, execution, completion, and stale-result rejection
- Find in Files emits debounced typed search/cancel intents and applies result data only
- Viewmodel routes intents to workspace start/cancel methods; accepted completion
  arrives only as typed `workspace.ProjectSearchCompletedMsg` through EventBus
- Overlay close and workspace shutdown cancel active project search
- Service, widget, and viewmodel integration tests cover execution and cancellation
- **`workspace.FileOpsService`** owns all filesystem mutations for the workspace:
  `CreateFile`, `CreateFolder`, `RenamePath`, `DeletePath`, `ApplyWorkspaceEdit`
- All operations validate paths (absolute, under workspace root) and publish
  typed `FileOperationResultMsg` through EventBus on success or failure
- **TUI tree operations migrated**: `startNewFileInTree`, `startNewFolderInTree`,
  `startRenameInTree`, `confirmTreeDelete` all delegate to workspace methods
- **LSP rename migrated**: `handleRenameResult` collects edits into `FileEdit`
  structs and calls `ws.ApplyWorkspaceEdit` instead of direct `os.ReadFile`/`WriteFile`
- **Comprehensive tests**: 25+ service tests (create/rename/delete/edit, validation,
  edge cases) + 15+ viewmodel integration tests (routing, EventBus publication,
  validation enforcement)
- **Agent lifecycle boundary:** `AgentManager` is an orchestrator forest rather than a
  single replaceable root; workspace tracks orchestrator-to-session ownership and
  exposes direct confirmation, question, cancellation, and close methods
- Dead per-session TUI callbacks were deleted; stale events no longer manufacture
  channel-less orchestrators; backend orchestrators stop through a private close signal
  without closing channels still owned by scheduled frontend commands
- Usage events update their owning session even when another tab is active; switching
  tabs restores that session's token/cost snapshot
- **First message view-data seam:** persisted hydration is stored as workspace-owned
  `ChatMessageView`; the TUI projects it into `types.ChatMessage`, so
  `workspace/session.go` no longer imports `tui/types` or stores rendering caches;
  hydration data is consumed once so reselecting an open session cannot duplicate it
- **Live message-data seam:** `AgentState` owns workspace `Conversation`/`Message`
  data with no rendering caches, scrolling, selection, or widget methods. The TUI
  projects snapshots into `MessageList` by stable message ID and source version,
  preserving unchanged retained caches and invalidating only changed items
- `workspace/agent_state.go` and `agent_state_events.go` no longer import or own
  `tui/widgets.MessageList`; the rejected callback-style `MessageSink` adapter was removed
- **SubPane seam (this slice):** `SubPane` no longer owns a `*widgets.MessageList`,
  theme reference, scrollbar widget, or any ANSI rendering. `workspace/subpane.go`
  has zero `crude/tui/*` imports. `SubPane.View()` returns a presentation-neutral
  `SubPaneView` (header, tool-lines, summary, Messages pointer). The TUI owns a
  `subPaneProjectionRegistry` that holds the concrete MessageList per pane and
  synchronizes by stable message ID + Conversation version (same pattern as
  `AgentState.Messages`). Compact-agent scroll/cache state moved to the TUI
  projection. `PaneManager` dropped `TickAll`/`SetSize`/`HasAnyScrollbarVisible`/
  `ScrollbarNestingLevel` (those touch TUI state and now live on the registry).
  `NewSubPane` and `PaneManager.GetOrCreate` drop the `*theme.Theme` parameter.
  9 new TUI projection tests + the prior 9 workspace view tests cover the seam.
- **Session presentation seam:** `workspace.Session` retains only
  presentation-neutral `SessionSettingsState`; confirmation, question, YOLO,
  text-overlay, composer, and settings widgets are retained by a TUI-owned
  per-session projection registry keyed by stable `*workspace.Session` identity.
  Settings saves and resets update the domain snapshot and refresh the matching
  projection without sharing interaction state between tabs.
- **Neutral contract extraction:** settings, message/event, and LSP value types
  are owned by `crude/types`; `tui/types` is now a compatibility alias package.
  `session/` and `workspace/` consume the neutral package rather than importing
  `tui/types`.
- **Workspace presentation ownership removal:** workspace no longer owns theme,
  input, status bar, windows, file picker, popup state, file-tree renderer/model,
  buffer manager, editor-tab wrappers, or dialog widgets. These are initialized
  and retained by the TUI host. `TreeState.ViewData()` exposes neutral tree rows;
  `Workspace.AgentTabViews()` and `Workspace.StatusView()` expose neutral tab and
  status snapshots consumed by the TUI.
- **LSP boundary:** `workspace.LSPManager` now owns neutral LSP lifecycle/value
  contracts and receives a runtime/client adapter from `tui/viewmodel`; it no
  longer imports `tui/editor`. Editor LSP value types alias `crude/types`.

Remaining:
- Two legacy workspace test fixtures still import `tui/editor`/`tui/filetree` for
  direct LSP/file-tree integration assertions; production workspace code has zero
  `crude/tui/*` imports. Move those fixtures to TUI-side integration tests before
  claiming the package-boundary exit criterion.
- Full tree control migration and filter-engine cutover remain under Phases 6,
  7, and 13; `TreeState.ViewData()` is the data seam, not a claim that the old
  file-tree control has been deleted.

Objectives:

- Prevent the new component system from preserving current layer violations.

Deliverables:

- View-data types for messages, subpanes, tree rows, tabs, status, settings, and
  launcher data.
- Workspace services for project search and file mutations.
- Typed workspace events consumed by screens.
- Removal plan for all workspace imports of TUI packages.

Exit criteria:

- New screens do not receive workspace pointers.
- New controls do not own domain goroutines or persistence.
- Every new screen action routes through a viewmodel/workspace method boundary.

### Phase 11: Compact-surface migration 🔄 PARTIAL

Order:

1. Tree text prompt. ✅ **WindowFrame** (`BuildTextPromptFrame`); keys still viewmodel
2. Tree delete confirmation. ✅ **WindowFrame + ui.Button** (`BuildTreeDeleteFrame`); Host hit-test mouse
3. Dirty-close prompt. ✅ **WindowFrame + ui.Button** (`BuildDirtyCloseFrame`); Host hit-test mouse
4. Generic confirmation prompt. ✅ **ConfirmPrompt / ConfirmPrompt3Button DELETED**; `ConfirmPromptSeverity` moved to `confirm_frame.go`; `BuildConfirmFrame` is the only path
5. Context menu. ✅ **WindowFrame + ui.List** (`BuildContextMenuFrame`); Host hit-test; **ContextMenuLines static tests migrated to BuildContextMenuFrame cell-buffer assertions** in `contextmenu_frame_test.go` (11 migrated tests); ContextMenuLines/Reveal retained for reveal-animation tests only (pending WindowFrame reveal support)
6. Diagnostic popup. ✅ **WindowFrame + ui.Label + severity accent** (`BuildDiagnosticFrame`); `diag-context` + `diag-hover` both migrated; PopupChromeLines no longer used for diagnostics
7. LSP references. ✅ **WindowFrame + ui.List** (`BuildListPopupFrame`); keys via floating-window dispatch
8. LSP completion. ✅ **WindowFrame + ui.List** (`BuildListPopupFrame`); cursor mutate path
9. LSP outline. ✅ **WindowFrame + ui.List** (depth indent in Icon); cursor mutate path
10. Find and replace. ✅ **WindowFrame (FramePopover) + ui.TextField + Label** (`BuildFindReplaceFrame`); TextEdit bridge syncs from `components.TextInput`; toggle/hint still ANSI bridge
11. Command palette. ✅ **WindowFrame + ui.TextField (search) + ui.List** (`BuildCommandPaletteFrame`); filter via TextEdit; navigation/activate via List; Host hit-test mouse; old View/scroll/hover deleted

Exit criteria:

- Each old implementation is deleted after its replacement lands.
- All compact surfaces use standard text, input, collections, actions, and chrome.
- Popup mouse switches for migrated content are removed rather than retained as a
  second routing layer.

**Remaining for full Phase 11 exit:** delete ContextMenuLines/Reveal when WindowFrame
reveal animation lands; delete find/replace TextEdit bridge
when find/replace switches fully to TextEdit engine.

### Phase 12: Structured-surface migration 🔄 PARTIAL

Order:

1. Workspace settings. PARTIAL (category search TextEdit + RenderFilterLine)
2. Session settings. OPEN (form TextItem engine only)
3. Question form. OPEN
4. Tool confirmation. OPEN
5. YOLO confirmation. OPEN
6. File/workspace picker. PARTIAL (filter TextEdit + RenderFilterLine)
7. Find in Files. PARTIAL (query TextEdit + RenderFilterLine + shared scrollbar cell;
   search execution/cancellation moved to workspace.ProjectSearchService)
8. Composer. OPEN
9. Text/reference editor. OPEN
10. Launcher. PARTIAL (filter TextEdit + RenderFilterLine + shared scrollbar cell)

Exit criteria:

- No migrated screen renders primitive rows or buttons itself.
- Search execution and filesystem operations are outside UI controls.
- Every screen passes theme, resize, keyboard, mouse, paste, and focus tests.

### Phase 13: Persistent-shell and core-view migration

Order:

1. Header and status segments.
2. Contextual hints and shortcut rendering.
3. Main and overlay tabs.
4. File tree and sidebar split pane.
5. Chat input and reference chips.
6. Session picker and async rows.
7. Message list and subpanes.
8. Editor chrome, scrollbar, diagnostics, search paint, and status integration.

The editor PieceTable and core document operations remain specialized. Their visual
chrome and shared interaction infrastructure migrate.

Exit criteria:

- Every persistent surface is mounted in the component tree.
- Message-list virtualization and editor performance meet recorded baselines.
- The manual `contentDirty` cache is removed only after equivalent subtree
  invalidation is verified.

### Phase 14: Package-boundary completion and deletion

Objectives:

- Remove the hybrid architecture and all superseded UI packages.

Deliverables:

- `workspace/` free of `tui/` imports.
- Feature screens under `tui/screens`.
- Deletion of old component, form, scrollbar, popup-chrome, and tab-render paths.
- Removal of legacy adapters whose consumers reached zero.
- Updated `AGENTS.md`, `PLAN.md`, and architecture documentation.

Exit criteria:

- There is no parallel widget system.
- The bypass allowlist is empty.
- Dependency checks enforce the three-layer architecture.

### Phase 15: Stabilization and permanent enforcement

Deliverables:

- Cell-buffer visual goldens for every gallery state and product screen.
- Keyboard/mouse/paste interaction contract tests.
- Fuzz tests for text editing, layout, scrolling, and geometry transforms.
- Race tests for TUI and workspace event boundaries.
- Performance and allocation benchmarks.
- Root lint configuration and CI workflow.
- Component authoring and design-language documentation.

Exit criteria:

- `go build ./...` passes.
- `go test ./...` passes apart from explicitly documented external/pre-existing cases.
- TUI race suites pass.
- Static design-system and dependency checks pass with no allowlist.
- Runtime theme switching passes all gallery and product-screen tests.

## 20. Surface Migration Ledger

| Current surface | Target composition | Phase | Progress |
|-----------------|--------------------|-------|----------|
| Command palette | Palette + SearchField + sectioned VirtualList | 11 | ✅ **WindowFrame + TextField + List** (`BuildCommandPaletteFrame`); TextEdit filter; Host mouse |
| Find/replace | Popover + TextField(s) + ToggleButton group + Hint | 11 | ✅ **WindowFrame FramePopover + TextField + Label** (`BuildFindReplaceFrame`); TextEdit bridge |
| Context menu | ContextMenu + Menu sections + Actions | 11 | ✅ **WindowFrame + ui.List** (headers/separators/disabled); static tests migrated to cell-buffer assertions |
| LSP popups | Popover + List/Table + actions | 11 | ✅ **refs + completion + outline** WindowFrame+List; no hand-drawn ╭─╮ |
| Tree text prompt | Dialog + PromptField + ActionBar | 11 | ✅ **WindowFrame + TextField + TextEdit** |
| Dirty/tree delete prompts | Dialog + ActionBar | 11 | ✅ **WindowFrame + ui.Button** (`BuildDirtyCloseFrame`/`BuildTreeDeleteFrame`); ConfirmPrompt/3Button **DELETED** |
| Diagnostic popup | Popover + Label + severity accent | 11 | ✅ **WindowFrame + ui.Label + severity border/title accent** (`BuildDiagnosticFrame`); `diag-context` + `diag-hover` both migrated |
| Workspace settings | Window + navigation list + Form + ActionBar | 12 | **WindowFrameWidget + OverlayHost**; search TextEdit + RenderFilterLine; legacy body |
| Session settings | Panel/Dialog + nested Form pages | 12 | TextItem TextEdit only |
| Question form | Form + option collection + TextField + ActionBar | 12 | open |
| YOLO/confirmation | Dialog + severity content + ActionBar | 12 | open |
| File picker | Dialog + Breadcrumbs + SearchField + Tree/List + ActionBar | 12 | **WindowFrameWidget + OverlayHost**; filter TextEdit + RenderFilterLine; legacy body |
| Find in Files | Window + SearchField + toggles + VirtualList + editor preview | 12 | **WindowFrameWidget + OverlayHost**; TextEdit + shared bridges; domain execution/cancellation in workspace service |
| Composer | Window + TextArea + ActionBar/Hint | 12 | **WindowFrameWidget + OverlayHost**; legacy ANSI body |
| Text overlay | Window + TextArea + ActionBar | 12 | **WindowFrameWidget + OverlayHost**; legacy ANSI body |
| Launcher | Responsive layout + actions + SearchField + workspace collection | 12 | filter TextEdit + RenderFilterLine + scrollbar ANSI |
| Header | Row layout + text/status primitives | 13 | open |
| Status bar | Segmented row + action-derived hints | 13 | `Workspace.StatusView()` supplies neutral durable status/usage data; shell migration open |
| Tabs | Tabs control + close actions + status indicators | 13 | `Workspace.AgentTabViews()` supplies neutral agent tab data; component migration open |
| File tree | Tree + Viewport + Scrollbar + SplitPane | 13 | `TreeState.ViewData()` exposes neutral `TreeRowView`; filter string still on TUI model |
| Chat input | TextArea + reference chips + action hints | 13 | still components.TextInput |
| Message list | VirtualList + message delegates + selection | 13 | Persisted hydration uses `ChatMessageView`; live data uses workspace `Conversation`; retained MessageList projection preserves caches by stable ID/version |
| Subpanes | Panel + VirtualList + nested viewport | 13 | `SubPane.Messages` is canonical data; TUI owns `*widgets.MessageList` projection via `subPaneProjectionRegistry` keyed by paneID + Conversation version (same pattern as `AgentState.Messages`); `workspace/subpane.go` has ZERO `tui/` imports; `SubPane.View()` returns presentation-neutral `SubPaneView` (header/tool-lines/summary/Messages pointer) |
| Editor chrome | DocumentEditor adapters + standard scrollbar/status/actions | 13 | open |

## 21. Test Strategy

### 21.1 Unit tests

Unit tests cover models and deterministic behavior:

- Theme resolution and state precedence.
- Constraints and layout algorithms.
- Focus traversal and restoration.
- Action and keymap resolution.
- Text editing and selection.
- Selection and scroll models.
- Scrollbar geometry.
- Form dirty state and validation.
- Overlay placement and outside-click policy.

### 21.2 Cell-buffer visual tests

Visual assertions compare cells after painting, including rune, width, foreground,
background, attributes, and transparency. ANSI output tests remain at compositor
boundaries only.

### 21.3 Interaction contracts

Each interactive control has table-driven tests for:

- Keyboard focus and navigation.
- Mouse hover, click, double-click where supported, and drag.
- Paste and clipboard where supported.
- Disabled and read-only behavior.
- Resize and clipping.
- Theme change while focused or editing.
- Modal focus and close behavior where applicable.

### 21.4 Property and fuzz tests

Priority fuzz targets:

- Unicode text insertion, deletion, cursor movement, and selection.
- Width-constrained text painting.
- Nested local/screen coordinate transforms.
- Scroll offset and thumb mapping.
- Layout under arbitrary valid constraints.
- Popup clamping at all screen edges.

### 21.5 Product-screen tests

Each screen is tested at:

- Its documented minimum dimensions.
- `80x24`.
- `120x40`.
- A wide terminal.
- Every reference theme.
- Empty, loading, error, populated, focused, and disabled states as applicable.

## 22. Performance Requirements

Phase 0 records baselines before APIs are implemented. Final numeric budgets are based
on those measurements rather than guessed values.

Required behavior regardless of final numbers:

- Hover invalidates only old and new hover targets.
- Cursor blink does not repaint unrelated screens.
- Scrolling reuses unchanged retained item surfaces.
- Agent streaming invalidates the active message item, not the entire application.
- Moving a window damages old and new bounds without rebuilding unrelated content.
- Theme color changes repaint without discarding input, scroll, selection, or focus.
- Editor Chroma work is not repeated for unrelated UI events.
- Component ID and surface reuse prevent per-frame tree allocation growth.

Benchmarks cover standard, wide, and worst-case populated screens. A phase cannot ship
with an unexplained regression against the latest accepted baseline.

## 23. Migration Discipline

Every migration change follows this sequence:

1. Record the existing behavior and known defects in the manifest.
2. Add behavior and visual contract tests.
3. Implement or extend the shared component.
4. Migrate the real surface.
5. Delete the old implementation.
6. Remove its bypass-manifest entries.
7. Run full build, tests, race tests where relevant, visual tests, and benchmarks.

The target architecture is designed before migration, but public APIs are frozen only
after they have been exercised by multiple real consumers. This avoids replacing
feature duplication with an abstract but unusable framework.

Adapters are one-way migration tools. New screens cannot depend on legacy adapters,
and an adapter with no consumers is deleted immediately.

## 24. Risks and Mitigations

| Risk | Mitigation |
|------|------------|
| Big-bang instability | Keep the application runnable at each phase; migrate by surface |
| Permanent dual systems | Checked manifest, consumer counts, explicit deletion gates |
| Framework over-generalization | Validate APIs against multiple existing consumers before freeze |
| Performance regression | Baselines, retained surfaces, subtree invalidation, benchmarks |
| Width regression | Preserve canonical width path and patched dependencies |
| Focus/mouse regressions | Geometry-derived hit tree and interaction contract tests |
| Theme only works for dark mode | Light and high-contrast themes required from Phase 3 |
| Domain leaks into new controls | Domain-seam phase before large screen migration |
| Visual snapshots become brittle | Assert normalized cell buffers and semantic regions |
| Editor/chat complexity stalls migration | Migrate shell and shared infrastructure before specialized internals |

## 25. Definition of Done

This program is complete only when all statements are true:

- Every user-visible surface is in the component tree.
- Every interactive element has a stable ID, semantics, focus behavior, and action.
- Every editable non-document value uses the shared input engine.
- Every collection uses shared selection, viewport, and scrolling infrastructure.
- Every scrollbar uses the standard Scrollbar control.
- Every form uses shared fields, validation, and action bars.
- Every floating surface uses the unified WindowFrame and OverlayHost.
- Every shortcut label derives from the action registry.
- Every color, glyph, metric, and state style resolves through the active theme.
- Runtime theme switching preserves state and updates the full UI.
- Feature screens perform no primitive painting or feature-local hit testing.
- Workspace performs no rendering and imports no TUI packages.
- Components perform no persistence or domain operations.
- Old UI packages and migration adapters are removed.
- The bypass manifest is empty and CI prevents recurrence.
- Build, test, race, visual, fuzz, static, and performance gates pass.

## 26. Immediate Execution Order

### Completed foundation blocks

1. Phase 0 artifacts + `scripts/tui_design_audit.sh` + canonical visual and numeric performance baselines.
2. Phase 1 cell/surface/compositor/scrollbar tests.
3. Phase 2 `tui/ui` Host + first controls.
4. Early Phase 6/7/9 foundations: TextEdit, TextField, List, VirtualList, WindowFrame,
   ScrollbarControl, ComponentWidget, filter/scrollbar ANSI bridges.
5. Product filter engines on TextEdit (palette, launcher, settings, filepicker, list,
   form TextItem, FIF query) + RenderFilterLine paint convergence.
6. WindowFrame product surfaces: tree prompt, dirty-close, tree-delete
   (`BuildTextPromptFrame` / `BuildDirtyCloseFrame` / `BuildTreeDeleteFrame`).
7. Context menu → `BuildContextMenuFrame` (WindowFrame + ui.List with section
   headers/separators/disabled); product path no longer uses PopupChrome.
8. LSP references → `BuildListPopupFrame` (WindowFrame + ui.List).
9. LSP completion + outline → `BuildListPopupFrame`; hand-drawn ╭─╮ deleted for all
   three LSP list popups; floating-window key routing + list cursor mutate.
10. ui.List: header/separator/disabled rows + wrap + skip navigation.
11. Command palette → `BuildCommandPaletteFrame` (WindowFrame + TextField + List);
    old View/scroll/hover path deleted; Host mouse routing.
12. Find/replace → `BuildFindReplaceFrame` (WindowFrame FramePopover + TextField +
    Label); TextEdit bridge syncs from components.TextInput; toggle/hint ANSI bridge.
13. Diagnostic popup → `BuildDiagnosticFrame` (WindowFrame + Label + severity
    border/title accent); `diag-context` + `diag-hover` both migrated off
    PopupChromeLines.
14. Phase 11 closeout: `ConfirmPrompt`/`ConfirmPrompt3Button` DELETED;
    `ConfirmPromptSeverity` moved to `confirm_frame.go`. ContextMenuLines
    static tests migrated to `BuildContextMenuFrame` cell-buffer assertions
    (`contextmenu_frame_test.go`, 11 tests). ContextMenuLines/Reveal retained
    for reveal-animation tests only.
15. Phase 9 OverlayHost: typed `OverlayID`, `OverlayPolicy`, `OverlayEntry`,
    `OverlayStack` (`Open`/`Close`/`CloseAll`/`Get`/`IsOpen`/`Top`/`All`),
    `HandleClickOutside`, `RegisterContentHandler`/`RegisterHoverHandler`,
    `DispatchContentClick`/`DispatchContentHover`; 11 tests.
16. Phase 5 ActionRegistry: `ActionID`, `ActionDef`, `KeyBinding`,
    `MatchGlobal`/`MatchContext`, `HintFor`, `FormatShortcut`,
    `DefaultRegistry` with all global actions + palette metadata; 12 tests.

17. Phase 5/9 wiring this session:
    - keyboard.go global switch replaced with registry.MatchGlobal
    - command palette entries derived from DefaultRegistry().All() (paletteCommands deleted)
    - launcher shortcuts from registry (welcomeShortcuts deleted)
    - status-bar hints from registry.HintFor
    - overlayHost + actionRegistry on App struct
    - per-overlay content/hover handlers registered on overlayHost
    - popupmouse outside-click/content/hover dispatch routed through overlayHost first

18. Phase 9 wiring completed:
    - All 16 product popup opens routed through OverlayHost.Open:
      dirty-close, tree-delete, tree-prompt, context-menu, find-replace,
      command-palette, find-in-files, textoverlay, filepicker,
      workspace-settings, composer, references, completion, outline,
      diag-context, diag-hover
    - closePopupWindowByID switch deleted; now delegates to
      overlayHost.Close(OverlayID(id))
    - All close functions (closeContextMenu, closeFindReplace, closeTreePrompt,
      closeReferencesPopup, closeCompletionPopup, closeOutlinePopup,
      closeFindInFiles, clearDiagHoverPopup) delegate to overlayHost.Close
    - Cleanup logic moved to OnClose callbacks registered at open time
    - Test helpers updated: newTestApp/newFullTestApp create overlayHost;
      setupTestAppWithPopup uses overlayHost.Open; all test defers use
      overlayHost.Close

19. Phase 3 state gallery:
    - DemoGallery expanded: TextField (default/focused/placeholder/disabled),
      Button (default/disabled), List (cursor=0/selected=2), Label
      (default/accent/muted/error/success), WindowFrame (dialog/popover/window)
    - Renders at 80x30 under dark and light themes
    - Tests verify paint/edit interaction and state-preserving runtime theme switch

20. Phase 9 WM chrome unification:
    - FloatingWindow no longer draws borders, titles, backgrounds, or close glyphs
    - `wm/decoration.go` and parallel decoration tests deleted
    - WindowFrameWidget mounts structured legacy bodies inside unified WindowFrame
    - File picker, workspace settings, Find in Files, composer, and text overlay
      migrated to WindowFrame visual chrome
    - WM retains frame-aligned close/title/edge hit testing, drag, resize, focus,
      placement, and z-order
    - BorderStyle/TextAlign visual APIs removed from WM
    - All product close paths now use reentrant-safe OverlayHost.Close/CloseAll

21. Phase 3 light reference theme:
    - `theme.DefaultLight` + `ui.LightTheme`
    - Shared semantic style factory for dark/light, removing theme-specific style literals
    - Gallery paint/edit tests under both themes
    - Runtime dark-to-light switch verifies token replacement and input-state preservation

22. Phase 10 first domain seam:
    - `workspace.ProjectSearchService` owns Find in Files execution, cancellation,
      request identity, and stale-result rejection
    - Widget owns display/query state and emits typed search/cancel intents only
    - Viewmodel starts/cancels through workspace methods and applies typed completion
      events published through the workspace EventBus

23. Phase 0 visual baselines:
    - `tui/ui/visual_baseline_test.go`: 80x24, 80x30, 120x40, 200x50 under dark/light
    - Runtime theme switch verification (token replacement + input state preservation)
    - WindowFrame: close glyph, border cells, content fill, no transparency in content area
    - TextField: cursor block, placeholder rendering
    - List: selected row visual difference
    - Button: disabled visual difference
    - Label: color role distinctness (accent/muted/error/success)
    - Ellipsis truncation, continuation cell integrity
    - Render and key event allocation benchmarks

24. Phase 10 filesystem mutation seam:
    - `workspace.FileOpsService` with `CreateFile`, `CreateFolder`, `RenamePath`,
      `DeletePath`, `ApplyWorkspaceEdit` methods
    - Path validation (absolute, under workspace root, no escape)
    - Typed `FileOperationResultMsg` published through EventBus for all operations
    - `FileEdit` struct for workspace edits with content verification
    - TUI tree operations migrated: `startNewFileInTree`, `startNewFolderInTree`,
      `startRenameInTree`, `confirmTreeDelete`
    - LSP rename migrated: `handleRenameResult` collects edits, calls `ApplyWorkspaceEdit`
    - 25+ service tests + 15+ viewmodel integration tests

25. Phase 0 numeric performance closeout:
    - `tui/ui/benchmark_test.go` contains real gallery paint and TextEdit event benchmarks
    - `docs/tui/PERFORMANCE_BASELINES.md` records five-sample timings, allocations,
      review ceilings, environment, reproduction command, and comparison rules

26. Agent lifecycle and reachability correction:
    - AgentManager supports multiple orchestrator roots without orphaning prior sessions
    - Workspace owns orchestrator-to-session routing for resolve/cancel/close methods
    - Background usage is recorded against its owning session and restored on tab switch
    - Session callbacks for confirmation/question/sub-agent cancellation/reset deleted
    - Safe private orchestrator stop signal replaces closing externally-owned input channels
    - Unknown late events are dropped; nil completion paths and context-menu cancellation fixed

27. Phase 10 persisted-message view-data seam:
    - Workspace stores hydration as presentation-neutral `ChatMessageView`
    - TUI boundary projects views into frontend `types.ChatMessage` values
    - `workspace/session.go` no longer imports `tui/types`; hydration snapshots are one-shot

28. Phase 10 live-message Conversation seam:
    - `workspace.Conversation` is the canonical mutable live-message store;
      `workspace.Message` contains domain/view data but no TUI caches or interaction state
    - `AgentState.Messages` replaces concrete `*widgets.MessageList` ownership
    - The callback-style `MessageSink` experiment was removed rather than retained as
      a widget-shaped interface
    - TUI projection converts snapshots to `types.ChatMessage` and synchronizes
      `MessageList` by stable ID/version, retaining unchanged caches and scroll state
    - All agent/user/help/hydration append paths update Conversation first
    - Tests cover streaming, tool lifecycle, cancellation, immutable snapshots,
      presentation-field exclusion, retained-cache preservation, item invalidation,
      and removal synchronization

### Next slices (priority order — stay on plan)

   1. **Phase 10:** ✅ workspace presentation ownership and view-data seam slice.
      `workspace/` production code has zero `tui/` imports; session dialog state,
      neutral settings/LSP contracts, editor-tab ownership, tree rows, agent tabs,
      and status snapshots now cross through TUI projections. NEXT: move the two
      remaining workspace test fixtures to TUI-side integration tests, then proceed
      to the structured-surface migration.
  2. **Phase 12:** structured-surface migration (settings, question form, YOLO,
     picker, Find in Files, composer, text overlay, launcher). Use FileOpsService
     for any file picker filesystem operations.
  3. **Phase 3 closeout:** high-contrast reference theme, metric/glyph token tables,
     and live config hot-reload.
  4. **Phase 13:** persistent-shell and core-view migration.
  5. **Phase 14:** package-boundary completion and deletion.
  6. **Phase 15:** stabilization and permanent enforcement.

### Anti-drift checks

- Do not invent a third chrome system.
- Do not add new `filter string` key handlers; use TextEdit.
- Do not add new scrollbar glyph math; use ScrollModel + ScrollbarControl/CellANSI.
- Do not claim a surface migrated until its old implementation is deleted or listed
  as a shrinking bypass with a removal phase.
- Prefer mutating WindowFrame content over full rebuild on every keystroke once
  Host event routing owns the surface.
