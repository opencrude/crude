package widgets

import (
	"charm.land/lipgloss/v2"

	"crude/tui/theme"
	"crude/tui/ui"
)

// TextPromptKind drives the prompt's appearance and behavior.
// The renderer uses Kind to pick an icon and a validation hint
// shown at the bottom of the popup.
type TextPromptKind int

const (
	TextPromptNewFile TextPromptKind = iota
	TextPromptNewFolder
	TextPromptRename
	TextPromptGeneric
)

// TextPromptState holds the data for a single text-input popup. The
// App owns this and re-renders the popup content on every change
// (the same lifecycle as the dirty-close and find-replace popups).
//
// Editing is backed by ui.TextEdit (shared rune-safe engine). The
// Value/Cursor fields are derived accessors for render compatibility.
type TextPromptState struct {
	Title        string         // popup window title (e.g. "New file")
	Prompt       string         // short instruction, e.g. "Create new file in <dir>:"
	Kind         TextPromptKind // drives the icon and the hint row
	InitialValue string         // pre-filled value (for rename)
	// Edit is the shared text engine. Never nil after NewTextPromptState.
	Edit *ui.TextEdit
	// Value and Cursor are kept for callers/tests that still read fields.
	// Prefer ValueText()/CursorPos() and Edit methods for new code.
	Value   string
	Cursor  int
	Focused bool
	// OnSubmit is called with the final value when the user presses
	// Enter. Returning a non-nil error surfaces in the status bar
	// without closing the popup (so the user can fix the name).
	OnSubmit func(value string) error
	// OnCancel is called on Esc. Optional.
	OnCancel func()
}

// NewTextPromptState constructs a prompt with a shared TextEdit engine.
func NewTextPromptState(kind TextPromptKind, title, prompt, initial string) *TextPromptState {
	ed := ui.NewTextEdit()
	ed.SetValue(initial)
	return &TextPromptState{
		Title:        title,
		Prompt:       prompt,
		Kind:         kind,
		InitialValue: initial,
		Edit:         ed,
		Value:        initial,
		Cursor:       ed.Cursor(),
		Focused:      true,
	}
}

// SyncFromEdit copies Edit state into Value/Cursor for renderers.
func (st *TextPromptState) SyncFromEdit() {
	if st == nil || st.Edit == nil {
		return
	}
	st.Value = st.Edit.Value()
	st.Cursor = st.Edit.Cursor()
}

// ValueText returns the current value from the edit engine.
func (st *TextPromptState) ValueText() string {
	if st == nil {
		return ""
	}
	if st.Edit != nil {
		return st.Edit.Value()
	}
	return st.Value
}

// CursorPos returns the rune cursor index.
func (st *TextPromptState) CursorPos() int {
	if st == nil {
		return 0
	}
	if st.Edit != nil {
		return st.Edit.Cursor()
	}
	return st.Cursor
}

// TextPromptPopupWidth computes the popup total width.
func TextPromptPopupWidth(st *TextPromptState) int {
	w := len(st.Prompt) + 12
	if len(st.Title)+12 > w {
		w = len(st.Title) + 12
	}
	if w < 50 {
		w = 50
	}
	if w > 70 {
		w = 70
	}
	return w
}

// TextPromptPopupHeight is the popup total height (chrome + body).
func TextPromptPopupHeight(st *TextPromptState) int {
	return 7 // chrome (2) + prompt (1) + input (1) + hint (1) + 2 breathing
}

// TextPromptLines renders the prompt popup into a slice of styled
// ANSI lines including the popup's own border, title, and padding
// (unified design). Pass to widgets.NewPopupContent.
func TextPromptLines(th *theme.Theme, st *TextPromptState) []string {
	st.SyncFromEdit()
	mutedStyle := lipgloss.NewStyle().Foreground(th.Colors.Muted).Background(th.Colors.PopupBg)
	cfg := PopupChromeConfig{
		Title:     st.Title,
		TitleIcon: kindIcon(st.Kind),
		Width:     TextPromptPopupWidth(st),
		Focused:   true,
	}
	innerW := cfg.InnerWidth()

	var content []string
	if st.Prompt != "" {
		content = append(content, padVisibleToWidth(mutedStyle.Render(st.Prompt), innerW))
	} else {
		content = append(content, PopupEmptyLine(th))
	}

	placeholder := "name"
	switch st.Kind {
	case TextPromptNewFile:
		placeholder = "filename"
	case TextPromptNewFolder:
		placeholder = "folder"
	case TextPromptRename:
		placeholder = "new name"
	}

	value := st.ValueText()
	cursor := st.CursorPos()
	if value == "" && st.InitialValue != "" && st.Edit != nil && st.Edit.Len() == 0 {
		// Prefill once if Edit was empty but InitialValue set.
		st.Edit.SetValue(st.InitialValue)
		value = st.InitialValue
		cursor = st.Edit.Cursor()
	}
	content = append(content, padVisibleToWidth(
		PopupInputField(th, "", value, placeholder, cursor, true),
		innerW))

	content = append(content, PopupEmptyLine(th))
	content = append(content, padVisibleToWidth(
		PopupHintLine(th, textPromptHintSegments(st)),
		innerW))

	return PopupChromeLines(th, cfg, content)
}

func textPromptHintSegments(st *TextPromptState) []HintSegment {
	switch st.Kind {
	case TextPromptNewFile:
		return []HintSegment{
			{Key: "enter", Desc: "create"},
			{Key: "esc", Desc: "cancel"},
		}
	case TextPromptNewFolder:
		return []HintSegment{
			{Key: "enter", Desc: "create"},
			{Key: "esc", Desc: "cancel"},
		}
	case TextPromptRename:
		return []HintSegment{
			{Key: "enter", Desc: "rename"},
			{Key: "esc", Desc: "cancel"},
		}
	default:
		return []HintSegment{
			{Key: "enter", Desc: "submit"},
			{Key: "esc", Desc: "cancel"},
		}
	}
}

func kindIcon(k TextPromptKind) string {
	switch k {
	case TextPromptNewFile:
		return "✎"
	case TextPromptNewFolder:
		return "▤"
	case TextPromptRename:
		return "↪"
	default:
		return "•"
	}
}
