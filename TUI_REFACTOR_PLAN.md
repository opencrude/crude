# TUI Refactoring Plan

## Problem Statement

The current `tui/` package is a monolith (~6200 lines) with severe maintainability issues:

- **God Model**: `Model` struct has ~40 fields mixing chat, input, agent, settings, selection, scroll, history, and error state
- **Monolithic Update**: `update.go` (1470 lines) — one giant switch handling every message type
- **Monolithic View**: `view.go` (1421 lines) — deeply nested rendering with inline styles
- **Duplicated patterns**: model/oracle model select are near-identical; confirm/yolo forms duplicate button navigation; provider select repeats list logic
- **No reusable widgets**: everything hand-rolled inline — no text input, list, button, or form primitives
- **Styles scattered**: colors in `styles.go` but also hardcoded inline everywhere (`#1a1a2e`, `#1e2a3a`, `#ff0000`, etc.)
- **No theme system**: no consistent typography, spacing, or design tokens
- **Cross-cutting concerns**: agent event handling interleaved with UI logic

The old code is preserved in `_tuiback/` for reference during migration.

## Design Principles

1. **Maximum decomposition** — each component owns its data, update, and view
2. **Reusable atomic widgets** — text input, list, buttons, form, dialog, spinner
3. **Consistent design language** — centralized theme with colors, spacing, typography, pre-built styles
4. **Go native + Bubble Tea friendly** — value receiver `Update` returning `(Self, tea.Cmd)`, no `tea.Model` interface on widgets, no channels for cross-component communication
5. **No repetition** — one implementation of each pattern (list, button group, form field nav)
6. **Separation of concerns** — chat, input, settings, statusbar are independent components; app routes between them
7. **No backward compatibility requirement** — clean break; external API (`tui.Settings`, `tui.TUI`, etc.) stays identical

## Public API Contract (MUST NOT CHANGE)

These types and functions are used by `main.go` and `session/session.go`. They must remain in the `tui` package root with identical signatures:

```go
// tui/ package root — consumed by main.go and session/session.go

type Settings struct {
    APIKey                   string
    Model                    string
    ModelContextWindow       int
    MainInputPrice           float64
    MainOutputPrice          float64
    MainCacheReadPrice       float64
    OracleModel              string
    OracleModelContextWindow int
    OracleInputPrice         float64
    OracleOutputPrice        float64
    OracleCacheReadPrice     float64
    ProviderID               string
    ProviderURL              string
    ProviderAPIKey           string
    ThinkingEffort           string
}

type ModelEntry struct {
    ID             string
    Name           string
    ContextWindow  int
    InputPrice     float64
    OutputPrice    float64
    CacheReadPrice float64
}

type ProviderConfig struct {
    APIKey         string
    Model          string
    OracleModel    string
    ProviderURL    string
    ThinkingEffort string
}

type ProviderPreset struct {
    ID       string
    Name     string
    HelpText string
    BaseURL  string
}

type ConfirmResult struct {
    Choice    string
    Operation string
    Tool      string
    Target    string
}

const (
    ConfirmAllow  = "allow"
    ConfirmAlways = "always"
    ConfirmDeny   = "deny"
)

type UndoResultMsg struct {
    Desc  string
    Error string
}

type TUI struct {
    Program            *tea.Program
    InputCh            <-chan string
    CancelCh           chan struct{}
    OnQuit             func()
    OnExitMessage      func()
    OnSettingsSave     func(Settings)
    OnConfirmResult    func(ConfirmResult)
    OnYoloCmd          func(enabled bool)
    OnUndoCmd          func(cmd string)
    FetchModelsFn      func(providerID, baseURL, apiKey string) ([]ModelEntry, error)
    GetProviderConfigFn func(providerID string) ProviderConfig
    ProviderPresets    []ProviderPreset
}

func Start(modelName, username string, initial Settings, presets []ProviderPreset, history []string) (*TUI, error)
func (t *TUI) Stop()
func (t *TUI) Wait()
func (t *TUI) ReleaseTerminal()
func (t *TUI) SendAgentEvent(typ string, data any)
func (t *TUI) SetError(err string)
func (t *TUI) UpdateModelName(name string)
func (t *TUI) OpenSettings()
func GetGlobal() *TUI

var OnHistorySave func([]string)
```

## Target Directory Structure

```
tui/
├── app.go              # Composition root, mode routing, agent event dispatch
├── theme.go            # Theme struct, colors, spacing, pre-built styles, default dark
├── types.go            # Settings, ModelEntry, ProviderConfig, ProviderPreset, UndoResultMsg
├── messages.go         # Internal tea.Msg types, slash commands, format helpers
├── statusbar.go        # StatusBar rendering (app-coupled, thin)
├── confirm.go          # Confirm dialog (uses widget.Dialog)
├── yolo.go             # Yolo dialog (uses widget.Dialog)
│
├── widget/             # Reusable UI primitives — no app-specific knowledge
│   ├── theme.go        #   Theme struct + DefaultDark() + Colors/Spacing/Styles types
│   ├── textinput.go    #   Multiline input: buffer, cursor, selection, word nav
│   ├── list.go         #   Filtered, paginated, scrollable list with search
│   ├── buttons.go      #   Button + ButtonGroup (focus nav, shortcut keys)
│   ├── form.go         #   Field container: tab/arrow nav, edit mode, save/cancel
│   ├── dialog.go       #   Modal overlay: title, icon, body, button group
│   ├── spinner.go      #   Phase-aware spinner with per-phase colors
│   ├── selection.go    #   ANSI-aware text selection + clipboard helpers
│   └── util.go         #   wrapText, hardWrap, wordWrap, truncateLine, findSegmentStart
│
├── chat/               # Chat area — messages, scroll, rendering
│   ├── chat.go         #   Model: messages slice, scroll, selection, size
│   ├── render.go       #   renderAllMessages, message labels, diff rendering, tool labels
│   └── message.go      #   ChatMessage type, role constants
│
├── input/              # Input area — text entry + history + slash autocomplete
│   └── input.go        #   Model wrapping widget.TextInput + history + slash list
│
├── settings/           # Settings overlay — form + provider/model select
│   ├── settings.go     #   Model: form, mode switching, snapshot/restore
│   └── modelselect.go  #   Model selection logic (shared for main + oracle)
│
├── diffview/           # Keep as-is (already well-decomposed)
│   ├── diffview.go
│   ├── style.go
│   ├── split.go
│   └── util.go
│
└── xchroma/            # Keep as-is
    └── chroma.go
```

## Dependency Graph (no cycles)

```
tui/app.go ──→ tui/chat/ ──→ tui/widget/
     │              ↑               ↑
     ├──→ tui/input/ ──────────────┘
     ├──→ tui/settings/ ───────────┘
     │
     ├──→ tui/confirm.go ─────────┘  (uses widget.Dialog)
     ├──→ tui/yolo.go ────────────┘  (uses widget.Dialog)
     └──→ tui/statusbar.go ──────┘   (uses widget.Spinner)
```

- `tui/` imports `chat/`, `input/`, `settings/`, `widget/`
- Nothing imports `tui/` — no cycles
- `chat/` and `input/` never import each other — communicate through app via typed events
- `widget/` imports nothing from `tui/` — fully self-contained

