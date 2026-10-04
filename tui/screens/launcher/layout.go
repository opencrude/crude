package launcher

import "crude/tui/ui"

const (
	launcherCrampedWidth = 72
	actionHeight         = 4
	actionRowGap         = 1
)

func (s *Screen) Children() []ui.Component {
	children := []ui.Component{s.newSession, s.openWorkspace, s.skills, s.mcp, s.divider, s.header, s.search, s.rule, s.hints}
	if s.actionError.Text() != "" {
		children = append(children, s.actionError)
	}
	if s.loadError != "" {
		children = append(children, s.retry)
	}
	if len(s.filtered) == 0 {
		children = append(children, s.empty)
	} else {
		children = append(children, s.list, s.scrollbar)
	}
	return children
}
func (s *Screen) Measure(_ ui.LayoutContext, cs ui.Constraints) ui.Size {
	return cs.ClampSize(ui.Size{W: cs.MaxW, H: cs.MaxH})
}
func (s *Screen) Layout(ctx ui.LayoutContext, r ui.Rect) {
	s.SetBounds(r)
	w, h := max(0, r.W), max(0, r.H)
	s.actionError.Layout(ctx, ui.Rect{Y: actionHeight, W: w, H: min(1, max(0, h-actionHeight))})
	if w < launcherCrampedWidth {
		// Constrained terminals keep only the primary actions and workspace
		// collection. Capability state remains available from its normal manager;
		// hiding the left pane keeps Recent workspaces at a useful full width.
		content := workspaceContentRect(ui.Rect{W: w, H: h})
		s.divider.Layout(ctx, ui.Rect{})
		s.skills.Layout(ctx, ui.Rect{})
		s.mcp.Layout(ctx, ui.Rect{})
		s.newSession.Layout(ctx, ui.Rect{X: content.X, W: content.W, H: min(actionHeight, h)})
		workspaceY := min(actionHeight+actionRowGap, h)
		s.openWorkspace.Layout(ctx, ui.Rect{X: content.X, Y: workspaceY, W: content.W, H: min(actionHeight, max(0, h-workspaceY))})
		paneY := min(workspaceY+actionHeight+actionRowGap, h)
		s.layoutWorkspacePane(ctx, ui.Rect{X: content.X, Y: paneY, W: content.W, H: max(0, h-paneY)})
		return
	}

	gap := 3
	leftW := max(24, min(40, w*40/100))
	s.split.SetMinSizes(24, 1)
	s.split.SetMaxFirst(max(24, w-gap-1))
	s.split.SetDividerSize(gap)
	if s.split.HasFirstSize() {
		leftW, gap, _ = s.split.Resolve(w)
	}
	rightX := leftW + gap
	rightW := max(0, w-rightX)
	bodyY := min(actionHeight+actionRowGap, h)
	s.openWorkspace.Layout(ctx, ui.Rect{Y: bodyY, W: leftW, H: min(actionHeight, max(0, h-bodyY))})
	capY := min(bodyY+actionHeight+actionRowGap, h)
	capAvailable := max(0, h-capY)
	skillH := capAvailable / 2
	mcpH := capAvailable - skillH
	s.skills.Layout(ctx, ui.Rect{Y: capY, W: leftW, H: skillH})
	s.mcp.Layout(ctx, ui.Rect{Y: capY + skillH, W: leftW, H: mcpH})
	s.divider.SetCurrentFirst(leftW)
	s.divider.Layout(ctx, ui.Rect{X: leftW, Y: bodyY, W: gap, H: max(0, h-bodyY)})
	content := workspaceContentRect(ui.Rect{X: rightX, Y: bodyY, W: rightW, H: max(0, h-bodyY)})
	// New Session remains the single full-width primary action. The right edge
	// comes from the Recent-workspaces content geometry so both columns stay
	// visually aligned through resize and sidebar changes.
	s.newSession.Layout(ctx, ui.Rect{W: content.X + content.W, H: min(actionHeight, h)})
	s.layoutWorkspacePane(ctx, content)
}

func workspaceContentRect(pane ui.Rect) ui.Rect {
	inset := min(2, pane.W/8)
	pane.X += inset
	pane.W = max(0, pane.W-2*inset)
	return pane
}

func (s *Screen) layoutWorkspacePane(ctx ui.LayoutContext, pane ui.Rect) {
	bottom := pane.Y + pane.H
	headerY := pane.Y
	searchY := headerY + 1
	if pane.H >= 16 {
		searchY++
	}
	s.header.Layout(ctx, ui.Rect{X: pane.X, Y: headerY, W: pane.W, H: min(1, pane.H)})
	s.search.Layout(ctx, ui.Rect{X: pane.X, Y: searchY, W: pane.W, H: min(1, max(0, bottom-searchY))})
	ruleY := searchY + 1
	s.rule.Layout(ctx, ui.Rect{X: pane.X, Y: ruleY, W: pane.W, H: min(1, max(0, bottom-ruleY))})
	listY := ruleY + 2
	if pane.H < 16 {
		listY = ruleY + 1
	}
	listY = min(bottom, listY)
	available := max(0, bottom-listY)
	hintsH := 0
	hintsGap := 0
	if available > 0 {
		hintsH = 1
	}
	if available > 1 {
		hintsGap = 1
	}
	listH := max(0, available-hintsGap-hintsH)
	contentH := max(0, len(s.filtered)*workspaceRowStride-1)
	barW := 0
	if contentH > listH && listH > 0 {
		barW = 1
	}
	listW := max(0, pane.W-barW)
	s.list.Layout(ctx, ui.Rect{X: pane.X, Y: listY, W: listW, H: listH})
	s.empty.Layout(ctx, ui.Rect{X: pane.X, Y: listY, W: listW, H: listH})
	s.scrollbar.Layout(ctx, ui.Rect{X: pane.X + listW, Y: listY, W: barW, H: listH})
	s.retry.Layout(ctx, ui.Rect{})
	if s.loadError != "" {
		b := s.header.Bounds()
		rw := min(9, b.W)
		s.retry.Layout(ctx, ui.Rect{X: b.X + b.W - rw, Y: b.Y, W: rw, H: b.H})
		b.W = max(0, b.W-rw-1)
		s.header.Layout(ctx, b)
	}
	s.hints.SetSegments([]ui.HintSegment{ui.ActionSeg("list.navigate"), ui.ActionSeg("list.open"), ui.ActionSeg("launcher.filter"), ui.TextSeg("Right-click for actions")})
	s.hints.Layout(ctx, ui.Rect{X: pane.X, Y: max(pane.Y, bottom-hintsH), W: pane.W, H: hintsH})
}
