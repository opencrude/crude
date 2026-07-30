package chatprojection

// PaneLineInfo maps a visible chat row to a nested subpane (if any).
// Used by shellchat.Compose for nested scrollbar / SubPaneBg painting.
type PaneLineInfo struct {
	PaneID  string
	LineIdx int
}