## Implementation Sessions

Each session is self-contained. The "Prerequisites" field tells you which sessions must be done first. The "Verification" section tells you how to confirm it works. All old code is in `_tuiback/` for reference.

---

### Session 1: `tui/types.go` — Shared Types

**Prerequisites**: None
**Files created**: `tui/types.go`
**Lines**: ~150

Move all cross-cutting types into `tui/types.go`. These are the types that both the tui package root and its sub-packages need. The sub-packages will import `crude/tui/types` but the root `tui` package will re-export them via type aliases or direct declarations.

However, since Go doesn't allow circular imports, the types package must be separate. We use `tui/types` as a sub-package.

**Actually** — to keep the public API identical (types live in `tui` package root, not `tui/types`), we declare them directly in `tui/types.go` (package `tui`) and have sub-packages accept interfaces or duplicate the small structs. But that creates duplication.

**Better approach**: Put shared types in a separate `tui/types` package. In the `tui` root, use type aliases:

```go
// tui/types.go  (package tui)
package tui

import ttypes "crude/tui/types"

type Settings = ttypes.Settings
type ModelEntry = ttypes.ModelEntry
type ProviderConfig = ttypes.ProviderConfig
type ProviderPreset = ttypes.ProviderPreset
type ConfirmResult = ttypes.ConfirmResult
type UndoResultMsg = ttypes.UndoResultMsg
```

This way all consumers (`main.go`, `session/session.go`) still use `tui.Settings` etc., while sub-packages import `crude/tui/types`.

**File: `tui/types/types.go`** (package `types`):

```go
package types

import "time"

const (
    ToolPreviewPreparing = "preparing"
    ToolPreviewStreaming = "streaming"
    ToolPreviewDone      = "done"
    ToolPreviewError     = "error"
)

const (
    RoleUser      = "user"
    RoleAssistant = "assistant"
    RoleThinking  = "thinking"
    RoleTool      = "tool"
    RoleError     = "error"
)

const (
    ConfirmAllow  = "allow"
    ConfirmAlways = "always"
    ConfirmDeny   = "deny"
)

type Settings struct {
    APIKey                   string
    Model                    string
    ModelContextWindow       int
    MainInputPrice           float64
    MainOutputPrice          float64
    MainCacheReadPrice       float64
    OracleModel              string
    OracleModelContextWindow int
    OracleInputPrice         float64
    OracleOutputPrice        float64
    OracleCacheReadPrice     float64
    ProviderID               string
    ProviderURL              string
    ProviderAPIKey           string
    ThinkingEffort           string
}

type ModelEntry struct {
    ID             string
    Name           string
    ContextWindow  int
    InputPrice     float64
    OutputPrice    float64
    CacheReadPrice float64
}

type ProviderConfig struct {
    APIKey         string
    Model          string
    OracleModel    string
    ProviderURL    string
    ThinkingEffort string
}

type ProviderPreset struct {
    ID       string
    Name     string
    HelpText string
    BaseURL  string
}

type ConfirmResult struct {
    Choice    string
    Operation string
    Tool      string
    Target    string
}

type UndoResultMsg struct {
    Desc  string
    Error string
}

type ChatMessage struct {
    Role    string
    Content string
    Time    time.Time

    ToolName   string
    ToolArgs   string
    ToolID     string
    ToolStatus string
    ToolIndex  int

    ToolProgressPhase   string
    ToolProgressMessage string
    ToolProgressLabel   string

    ToolPreviewPhase string
    ToolEstTokens    int
    ToolLivedAdded   int
    ToolLivedRemoved int
    ToolStartTime    time.Time
    ToolDuration     time.Duration

    ThinkingActive bool

    CachedLines []string
    CursorLine  int
}

type AgentEventMsg struct {
    Type string
    Data any
}

// Internal Bubble Tea messages — shared across sub-packages

type TickMsg time.Time
type StatusMsg string
type ErrorMsg string

type AgentMsg AgentEventMsg

type OpenSettingsMsg struct{}

type FetchModelsMsg struct {
    ProviderID string
    BaseURL    string
    APIKey     string
}

type SaveSettingsMsg Settings

type UpdateModelMsg string

type ModelsListMsg []ModelEntry
type ModelsErrorMsg string

type OracleModelsListMsg []ModelEntry
type OracleModelsErrorMsg string

type SelectionTickMsg time.Time

type ClipboardCopiedMsg struct{}
type ClipboardErrorMsg string
```

**File: `tui/types.go`** (package `tui`):

```go
package tui

import ttypes "crude/tui/types"

type Settings = ttypes.Settings
type ModelEntry = ttypes.ModelEntry
type ProviderConfig = ttypes.ProviderConfig
type ProviderPreset = ttypes.ProviderPreset
type ConfirmResult = ttypes.ConfirmResult
type UndoResultMsg = ttypes.UndoResultMsg
```

**Verification**: `go build ./tui/types/` compiles.

---

### Session 2: `tui/widget/theme.go` — Theme System

**Prerequisites**: None (widget package is self-contained)
**Files created**: `tui/widget/theme.go`
**Lines**: ~150

Move all colors, spacing, and pre-built styles from `_tuiback/styles.go` into a centralized theme. Replace all inline `lipgloss.NewStyle()` calls throughout the codebase with references to `Theme.Styles.*`.

```go
package widget

import "github.com/charmbracelet/lipgloss"

type Theme struct {
    Colors  Colors
    Spacing Spacing
    Styles  Styles
}

type Colors struct {
    Bg, Fg, Muted, Accent, Accent2       lipgloss.Color
    Success, Error, Warning, Border       lipgloss.Color
    Selection                            lipgloss.Color
    Code       CodeColors
    Spinner    SpinnerColors
    Diff       DiffColors
}

type CodeColors struct {
    Keyword, String, Number, Comment    lipgloss.Color
    Function, Type, Operator, Bg, Border lipgloss.Color
}

type SpinnerColors struct {
    Grounding, Planning, Executing, Default, Done, Error lipgloss.Color
}

type DiffColors struct {
    File, Hunk, Context, Add, Delete lipgloss.Color
}

type Spacing struct {
    PadLR       int
    InputPadLR  int
    FieldGap    int
    ButtonGap   int
    DialogPad   int
}

type Styles struct {
    Header, Separator, DimLabel, AccentLabel    lipgloss.Style
    Body, Button, ButtonFocus, DialogBg         lipgloss.Style
    CodeBlock, ToolActive, ToolDone, ToolError  lipgloss.Style
    ThinkingLine, UserLabel, AsstLabel          lipgloss.Style
    ErrorText, StatusBar, InputPrompt, InputText lipgloss.Style
}

func DefaultDark() *Theme {
    // Map all colors from _tuiback/styles.go:
    // colorBg="#0d1117", colorFg="#e6edf3", colorMuted="#8b949e",
    // colorAccent="#f0883e", colorAccent2="#a5d6ff", colorSuccess="#7ee787",
    // colorError="#ff7b72", colorWarning="#d29922"
    // + code colors, spinner colors, diff colors
    // + spacing: padLR=2
    // + all pre-built styles (Header, Separator, etc.)
}
```

