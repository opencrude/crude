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
		if !strings.Contains(text, "◆ crude") || !strings.Contains(text, "SAVE FILE") {
			t.Fatalf("width %d: save window lacks the shared picker header:\n%s", width, text)
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
	host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	host.Handle(tea.KeyPressMsg{Code: tea.KeyDown})
	host.Handle(tea.KeyPressMsg{Code: tea.KeyDown})
	host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
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
	host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	host.Handle(tea.KeyPressMsg{Code: tea.KeyDown})
	host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
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

// The encoding is a compact dropdown, not a pane-wide control: it opens over
// the browser above the form, a pointer pick commits it, and a click anywhere
// else in the window only closes it.
func TestSaveFileEncodingDropdownOpensOverBrowser(t *testing.T) {
	const width, height = 100, 22
	screen, host := pickerHost(t, ModeSaveFile)
	host.Resize(width, height)
	host.Layout()
	formRect, ok := host.ScreenRect(SaveFormID)
	if !ok {
		t.Fatal("save form is not mounted")
	}
	buf := surface.NewCellBuffer(width, height)
	host.Paint(buf)
	encodingY := formRect.Y + 2
	row := pickerBufferText(buf)
	lines := strings.Split(row, "\n")
	encodingLine := lines[encodingY]
	x := strings.Index(encodingLine, "UTF-8")
	arrow := strings.Index(encodingLine, "▾")
	if x < 0 || arrow < 0 {
		t.Fatalf("encoding dropdown not painted on its row:\n%s", row)
	}
	// Byte offsets: count cells, not bytes, to measure the box.
	if cells := len([]rune(encodingLine[:arrow])); cells > formRect.X+40 {
		t.Fatalf("encoding box spans %d cells; want a compact dropdown:\n%s", cells, row)
	}
	arrowX := len([]rune(encodingLine[:arrow]))
	click := func(x, y int) {
		host.Handle(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
		host.Handle(tea.MouseReleaseMsg{X: x, Y: y, Button: tea.MouseLeft})
	}
	click(arrowX, encodingY)
	buf = surface.NewCellBuffer(width, height)
	host.Paint(buf)
	opened := strings.Split(pickerBufferText(buf), "\n")
	optionY, panelTop := -1, -1
	for y := 0; y < encodingY; y++ {
		if strings.Contains(opened[y], "UTF-16 BE") {
			optionY = y
		}
		if panelTop < 0 && strings.Contains(opened[y], "╭") {
			panelTop = y
		}
	}
	if optionY < 0 || panelTop < 0 || panelTop >= formRect.Y {
		t.Fatalf("dropdown did not open over the browser above the form:\n%s", strings.Join(opened, "\n"))
	}
	optionX := len([]rune(opened[optionY][:strings.Index(opened[optionY], "UTF-16 BE")]))
	click(optionX, optionY)
	if screen.SaveEncoding() != textencoding.UTF16BE {
		t.Fatalf("pointer pick over the browser chose %q", screen.SaveEncoding())
	}
	buf = surface.NewCellBuffer(width, height)
	host.Paint(buf)
	if strings.Contains(strings.Join(strings.Split(pickerBufferText(buf), "\n")[:formRect.Y], "\n"), "UTF-16 BE") {
		t.Fatal("closed dropdown left ink over the browser")
	}

	// Reopen, then click a tree row: the click closes the dropdown and does
	// not select the row underneath.
	click(arrowX, encodingY)
	selectedBefore, _ := screen.Selected()
	treeRect, _ := host.ScreenRect(ListID)
	click(treeRect.X+2, treeRect.Y)
	if field := screen.saveForm.Field("encoding"); field == nil || field.SelectOpen() {
		t.Fatal("outside click left the dropdown open")
	}
	if selectedAfter, _ := screen.Selected(); selectedAfter != selectedBefore {
		t.Fatalf("outside click acted on the tree: %+v", selectedAfter)
	}
	if screen.SaveEncoding() != textencoding.UTF16BE {
		t.Fatal("outside click changed the encoding")
	}
	// Footer clicks also dismiss first; they cannot save through the overlay.
	click(arrowX, encodingY)
	for _, child := range screen.actions.Children() {
		if !strings.Contains(child.Semantics().Label, "Save") {
			continue
		}
		r, _ := host.ScreenRect(child.ID())
		result := host.Handle(tea.MouseClickMsg{X: r.X + 1, Y: r.Y, Button: tea.MouseLeft})
		if result.Cmd != nil || screen.saveForm.Field("encoding").SelectOpen() {
			t.Fatal("Save activated through the open encoding dropdown")
		}
		host.Handle(tea.MouseReleaseMsg{X: r.X + 1, Y: r.Y, Button: tea.MouseLeft})
	}
}
