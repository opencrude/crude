package viewmodel

import "crude/tui/workspace"

type SubPane = workspace.SubPane
type PaneManager = workspace.PaneManager
type PaneRenderResult = workspace.PaneRenderResult

var NewSubPane = workspace.NewSubPane
var NewPaneManager = workspace.NewPaneManager

// PaneType aliases — kept here because agent_events.go, app.go, and view.go
// reference PaneType/PaneThinking/PaneAgent directly.
type PaneType = workspace.PaneType

const (
	PaneThinking = workspace.PaneThinking
	PaneAgent    = workspace.PaneAgent
)