**Key color mapping from `_tuiback/styles.go`**:
- `colorBg` → `Colors.Bg` = `#0d1117`
- `colorFg` → `Colors.Fg` = `#e6edf3`
- `colorMainText` → `Colors.Fg` = `#e6edf3` (same as Fg)
- `colorMuted` → `Colors.Muted` = `#8b949e`
- `colorAccent` → `Colors.Accent` = `#f0883e`
- `colorAccent2` → `Colors.Accent2` = `#a5d6ff`
- `colorSuccess` → `Colors.Success` = `#7ee787`
- `colorError` → `Colors.Error` = `#ff7b72`
- `colorWarning` → `Colors.Warning` = `#d29922`
- `spinnerColorGrounding` → `Spinner.Grounding` = `#f0883e`
- `spinnerColorPlanning` → `Spinner.Planning` = `#a5d6ff`
- `spinnerColorExecuting` → `Spinner.Executing` = `#7ee787`
- `spinnerColorDefault` → `Spinner.Default` = `#8b949e`
- `spinnerColorDone` → `Spinner.Done` = `#7ee787`
- `spinnerColorError` → `Spinner.Error` = `#ff7b72`

**Inline colors to capture** (found in `_tuiback/view.go` and `_tuiback/confirm_form.go`):
- `#333333` — separator lines
- `#1a1a2e` — dialog background (confirm, yolo)
- `#1e2a3a` — list highlight background (slash autocomplete, model select)
- `#ff0000` — yolo danger text, blocked risk
- `#2a2a3a` — label background (user/asst labels)
- `#555555` — thinking border
- `#888888` — thinking text
- `#d0681e`, `#b85010`, `#f0883e` — thinking dot animation
- `#d4a017` — project-write risk

**Verification**: `go build ./tui/widget/` compiles.

---

### Session 3: `tui/widget/util.go` — Text Utilities

**Prerequisites**: Session 2 (needs theme for PadLR)
**Files created**: `tui/widget/util.go`
**Lines**: ~120

Extract from `_tuiback/view.go`:

| Function | Source lines |
|---|---|
| `WrapText(text string, width int) []string` | view.go:814-844 |
| `WordWrap(s string, maxWidth int) []string` | view.go:884-946 |
| `HardWrap(s string, maxWidth int) []string` | view.go:857-878 |
| `TruncateLine(s string, max int) string` | view.go:1128-1133 |
| `FindSegmentStart(logLine []rune, textW, segIdx int) int` | view.go:1061-1126 |

Also the `max()` helper (view.go:806-811) — can use the builtin `max` in Go 1.21+ or keep as utility.

**Verification**: `go build ./tui/widget/` compiles. Write a simple test that `WrapText("hello world", 5)` returns expected output.

---

### Session 4: `tui/widget/selection.go` — ANSI Selection Helpers

**Prerequisites**: None
**Files created**: `tui/widget/selection.go`
**Lines**: ~250

Extract from `_tuiback/selection.go`:

| Function | Source lines |
|---|---|
| `TrimRightAnsiSpaces(s string) string` | selection.go:16-49 |
| `StripAnsi(s string) string` | selection.go:51-86 |
| `ApplySelection(line string, startCol, endCol int) string` | selection.go:88-167 (was `applySelectionReverse`) |
| `ExtractVisibleRange(s string, startCol, endCol int) string` | selection.go:169-183 |
| `ExtractSelectedText(lines []string, sl, sc, el, ec int) string` | selection.go:185-212 |
| `CopyToClipboardCmd(text string) tea.Cmd` | selection.go:214-221 |

**Verification**: `go build ./tui/widget/` compiles.

---

### Session 5: `tui/widget/spinner.go` — Phase-Aware Spinner

**Prerequisites**: Session 2 (needs theme)
**Files created**: `tui/widget/spinner.go`
**Lines**: ~60

Extract from `_tuiback/styles.go:57-62` (blockCursor) and `_tuiback/messages.go:195-222` (workAnimation, streamAnimation):

```go
package widget

type Phase string
const (
    PhaseGrounding Phase = "grounding"
    PhasePlanning  Phase = "planning"
    PhaseExecuting Phase = "executing"
    PhaseStreaming Phase = "streaming"
    PhaseDefault   Phase = ""
)

type Spinner struct {
    theme *Theme
    frame int
    phase Phase
}

func NewSpinner(t *Theme) Spinner
func (m Spinner) View() string
func (m *Spinner) SetPhase(p Phase)
func (m *Spinner) Tick()
func (m Spinner) WorkAnimation() string    // was workAnimation()
func (m Spinner) StreamAnimation() string  // was streamAnimation()
func BlockCursor(frame int, color lipgloss.Color) string  // was blockCursor()
```

**Verification**: `go build ./tui/widget/` compiles.

---

### Session 6: `tui/widget/buttons.go` — Button Group

**Prerequisites**: Session 2 (needs theme)
**Files created**: `tui/widget/buttons.go`
**Lines**: ~80

Extract from `_tuiback/confirm_form.go:216-237` and `_tuiback/yolo_form.go:103-121`:

```go
package widget

type Button struct {
    Label string
    Key   string
}

type ButtonGroup struct {
    theme   *Theme
    buttons []Button
    focused int
}

func NewButtonGroup(t *Theme, buttons ...Button) ButtonGroup
func (m ButtonGroup) Update(msg tea.Msg) (ButtonGroup, int, bool)  // returns (updated, selectedIdx, done)
func (m ButtonGroup) View() string
func (m ButtonGroup) FocusedIndex() int
```

The rendering logic: focused button uses `boxStyle.Render(fmt.Sprintf("❯ [ %s ]", btn.label))` + dim key hint; unfocused uses `dim.Render(fmt.Sprintf("  [ %s ]", btn.label))` + dim key hint. Box style: `Foreground(colorMainText).Background(lipgloss.Color("#1a1a2e")).Padding(0, 1)`.

**Verification**: `go build ./tui/widget/` compiles.

---

### Session 7: `tui/widget/list.go` — Filtered, Paginated List

**Prerequisites**: Session 2, Session 3
**Files created**: `tui/widget/list.go`
**Lines**: ~200

Extract from `_tuiback/model_select.go` (filter/scroll/select), `_tuiback/provider_select.go` (scroll/select), `_tuiback/settings_view.go:29-116` (render).

This is the key deduplication: model select, oracle model select, and provider select all become instances of `widget.List`.

```go
package widget

type ListItem struct {
    ID     string
    Label  string
    Detail string  // right-aligned info (e.g., context window size)
}

type List struct {
    theme     *Theme
    items     []ListItem
    filtered  []ListItem
    filter    string
    idx       int
    offset    int
    pageSize  int
    searching bool
    fetching  bool
    fetchError string
}

func NewList(t *Theme, pageSize int) List
func (m List) Update(msg tea.Msg) (List, tea.Cmd)
func (m List) View(width int) string
func (m *List) SetItems(items []ListItem)
func (m *List) SetFetching(fetching bool)
func (m *List) SetFetchError(err string)
func (m *List) SetFilter(s string)
func (m List) Selected() (ListItem, bool)
func (m List) TypedValue() string  // returns filter text if no selection
```

The View renders header + search bar + paginated items + footer, matching the layout from `_tuiback/settings_view.go:29-116`.

