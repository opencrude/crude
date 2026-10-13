package input

import (
	tea "charm.land/bubbletea/v2"
	"crude/tui/theme"
	"strings"
	"testing"
)

func TestSemanticSendBarrierTracksExactCurrentPastes(t *testing.T) {
	m := NewModel(theme.DefaultDark())
	if m.HasPendingPastes() {
		t.Fatal("new input blocked")
	}
	m, _, first := m.Update(tea.PasteMsg{Content: strings.Repeat("a", 4096)}, false, false)
	m, _, second := m.Update(tea.PasteMsg{Content: strings.Repeat("b", 4096)}, false, false)
	if !m.HasPendingPastes() {
		t.Fatal("pending attachments permit Send")
	}
	one, two := first.(PasteAttachmentEvent), second.(PasteAttachmentEvent)
	stale := one
	stale.Epoch++
	if m.CompletePaste(stale) || !m.HasPendingPastes() {
		t.Fatal("stale completion released Send")
	}
	if !m.CompletePaste(two) || !m.HasPendingPastes() {
		t.Fatal("out-of-order completion released earlier paste")
	}
	if !m.CompletePaste(one) || m.HasPendingPastes() {
		t.Fatal("exact completion failed to release Send")
	}
	m, _, pending := m.Update(tea.PasteMsg{Content: strings.Repeat("c", 4096)}, false, false)
	m.SetInput("replacement")
	if m.HasPendingPastes() || m.CompletePaste(pending.(PasteAttachmentEvent)) {
		t.Fatal("replacement retained old paste ownership")
	}
}
