package viewmodel

import (
	"crude/tui/screens/shelltabs"
	"crude/workspace"
)

type FocusTarget = workspace.FocusTarget

const (
	FocusNone           = workspace.FocusNone
	FocusTree           = workspace.FocusTree
	FocusEditor         = workspace.FocusEditor
	FocusTabBar         = workspace.FocusTabBar
	FocusInput          = workspace.FocusInput
	FocusFloatingWindow = workspace.FocusFloatingWindow
	FocusLauncher       = workspace.FocusLauncher
)

// focusTarget resolves which region owns keyboard input.
//
// Precedence (highest first):
//  1. FocusFloatingWindow — a focused OverlayHost / FloatingWindow is modal for keys.
//  2. FocusTree — only when the sidebar was *explicitly* focused (Ctrl+1 toggle,
//     mouse click on the tree, or EnterFilterMode). Explicit tree focus wins even
//     while the welcome launcher is still visible, so `/` filter and tree nav work
//     before any tab is open. Startup must NOT push FocusTree; the launcher keeps
//     Enter = New Session until the user moves focus.
//  3. FocusLauncher — welcome screen with no agent/editor tabs open.
//  4. FocusTabBar — explicit tab-strip focus (Ctrl+T / click).
//  5. FocusEditor — an open editor tab is the main document.
//  6. FocusInput — chat input (agent session without an editor tab).
//
// Move focus to the file tree with Ctrl+1 (view.focus.sidebar); press Ctrl+1 or
// Esc again to return. Status-bar hints must follow this same resolution.
func (m *App) focusTarget() FocusTarget {
	if m.windowManager != nil && m.windowManager.Focused() != nil {
		return FocusFloatingWindow
	}
	top := FocusNone
	if m.FocusStack != nil {
		top = m.FocusStack.Top()
	}
	if top == FocusTree && m.Tree != nil && m.Tree.Model != nil && m.Tree.Visible {
		return FocusTree
	}
	if m.launcherHostVisible() {
		return FocusLauncher
	}
	if top == FocusTabBar {
		return FocusTabBar
	}
	if m.EditorState != nil && m.EditorState.ActiveView != "" && m.EditorState.Editor != nil && m.EditorState.Editor.IsOpen() {
		return FocusEditor
	}
	return FocusInput
}

func (m *App) unfocus() {
	m.setSessionDeckFocused(false)
	m.FocusStack.Clear()
}

func (m *App) setFocusTree() {
	m.setSessionDeckFocused(false)
	m.FocusStack.Clear()
	m.FocusStack.Push(FocusTree)
}

// toggleFocusTree explicitly moves keyboard focus to the file tree, or back
// to the previous region (launcher / editor / input) when the tree already
// owns focus. Bound to Ctrl+1 (view.focus.sidebar).
func (m *App) toggleFocusTree() {
	if m.Tree == nil || m.Tree.Model == nil || !m.Tree.Visible {
		return
	}
	if m.FocusStack.Top() == FocusTree {
		m.FocusStack.Pop()
		return
	}
	m.setFocusTree()
}

func (m *App) setFocusTabBar() {
	m.setSessionDeckFocused(false)
	m.FocusStack.Clear()
	m.FocusStack.Push(FocusTabBar)
	if m.tabHost != nil {
		m.tabHost.Focus(shelltabs.TabsID)
	}
}

func (m *App) syncRenderFocus() {
	if m.renderCtx == nil {
		return
	}
	focused := m.focusTarget() == FocusEditor
	m.renderCtx.Focused = focused
}
