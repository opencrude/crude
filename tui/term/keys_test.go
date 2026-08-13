package term

import (
	"bytes"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestEncodeKeyPrintable(t *testing.T) {
	got := EncodeKey(tea.KeyPressMsg{Text: "a", Code: 'a'})
	if !bytes.Equal(got, []byte("a")) {
		t.Fatalf("%q", got)
	}
}

func TestEncodeKeyEnterAndArrows(t *testing.T) {
	if !bytes.Equal(EncodeKey(tea.KeyPressMsg{Code: tea.KeyEnter}), []byte{'\r'}) {
		t.Fatal("enter")
	}
	if !bytes.Equal(EncodeKey(tea.KeyPressMsg{Code: tea.KeyUp}), []byte{0x1b, '[', 'A'}) {
		t.Fatal("up")
	}
	got := EncodeKey(tea.KeyPressMsg{Code: tea.KeyUp, Mod: tea.ModCtrl})
	if !bytes.Equal(got, []byte{0x1b, '[', '1', ';', '5', 'A'}) {
		t.Fatalf("ctrl-up %q", got)
	}
}

func TestEncodeKeyCtrlC(t *testing.T) {
	got := EncodeKey(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if !bytes.Equal(got, []byte{0x03}) {
		t.Fatalf("ctrl-c %q", got)
	}
}