Key behaviors from `_tuiback/model_select.go`:
- Up/Down: wrap around
- PgUp/PgDown: jump by pageSize
- Backspace/Delete: remove last char from filter, re-filter
- Printable chars: append to filter, re-filter
- Enter: select current item (or use filter text if no items)
- Esc: cancel
- `clampOffset(pageSize)`: keep selected item visible

**Verification**: `go build ./tui/widget/` compiles.

---

### Session 8: `tui/widget/dialog.go` — Modal Overlay

**Prerequisites**: Session 6 (needs ButtonGroup)
**Files created**: `tui/widget/dialog.go`
**Lines**: ~60

Extract from `_tuiback/confirm_form.go` and `_tuiback/yolo_form.go`:

```go
package widget

type Dialog struct {
    theme   *Theme
    title   string
    icon    string
    body    string
    buttons ButtonGroup
}

func NewDialog(t *Theme, title, icon string, buttons ButtonGroup) Dialog
func (m Dialog) Update(msg tea.Msg) (Dialog, int, bool)  // returns (updated, btnIdx, done)
func (m Dialog) View() string
func (m *Dialog) SetBody(s string)
func (m *Dialog) SetButtons(buttons ButtonGroup)
```

Layout from `_tuiback/confirm_form.go:134-241`:
1. Icon + title line (e.g. `⚠ confirmation required` or `☠ YOLO MODE`)
2. Separator line `──────────────────────────`
3. Detail lines (risk, operation, tool, target, reason, suggestion)
4. Blank line
5. Question line
6. Button group
7. Navigation hint line

**Verification**: `go build ./tui/widget/` compiles.

---

### Session 9: `tui/widget/form.go` — Field Container

**Prerequisites**: Session 2, Session 5 (BlockCursor)
**Files created**: `tui/widget/form.go`
**Lines**: ~250

Extract from `_tuiback/settings_form.go:44-519`:

```go
package widget

type Field struct {
    Label  string
    Value  []rune
    Masked bool
}

type FormAction int
const (
    FormActionNone FormAction = iota
    FormActionSave
    FormActionCancel
)

type Form struct {
    theme       *Theme
    fields      []Field
    focused     int
    editing     bool
    editCursor  int
    focusedBtn  int  // 0=fields, 1=save, 2=cancel
    snapshot    *Form
}

func NewForm(t *Theme, fields ...Field) Form
func (m Form) Update(msg tea.Msg) (Form, tea.Cmd, FormAction)
func (m Form) View(spinFrame int) string
func (m Form) ToMap() map[string]string
func (m *Form) SaveSnapshot()
func (m *Form) RestoreSnapshot()
func (m *Form) SetFieldValue(idx int, val string)
func (m Form) FieldValue(idx int) string
```

Navigation from `_tuiback/settings_form.go:289-518`:
- Up/Shift+Tab: move focus up (fields→save→cancel wraps)
- Down/Tab: move focus down
- Enter on field: enter edit mode (or open selector for special fields)
- Enter on save btn: `FormActionSave`
- Enter on cancel btn / Esc: `FormActionCancel`
- Edit mode: Esc/Enter exits editing, printable chars insert, backspace/delete, home/end, left/right
- j/k vim keys also navigate

View from `_tuiback/settings_view.go:206-283`:
- Each field: `❯ label  value` (focused) or `  label  value` (unfocused)
- Editing: cursor rendered with `BlockCursor()`
- Masked fields show `*` characters
- Save/Cancel buttons at bottom

**Verification**: `go build ./tui/widget/` compiles.

---

### Session 10: `tui/widget/textinput.go` — Multiline Text Input

**Prerequisites**: Session 2, Session 3 (WordWrap, FindSegmentStart), Session 5 (BlockCursor)
**Files created**: `tui/widget/textinput.go`
**Lines**: ~450

This is the highest-risk widget. Extract all buffer operations from `_tuiback/model.go:237-521` and visual cursor logic from `_tuiback/view.go:948-1056`.

```go
package widget

type TextInput struct {
    buf         [][]rune
    curLine     int
    curCol      int
    lastVisCol  int
    selecting   bool
    selAnchor   int
    selEnd      int
    prompt      string
    theme       *Theme
    lastInputTime time.Time
}

func NewTextInput(t *Theme) TextInput
func (m TextInput) Update(msg tea.Msg, disabled bool) (TextInput, tea.Cmd)
func (m TextInput) View(width int) string
func (m TextInput) Value() string
func (m *TextInput) SetValue(s string)
func (m *TextInput) Clear()
func (m TextInput) Len() int             // flat buffer length including \n
func (m TextInput) CursorFlat() int
func (m TextInput) VisualLineCount() int
func (m TextInput) Height() int          // visual lines for layout calculation
func (m TextInput) SelectionRange() (start, end int)
func (m *TextInput) SetSelecting(selecting bool)
```

**Buffer operations from `_tuiback/model.go`**:

| Method | Source lines | Notes |
|---|---|---|
| `inputText()` | 237-243 | Flatten buf to string |
| `inputLen()` | 246-255 | Total rune count + newlines |
| `setInput(s)` | 258-270 | Parse string into buf lines |
| `clearInput()` | 273-277 | Reset to single empty line |
| `flatToLineCol(pos)` | 293-309 | Flat position → (line, col) |
| `lineColToFlat(line, col)` | 312-327 | (line, col) → flat position |
| `cursorPrevWord()` | 351-371 | Word-boundary backward |
| `cursorNextWord()` | 374-392 | Word-boundary forward |
| `splitLineAt(line, col)` | 395-411 | Split line at cursor (Alt+Enter) |
| `insertRuneAt(pos, r)` | 414-428 | Insert rune at flat position |
| `deleteRuneAt(pos)` | 431-449 | Backspace — delete before position |
| `deleteForwardAt(pos)` | 452-470 | Delete key — delete at position |
| `deleteRange(start, end)` | 473-496 | Delete range of flat positions |
| `subRunes(start, end)` | 499-521 | Extract rune range |

**Visual cursor logic from `_tuiback/view.go`**:

| Method | Source lines | Notes |
|---|---|---|
| `visualLineCount()` | 949-960 | Count wrapped display lines |
| `cursorVisualInfo()` | 972-1017 | Compute visual line/col position |
| `cursorPosFromVisual(visLine, visCol)` | 1020-1056 | Visual position → flat position |
| `findSegmentStart()` | 1061-1126 | Word-wrap segment offset |

**Key handling** from `_tuiback/update.go:424-706`:
- Enter (without Alt): returns SubmitEvent (handled by input package, not widget)
- Alt+Enter: `splitLineAt`
- Backspace/Delete: `deleteRuneAt`/`deleteForwardAt` + slash filter update
- Up/Down: visual line navigation with history fallback (history is in input package)
- Left/Right: character movement
- Ctrl+Left/Right: word movement
- Home/End: line start/end
- Ctrl+Home/End: buffer start/end
- Ctrl+A/E: line start/end (emacs)
- Ctrl+U/K: kill to line start/end
- Ctrl+W: delete word backward
- Printable chars: `insertRuneAt`

