package editor

import (
	tea "charm.land/bubbletea/v2"
	"crude/indentation"
	"crude/types"
	"testing"
)

func TestIndentationChoicesPreserveSourceAndSharedGeometry(t *testing.T) {
	e := loadEditorText(t, "policy.txt", "\tword\n  text\n")
	peer := e.NewLinkedView()
	defer peer.Close()
	e.SetPresentation(types.EditorSettings{TabSize: 4, IndentStyle: indentation.Spaces, IndentSize: 4})
	e.SetCursorPosition(0, 8)
	e.SetSelection(0, 4, 0, 8)
	revision := e.Revision()
	if err := e.SetProjectIndentation(indentation.Choices{Style: indentation.Tabs, SizeIsTab: true, TabWidth: 8}); err != nil {
		t.Fatal(err)
	}
	if e.SelectedText() != "word" || e.CursorCol() != 12 || peer.TabWidth() != 8 || e.Revision() != revision || e.Modified() {
		t.Fatal("project policy edited or moved selected source")
	}
	if err := peer.SetIndentationChoices(indentation.Choices{Style: indentation.Spaces, Size: 2, TabWidth: 3}); err != nil {
		t.Fatal(err)
	}
	if e.TabWidth() != 3 || e.SelectedText() != "word" || e.Indentation().Size != 2 {
		t.Fatal("file choice did not override project on shared table")
	}
	e.SetCursorPosition(2, 0)
	e.HandleEvent(tea.KeyPressMsg{Code: tea.KeyTab})
	if e.LineText(2) != "  " {
		t.Fatal("Tab ignored explicit file size")
	}
	e.Undo()
	if e.Value() != "\tword\n  text\n" {
		t.Fatal("policy entered Undo")
	}
	if err := e.SetIndentationChoices(indentation.Choices{}); err != nil {
		t.Fatal(err)
	}
	if e.Indentation().Style != indentation.Tabs || e.TabWidth() != 8 {
		t.Fatal("Reset did not restore EditorConfig")
	}
}
func TestIndentConversionIsOneSourceTransaction(t *testing.T) {
	e := loadEditorText(t, "policy.txt", "\tword\r\n  two\r\n")
	e.SetSelection(0, 4, 0, 8)
	e.HandleEvent(ActionMsg{ID: "edit.indentToSpaces"})
	if e.Value() != "    word\r\n  two\r\n" || e.SelectedText() != "word" {
		t.Fatal("conversion changed body/selection or line endings", e.Value(), e.SelectedText())
	}
	e.Undo()
	if e.Value() != "\tword\r\n  two\r\n" || e.SelectedText() != "word" {
		t.Fatal("conversion did not undo in one step")
	}
}

func TestIndentationStepDoesNotConfuseTabWidth(t *testing.T) {
	source := "\tword\r\n"
	e := loadEditorText(t, "policy.txt", source)
	if err := e.SetIndentationChoices(indentation.Choices{Style: indentation.Tabs, Size: 2, TabWidth: 8}); err != nil {
		t.Fatal(err)
	}
	e.SetSelection(0, 8, 0, 12)
	e.HandleEvent(tea.KeyPressMsg{Code: tea.KeyTab})
	if e.Value() != "\t  word\r\n" || e.SelectedText() != "word" {
		t.Fatal("indentation inserted before a tab without moving source by one level", e.Value(), e.SelectedText())
	}
	e.Undo()
	e.SetSelection(0, 8, 0, 12)
	e.HandleEvent(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	if e.Value() != "      word\r\n" || e.SelectedText() != "word" {
		t.Fatal("outdent removed a whole tab instead of one level", e.Value(), e.SelectedText())
	}
	e.Undo()
	e.SetCursorPosition(0, 8)
	e.HandleEvent(tea.KeyPressMsg{Code: tea.KeyBackspace})
	if e.Value() != "      word\r\n" || e.CursorCol() != 6 {
		t.Fatal("Backspace removed more than one indentation stop", e.Value(), e.CursorCol())
	}
	e.Undo()
	if e.Value() != source {
		t.Fatal("indentation step did not undo")
	}
}

func TestIndentationRewriteKeepsSelectedSourceAfterBodyTabs(t *testing.T) {
	e := loadEditorText(t, "tabs.txt", "\tword\tname\n")
	if err := e.SetIndentationChoices(indentation.Choices{Style: indentation.Tabs, Size: 2, TabWidth: 8}); err != nil {
		t.Fatal(err)
	}
	e.SetSelection(0, 16, 0, 20)
	e.HandleEvent(tea.KeyPressMsg{Code: tea.KeyTab})
	if e.Value() != "\t  word\tname\n" || e.SelectedText() != "name" {
		t.Fatal("indent step interpreted the old column after an internal tab", e.Value(), e.SelectedText())
	}
	e.Undo()
	e.SetCursorPosition(0, 8)
	e.ReplaceDocument("\t\tword")
	e.SetCursorPosition(0, 8)
	e.HandleEvent(tea.KeyPressMsg{Code: tea.KeyBackspace})
	if LineVisualWidth(leadingWhitespace(e.LineText(0)), 8) != 14 || e.CursorCol() != 6 {
		t.Fatal("partial-tab Backspace collapsed later tabs", e.Value(), e.CursorCol())
	}
}
