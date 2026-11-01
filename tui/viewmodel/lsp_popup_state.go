package viewmodel

import (
	"crude/tui/editor"
	"time"
)

// OutlineRow is one row in the outline popup list.
type OutlineRow struct {
	Sym   editor.LSPSymbol
	Depth int
}

// LSPPopupState holds the transient UI state for LSP-related popups:
// rename preview, outline, and the automatic-completion debounce.
//
// This is DISPLAY state: it tracks which LSP popups are open right now,
// cursor positions within them, and the rendered data.
type LSPPopupState struct {
	EditPreview   *reviewedEditDialog
	RenameArmed   bool
	OutlineOpen   bool
	OutlineRows   []OutlineRow
	OutlineCursor int
	CompletionDue time.Time
}

// NewLSPPopupState creates an LSPPopupState with all fields zeroed.
func NewLSPPopupState() *LSPPopupState {
	return &LSPPopupState{}
}