**Rendering** from `_tuiback/view.go:199-275`:
- For each logical line, word-wrap into segments
- First segment of first line gets `❯ ` prefix, others get `  `
- On the cursor segment: split at cursor position, render left part + reverse-video under-cursor char + right part
- When cursor is at end of segment: render `BlockCursor()` (solid when recently typed, blinking otherwise)
- Input selection: apply `ApplySelection()` to rendered segments

**Verification**: `go build ./tui/widget/` compiles.

---

### Session 11: `tui/chat/message.go` — Chat Message Type

**Prerequisites**: Session 1 (types package)
**Files created**: `tui/chat/message.go`
**Lines**: ~30

```go
package chat

import (
    ttypes "crude/tui/types"
    "time"
)

const (
    RoleUser      = ttypes.RoleUser
    RoleAssistant = ttypes.RoleAssistant
    RoleThinking  = ttypes.RoleThinking
    RoleTool      = ttypes.RoleTool
    RoleError     = ttypes.RoleError
)

type ChatMessage struct {
    Role    string
    Content string
    Time    time.Time

    ToolName   string
    ToolArgs   string
    ToolID     string
    ToolStatus string
    ToolIndex  int

    ToolProgressPhase   string
    ToolProgressMessage string
    ToolProgressLabel   string

    ToolPreviewPhase string
    ToolEstTokens    int
    ToolLivedAdded   int
    ToolLivedRemoved int
    ToolStartTime    time.Time
    ToolDuration     time.Duration

    ThinkingActive bool

    CachedLines []string
    CursorLine  int
}
```

**Verification**: `go build ./tui/chat/` compiles.

---

### Session 12: `tui/chat/render.go` — Message Rendering

**Prerequisites**: Sessions 1, 2, 3, 4, 5, 11
**Files created**: `tui/chat/render.go`
**Lines**: ~550

Extract from `_tuiback/view.go:426-789` (renderAllMessages) and `_tuiback/messages.go:103-515` (tool label helpers).

This is the largest single extraction. The render function converts `[]ChatMessage` into `[]string` (ANSI display lines).

**Functions to extract**:

| Function | Source | Notes |
|---|---|---|
| `renderAllMessages()` | view.go:427-789 | Main render loop |
| `renderUserMessage()` | view.go:472-520 | User label + markdown + timestamp |
| `renderAssistantMessage()` | view.go:522-577 | Asst label + markdown + streaming cursor |
| `renderThinkingMessage()` | view.go:579-608 | Thinking border + dot animation |
| `renderToolMessage()` | view.go:609-728 | Tool status + compact diff + plan box |
| `renderErrorMessage()` | view.go:729-757 | Error label + styling |
| `formatTimeLabel(t)` | view.go:792-804 | "HH:MM" + "today"/"yesterday"/"MM:DD" |
| `renderPlanBox(text, width)` | view.go:1170-1213 | Plan status with colored step markers |
| `renderCompactDiff(tool, content)` | view.go:1314-1353 | Inline diff summary |
| `isDiffContent(text)` | view.go:1136-1156 | Detect unified diff |
| `isPlanTool(name)` | view.go:1159-1166 | Plan-related tool names |
| `extractDiffBlock(content)` | view.go:1301-1312 | Extract ```diff block |
| `summarizeToolArgs(tool, args)` | messages.go:104-153 | Compact tool args display |
| `extractArg(str, key)` | messages.go:155-174 | Parse Go-map-style args |
| `toolLabel(tool, argsSummary)` | messages.go:177-182 | "tool args" format |
| `pascalCaseTool(name)` | messages.go:185-193 | snake_case → Pascal Case |
| `buildToolTitle(msg)` | messages.go:240-275 | Tool title from preview phase |
| `buildStreamingLabel(...)` | messages.go:277-352 | Streaming tool label |
| `buildDoneLabel(...)` | messages.go:354-514 | Completed tool label |
| `formatToolDuration(d)` | messages.go:224-238 | Duration formatting |
| `terminalResultHeight()` | view.go:1293-1299 | Max lines for tool result |

**Key rendering details**:

User messages (view.go:472-520):
- Markdown render via glamour
- Label: `❯ username ` with `colorMainText` on `#2a2a3a` bg
- Timestamps right-aligned on lines 1-2

Assistant messages (view.go:522-577):
- Markdown render via glamour
- Label: `❯ Crude modelname ` with `colorAccent2` on `#2a2a3a` bg
- Streaming cursor on last line
- `trimRightAnsiSpaces` on cursor line

Thinking messages (view.go:579-608):
- `│ ` border in `#555555`
- Italic text in `#888888`
- Active dot: cycling orange `● ` (phase colors: `#f0883e`, `#d0681e`, `#b85010`)
- Inactive dot: `● ` in `#888888`

Tool messages (view.go:609-728):
- `│ ` prefix (dim)
- Spinner: `● `/`○ ` alternating by spinFrame
- Status determines color (grounding=orange, planning=blue, executing=green, done=green, error=red)
- Success content: plan box for plan tools, compact diff for write/edit, line count for read_file, wrapped text for others
- Max result lines: `height/3` (min 4)

Error messages (view.go:729-757):
- `error` label + content in `colorError`
- Timestamps right-aligned

**Verification**: `go build ./tui/chat/` compiles.

---

### Session 13: `tui/chat/chat.go` — Chat Model

**Prerequisites**: Sessions 2, 3, 4, 5, 11, 12
**Files created**: `tui/chat/chat.go`
**Lines**: ~250

```go
package chat

type Model struct {
    theme       *widget.Theme
    messages    []ChatMessage
    offset      int
    scrolled    bool
    width       int
    height      int
    modelName   string
    selecting   bool
    selAnchorLine int
    selAnchorCol  int
    selEndLine    int
    selEndCol     int
    selAutoScroll bool
    cachedLines []string
    cachedWidth int
    mdRenderer  *glamour.TermRenderer
    spinner     widget.Spinner
}

func NewModel(t *widget.Theme) Model
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd)
func (m Model) View() string

// Mutation API (called by app.go)
func (m *Model) AppendMessage(msg ChatMessage)
func (m *Model) AppendToken(token string)
func (m *Model) AppendThinking(s string)
func (m *Model) UpdateToolStreaming(toolID, name string, data map[string]any)
func (m *Model) UpdateToolCall(toolID, name, args string)
func (m *Model) UpdateToolProgress(toolID, name, phase, message string)
func (m *Model) UpdateToolResult(toolID, name, result string, isError bool)
func (m *Model) ClearStreamingTools()
func (m *Model) SetModelName(name string)
func (m *Model) SetSize(w, h int)
func (m *Model) ScrollToBottom()
func (m *Model) MarkAllThinkingDone()
func (m *Model) InvalidateCache()

// Selection helpers
func (m *Model) SelectionRange() (startLine, startCol, endLine, endCol int)
func (m *Model) MouseToAllLine(mouseY int) (allIdx int, ok bool)
func (m *Model) ChatAreaContentBounds() (firstLine, lastLine int)
```

**Update handling** (extracted from `_tuiback/update.go`):
- Mouse scroll (wheel up/down with velocity): update.go:820-857
- Mouse press/motion/release for text selection: update.go:714-818
- Selection auto-scroll tick: update.go:1411-1444
- Page Up/Down: update.go:658-669
- Home/End (when thinking): update.go:572-605

