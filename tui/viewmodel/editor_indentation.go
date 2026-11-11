package viewmodel

import (
	tea "charm.land/bubbletea/v2"
	"crude/indentation"
	"crude/tui/editor"
	"crude/tui/ui"
	"fmt"
	"strconv"
	"strings"
)

func (m *App) openIndentationMenu(ed *editor.Editor) tea.Cmd {
	if ed == nil || !ed.IsOpen() {
		return nil
	}
	token, generation := ed.ActionToken(), m.workspaceSwitchGen.Load()
	valid := func() bool {
		return generation == m.workspaceSwitchGen.Load() && ed.IsOpen() && m.EditorState.Editor == ed && ed.ActionToken() == token
	}
	set := func(style string) func() {
		return func() {
			if valid() {
				c := ed.IndentationChoices()
				c.Style = style
				_ = ed.SetIndentationChoices(c)
			}
		}
	}
	action := func(id ui.ActionID) func() {
		return func() {
			if valid() {
				m.runDetachedAsyncCmd(m.runEditorAction(ed, token, id))
			}
		}
	}
	menu := &ui.ContextMenuState{Title: ed.Indentation().Label()}
	menu.Sections = []ui.ContextMenuSection{
		{SectionLabel: ed.IndentationSource(), Items: []ui.ContextMenuItem{
			{Label: "Indent with spaces", Action: set(indentation.Spaces)},
			{Label: "Indent with tabs", Action: set(indentation.Tabs)},
			{Label: "Indent size…", Action: func() {
				if valid() {
					m.runDetachedAsyncCmd(m.openIndentationWidth(ed, false))
				}
			}},
			{Label: "Tab width…", Action: func() {
				if valid() {
					m.runDetachedAsyncCmd(m.openIndentationWidth(ed, true))
				}
			}},
			{Label: "Detect from text", Action: action("view.indentationDetect")},
			{Label: "Use project settings", Action: action("view.indentationInherit")},
			{Label: "Reload EditorConfig", Action: func() {
				if valid() {
					m.invalidateEditorConfiguration()
					m.runDetachedAsyncCmd(m.editorConfigurationCommand(ed))
				}
			}},
		}},
		{SectionLabel: "Change leading whitespace", Items: []ui.ContextMenuItem{
			{Label: "Convert to spaces", Disabled: ed.ReadOnly(), Action: action("edit.indentToSpaces")},
			{Label: "Convert to tabs", Disabled: ed.ReadOnly(), Action: action("edit.indentToTabs")},
		}},
	}
	if r := m.editorConfigRequests[ed.Primary()]; r != nil && r.err != nil {
		menu.Sections[0].SectionLabel = "EditorConfig could not be read. Reload to retry."
	}
	return m.openHeaderPopup(menu)
}
func (m *App) openIndentationWidth(ed *editor.Editor, tab bool) tea.Cmd {
	token, generation := ed.ActionToken(), m.workspaceSwitchGen.Load()
	title := "Indent size"
	current := ed.Indentation().Size
	if tab {
		title = "Tab width"
		current = ed.TabWidth()
	}
	prompt := m.openTextPrompt(ui.TextPromptGeneric, title, fmt.Sprintf("Enter 1 to %d columns.", indentation.MaxWidth), strconv.Itoa(current), func(value string) error {
		if generation != m.workspaceSwitchGen.Load() || m.EditorState.Editor != ed || !ed.IsOpen() || ed.ActionToken() != token {
			return fmt.Errorf("the editor changed; close this prompt and try again")
		}
		width, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil || width < 1 || width > indentation.MaxWidth {
			return fmt.Errorf("enter a whole number from 1 to %d", indentation.MaxWidth)
		}
		c := ed.IndentationChoices()
		if tab {
			c.TabWidth = width
		} else {
			c.Size = width
			c.SizeIsTab = false
		}
		return ed.SetIndentationChoices(c)
	})
	if prompt != nil {
		prompt.Edit.SelectAll()
	}
	return nil
}
