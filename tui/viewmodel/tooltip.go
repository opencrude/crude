package viewmodel

import (
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/tui/screens/activitylist"
	"crude/tui/screens/launcher"
	"crude/tui/ui"
	"crude/tui/window"
	"crude/tui/wm"
)

// pointerTooltip is the shell's one tooltip. Controls implement
// ui.TooltipSource; this owner decides when the popover opens and closes.
type pointerTooltip struct {
	seq       uint64
	pending   *tooltipTarget // waiting for ui.TooltipDelay
	shown     *tooltipTarget
	dismissed *tooltipTarget // closed by input; stays closed until the pointer leaves it
}

// tooltipTarget is one hovered item: its source control, text and the item's
// screen rectangle.
type tooltipTarget struct {
	source ui.ComponentID
	text   string
	anchor ui.Rect
}

func (t *tooltipTarget) same(other *tooltipTarget) bool {
	return t != nil && other != nil && t.source == other.source && t.anchor == other.anchor && t.text == other.text
}

// tooltipDueMsg ends the hover delay of one pending target. A later pointer
// move makes it stale through seq.
type tooltipDueMsg struct{ seq uint64 }

// syncPointerTooltip follows pointer motion and closes the tooltip on any
// input that acts, so it never covers what the user is about to use.
func (m *App) syncPointerTooltip(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.MouseMotionMsg:
		return m.setTooltipTarget(m.tooltipTargetAt(msg.Mouse().X, msg.Mouse().Y))
	case tea.KeyPressMsg, tea.MouseClickMsg, tea.MouseWheelMsg, tea.PasteMsg, tea.BlurMsg:
		m.dismissTooltip()
	case workspaceActivityResultMsg, launcherWorkspacesMsg:
		// Activity can settle while the pointer is still on the row.
		target := m.tooltip.shown
		if target == nil {
			target = m.tooltip.pending
		}
		if target != nil && (target.source == activitylist.ListID || target.source == launcher.WorkspaceListID) {
			return m.setTooltipTarget(m.tooltipTargetAt(target.anchor.X, target.anchor.Y))
		}
	}
	return nil
}

// tooltipTargetAt asks the control in the topmost mounted host. Ordinary
// dialogs still shield the shell; a pane window owns the same tab controls
// as its docked counterpart and can expose their tooltips.
func (m *App) tooltipTargetAt(x, y int) *tooltipTarget {
	if m.rootHost == nil || m.overlayHost == nil {
		return nil
	}
	host := m.rootHost
	originX, originY := 0, 0
	if m.windowManager != nil {
		if fw := m.windowManager.WindowAt(x, y); fw != nil && fw.ID != string(ui.OverlayTooltip) {
			if group, ok := paneGroupFromOverlay(ui.OverlayID(fw.ID)); ok {
				host = m.paneWindowHost(group)
				originX, originY = fw.Position()
			} else {
				return nil
			}
		}
	}
	if host == nil {
		return nil
	}
	id := host.Hovered()
	source, ok := host.Component(id).(ui.TooltipSource)
	if !ok {
		return nil
	}
	text, anchor, ok := source.Tooltip()
	if !ok {
		return nil
	}
	rect, ok := host.ScreenRect(id)
	if !ok {
		return nil
	}
	anchor.X += rect.X + originX
	anchor.Y += rect.Y + originY
	if !anchor.Contains(x, y) {
		return nil
	}
	return &tooltipTarget{source: id, text: text, anchor: anchor}
}

func (m *App) setTooltipTarget(target *tooltipTarget) tea.Cmd {
	tip := &m.tooltip
	if target == nil {
		tip.dismissed = nil
		m.hideTooltip()
		return nil
	}
	if target.same(tip.dismissed) || target.same(tip.shown) || target.same(tip.pending) {
		return nil
	}
	tip.dismissed = nil
	if tip.shown != nil {
		// A tooltip is already open: moving to the next tab shows its
		// tooltip at once instead of making the user wait again.
		m.hideTooltip()
		m.openTooltip(target)
		return nil
	}
	m.hideTooltip()
	tip.pending = target
	seq := tip.seq
	return tea.Tick(ui.TooltipDelay, func(time.Time) tea.Msg { return tooltipDueMsg{seq: seq} })
}

