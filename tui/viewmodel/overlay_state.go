package viewmodel

import "crude/tui/workspace"

type OverlayState = workspace.OverlayState

func NewOverlayState() *OverlayState {
	return workspace.NewOverlayState()
}
