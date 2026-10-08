package viewmodel

import (
	"crude/tui/editor"
	"time"
)

// RefRow is one row in the references popup list.
type RefRow struct {
	File    string
	Line    int
	Col     int
	Preview string
}

// OutlineRow is one row in the outline popup list.
type OutlineRow struct {
	Sym   editor.LSPSymbol
	Depth int
}

// LSPPopupState holds the transient UI state for LSP-related popups:
// rename preview, outline, references, and completion.
//
// This is DISPLAY state — it tracks which LSP popups are open right now,
// cursor positions within them, and the rendered data.
type LSPPopupState struct {
	RenamePreview                 *lspRenamePreview
	RenameArmed                   bool
	OutlineOpen                   bool
	OutlineRows                   []OutlineRow
	OutlineCursor                 int
	ReferencesID                  string
	RefRows                       []RefRow
	RefCursor                     int
	CompletionID                  string
	CompletionItems               []editor.CompletionItem
	CompletionCursor              int
	CompletionOwner               *editor.Editor
	CompletionRevision            uint64
	CompletionLine, CompletionCol int
	CompletionDue                 time.Time
	CompletionAutomatic           bool
}

// NewLSPPopupState creates an LSPPopupState with all fields zeroed.
func NewLSPPopupState() *LSPPopupState {
	return &LSPPopupState{}
}
