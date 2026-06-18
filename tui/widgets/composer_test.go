package widgets

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/theme"
)

func TestNewComposer(t *testing.T) {
	th := theme.DefaultDark()
	c := NewComposer(th)
	if c == nil {
		t.Fatal("NewComposer returned nil")
	}
	if c.IsOpen() {
		t.Error("new composer should not be open")
	}
	if c.Value() != "" {
		t.Error("new composer should have empty value")
	}
}

func TestComposerOpenClose(t *testing.T) {
	th := theme.DefaultDark()
	c := NewComposer(th)

	c.Open("")
	if !c.IsOpen() {
		t.Error("composer should be open after Open()")
	}

	c.Close()
	if c.IsOpen() {
		t.Error("composer should be closed after Close()")
	}
}

func TestComposerOpenWithInitialValue(t *testing.T) {
	th := theme.DefaultDark()
	c := NewComposer(th)

	c.Open("initial text")
	if c.Value() != "initial text" {
		t.Errorf("expected 'initial text', got %q", c.Value())
	}
}

func TestComposerSubmit(t *testing.T) {
	th := theme.DefaultDark()
	c := NewComposer(th)
	c.Open("test prompt")

	// Ctrl+Enter should submit.
	_, _, action := c.Update(tea.KeyPressMsg{
		Code: tea.KeyEnter,
		Mod:  tea.ModCtrl,
	})
	if action != ComposerSubmit {
		t.Errorf("expected ComposerSubmit, got %v", action)
	}
}

func TestComposerCancel(t *testing.T) {
	th := theme.DefaultDark()
	c := NewComposer(th)
	c.Open("test")

	// Esc should cancel.
	_, _, action := c.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	if action != ComposerCancel {
		t.Errorf("expected ComposerCancel, got %v", action)
	}
}

func TestComposerNewline(t *testing.T) {
	th := theme.DefaultDark()
	c := NewComposer(th)
	c.Open("line1")

	// Plain Enter should insert newline.
	_, _, action := c.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if action != ComposerNone {
		t.Errorf("expected ComposerNone for plain enter, got %v", action)
	}
	if c.Value() != "line1\n" {
		t.Errorf("expected 'line1\\n', got %q", c.Value())
	}
}

func TestComposerIgnoreWhenClosed(t *testing.T) {
	th := theme.DefaultDark()
	c := NewComposer(th)

	// Updates on closed composer should be no-ops.
	_, _, action := c.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if action != ComposerNone {
		t.Errorf("expected ComposerNone when closed, got %v", action)
	}
}

func TestComposerSetValue(t *testing.T) {
	th := theme.DefaultDark()
	c := NewComposer(th)
	c.Open("")
	c.SetValue("new value")
	if c.Value() != "new value" {
		t.Errorf("expected 'new value', got %q", c.Value())
	}
}

func TestComposerMeasure(t *testing.T) {
	th := theme.DefaultDark()
	c := NewComposer(th)
	w, h := c.Measure(100, 50)
	if w != 100 || h != 50 {
		t.Errorf("expected (100, 50), got (%d, %d)", w, h)
	}
}
