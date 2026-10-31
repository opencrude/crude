package viewmodel

import (
	"fmt"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"crude/tui/editor"
	"crude/tui/ui"
	"crude/tui/wm"
)

type editorSnippetState struct {
	editor     *editor.Editor
	path       string
	generation uint64
	choice     *editorSnippetChoice
}

type editorSnippetChoice struct {
	ui.Base
	list      *ui.List
	scrollbar *ui.ScrollbarControl
	state     *editorSnippetState
	token     editor.SnippetChoices
	window    *wm.FloatingWindow
}

type editorSnippetChoiceMsg struct {
	choice *editorSnippetChoice
	value  string
}

func (p *editorSnippetChoice) Children() []ui.Component {
	return []ui.Component{p.list, p.scrollbar}
}

func (p *editorSnippetChoice) Layout(ctx ui.LayoutContext, r ui.Rect) {
	p.SetBounds(r)
	p.list.Layout(ctx, ui.Rect{W: max(0, r.W-1), H: r.H})
	p.scrollbar.Layout(ctx, ui.Rect{X: max(0, r.W-1), W: min(1, r.W), H: r.H})
}

// The ordinary native List owns keys, scrolling, selection, pointer and semantic
// activation. Only the exact active choice token adds the editor effect.
func (p *editorSnippetChoice) HandleAction(_ ui.EventContext, action ui.Action) ui.EventResult {
	if action.ID != "snippet.choose" {
		return ui.EventResult{}
	}
	item, ok := action.Payload.(ui.ListItem)
	if !ok {
		return ui.EventResult{}
	}
	value, ok := item.Payload.(string)
	if !ok {
		return ui.EventResult{}
	}
	return ui.EventResult{Handled: true, Cmd: func() tea.Msg { return editorSnippetChoiceMsg{p, value} }}
}

func (m *App) adoptEditorSnippet(ed *editor.Editor) {
	if previous := m.editorSnippet; previous != nil && previous.editor != ed {
		previous.editor.CancelSnippet()
	}
	m.editorSnippet = nil
	if m.overlayHost != nil {
		m.overlayHost.Close(ui.OverlaySnippetChoices)
	}
	if ed != nil && ed.SnippetActive() {
		m.editorSnippet = &editorSnippetState{editor: ed, path: ed.FilePath(), generation: m.workspaceSwitchGen.Load()}
	}
}

func (m *App) closeEditorSnippet() {
	state := m.editorSnippet
	m.editorSnippet = nil
	if state != nil {
		state.editor.CancelSnippet()
	}
	if m.overlayHost != nil {
		m.overlayHost.Close(ui.OverlaySnippetChoices)
	}
}

// One exact active view is reconciled on Update. No inventory scan, I/O, Paint
// mutation, or second copy of the snippet's source/anchors belongs to the shell.
func (m *App) syncEditorSnippet() {
	s := m.editorSnippet
	if s == nil {
		return
	}
	popup := m.focusedPopup()
	owns := m.focusTarget() == FocusEditor && popup == nil
	if s.choice != nil && popup == s.choice.window {
		owns = true
	}
	if m.EditorState.Editor != s.editor || !s.editor.IsOpen() || !s.editor.SnippetActive() ||
		s.path != s.editor.FilePath() || s.generation != m.workspaceSwitchGen.Load() ||
		!owns || m.termDockOwnsKeys() || m.sessionDeckCapturesInput() || m.sessionDeckModal() {
		m.closeEditorSnippet()
	}
}

func (m *App) openSnippetChoices() bool {
	s := m.editorSnippet
	if s == nil || m.overlayHost == nil {
		return false
	}
	token, ok := s.editor.SnippetChoiceOptions()
	if !ok {
		return false
	}
	m.overlayHost.Close(ui.OverlaySnippetChoices)
	tokens := ui.NewTokenSet(m.Theme)
	p := &editorSnippetChoice{Base: ui.NewBase("snippet-choice/body"), state: s, token: token}
	p.list = ui.NewList("snippet-choice/list", tokens)
	p.list.SetActivateAction("snippet.choose")
	p.scrollbar = ui.NewScrollbarControl("snippet-choice/scrollbar", p.list.Scroll(), tokens)
	items := make([]ui.ListItem, len(token.Choices))
	for i, value := range token.Choices {
		label := strings.NewReplacer("\r", `\r`, "\n", `\n`, "\t", `\t`).Replace(value)
		if label == "" {
			label = "(empty)"
		}
		items[i] = ui.ListItem{ID: strconv.Itoa(i), Label: label, Payload: value}
	}
	p.list.SetItems(items)
	p.list.SelectIndex(0)
	w, h := min(64, max(1, m.width-4)), min(len(items)+2, max(1, m.height-4))
	frame := ui.NewWindowFrameWidget(m.Theme, "snippet-choice/frame", ui.FramePopover,
		fmt.Sprintf("Snippet choices · stop %d", token.Stop), p, nil, true, w, h)
	frame.Host().Focus(p.list.ID())
	fw := wm.NewFloatingWindow(string(ui.OverlaySnippetChoices), "", frame, w, h,
		wm.WithDecoration(true), wm.WithFocusable(true), wm.WithClosable(true),
		wm.WithClickOutside(wm.ClickOutsideCloseRoute))
	wm.CenterOnScreen(fw, m.width, m.height)
	p.window, s.choice = fw, p
	m.overlayHost.Open(ui.OverlaySnippetChoices, fw, ui.PopoverPolicy(true), func() {
		if s.choice == p {
			s.choice = nil
		}
	})
	return true
}

func (m *App) ownsSnippetChoice(p *editorSnippetChoice) bool {
	return p != nil && m.editorSnippet == p.state && p.state.choice == p && m.overlayHost != nil &&
		m.overlayHost.Window(ui.OverlaySnippetChoices) == p.window
}

func (m *App) applySnippetChoice(msg editorSnippetChoiceMsg) error {
	p := msg.choice
	if !m.ownsSnippetChoice(p) {
		return fmt.Errorf("snippet choice owner changed")
	}
	m.syncEditorSnippet()
	if m.editorSnippet != p.state {
		return fmt.Errorf("snippet choice input owner changed")
	}
	err := p.state.editor.ApplySnippetChoice(p.token, msg.value)
	m.overlayHost.Close(ui.OverlaySnippetChoices)
	return err
}

func (m *App) handleSnippetChoice(msg editorSnippetChoiceMsg) {
	if !m.ownsSnippetChoice(msg.choice) {
		return // a retired callback must not dismiss or notify over a newer picker
	}
	if err := m.applySnippetChoice(msg); err != nil {
		m.Feedback.NotifyInfo(err.Error())
	}
}
