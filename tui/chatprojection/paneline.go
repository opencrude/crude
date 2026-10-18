package chatprojection

// PaneTarget identifies one pane's viewport-local body row.
type PaneTarget struct {
	PaneID  string
	LineIdx int
}

// PaneLineInfo maps a rendered chat row to its deepest nested pane.
// Ancestors are ordered outermost to immediate parent, with exact body-row
// indices in each enclosing viewport. LineIdx -1 identifies the pane's header
// or summary: wheel input belongs to its nearest ancestor, not that pane itself.
// An empty PaneID means ordinary outer chat.
// The slices are borrowed immutable projection data, like the rendered rows.
type PaneLineInfo struct {
	PaneID    string
	LineIdx   int
	Ancestors []PaneTarget
}

// WithinPane encloses this row in one pane without changing its deepest owner.
// It allocates fresh ancestry so embedding cannot mutate a retained child map.
func (info PaneLineInfo) WithinPane(target PaneTarget) PaneLineInfo {
	if target.PaneID == "" {
		return info
	}
	if info.PaneID == "" {
		return PaneLineInfo{PaneID: target.PaneID, LineIdx: target.LineIdx}
	}
	ancestors := make([]PaneTarget, len(info.Ancestors)+1)
	ancestors[0] = target
	copy(ancestors[1:], info.Ancestors)
	info.Ancestors = ancestors
	return info
}