**View**: Renders the scrollable chat area with selection highlighting. The actual message rendering is in `render.go`.

**MdRenderer** from `_tuiback/model.go:169-226`: Creates glamour renderer with custom chroma colors and zero margins. Recreated on width change.

**Verification**: `go build ./tui/chat/` compiles.

---

### Session 14: `tui/input/input.go` — Input Model

**Prerequisites**: Sessions 1, 2, 5, 10
**Files created**: `tui/input/input.go`
**Lines**: ~350

```go
package input

type Event interface{ event() }
type SubmitEvent struct{ Text string }
type SlashCommandEvent struct{ Command string }
type SettingsOpenEvent struct{}
type HistorySaveEvent struct{ History []string }

type Model struct {
    theme    *widget.Theme
    input    widget.TextInput
    history  []string
    histIdx  int
    histDraft, histPrefix string

    slashActive   bool
    slashIdx      int
    slashOffset   int
    slashFilter   string
    slashFiltered []slashCmd

    showHint bool
    width    int
    thinking bool
    blocking bool
}

func NewModel(t *widget.Theme) Model
func (m Model) Update(msg tea.Msg, thinking, blocking bool) (Model, tea.Cmd, Event)
func (m Model) View(width, chatAreaHeight int, settingsActive, confirmActive bool) string
func (m *Model) SetState(thinking, blocking bool)
func (m *Model) SetWidth(w int)
func (m *Model) SetHistory(h []string)
func (m Model) History() []string
func (m *Model) SetInput(s string)
func (m *Model) ClearInput()
func (m Model) Height() int
func (m Model) InputTextWidth() int
func (m Model) FirstLineY(chatAreaHeight int, showHint, thinking bool, inputLen int) int
func (m Model) MouseToInputPos(mouseX, mouseY, chatAreaHeight int, showHint bool) (runeIdx int, ok bool)
```

**History navigation** from `_tuiback/history.go`:
- `browseHistoryUp()`: Enter history, navigate older matching prefix
- `browseHistoryDown()`: Navigate newer, restore draft at end
- `findHistoryIndex(startIdx)`: Find next older entry matching prefix
- `findHistoryDown(startIdx)`: Find next newer entry matching prefix
- `resetHistory()`: Exit history mode, restore draft
- `appendHistory(text)`: Add to history, dedup consecutive, trim to 500

**Slash command autocomplete** from `_tuiback/messages.go:11-43`:
- 12 commands: /settings, /provider, /model, /oracle-model, /api-key, /effort, /undo, /undo-n, /undo-all, /changes, /yolo, /help
- Filtered by prefix, displayed as paginated list
- Tab/Enter to accept, Up/Down to navigate

**Input rendering** from `_tuiback/view.go:129-318`:
- Prompt `❯ ` on first visual line, `  ` on continuation lines
- Cursor rendering delegated to `widget.TextInput.View()`
- Selection highlighting via `widget.ApplySelection()`
- Slash autocomplete overlay below input

**Submit logic** from `_tuiback/update.go:144-178`:
- Trims whitespace
- Slash commands → `SlashCommandEvent`
- Regular text → append to history, `SubmitEvent`
- While thinking (and not blocking): input disabled

**Verification**: `go build ./tui/input/` compiles.

---

### Session 15: `tui/settings/settings.go` — Settings Overlay

**Prerequisites**: Sessions 1, 2, 7, 9
**Files created**: `tui/settings/settings.go`
**Lines**: ~200

```go
package settings

type settingsMode int
const (
    settingsForm settingsMode = iota
    settingsProviderSelect
    settingsModelSelect
    settingsOracleModelSelect
)

type Event interface{ event() }
type SaveEvent struct{ Settings ttypes.Settings }
type CancelEvent struct{}
type FetchModelsEvent struct{ ProviderID, BaseURL, APIKey string }

type Model struct {
    theme    *widget.Theme
    form     widget.Form
    provider widget.List
    models   widget.List
    mode     settingsMode
    providerID string
    activePresets []ttypes.ProviderPreset

    modelCtx, oracleCtx                 int
    modelInPrice, modelOutPrice         float64
    modelCachePrice                     float64
    oracleInPrice, oracleOutPrice       float64
    oracleCachePrice                    float64

    getProviderConfigFn func(string) ttypes.ProviderConfig
    fetchModelsFn       func(string, string, string) tea.Cmd
    selectTarget        string  // "model" or "oracle"
}

func NewModel(t *widget.Theme, initial ttypes.Settings, ...) Model
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd, Event)
func (m Model) View(spinFrame int) string
func (m *Model) SetPresets(presets []ttypes.ProviderPreset)
func (m *Model) SaveSnapshot()
func (m *Model) RestoreSnapshot()
```

**Field definitions** from `_tuiback/settings_form.go:11-18`:
- sfProvider=0, sfBaseURL=1, sfAPIKey=2, sfModel=3, sfOracleModel=4, sfThinkingEffort=5

**Thinking effort cycling** from `_tuiback/settings_form.go:21-42`:
- Efforts: ["none", "low", "medium", "high", "xhigh"]
- `cycleEffort(current, delta)`: wrap-around cycle

**Form fields with special behavior**:
- sfProvider: Enter opens `widget.List` (provider select)
- sfModel: Enter opens `widget.List` (model select) OR enters edit mode (ollama/custom/no API key)
- sfOracleModel: Same as sfModel but for oracle
- sfThinkingEffort: Left/Right cycles effort value, no edit mode
- sfAPIKey: Masked field (shows `*`)
- Other fields: Enter enters edit mode

**Provider select** from `_tuiback/provider_select.go`:
- On select: load saved config for new provider, clear model state
- Uses `widget.List` for rendering and navigation

**View** from `_tuiback/settings_view.go`:
- Provider select mode: render list of presets with `❯ ` indicator
- Model select mode: header + search bar + paginated model list + footer (uses `widget.List.View()`)
- Oracle model select: same as model select
- Form mode: field list + save/cancel buttons (uses `widget.Form.View()`)

**Verification**: `go build ./tui/settings/` compiles.

---

### Session 16: `tui/settings/modelselect.go` — Shared Model Selection

**Prerequisites**: Session 15
**Files created**: `tui/settings/modelselect.go`
**Lines**: ~100

```go
package settings

func (m *Model) openModelSelect(target string)
func (m *Model) handleModelSelectResult(selected widget.ListItem, target string)
func fetchModelsCmd(fn func(string, string, string) tea.Cmd, providerID, baseURL, apiKey string, isOracle bool) tea.Cmd
```

This eliminates the duplication between model select and oracle model select from `_tuiback/model_select.go`. The `selectTarget` field determines which field the selection writes back to.

**Model select key handling** from `_tuiback/model_select.go:10-83`:
- Delegated to `widget.List.Update()`
- On select: write model ID to appropriate form field, store pricing metadata
- On empty list + search text: use search text as manual entry

**Model filter** from `_tuiback/model_select.go:85-95`:
- Filter by model ID or Name (case-insensitive substring)

**Fetch models** from `_tuiback/update.go:317-345`:
- Dispatches fetch command, routes result to ModelsListMsg or OracleModelsListMsg based on `selectTarget`

**Verification**: `go build ./tui/settings/` compiles.

---

