package viewmodel

import "crude/tui/workspace"

type LSPPopupState = workspace.LSPPopupState

func NewLSPPopupState() *LSPPopupState {
	return workspace.NewLSPPopupState()
}
