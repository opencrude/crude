package input

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/theme"
)

// newClipboardModel builds a composer with a recording clipboard writer so
// chords are asserted without touching the system clipboard.
func newClipboardModel(t *testing.T, value string) (Model, *[]string) {
	t.Helper()
	m := NewModel(theme.DefaultDark())
	m.SetInput(value)
	copied := &[]string{}
	m.input.SetClipboardCopy(func(text string) tea.Cmd {
		return func() tea.Msg {
			*copied = append(*copied, text)
			return nil
		}
	})
	return m, copied
}

func runCopyCmd(t *testing.T, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		t.Fatal("expected a clipboard command")
	}
	_ = cmd()
}

// Ctrl+C / Ctrl+Shift+C copy a live selection and keep the draft; with nothing
// selected plain Ctrl+C clears the draft (copy when there is something to copy,
// start over otherwise) while the explicit Ctrl+Shift+C copy stays a no-op.
func TestComposerCopyChordCopiesSelectionThenClearsDraft(t *testing.T) {
	keyCtrlC := tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
	keyCtrlShiftC := tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl | tea.ModShift}

	t.Run("selection copies and keeps draft", func(t *testing.T) {
		m, copied := newClipboardModel(t, "hello world")
		m.input.SetSelection(0, 5)
		updated, cmd, _ := m.Update(keyCtrlC, false, false)
		if updated.Value() != "hello world" {
			t.Fatalf("copy changed the draft: %q", updated.Value())
		}
		runCopyCmd(t, cmd)
		if len(*copied) != 1 || (*copied)[0] != "hello" {
			t.Fatalf("copied=%q want [hello]", *copied)
		}
	})

	t.Run("no selection clears the draft", func(t *testing.T) {
		m, copied := newClipboardModel(t, "hello world")
		m.input.ClearSelection()
		updated, cmd, _ := m.Update(keyCtrlC, false, false)
		if got := updated.Value(); got != "" {
			t.Fatalf("draft=%q want empty", got)
		}
		if cmd != nil {
			t.Fatal("clearing the draft must not emit a clipboard command")
		}
		if len(*copied) != 0 {
			t.Fatalf("copied=%q want none", *copied)
		}
	})

	t.Run("explicit ctrl+shift+c never clears the draft", func(t *testing.T) {
		m, copied := newClipboardModel(t, "hello world")
		m.input.ClearSelection()
		updated, cmd, _ := m.Update(keyCtrlShiftC, false, false)
		if got := updated.Value(); got != "hello world" {
			t.Fatalf("draft=%q want unchanged", got)
		}
		if cmd != nil {
			t.Fatal("explicit copy with no selection must emit no command")
		}
		if len(*copied) != 0 {
			t.Fatalf("copied=%q want none", *copied)
		}
	})
}

func TestComposerCutChordCutsSelectionThenWholeDraft(t *testing.T) {
	keyCtrlX := tea.KeyPressMsg{Code: 'x', Mod: tea.ModCtrl}

	t.Run("selection", func(t *testing.T) {
		m, copied := newClipboardModel(t, "hello world")
		m.input.SetSelection(0, 6)
		updated, cmd, _ := m.Update(keyCtrlX, false, false)
		if got := updated.Value(); got != "world" {
			t.Fatalf("draft=%q want 'world'", got)
		}
		runCopyCmd(t, cmd)
		if len(*copied) != 1 || (*copied)[0] != "hello " {
			t.Fatalf("copied=%q want ['hello ']", *copied)
		}
	})

	t.Run("no selection cuts the whole draft", func(t *testing.T) {
		m, copied := newClipboardModel(t, "draft text")
		m.input.ClearSelection()
		updated, cmd, _ := m.Update(keyCtrlX, false, false)
		if got := updated.Value(); got != "" {
			t.Fatalf("draft=%q want empty", got)
		}
		runCopyCmd(t, cmd)
		if len(*copied) != 1 || (*copied)[0] != "draft text" {
			t.Fatalf("copied=%q want ['draft text']", *copied)
		}
	})
}

// Esc's first rung is the selection: the draft survives, so the viewmodel's
// cancel ladder (draft, then cancel the turn) is reached only afterwards.
func TestComposerEscClearsSelectionOnly(t *testing.T) {
	m, _ := newClipboardModel(t, "hello world")
	m.input.SetSelection(2, 7)
	updated, cmd, evt := m.Update(tea.KeyPressMsg{Code: tea.KeyEsc}, false, false)
	if cmd != nil || evt != nil {
		t.Fatalf("esc cmd=%v evt=%v want none", cmd, evt)
	}
	if updated.HasInputSelection() {
		t.Fatal("esc kept the selection")
	}
	if got := updated.Value(); got != "hello world" {
		t.Fatalf("esc changed the draft: %q", got)
	}
}

// The draft is editable: a completed drag ends pointer capture but keeps the
// selection for typing over, Ctrl+X or Ctrl+C, without touching the clipboard.
func TestComposerDragKeepsSelectionForEditing(t *testing.T) {
	m, copied := newClipboardModel(t, "hello world")
	m.LayoutShell(40, 4)
	click := tea.MouseClickMsg{X: 0, Y: m.textAreaY(), Button: tea.MouseLeft}
	if _, _, _ = m.Update(click, false, false); !m.input.IsSelecting() {
		t.Fatal("click did not start a selection drag")
	}
	drag := tea.MouseMotionMsg{X: 6, Y: m.textAreaY(), Button: tea.MouseLeft}
	updated, _, _ := m.Update(drag, false, false)
	selected := updated.SelectedText()
	if selected == "" {
		t.Fatal("drag produced no selection")
	}
	release := tea.MouseReleaseMsg{X: 6, Y: updated.textAreaY(), Button: tea.MouseLeft}
	released, cmd, _ := updated.Update(release, false, false)
	if released.input.IsSelecting() || released.SelectedText() != selected {
		t.Fatalf("release selection %q selecting=%v, want %q kept", released.SelectedText(), released.input.IsSelecting(), selected)
	}
	if cmd != nil {
		t.Fatal("release of a composer drag returned a clipboard command")
	}
	copiedModel, copyCmd, _ := released.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}, false, false)
	runCopyCmd(t, copyCmd)
	if len(*copied) != 1 || (*copied)[0] != selected {
		t.Fatalf("copied=%q want [%s]", *copied, selected)
	}
	if copiedModel.Value() != "hello world" {
		t.Fatalf("copy changed the draft: %q", copiedModel.Value())
	}
}
