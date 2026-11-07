package viewmodel

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"crude/tui/editor"
	"crude/tui/screens/gitpanel"
	"crude/tui/window"
)

// Back and forward navigation, as in established IDEs (VS Code's Go Back /
// Go Forward). The history records the places the user worked: every tab
// change, every jump of the caret by navigationJumpLines or more inside one
// file, and the start of every code navigation (Go to Definition, references,
// Ctrl+click). Go Back returns to the last place; Go Forward undoes Go Back.
//
// Recording is O(1) per update and never allocates in steady state: the
// shell compares the focused place with the last observed one after each
// update. Restoring is synchronous when the place is still open (the tab, its
// caret and its scroll), so Back and Forward are instant; a file closed since
// then reopens in the pane it was in.

// navigationJumpLines is the caret distance that counts as a new place, the
// same threshold VS Code uses for its editor navigation stack.
const navigationJumpLines = 10

// navigationHistoryLimit bounds each stack.
const navigationHistoryLimit = 64

// navLocation is one place: a tab, and for files the caret and scroll.
type navLocation struct {
	tab     window.TabID
	group   window.GroupID
	path    string // file path; empty for agent and panel tabs
	caret   editor.Position
	scrollY int
}

type navigationHistory struct {
	back, forward []navLocation
	current       navLocation
	observed      bool
	// restoring is set while Back/Forward switches tabs, so the switch does
	// not record the place it leaves a second time.
	restoring bool
}

func (m *App) currentNavLocation() (navLocation, bool) {
	if m.Win == nil {
		return navLocation{}, false
	}
	tab := m.Win.ActiveTab()
	if tab == nil {
		return navLocation{}, false
	}
	loc := navLocation{tab: tab.ID, group: tab.Group}
	if v := m.editorView(tab.ID); v != nil {
		loc.path = v.Path
		loc.caret, loc.scrollY = v.Editor.Caret()
	}
	return loc, true
}

// syncNavigationHistory observes the focused place after each update.
func (m *App) syncNavigationHistory() {
	loc, ok := m.currentNavLocation()
	if !ok {
		return
	}
	h := &m.navHistory
	if !h.observed {
		h.current, h.observed = loc, true
		return
	}
	cur := h.current
	switch {
	case cur.tab == loc.tab || cur.tab == 0 && cur.path != "" && cur.path == loc.path:
		if loc.path != "" && lineDistance(loc.caret.Line, cur.caret.Line) >= navigationJumpLines {
			h.push(cur)
		}
	default:
		h.push(cur)
	}
	h.current = loc
}

// noteLeavingPlace runs before every tab switch is applied: the focused file
// view is the place being left, with its caret as it is now. A switch made
// outside an update (an open from a menu callback) is then still recorded.
func (m *App) noteLeavingPlace() {
	h := &m.navHistory
	if h.restoring || m.EditorState == nil {
		return
	}
	v := m.editorView(m.EditorState.ActiveTab)
	if v == nil {
		return
	}
	loc := navLocation{tab: v.Tab, group: m.Win.GroupOf(v.Tab), path: v.Path}
	loc.caret, loc.scrollY = v.Editor.Caret()
	if !h.observed || h.current.tab != loc.tab {
		if h.observed && h.current.tab != 0 && m.Win.IndexOf(h.current.tab) >= 0 {
			h.push(h.current)
		}
		h.current, h.observed = loc, true
		return
	}
	h.current = loc
}

// recordNavigationOrigin keeps the current place before an explicit jump, even
// a short one inside the same file.
func (m *App) recordNavigationOrigin() {
	if loc, ok := m.currentNavLocation(); ok {
		m.navHistory.push(loc)
		m.navHistory.current, m.navHistory.observed = loc, true
	}
}

// recordNavigationLanding makes the place a navigation reached the current
// one, so the next jump records it.
func (m *App) recordNavigationLanding() {
	if loc, ok := m.currentNavLocation(); ok {
		m.navHistory.current, m.navHistory.observed = loc, true
	}
}

func (h *navigationHistory) push(loc navLocation) {
	if n := len(h.back); n > 0 && h.back[n-1].same(loc) {
		// Coalesce: one place is recorded once, at its latest caret.
		h.back[n-1] = loc
	} else {
		if n >= navigationHistoryLimit {
			copy(h.back, h.back[1:])
			h.back = h.back[:n-1]
		}
		h.back = append(h.back, loc)
	}
	h.forward = h.forward[:0]
}