### Session 17: `tui/confirm.go` — Confirm Dialog

**Prerequisites**: Sessions 2, 6, 8
**Files created**: `tui/confirm.go`
**Lines**: ~80

```go
package tui

type ConfirmDialog struct {
    dialog    widget.Dialog
    question  string
    operation string
    tool      string
    target    string
    risk      string
    reason    string
    suggestion string
}

func NewConfirmDialog(theme *widget.Theme) ConfirmDialog
func (m ConfirmDialog) Update(msg tea.Msg) (ConfirmDialog, ConfirmResult, bool)
func (m ConfirmDialog) View(spinFrame int) string
func (m *ConfirmDialog) Reset(question, operation, tool, target string)
func (m *ConfirmDialog) ResetWithDetails(question, operation, tool, target, risk, reason, suggestion string)
func (m *ConfirmDialog) Dismiss()
```

Extract from `_tuiback/confirm_form.go`. The `Update` method delegates to `widget.Dialog.Update()` for button navigation. The `View` method builds the body (risk, operation, tool, target, reason, suggestion lines) and sets it on the dialog.

**Risk display** from `_tuiback/confirm_form.go:153-177`:
- blocked → `#ff0000` "BLOCKED"
- destructive → `colorWarning` "DANGEROUS"
- network → `colorAccent` "network"
- project-write → `#d4a017` "write"
- unknown → `colorMuted` "unknown"

**Buttons**: Allow (y), Always Allow (a), Deny (n)

**Verification**: `go build ./tui/` compiles.

---

### Session 18: `tui/yolo.go` — YOLO Dialog

**Prerequisites**: Sessions 2, 6, 8
**Files created**: `tui/yolo.go`
**Lines**: ~50

```go
package tui

type YoloDialog struct {
    dialog widget.Dialog
}

func NewYoloDialog(theme *widget.Theme) YoloDialog
func (m YoloDialog) Update(msg tea.Msg) (YoloDialog, bool, bool)  // (updated, enabled, done)
func (m YoloDialog) View() string
func (m *YoloDialog) Reset()
func (m *YoloDialog) Dismiss()
```

Extract from `_tuiback/yolo_form.go`. Simpler than confirm — just the warning text and Enable/Cancel buttons.

**Body text** from `_tuiback/yolo_form.go:92-101`:
```
This is DANGEROUS. Safety checks protect
your system from accidental damage and
prompt injection attacks.

Are you absolutely sure?
```

**Buttons**: Enable YOLO (y), Cancel (n)

**Verification**: `go build ./tui/` compiles.

---

### Session 19: `tui/statusbar.go` — Status Bar

**Prerequisites**: Sessions 2, 5
**Files created**: `tui/statusbar.go`
**Lines**: ~180

```go
package tui

type StatusBar struct {
    theme  *widget.Theme
    width  int

    thinking, streaming, blocking bool
    spinner    widget.Spinner
    statusLine string
    statusPhase string

    tokenPrompt, tokenCompletion, tokenCached int
    sessionCost   float64
    contextWindow int
    contextUsed   int

    lastError   string
    errorTime   time.Time
    infoMsg     string
    infoMsgTime time.Time
    retryMsg    string
    retryTime   time.Time

    asyncCommands []fileio.AsyncCmdInfo
}

func NewStatusBar(t *widget.Theme) *StatusBar
func (s *StatusBar) View() string
func (s *StatusBar) RenderAsyncBar() []string
func (s *StatusBar) AsyncBarHeight() int
func (s *StatusBar) SetError(err string)
func (s *StatusBar) SetInfo(msg string)
func (s *StatusBar) SetRetry(msg string)
func (s *StatusBar) ClearExpired()
func (s *StatusBar) UpdateUsage(prompt, completion, cached int, cost float64, ctxWindow, ctxUsed int)
func (s *StatusBar) SetThinking(thinking, streaming, blocking bool)
func (s *StatusBar) SetPhase(phase string)
func (s *StatusBar) SetWidth(w int)
func (s *StatusBar) SetAsyncCommands(cmds []fileio.AsyncCmdInfo)
```

Extract from `_tuiback/view.go:324-382` (status bar) and `_tuiback/view.go:1355-1421` (async bar).

**Status bar layout** from view.go:324-382:
- Left side: info msg (green) → retry msg (orange `#ffaa00`) → error msg (red) → confirming (orange) → awaiting input (orange) → streaming animation → thinking animation → empty
- Right side: token usage (`↑prompt ·cached ↓completion N% $cost`) or "ready"
- Spaced to fill width

**Token formatting** from `_tuiback/messages.go:76-101`:
- `formatTokens(n)`: ≥1M → "1.2M", ≥1k → "1.2k", else "123"
- `formatCost(cost)`: ≥$1 → "$1.23", ≥$0.01 → "$0.0123", etc.

**Context percentage**: `(contextUsed / contextWindow) * 100`%

**Async bar** from view.go:1355-1421:
- Separator line above
- Each command: `│ dot [handle] command duration`
- Dot: `●`/`○` alternating for running, `✓` completed, `✗` failed, `!` killed
- Commands hidden after 15 seconds if not running

**Error/info/retry expiry** from `_tuiback/update.go:190-195`:
- Error: 8 seconds
- Info: 5 seconds
- Retry: 30 seconds

**Verification**: `go build ./tui/` compiles.

---

### Session 20: `tui/messages.go` — Format Helpers

**Prerequisites**: Session 1
**Files created**: `tui/messages.go`
**Lines**: ~80

```go
package tui

import ttypes "crude/tui/types"

// Re-export message types from types package as package-local for convenience
type tickMsg = ttypes.TickMsg
type errorMsg = ttypes.ErrorMsg
// ... etc

func formatSettingsSummary(s ttypes.Settings) string
func valueOr(s, fallback string) string
func maskOr(s, fallback string) string
func formatTokens(n int) string
func formatCost(cost float64) string
```

Extract from `_tuiback/messages.go:45-101`.

**Verification**: `go build ./tui/` compiles.

---

### Session 21: `tui/theme.go` — App-Level Theme

**Prerequisites**: Session 2
**Files created**: `tui/theme.go`
**Lines**: ~30

```go
package tui

import "crude/tui/widget"

// appTheme is the singleton theme instance for the tui package.
var appTheme = widget.DefaultDark()
```

Thin wrapper so the tui root package has access to the widget theme.

**Verification**: `go build ./tui/` compiles.

---

### Session 22: `tui/app.go` — Composition Root + TUI Wrapper

**Prerequisites**: ALL previous sessions
**Files created**: `tui/app.go`
**Lines**: ~400

This is the final assembly. It wires everything together and replaces `_tuiback/tui.go`, `_tuiback/model.go`, `_tuiback/update.go`, and `_tuiback/view.go`.