func (m *App) handleTooltipDue(msg tooltipDueMsg) {
	if msg.seq != m.tooltip.seq || m.tooltip.pending == nil {
		return
	}
	target := m.tooltip.pending
	m.tooltip.pending = nil
	m.openTooltip(target)
}

func (m *App) openTooltip(target *tooltipTarget) {
	w := ui.TooltipFrameWidth(target.text, max(1, m.width-2))
	tokens := ui.NewTokenSet(m.Theme)
	area := ui.NewTextArea(ui.NewID(ui.ComponentID(ui.OverlayTooltip), "text"), tokens, true)
	area.SetReadOnly(true)
	area.SetValue(target.text)
	area.SetCursorFlat(0)
	// A long path wraps instead of being cut at a narrow terminal.
	h := min(area.VisualLineCount(max(1, w-ui.TooltipChrome))+2, max(3, m.height-2))
	frame := ui.NewWindowFrameWidget(m.Theme, ui.NewID(ui.ComponentID(ui.OverlayTooltip), "frame"), ui.FramePopover, "", area, nil, false, w, h)
	fw := wm.NewFloatingWindow(string(ui.OverlayTooltip), "", frame, w, h,
		wm.WithDecoration(true), wm.WithFocusable(false), wm.WithMovable(false), wm.WithResizable(false),
		wm.WithClosable(false), wm.WithModal(false), wm.WithClickOutside(wm.ClickOutsideCloseRoute))
	// Below the item when it fits, otherwise above it.
	y := target.anchor.Y + 1
	if y+h > m.height && target.anchor.Y-h >= 0 {
		y = target.anchor.Y - h
	}
	x, y := clampPopupPosition(target.anchor.X, y, w, h, m.width, m.height)
	fw.SetPosition(x, y)
	policy := ui.PopoverPolicy(true)
	policy.Passive = true
	m.overlayHost.Open(ui.OverlayTooltip, fw, policy, func() {
		if m.tooltip.shown == target {
			m.tooltip.shown = nil
		}
	})
	m.tooltip.shown = target
	m.forceFrame()
}

// hideTooltip closes the open tooltip and forgets a pending one.
func (m *App) hideTooltip() {
	m.tooltip.seq++
	m.tooltip.pending = nil
	if m.tooltip.shown != nil && m.overlayHost != nil {
		m.overlayHost.Close(ui.OverlayTooltip)
		m.forceFrame()
	}
	m.tooltip.shown = nil
}

// dismissTooltip hides the tooltip after input. It does not come back for the
// same item until the pointer leaves it.
func (m *App) dismissTooltip() {
	if target := m.tooltip.shown; target != nil {
		m.tooltip.dismissed = target
	} else if target := m.tooltip.pending; target != nil {
		m.tooltip.dismissed = target
	}
	m.hideTooltip()
}

// tabTooltip is the hover text of a main tab: the absolute path of a file and
// the identity and state of an agent.
func (m *App) tabTooltip(id window.TabID) string {
	if m.Win == nil {
		return ""
	}
	tab, ok := m.Win.TabAt(m.Win.IndexOf(id))
	if !ok {
		return ""
	}
	// Every kind of tab says what it shows: the full name its label shortens
	// and the state its marker only hints at.
	switch {
	case isImageTab(&tab):
		return tab.Resource.ID
	case m.isEditorTabTitle(tab.Title):
		return m.editorTabTooltip(id, tab)
	case m.isAgentTabTitle(tab.Title):
		return m.agentTooltip(m.agentIDFromTabTitle(tab.Title))
	case tab.Resource.Kind == window.ResourceLocalDiff:
		return m.localDiffTooltip(m.localDiffs[id])
	case tab.Resource.Kind == window.ResourceGitReview:
		return m.gitReviewTooltip(tab)
	case tab.Resource.Kind == window.ResourcePanel:
		if terminal, ok := terminalIDFromPanel(tab.Resource.ID); ok {
			return m.terminalTooltip(terminal)
		}
		if title := panelTitles[tab.Resource.ID]; title != "" {
			return title + "\nClose the tab to return it to the bottom panel."
		}
	}
	return ""
}

