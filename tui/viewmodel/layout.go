package viewmodel

import (
	"crude/tui/settings"
	"crude/tui/ui"
)

type LayoutInfo struct {
	SidebarW    int
	ContentW    int
	InnerW      int
	EditorW     int // width of the editor pane (ContentW - PadLR*2)
	ChatAreaTop int
	ChatH       int
	PadLR       int
}

// frameInsets returns the effective blank inset around the whole shell: the
// persisted theme frame, or — while the settings overlay previews unsaved
// edits (FramePreviewMsg) — the overlay's own complete frame in its place.
// Everything the viewmodel positions is measured inside this frame: the
// header/status/activity/tab bar and the content column all start at its
// inner edge, and the frame itself stays background-painted. Values are
// clamped non-negative so a stale overlay can never invert the geometry.
//
// The overlay REPLACES the persisted frame rather than adding to it: the
// settings form seeds its rows with the effective (persisted) values, so the
// preview message already carries the full frame — adding would double every
// untouched side on each edit.
func (m *App) frameInsets() ui.Insets {
	if m == nil || m.Theme == nil {
		return ui.Insets{}
	}
	if m.frameOverlay != nil {
		ov := *m.frameOverlay
		return ui.Insets{
			Top:    clampFramePad(ov.Top),
			Right:  clampFramePad(ov.Right),
			Bottom: clampFramePad(ov.Bottom),
			Left:   clampFramePad(ov.Left),
		}
	}
	insets := m.Theme.Spacing.Frame
	return ui.Insets{
		Top:    clampFramePad(insets.Top),
		Right:  clampFramePad(insets.Right),
		Bottom: clampFramePad(insets.Bottom),
		Left:   clampFramePad(insets.Left),
	}
}

// applyFramePreview installs the live shell-frame overlay from the settings
// preview. The message carries the complete save-faithful frame (the settings
// model resolves blank fields to what save would produce), so the overlay
// wholesale replaces the persisted frame until save or cancel.
func (m *App) applyFramePreview(frame settings.FramePreviewMsg) {
	if m == nil {
		return
	}
	if frame.Top == nil && frame.Right == nil && frame.Bottom == nil && frame.Left == nil {
		// Zero-value message: no preview to apply.
		m.frameOverlay = nil
		return
	}
	m.frameOverlay = &ui.Insets{
		Top:    clampFramePad(derefPad(frame.Top)),
		Right:  clampFramePad(derefPad(frame.Right)),
		Bottom: clampFramePad(derefPad(frame.Bottom)),
		Left:   clampFramePad(derefPad(frame.Left)),
	}
}

// derefPad resolves an optional preview side; nil falls back to the configured
// default of zero.
func derefPad(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

// clearFramePreview drops any live frame overlay so geometry falls back to
// the persisted values (settings cancel / close without save).
func (m *App) clearFramePreview() {
	if m != nil {
		m.frameOverlay = nil
	}
}

// frameTopRow is the first terminal row usable by shell chrome (below the
// top frame). The legacy geometry hardcoded this as row 1 (one pad row
// above the header).
func (m *App) frameTopRow() int { return m.frameInsets().Top }

// frameBottomRow is the first terminal row BELOW the shell (the row count
// occupied from the bottom by the bottom frame). Legacy value: 0.
func (m *App) frameBottomRow() int { return m.frameInsets().Bottom }

// frameLeftCol is the first terminal column usable by shell chrome.
// Legacy value: 0. The viewmodel folds it into the sidebar/content origin.
func (m *App) frameLeftCol() int { return m.frameInsets().Left }

// frameRightCol is the number of reserved blank columns at the right edge.
// Legacy value: 0. The viewmodel subtracts it from the content width.
func (m *App) frameRightCol() int { return m.frameInsets().Right }

// shellTopRow is the first row of the sidebar chrome (activity rail and
// content tree): the global header (content + separator rows) ends just
// above it. Legacy constant: 3.
func (m *App) shellTopRow() int { return m.frameTopRow() + 2 }

// chromeTopRow is the anchor shared by the tab bar, chat area, deck, and
// input stack: shellTopRow plus the two tab-bar rows when visible.
// Legacy value: 3, or 5 with the tab bar.
func (m *App) chromeTopRow() int {
	top := m.shellTopRow()
	if m.tabBarVisible() {
		top += 2
	}
	return top
}

// chromeBottomRows includes the footer separator/content rows and frame.
func (m *App) chromeBottomRows() int { return 2 + m.frameBottomRow() }

func (m *App) computeLayout() LayoutInfo {
	frame := m.frameInsets()
	sidebarW := frame.Left + m.activityChromeWidth()
	if m.Tree != nil && m.Tree.Visible && m.Tree.Model != nil && m.sidebarScreen != nil {
		sidebarW += m.sidebarPanelWidth() + 1
	}

	contentW := m.width - frame.Right - sidebarW
	padLR := m.Theme.Spacing.PadLR
	innerW := contentW - padLR*2
	if innerW < 20 {
		innerW = 20
	}
	editorW := contentW - padLR*2
	if editorW < 20 {
		editorW = 20
	}

	chatAreaTop := m.chromeTopRow()
	// Editor popovers/cursor navigation use the focused pane, not the whole
	// shell content column. Sidebar hit policy has its own geometry owner.
	if m.isEditorActive() && !m.editorRect.Empty() {
		r := m.editorRect
		padLR, innerW = boundedContentGeometry(r.W, m.Theme.Spacing.PadLR)
		return LayoutInfo{SidebarW: r.X, ContentW: r.W, InnerW: innerW, EditorW: innerW, ChatAreaTop: r.Y, ChatH: r.H, PadLR: padLR}
	}

	return LayoutInfo{
		SidebarW:    sidebarW,
		ContentW:    contentW,
		InnerW:      innerW,
		EditorW:     editorW,
		ChatAreaTop: chatAreaTop,
		PadLR:       padLR,
	}
}