```go
package tui

type mode int
const (
    modeChat mode = iota
    modeSettings
    modeConfirm
    modeYolo
)

type App struct {
    theme  *widget.Theme
    mode   mode
    width  int
    height int

    chat      chat.Model
    input     input.Model
    statusbar *StatusBar
    settings  settings.Model
    confirm   ConfirmDialog
    yolo      YoloDialog

    thinking  bool
    blocking  bool
    streaming bool

    inputCh  chan<- string
    cancelCh chan<- struct{}

    OnQuit, OnExitMessage               func()
    OnSettingsSave                      func(Settings)
    OnConfirmResult                     func(ConfirmResult)
    OnYoloCmd                           func(bool)
    OnUndoCmd                           func(string)
    FetchModelsFn                       func(string, string, string) ([]ModelEntry, error)
    GetProviderConfigFn                 func(string) ProviderConfig
    ProviderPresets                     []ProviderPreset

    quit           bool
    lastCancelTime time.Time
    modelName      string
    username       string
    startTime      time.Time
}

func (m App) Init() tea.Cmd
func (m App) Update(msg tea.Msg) (tea.Model, tea.Cmd)
func (m App) View() string
func (m *App) handleCancel() tea.Cmd
func (m *App) handleAgentEvent(evt ttypes.AgentEventMsg) tea.Cmd
func (m *App) chatAreaHeight() int
```

**Update routing** (replaces the 1470-line switch):

```go
func (m App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
    // 1. Always: resize, tick, global cancel
    switch msg := msg.(type) {
    case tea.WindowSizeMsg: ...
    case ttypes.TickMsg: ...
    }
    // 2. Global cancel (Ctrl+C/Esc) hierarchy:
    //    editing → model select → provider select → settings → agent → double-press quit
    // 3. Mode-specific:
    //    modeSettings  → m.settings.Update(msg)
    //    modeConfirm   → m.confirm.Update(msg)
    //    modeYolo      → m.yolo.Update(msg)
    //    modeChat      → m.input.Update(msg) + m.chat.Update(msg)
    // 4. Agent events → dispatch to chat + statusbar
    // 5. External messages: errorMsg, updateModelMsg, clipboard, undo
}
```

**Agent event dispatch** from `_tuiback/update.go:860-1346`:

| Event | Target | Action |
|---|---|---|
| `phase` | statusbar | Set phase + status line |
| `thinking` | chat | Append/extend thinking message |
| `stream_token` | chat | Append/extend assistant message |
| `tool_call_streaming` | chat | Create/update streaming tool message |
| `tool_call` | chat | Update tool call with args |
| `tool_progress` | chat | Update tool progress phase/message |
| `tool_result` | chat | Mark tool done with result |
| `blocking` | confirm | Open confirm dialog with details |
| `blocking_complete` | confirm | Dismiss confirm |
| `done` | app + chat | Clear thinking, clean streaming tools, show reason |
| `error` | app + chat + statusbar | Clear thinking, show error |
| `usage` | statusbar | Update token counts and cost |
| `retry` | statusbar | Show retry message |
| `async_command_status` | statusbar | Update async commands |

**View layout** (replaces the 1421-line view):

```go
func (m App) View() string {
    // 1. Top padding
    // 2. Header: crude + model + uptime + yolo badge
    // 3. Separator
    // 4. Chat area → m.chat.View()
    // 5. Async bar → m.statusbar.RenderAsyncBar()
    // 6. Input area → m.input.View()
    // 7. Slash autocomplete overlay
    // 8. Bottom separator
    // 9. Status bar → m.statusbar.View()
    // 10. Overlays: settings/confirm/yolo if active
}
```

**TUI wrapper** (preserves public API from `_tuiback/tui.go`):
The `TUI` struct wraps `tea.Program` with the same public fields and methods. `Start()` creates `App` model, wires callbacks, starts program.

**chatAreaHeight calculation** from `_tuiback/view.go:389-424`:
- Settings active: `height - 7`
- Confirm active: `height - 7`
- Normal: `height - (2 + 1 + hintLines + inputVisualLines + slashLines + asyncLines + 1 + 1 + 1)`

**Verification**: `go build ./tui/` and `go build ./...` compile. Full integration test by running the app.

---

## Session Dependency Graph

```
Session 1 (types) ─────────────────────────────────────────────┐
                                                                │
Session 2 (widget/theme) ──────────────────────────────────┐   │
                                                            │   │
Session 3 (widget/util) ← 2                                │   │
Session 4 (widget/selection)                               │   │
Session 5 (widget/spinner) ← 2                             │   │
Session 6 (widget/buttons) ← 2                             │   │
Session 7 (widget/list) ← 2, 3                             │   │
Session 8 (widget/dialog) ← 6                              │   │
Session 9 (widget/form) ← 2, 5                             │   │
Session 10 (widget/textinput) ← 2, 3, 5                    │   │
                                                            │   │
Session 11 (chat/message) ← 1                              │   │
Session 12 (chat/render) ← 1,2,3,4,5,11                    │   │
Session 13 (chat/chat) ← 2,3,4,5,11,12                     │   │
                                                            │   │
Session 14 (input) ← 1,2,5,10                              │   │
                                                            │   │
Session 15 (settings) ← 1,2,7,9                            │   │
Session 16 (settings/modelselect) ← 15                     │   │
                                                            │   │
Session 17 (confirm) ← 2,6,8                               │   │
Session 18 (yolo) ← 2,6,8                                  │   │
Session 19 (statusbar) ← 2,5                               │   │
Session 20 (messages) ← 1                                  │   │
Session 21 (theme) ← 2                                     │   │
                                                            │   │
Session 22 (app) ← ALL                                     ──┘
```

**Parallelizable sessions**:
- Sessions 3-10 can be done in parallel (widget sub-package, no inter-dependencies except on theme)
- Sessions 17-21 can be done in parallel (independent tui root files)
- Sessions 11-16 can overlap as long as dependencies are met

## Files Deleted After Completion

| File | Lines | Replaced by |
|---|---|---|
| `_tuiback/tui.go` | 187 | `tui/app.go` |
| `_tuiback/model.go` | 546 | `tui/widget/textinput.go` + `tui/chat/chat.go` + `tui/app.go` |
| `_tuiback/update.go` | 1470 | `tui/app.go` + `tui/input/input.go` + `tui/chat/chat.go` + `tui/settings/settings.go` |
| `_tuiback/view.go` | 1421 | `tui/app.go` + `tui/chat/render.go` + `tui/input/input.go` + `tui/statusbar.go` |
| `_tuiback/styles.go` | 74 | `tui/widget/theme.go` |
| `_tuiback/messages.go` | 515 | `tui/chat/render.go` + `tui/messages.go` + `tui/widget/spinner.go` |
| `_tuiback/history.go` | 99 | `tui/input/input.go` |
| `_tuiback/selection.go` | 406 | `tui/widget/selection.go` + `tui/chat/chat.go` + `tui/input/input.go` |
| `_tuiback/settings_form.go` | 519 | `tui/widget/form.go` + `tui/settings/settings.go` |
| `_tuiback/settings_view.go` | 284 | `tui/settings/settings.go` + `tui/widget/list.go` |
| `_tuiback/model_select.go` | 208 | `tui/settings/modelselect.go` + `tui/widget/list.go` |
| `_tuiback/provider_select.go` | 57 | `tui/settings/settings.go` + `tui/widget/list.go` |
| `_tuiback/confirm_form.go` | 243 | `tui/confirm.go` + `tui/widget/dialog.go` |
| `_tuiback/yolo_form.go` | 127 | `tui/yolo.go` + `tui/widget/dialog.go` |
| **Total** | **6162** | **24 files, max ~550 lines each** |