func (m *App) editorTabTooltip(id window.TabID, tab window.Tab) string {
	ed := m.editorForTab(id)
	path := m.editorPathFromTabTitle(tab.Title)
	if ed != nil && ed.FilePath() == "" {
		path = ed.BaseTitle()
	} else if !filepath.IsAbs(path) && m.ws != nil && m.ws.RootPath() != "" {
		path = filepath.Join(m.ws.RootPath(), path)
	}
	lines := []string{path}
	if ed == nil {
		return path
	}
	switch conflict, removed := m.editorDiskState(ed); {
	case removed:
		lines = append(lines, "Deleted on disk")
	case conflict:
		lines = append(lines, "Changed on disk. Choose the text to keep.")
	}
	if ed.Modified() {
		lines = append(lines, "Unsaved changes")
	}
	return strings.Join(lines, "\n")
}

// localDiffTooltip names the compared file and versions, and the decisions
// still open.
func (m *App) localDiffTooltip(view *localDiffView) string {
	if view == nil {
		return ""
	}
	comparison := view.screen.Comparison()
	path := comparison.Path
	if path == "" && view.source != nil {
		path = view.source.FilePath()
	}
	lines := []string{path}
	if comparison.BeforeLabel != "" && comparison.AfterLabel != "" {
		lines = append(lines, "Left: "+comparison.BeforeLabel+". Right: "+comparison.AfterLabel+".")
	}
	if status := view.screen.ComparisonReader().ResolutionStatus(); status != "" {
		lines = append(lines, status)
	} else if !view.screen.Resolvable() {
		lines = append(lines, "Read-only")
	}
	return strings.Join(lines, "\n")
}

func (m *App) gitReviewTooltip(tab window.Tab) string {
	view := m.gitReviews[tab.ID]
	if view == nil {
		return ""
	}
	root := ""
	if m.gitPanel != nil {
		root = m.gitPanel.Snapshot().RepositoryRoot
	}
	if view.commit != "" {
		return "Commit " + view.commit + "\nRead-only"
	}
	scope := strings.TrimPrefix(tab.Resource.ID, "changes:")
	if scope == "" {
		return "Uncommitted changes in " + root
	}
	if root != "" {
		scope = filepath.Join(root, filepath.FromSlash(scope))
	}
	return "Uncommitted changes in " + scope
}

// terminalTooltip names a terminal, its folder and its state.
func (m *App) terminalTooltip(id string) string {
	info, ok := m.terminalInfo(id)
	if !ok {
		return ""
	}
	lines := []string{info.DisplayName()}
	if screen := m.termScreens[id]; screen != nil && screen.Title() != info.DisplayName() && screen.Title() != "Terminal" {
		lines[0] += " · " + screen.Title()
	}
	if info.CWD != "" {
		lines = append(lines, info.CWD)
	}
	switch screen := m.termScreens[id]; {
	case !info.Alive:
		lines = append(lines, "The process ended")
	case screen != nil && !screen.ReadOnly() && !screen.Controlled():
		lines = append(lines, "Gets input from another window")
	}
	return strings.Join(lines, "\n")
}

func (m *App) agentTooltip(agentID string) string {
	if m.ws == nil || m.ws.AgentMgr() == nil {
		return ""
	}
	node := m.ws.AgentMgr().Get(agentID)
	if node == nil {
		return ""
	}
	lines := []string{m.agentLabel(node)}
	if node.IsOrchestrator() && m.isErrandAgent(agentID) {
		lines = append(lines, "Works in the background and asks with a prompt")
	}
	state := capitalizeFirst(m.agentStatus(agentID))
	if node.Model != "" {
		state += " · " + node.Model
	}
	return strings.Join(append(lines, state), "\n")
}

func capitalizeFirst(text string) string {
	if text == "" {
		return ""
	}
	return strings.ToUpper(text[:1]) + text[1:]
}
