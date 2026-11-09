package filepicker

import (
	"crude/textencoding"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/surface"
)

func TestSaveFileUsesNativeBrowserAndFilename(t *testing.T) {
	for _, width := range []int{60, 100} {
		screen, host := pickerHost(t, ModeSaveFile)
		host.Resize(width, 18)
		host.Layout()
		buf := surface.NewCellBuffer(width, 18)
		host.Paint(buf)
		text := pickerBufferText(buf)
		for _, want := range []string{"File name", "Encoding", "UTF-8", "Save", "Cancel", "a.txt"} {
			if !strings.Contains(text, want) {
				t.Fatalf("width %d: missing %q:\n%s", width, want, text)
			}
		}
		if strings.Contains(text, "SAVE FILE") {
			t.Fatal("save body duplicated the containing window title")
		}
		if !host.Focus(SaveFormID) {
			t.Fatal("file name field is not focusable")
		}
		host.Handle(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
		if host.Focused() != ListID {
			t.Fatal("Shift+Tab did not reach the file tree")
		}
		host.Handle(tea.KeyPressMsg{Code: tea.KeyTab})
		if host.Focused() != SaveFormID {
			t.Fatal("Tab did not return from the tree to the file name")
		}
		host.Handle(tea.PasteMsg{Content: "notes.txt"})
		result := host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
		msg := commandMessage[SelectMsg](t, result)
		if !msg.ValidFor(screen) || !msg.Save || msg.Path != screen.Current() || msg.Name != "notes.txt" {
			t.Fatalf("wrong save destination: %+v", msg)
		}
		screen.SetSavePending(true, "Saving…")
		if result := screen.ConfirmSelection(); result.Cmd != nil {
			t.Fatal("pending save allowed another submission")
		}
		screen.SetSavePending(false, "Choose another file name.")
		if screen.SaveName() != "notes.txt" || !screen.IsOpen() {
			t.Fatal("failed save lost the name or closed the window")
		}
	}
}

func TestSaveFileFormNativeEncodingAndFilenameEditing(t *testing.T) {
	screen, host := pickerHost(t, ModeSaveFile)
	host.Resize(60, 18)
	host.Layout()
	screen.FocusSaveName()
	host.Handle(tea.PasteMsg{Content: "wrong.txt"})
	host.Handle(tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl})
	host.Handle(tea.PasteMsg{Content: "café.txt"})
	if screen.SaveName() != "café.txt" {
		t.Fatal("filename did not use native select-all/paste editing")
	}
	host.Handle(tea.KeyPressMsg{Code: tea.KeyDown})
	host.Handle(tea.KeyPressMsg{Code: tea.KeyRight})
	host.Handle(tea.KeyPressMsg{Code: tea.KeyRight})
	if screen.SaveEncoding() != textencoding.UTF16LE {
		t.Fatalf("native encoding choice = %q", screen.SaveEncoding())
	}
	buf := surface.NewCellBuffer(60, 18)
	host.Paint(buf)
	if !strings.Contains(pickerBufferText(buf), "UTF-16 LE") {
		t.Fatal("chosen encoding was not painted")
	}
	result := screen.ConfirmSelection()
	if result.Cmd == nil {
		t.Fatal("encoded save not submitted")
	}
	msg := result.Cmd().(SelectMsg)
	if msg.Name != "café.txt" || msg.Encoding != textencoding.UTF16LE {
		t.Fatalf("save lost form values: %+v", msg)
	}
	screen.SetSavePending(true, "Saving…")
	host.Handle(tea.KeyPressMsg{Code: tea.KeyRight})
	if screen.SaveEncoding() != textencoding.UTF16LE {
		t.Fatal("pending save allowed a different encoding")
	}
	screen.SetSavePending(false, "")
	if screen.SaveName() != "café.txt" || screen.SaveEncoding() != textencoding.UTF16LE {
		t.Fatal("return from confirmation reset form values")
	}
}

func TestSaveFileUncertainOutcomeKeepsCancelAvailable(t *testing.T) {
	screen, host := pickerHost(t, ModeSaveFile)
	screen.SetSavePending(true, "Saving…")
	screen.SetSaveUncertain("Check the destination.")
	if result := screen.ConfirmSelection(); result.Cmd != nil {
		t.Fatal("an uncertain save allowed another submission")
	}
	if !host.Focus(ActionsID) {
		t.Fatal("save actions are not focusable")
	}
	screen.actions.FocusLast()
	msg := commandMessage[CancelMsg](t, host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter}))
	if !msg.ValidFor(screen) {
		t.Fatal("Cancel did not remain available after an uncertain save")
	}
}
