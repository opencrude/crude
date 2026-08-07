package ui

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
// Editing is backed by TextEdit (shared rune-safe engine). The
// Value/Cursor fields are derived accessors for render compatibility.
type TextPromptState struct {
	Title        string         // popup window title (e.g. "New file")
	Prompt       string         // short instruction, e.g. "Create new file in <dir>:"
	Kind         TextPromptKind // drives the icon and the hint row
	InitialValue string         // pre-filled value (for rename)
	// Edit is the shared text engine. Never nil after NewTextPromptState.
	Edit *TextEdit
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
	ed := NewTextEdit()
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