// same reports whether two places are the same for history purposes: the same
// tab, and for files a caret within the jump threshold.
func (l navLocation) same(o navLocation) bool {
	return l.tab == o.tab && l.path == o.path && lineDistance(l.caret.Line, o.caret.Line) < navigationJumpLines
}

func lineDistance(a, b int) int {
	if a < b {
		return b - a
	}
	return a - b
}

// canNavigate reports whether Go Back (back) or Go Forward has a place.
func (m *App) canNavigate(back bool) bool {
	h := &m.navHistory
	stack := h.forward
	if back {
		stack = h.back
	}
	for i := len(stack) - 1; i >= 0; i-- {
		if m.navLocationReachable(stack[i]) {
			return true
		}
	}
	return false
}

func (m *App) navLocationReachable(loc navLocation) bool {
	return m.Win.IndexOf(loc.tab) >= 0 || loc.path != ""
}

// navigateHistory runs Go Back (back) or Go Forward. Places that no longer
// exist (a closed agent tab) are skipped.
func (m *App) navigateHistory(back bool) tea.Cmd {
	m.syncNavigationHistory()
	h := &m.navHistory
	src, dst := &h.forward, &h.back
	if back {
		src, dst = &h.back, &h.forward
	}
	for len(*src) > 0 {
		loc := (*src)[len(*src)-1]
		*src = (*src)[:len(*src)-1]
		if !m.navLocationReachable(loc) {
			continue
		}
		if cur, ok := m.currentNavLocation(); ok {
			*dst = append(*dst, cur)
		}
		h.current = loc
		return m.restoreNavLocation(loc)
	}
	return nil
}

func (m *App) restoreNavLocation(loc navLocation) tea.Cmd {
	m.closeNavigation()
	m.navHistory.restoring = true
	defer func() { m.navHistory.restoring = false }()
	if index := m.Win.IndexOf(loc.tab); index >= 0 {
		if index != m.Win.ActiveIndex() || m.Win.ActiveGroup() != loc.group {
			m.Win.SwitchTo(index)
			m.handleTabSwitch()
		}
		m.focusMainGroup(m.Win.GroupOf(loc.tab))
		if v := m.editorView(loc.tab); v != nil && loc.path != "" {
			v.Editor.RestoreCaret(loc.caret, loc.scrollY)
		}
		m.forceFrame()
		return nil
	}
	// The file was closed: open it again where it was, at the same place.
	group := loc.group
	if !m.Win.HasGroup(group) {
		group = m.Win.ActiveGroup()
	}
	if v := m.groupEditorView(group, loc.path); v != nil {
		m.Win.SwitchTo(m.Win.IndexOf(v.Tab))
		m.handleTabSwitch()
		v.Editor.RestoreCaret(loc.caret, loc.scrollY)
		m.navHistory.current = navLocation{tab: v.Tab, group: group, path: loc.path, caret: loc.caret, scrollY: loc.scrollY}
		return nil
	}
	cmd := m.openPaneFiles([]string{loc.path}, group)
	if cmd == nil {
		return nil
	}
	m.navHistory.current.tab = 0 // adopted by the view that opens
	restore := loc
	return func() tea.Msg {
		msg, ok := cmd().(paneFilesOpenedMsg)
		if ok {
			msg.restore = &restore
		}
		return msg
	}
}

// applyNavRestore positions a view reopened by Go Back or Go Forward.
func (m *App) applyNavRestore(loc *navLocation) {
	if loc == nil || m.EditorState.Editor == nil || m.EditorState.ActiveView != loc.path {
		return
	}
	m.EditorState.Editor.RestoreCaret(loc.caret, loc.scrollY)
	m.recordNavigationLanding()
}

// focusedDiffDocument reports whether a diff document has the keyboard; it
// binds Alt+Left/Right to its previous and next file.
func (m *App) focusedDiffDocument() bool {
	if m.rootHost == nil {
		return false
	}
	focused := m.rootHost.Focused()
	return focused == gitpanel.PreviewID || focused == gitpanel.CommitDocID ||
		m.isGitReviewActive() && strings.HasSuffix(string(focused), "/document")
}

// handleNavigationMouse runs Go Back / Go Forward for the mouse's back and
// forward buttons, anywhere in the window.
func (m *App) handleNavigationMouse(msg tea.MouseClickMsg) (tea.Cmd, bool) {
	switch msg.Button {
	case tea.MouseBackward:
		return m.navigateHistory(true), true
	case tea.MouseForward:
		return m.navigateHistory(false), true
	}
	return nil, false
}
