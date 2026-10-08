package viewmodel

import (
	tea "charm.land/bubbletea/v2"

	"crude/tui/screens/planpopover"
	"crude/tui/screens/shellchat"
	"crude/tui/ui"
	"crude/tui/wm"
	"crude/types/activity"
)

// planHoverState binds a passive reading surface to the exact mounted chat,
// never the focused-agent alias. It borrows the plan owner's immutable snapshot.
type planHoverState struct {
	owner  *shellchat.Screen
	plan   *activity.Plan
	body   *planpopover.Screen
	widget *ui.WindowFrameWidget
	anchor ui.Rect
	width  int
	height int
}

func (m *App) planActivityRect(screen *shellchat.Screen) ui.Rect {
	if screen == nil || m.rootHost == nil {
		return ui.Rect{}
	}
	r, ok := m.rootHost.ScreenRect(screen.ID())
	if !ok || r.Empty() {
		return ui.Rect{}
	}
	local := screen.ActivityBounds()
	if local.Empty() {
		return ui.Rect{}
	}
	local.X += r.X
	local.Y += r.Y
	return local
}

func (m *App) clearPlanHover() {
	if m.overlayHost != nil {
		m.overlayHost.Close(ui.OverlayPlanHover)
	}
	m.planHover = nil
}

// routePlanHoverMotion runs after captured shell gestures, before ordinary
// popup routing. Moving from the status band into the adjacent popover keeps it
// open; leaving both closes it without changing the composer or selected pane.
func (m *App) routePlanHoverMotion(msg tea.MouseMotionMsg) bool {
	if m.windowManager == nil || m.overlayHost == nil || m.rootHost == nil {
		return false
	}
	if h := m.planHover; h != nil && h.widget.Host().HasPointerCapture() && !m.sessionDeckBlocking() {
		if fw := m.overlayHost.Window(ui.OverlayPlanHover); fw != nil && m.windowManager.TopWindow() == fw {
			m.forwardEventToWindowContent(fw, msg)
			return true
		}
	}
	if m.sessionDeckBlocking() || msg.Button != tea.MouseNone {
		m.clearPlanHover()
		return false
	}
	if top := m.windowManager.TopWindow(); top != nil && top.ID != string(ui.OverlayPlanHover) {
		m.clearPlanHover()
		return false
	}
	if m.planHover != nil {
		m.syncPlanHover()
		if h := m.planHover; h != nil {
			if fw := m.overlayHost.Window(ui.OverlayPlanHover); fw != nil && fw.ContainsPoint(msg.X, msg.Y) {
				m.forwardEventToWindowContent(fw, msg)
				return true
			}
			if h.anchor.Contains(msg.X, msg.Y) {
				return false
			}
		}
		m.clearPlanHover()
	}
	if m.mainPanes == nil {
		return false
	}
	for _, group := range m.mainPanes.groups {
		if group.view == nil || group.chat.Empty() {
			continue
		}
		screen := group.view.chat
		anchor := m.planActivityRect(screen)
		if !anchor.Contains(msg.X, msg.Y) || screen.ActivityPlan() == nil {
			continue
		}
		m.openPlanHover(screen, anchor)
		break
	}
	return false
}

func (m *App) openPlanHover(owner *shellchat.Screen, anchor ui.Rect) {
	p := owner.ActivityPlan()
	if p == nil || len(p.Steps) == 0 || m.width < 12 || anchor.Y < 4 {
		return
	}
	body := planpopover.New(m.Theme, p)
	size := body.PreferredSize(min(m.width-2, anchor.W), min(anchor.Y, max(4, m.height*2/3)))
	if size.W < 10 || size.H < 3 {
		return
	}
	widget := ui.NewWindowFrameWidget(m.Theme, "plan-hover/frame", ui.FramePopover, "Plan", body, nil, false, size.W, size.H)
	widget.Focus(body.ID())
	fw := wm.NewFloatingWindow(string(ui.OverlayPlanHover), "", widget, size.W, size.H,
		wm.WithDecoration(true), wm.WithFocusable(true), wm.WithMovable(false),
		wm.WithResizable(false), wm.WithClosable(false), wm.WithModal(false))
	x, y := clampPopupPosition(anchor.X, anchor.Y-size.H, size.W, size.H, m.width, m.height)
	fw.SetPosition(x, y)
	policy := ui.PopoverPolicy(true)
	policy.Passive = true
	m.overlayHost.Open(ui.OverlayPlanHover, fw, policy, func() { m.planHover = nil })
	m.planHover = &planHoverState{owner: owner, plan: p, body: body, widget: widget, anchor: anchor, width: m.width, height: m.height}
}

// syncPlanHover refreshes only the already-open read-only projection. Hidden
// owners, plan completion, workspace switches and other overlays retire it.
func (m *App) syncPlanHover() {
	h := m.planHover
	if h == nil {
		return
	}
	anchor := m.planActivityRect(h.owner)
	p := h.owner.ActivityPlan()
	fw := m.overlayHost.Window(ui.OverlayPlanHover)
	if fw == nil || anchor.Empty() || p == nil || len(p.Steps) == 0 || m.sessionDeckBlocking() ||
		m.windowManager.TopWindow() != fw || m.width < 12 || anchor.Y < 4 {
		m.clearPlanHover()
		return
	}
	if p == h.plan && h.anchor == anchor && h.width == m.width && h.height == m.height {
		return
	}
	h.body.SetPlan(p)
	size := h.body.PreferredSize(min(m.width-2, anchor.W), min(anchor.Y, max(4, m.height*2/3)))
	if size.W < 10 || size.H < 3 {
		m.clearPlanHover()
		return
	}
	fw.SetSize(size.W, size.H)
	x, y := clampPopupPosition(anchor.X, anchor.Y-size.H, size.W, size.H, m.width, m.height)
	fw.SetPosition(x, y)
	h.widget.Host().MarkLayout()
	fw.MarkContentDirty()
	h.plan, h.anchor, h.width, h.height = p, anchor, m.width, m.height
}
