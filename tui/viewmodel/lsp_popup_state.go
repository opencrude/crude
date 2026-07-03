package viewmodel

import (
	"crude/tui/editor"
)

type LSPPopupState struct {
	RenameArmed       bool
	OutlineOpen       bool
	OutlineRows       []outlineRow
	OutlineCursor     int
	ReferencesID      string
	RefRows           []refRow
	RefCursor         int
	CompletionID      string
	CompletionItems   []editor.CompletionItem
	CompletionCursor  int
}

func NewLSPPopupState() *LSPPopupState {
	return &LSPPopupState{}
}
