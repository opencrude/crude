package viewmodel

import (
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
//  5. FocusInput — *explicitly* requested chat focus (view.focus.chat) — wins
//     over the editor even when an editor tab is open, so the user can drive
//     the chat input without closing the editor.
//  6. FocusEditor — an open editor tab is the main document. Wins when no
//     explicit FocusInput/FocusTabBar is on the stack.
//  7. FocusInput — implicit chat input (agent session without an editor tab).
//
// Move focus to the file tree with Ctrl+1 (view.focus.sidebar); press Ctrl+1 or
// Esc again to return. Status-bar hints must follow this same resolution.
//
// The FocusInput-on-stack check (step 5) is inserted between FocusTabBar and
// the editor-or-input fallthrough so an explicit "Focus Chat" keeps working
// while an editor is open. Without it, unfocus()/Clear() would let the natural
// editor-open resolution win and "Focus Chat" would be a no-op — the exact bug
// the palette cleanup fixed.
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
	// Explicit FocusInput on the stack wins over the editor. This lets
	// view.focus.chat drive the chat input while an editor is open.
	if top == FocusInput {
		return FocusInput
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

// setFocusInput explicitly moves keyboard focus to the chat input,
// pushing FocusInput onto the stack so focusTarget() returns FocusInput
// even when an editor tab is open. Used by view.focus.chat.
func (m *App) setFocusInput() {
	m.setSessionDeckFocused(false)
	m.FocusStack.Clear()
	m.FocusStack.Push(FocusInput)
}

// setFocusEditor explicitly moves keyboard focus to the editor pane.
// Used by view.focus.editor. Only meaningful when an editor tab is open;
// callers should check before calling (the palette handler emits a
// status-bar message when no editor is open).
//
// Implementation: clear the stack so the natural focusTarget()
// fallthrough returns FocusEditor. Pushing a FocusEditor token would be
// dead weight — focusTarget() never consults the stack for FocusEditor
// because launcherHostVisible() is false whenever an editor is open
// (see host_capture.go:37), so the FocusTree/FocusLauncher/FocusTabBar
// checks are skipped and the editor-open branch wins. Clearing the
// stack also drops any stale FocusInput (from a prior view.focus.chat)
// that would otherwise shadow the editor.
func (m *App) setFocusEditor() {
	m.setSessionDeckFocused(false)
	m.FocusStack.Clear()
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
	m.syncRootFocus()
}

func (m *App) syncRenderFocus() {
	if m.renderCtx == nil {
		return
	}
	focused := m.focusTarget() == FocusEditor
	m.renderCtx.Focused = focused
}
