package viewmodel

import (
	"fmt"
	"path"
	"strings"

	tea "charm.land/bubbletea/v2"

	"crude/insight/catalog"
	"crude/tui/editor"
	"crude/tui/rendering"
	"crude/tui/screens/markdownview"
	"crude/tui/ui"
	"crude/tui/window"
	"crude/workspace"
)

// A Markdown file opens in an ordinary editor tab with viewer controls:
// Preview, Side by side or Source. The editor owns the text, save, undo and
// language features; markdownview renders its buffer off the UI goroutine
// with the chat's Markdown renderer. The mode is per tab; new tabs take the
// Editor › Markdown files setting.

// isMarkdownFile asks the language catalog, which owns file types.
func isMarkdownFile(file string) bool {
	language, ok := catalog.Current().LanguageForPath(file, nil)
	return ok && language.MarkdownPreview
}

// markdownLinks resolves a document's relative links against its folder.
func markdownLinks(file string) rendering.MarkdownLinks {
	self := strings.ReplaceAll(file, `\`, "/")
	return rendering.MarkdownLinks{Base: path.Dir(self), Self: self}
}

// markdownScreen creates the Markdown screen of an editor tab for a
// Markdown file, or returns nil.
func (p *mainPaneLayout) markdownScreen(tab *window.Tab) *markdownview.Screen {
	m := p.app
	file := m.editorPathFromTabTitle(tab.Title)
	if !isMarkdownFile(file) {
		return nil
	}
	mode := markdownview.Preview
	if m.ws != nil {
		mode = markdownview.ModeFromSetting(m.ws.LoadAppSettings().Editor)
	}
	// A proposed plan opens to be reviewed: its comments and review bar
	// belong to the source. The strip still shows its preview.
	if planFilePath(file) {
		mode = markdownview.Source
	}
	bus := m.eventBus
	screen := markdownview.New(m.Theme, ui.ComponentID(fmt.Sprintf("main-panes/markdown-%d", tab.ID)), mode, func(msg tea.Msg) {
		if bus != nil {
			bus.Send(msg)
		}
	})
	screen.Preview().SetClipboardCopy(m.clipboardCopyCommand)
	return screen
}

func (m *App) closeMarkdownViews() {
	if m.mainPanes != nil {
		for _, view := range m.mainPanes.views {
			if view.markdown != nil {
				view.markdown.Close()
			}
		}
	}
}

// configureMarkdown publishes a Markdown tab's geometry inside body and
// returns the editor's area, empty when the editor is not on screen.
func (m *App) configureMarkdown(g *mainPaneGroup, md *markdownview.Screen, ed *editor.Editor, body ui.Rect) {
	var links rendering.MarkdownLinks
	if ed != nil {
		links = markdownLinks(ed.FilePath())
	}
	md.Bind(g.view.editor, ed, links)
	md.SetTokens(m.rootHost.Tokens())
	md.Arrange(body)
	g.add(md, body)
	if source := md.SourceRect(); !source.Empty() {
		g.editor = g.screen(source)
	}
	md.Configure(m.mainViewResizeActive())
}

// applyMarkdownRendered installs a finished rendering in the tab that asked
// for it. A closed tab's rendering finds no owner and is dropped.
func (m *App) applyMarkdownRendered(msg markdownview.RenderedMsg) {
	if m.mainPanes == nil {
		return
	}
	for _, v := range m.mainPanes.views {
		if v.markdown != nil && v.markdown.Apply(msg) {
			if m.rootHost != nil {
				m.rootHost.MarkComponentPaint(v.markdown.ID())
			}
			m.forceFrame()
			return
		}
	}
}

// activeMarkdown returns the Markdown screen of the focused editor tab.
func (m *App) activeMarkdown() *markdownview.Screen {
	if m.mainPanes == nil || m.Win == nil || m.EditorState == nil || m.EditorState.Editor == nil {
		return nil
	}
	tab := m.Win.ActiveTab()
	if tab == nil || !m.isEditorTabTitle(tab.Title) {
		return nil
	}
	if v := m.mainPanes.views[tab.ID]; v != nil {
		return v.markdown
	}
	return nil
}

// markdownPreviewOwnsKeys reports a focused Markdown tab that shows only its
// rendered document. Its keys scroll, find and copy in the document; the
// hidden editor receives no edits.
func (m *App) markdownPreviewOwnsKeys() bool {
	md := m.activeMarkdown()
	return md != nil && md.PreviewOwnsKeys()
}

// setMarkdownMode switches the focused Markdown tab. It reports false when
// the focused tab is not a Markdown file.
func (m *App) setMarkdownMode(mode markdownview.Mode) bool {
	md := m.activeMarkdown()
	if md == nil {
		if m.Feedback != nil {
			m.Feedback.NotifyInfo("Open a Markdown file to change how it shows.")
		}
		return false
	}
	md.SetMode(mode)
	m.markdownModeChanged()
	return true
}

func (m *App) markdownModeChanged() {
	m.reconcileTranscriptFind()
	m.syncRootFocus()
	if m.rootHost != nil {
		m.rootHost.MarkLayout()
	}
	m.forceFrame()
}

// handleMarkdownCommand runs the Markdown view commands from the palette and
// the editor's key bindings.
func (m *App) handleMarkdownCommand(id string) bool {
	switch id {
	case "markdown.preview":
		m.setMarkdownMode(markdownview.Preview)
	case "markdown.split":
		m.setMarkdownMode(markdownview.Split)
	case "markdown.source":
		m.setMarkdownMode(markdownview.Source)
	case "markdown.cycle":
		if md := m.activeMarkdown(); md != nil {
			m.setMarkdownMode(md.Mode().Next())
		}
	default:
		return false
	}
	return true
}

// dispatchMarkdownPreviewKey handles a key while the rendered document is the
// only view of its file. Save, find, Go to Line and the view commands keep
// working; keys that would edit the hidden buffer do nothing.
func (m *App) dispatchMarkdownPreviewKey(md *markdownview.Screen, msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	preview := md.Preview()
	if ui.IsCopyChord(ui.EditorClipboardKey(msg)) {
		// A finished drag already copied its text, as in the chat.
		if preview.IsSelecting() {
			return m, m.clipboardCopyCommand(preview.SelectedText())
		}
		return m, nil
	}
	if m.actionRegistry != nil {
		if match := m.actionRegistry.MatchContext("editor", msg); match != nil {
			switch match.ID {
			case "file.save":
				return m, m.requestActiveEditorSave(workspace.DocumentSaveManual)
			case "edit.find":
				m.openPreviewFind(md)
				return m, nil
			case "edit.gotoLine":
				m.openGoToLine()
				return m, nil
			case "markdown.cycle":
				m.handleMarkdownCommand(string(match.ID))
				return m, nil
			}
			return m, nil
		}
	}
	if msg.Code == tea.KeyEsc && preview.HasActiveSelection() {
		preview.ClearSelection()
		m.forceFrame()
		return m, nil
	}
	host := m.mainHost()
	if msg.Code == tea.KeyTab && msg.Mod&^tea.ModShift == 0 {
		host.Focus(md.NextPreviewFocus(host.Focused(), msg.Mod.Contains(tea.ModShift)))
		return m, nil
	}
	if !md.ToolbarOwnsFocus(host.Focused()) {
		host.Focus(preview.ID())
	}
	result := host.Handle(msg)
	return m, tea.Batch(result.Cmd, m.handleHostAction(result.Action))
}

// openPreviewFind searches the rendered document with the same find window
// the editor and the chat use.
func (m *App) openPreviewFind(md *markdownview.Screen) {
	title := "Preview"
	if ed := m.EditorState.Editor; ed != nil {
		title = ed.BaseTitle()
	}
	m.openTranscriptFind(md.Preview(), m.mainScreenRect(md.PreviewRect()), title)
}

// mainScreenRect converts a rect in the focused group's coordinates to the
// screen.
func (m *App) mainScreenRect(r ui.Rect) ui.Rect {
	if m.mainPanes == nil || m.Win == nil {
		return r
	}
	if g := m.mainPanes.groups[m.Win.ActiveGroup()]; g != nil {
		return g.screen(r)
	}
	return r
}
