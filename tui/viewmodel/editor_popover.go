package viewmodel

import (
	"crude/tui/editor"
	"crude/tui/ui"
	"crude/tui/wm"
	"crude/types"
)

// Transient editor aids share chrome and dismissal. Their request owners keep
// cancellation and source validation; WindowFrame/OverlayHost keep interaction.
func (m *App) newEditorPopover(id ui.OverlayID, title string, body ui.Component, focus ui.ComponentID, bounds ui.Rect) (*ui.WindowFrameWidget, *wm.FloatingWindow) {
	frame := ui.NewWindowFrameWidget(m.Theme, ui.ComponentID(string(id)+"/frame"),
		ui.FramePopover, title, body, nil, false, bounds.W, bounds.H)
	if focus != "" {
		frame.Focus(focus)
	}
	window := wm.NewFloatingWindow(string(id), "", frame, bounds.W, bounds.H,
		wm.WithDecoration(false), wm.WithMovable(false), wm.WithResizable(false),
		wm.WithClosable(false), wm.WithFocusable(true), wm.WithModal(false),
		wm.WithClickOutside(wm.ClickOutsideCloseRoute))
	window.SetPosition(bounds.X, bounds.Y)
	return frame, window
}

// Readers, outline and rename retain their original source point. Other LSP
// request owners already retain this information in their invocation state.
type editorPopoverAnchor struct {
	editor               *editor.Editor
	source               types.InsightSource
	selection            codeActionSelection
	generation, revision uint64
	width, height        int
}

func (m *App) editorPopoverAnchor(ed *editor.Editor, width, height int) *editorPopoverAnchor {
	if ed == nil {
		return nil
	}
	source, current := ed.CurrentInsightSource()
	if !current {
		return nil
	}
	return &editorPopoverAnchor{editor: ed, source: source, selection: currentCodeActionSelection(ed),
		generation: m.workspaceSwitchGen.Load(), revision: ed.Revision(), width: width, height: height}
}

func (a *editorPopoverAnchor) current(m *App) bool {
	return a != nil && m.EditorState.Editor == a.editor && a.editor.IsOpen() &&
		a.generation == m.workspaceSwitchGen.Load() && a.revision == a.editor.Revision() &&
		a.editor.MatchesInsightSource(a.source) && a.selection == currentCodeActionSelection(a.editor)
}

func (m *App) positionSourcePopover(id ui.OverlayID, a *editorPopoverAnchor) bool {
	window := m.overlayHost.Window(id)
	if window == nil || a == nil {
		return false
	}
	root := window.Content.(interface{ Root() ui.Component }).Root().(*ui.WindowFrame)
	size := root.Measure(ui.LayoutContext{}, ui.Constraints{
		MaxW: min(a.width, max(1, m.width-4)), MaxH: min(a.height, max(1, m.height-4)),
	})
	bounds, visible := m.editorPositionHelpBounds(a.editor, editor.Position{Line: a.selection.line, Col: a.selection.col}, size.W, size.H)
	if !visible {
		m.overlayHost.Close(id)
		return false
	}
	if x, y := window.Position(); x != bounds.X || y != bounds.Y {
		window.SetPosition(bounds.X, bounds.Y)
	}
	if w, h := window.Size(); w != bounds.W || h != bounds.H {
		window.SetSize(bounds.W, bounds.H)
	}
	return true
}

func (m *App) syncSourcePopovers() {
	for _, popup := range []struct {
		id     ui.OverlayID
		anchor *editorPopoverAnchor
	}{
		{ui.OverlayTextPrompt, m.LSPPopup.RenameInput},
		{ui.OverlaySymbolHover, m.LSPPopup.Hover},
		{ui.OverlayOutline, m.LSPPopup.Outline},
	} {
		if popup.anchor != nil && !popup.anchor.current(m) {
			m.overlayHost.Close(popup.id)
		}
	}
}

func (m *App) positionSourcePopovers() {
	m.positionSourcePopover(ui.OverlayTextPrompt, m.LSPPopup.RenameInput)
	m.positionSourcePopover(ui.OverlaySymbolHover, m.LSPPopup.Hover)
	m.positionSourcePopover(ui.OverlayOutline, m.LSPPopup.Outline)
}

func (m *App) openEditorTextPrompt(title, prompt, initial string, onSubmit func(string) error) {
	anchor := m.editorPopoverAnchor(m.EditorState.Editor, 70, 10)
	if anchor == nil {
		return
	}
	m.closeTextPrompt()
	state := ui.NewTextPromptState(ui.TextPromptRename, title, prompt, initial)
	state.OnSubmit = onSubmit
	body := ui.TextPromptBody(m.Theme, state)
	_, window := m.newEditorPopover(ui.OverlayTextPrompt, title, body, ui.TextPromptFieldID,
		ui.Rect{W: ui.TextPromptPopupWidth(state), H: ui.TextPromptPopupHeight(state)})
	anchor.width = ui.TextPromptPopupWidth(state)
	m.overlayHost.Open(ui.OverlayTextPrompt, window, ui.PopoverPolicy(true), func() {
		if m.TextPrompt == state {
			m.TextPrompt = nil
			m.LSPPopup.RenameInput = nil
		}
	})
	m.TextPrompt, m.LSPPopup.RenameInput = state, anchor
	m.positionSourcePopover(ui.OverlayTextPrompt, anchor)
}
