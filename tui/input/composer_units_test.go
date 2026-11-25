package input

import (
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"crude/agent/sessiondb"
	"crude/tui/cell"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

const pasteLabel = "[Pasted text #1 +20 lines]"

func composerWithChip(t *testing.T, prefix, suffix string) Model {
	t.Helper()
	m, _ := newClipboardModel(t, prefix)
	ref := sessiondb.Reference{ID: "paste", Label: pasteLabel, Kind: sessiondb.KindPastedText, Inline: true, Content: "payload"}
	m.SetReferenceStore(&pasteReferenceStore{references: []sessiondb.Reference{ref}})
	m.InsertReference(ref)
	m = typeInto(t, m, suffix)
	return m
}

// Exercise the same editor, key/paste routes and surface paint as the mounted composer.
func TestComposerChipFollowsEdits(t *testing.T) {
	key := func(code rune, mod tea.KeyMod) tea.KeyPressMsg { return tea.KeyPressMsg{Code: code, Mod: mod} }
	for _, tc := range []struct {
		name, prefix string
		at           int
		selectFirst  bool
		msgs         []tea.Msg
	}{
		{"space", "ab ", 2, false, []tea.Msg{tea.KeyPressMsg{Code: ' ', Text: " "}}},
		{"newline", "ab\n", 2, false, []tea.Msg{key(tea.KeyEnter, tea.ModShift)}},
		{"backspace", "a", 2, false, []tea.Msg{key(tea.KeyBackspace, 0)}},
		{"delete", "b", 0, false, []tea.Msg{key(tea.KeyDelete, 0)}},
		{"paste", "ab123", 2, false, []tea.Msg{tea.PasteMsg{Content: "123"}}},
		{"cut", "b", 0, true, []tea.Msg{key('x', tea.ModCtrl)}},
		{"undo", "ab", 0, true, []tea.Msg{key('x', tea.ModCtrl), key('z', tea.ModCtrl)}},
		{"redo", "b", 0, true, []tea.Msg{key('x', tea.ModCtrl), key('z', tea.ModCtrl), key('y', tea.ModCtrl)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := composerWithChip(t, "ab", "!")
			m.SetCursorFlat(tc.at)
			if tc.selectFirst {
				m.SetSelection(0, 1)
			}
			for _, msg := range tc.msgs {
				var event Event
				m, _, event = m.Update(msg, false, false)
				if event != nil {
					t.Fatalf("edit emitted %T instead of editing the chip prefix", event)
				}
			}
			if want := tc.prefix + pasteLabel + "!"; m.Value() != want {
				t.Fatalf("draft=%q, want %q", m.Value(), want)
			}
			m.LayoutShell(68, 8)
			buf := surface.NewCellBuffer(68, 8)
			m.PaintShell(buf, 0, 0, 68, 8, ui.PaintContext{})
			area := m.input.Bounds()
			rows := strings.Split(tc.prefix, "\n")
			chipX, chipY := len(rows[len(rows)-1]), len(rows)-1
			for y := 0; y < area.H; y++ {
				for x := 0; x < area.W; x++ {
					c := buf.Get(area.X+x, area.Y+y)
					chip := y == chipY && x >= chipX && x < chipX+len(pasteLabel)
					if c.Attr.Has(cell.AttrBold) != chip || c.BG.Equal(m.tokens.Color(ui.ColorSelection)) != chip || chip && c.Char != rune(pasteLabel[x-chipX]) {
						t.Fatalf("cell (%d,%d) %q: chip style=%v, want %v", x, y, c.Char, c.Attr.Has(cell.AttrBold), chip)
					}
				}
			}
		})
	}
}

func TestComposerSlashMatchesInEveryDraft(t *testing.T) {
	var first []CmdInfo
	for _, prefix := range []string{"", "review this "} {
		m := typeInto(t, NewModel(theme.DefaultDark()), prefix+"/model")
		items := m.SlashFiltered()
		if !m.SlashActive() || len(items) == 0 || items[0].Name != "settings" {
			t.Fatalf("%q: settings not offered: active=%v, items=%v", prefix, m.SlashActive(), items)
		}
		if first != nil && !slices.Equal(first, items) {
			t.Fatalf("dirty draft offers %v instead of %v", items, first)
		}
		first = items
		h := m.ShellHeight(72)
		m.LayoutShell(72, h)
		buf := surface.NewCellBuffer(72, h)
		m.PaintShell(buf, 0, 0, 72, h, ui.PaintContext{})
		var choices strings.Builder
		for _, c := range buf.Row(m.slashList.Bounds().Y) {
			choices.WriteRune(c.Char)
		}
		if !strings.Contains(choices.String(), "settings") {
			t.Fatal("settings match was not painted")
		}
		m, _, event := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter}, false, false)
		if slash, ok := event.(SlashCommandEvent); !ok || slash.Command != "/settings" || m.Value() != strings.TrimSpace(prefix) {
			t.Fatalf("prefix=%q: event=%#v, draft=%q", prefix, event, m.Value())
		}
	}
}

func TestComposerSlashWithoutChoicesDoesNotCaptureEnter(t *testing.T) {
	for _, draft := range []string{"/usr/bin", "look at /usr/bin"} {
		m := typeInto(t, NewModel(theme.DefaultDark()), draft)
		if m.SlashActive() {
			t.Errorf("%q: invisible list captured keyboard input", draft)
		}
		_, _, event := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter}, false, false)
		if submit, ok := event.(SubmitEvent); !ok || submit.Text != draft {
			t.Fatalf("%q: first Enter emitted %#v", draft, event)
		}
	}
	m := typeInto(t, NewModel(theme.DefaultDark()), "review /model")
	m.LayoutShell(72, 2) // The draft is visible, but there is no suggestion row.
	_, _, event := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter}, false, false)
	if submit, ok := event.(SubmitEvent); !ok || submit.Text != "review /model" {
		t.Fatalf("hidden description match captured Enter: %#v", event)
	}
}

// Deleting a chip edits the draft. Undo restores its original reference, not
// a fresh attachment matched by similarly named prose.
func TestComposerUndoRestoresDeletedChip(t *testing.T) {
	for _, key := range []rune{tea.KeyBackspace, tea.KeyDelete} {
		t.Run((tea.KeyPressMsg{Code: key}).String(), func(t *testing.T) {
			m := composerWithChip(t, "before ", " after")
			start := len("before ")
			at := start
			if key == tea.KeyBackspace {
				at += len(pasteLabel)
			}
			m.SetCursorFlat(at)
			m, _, _ = m.Update(tea.KeyPressMsg{Code: key}, false, false)
			if got := m.Value(); got != "before  after" || len(m.ReferencedIDs()) != 0 {
				t.Fatalf("deletion left %q, refs=%v", got, m.ReferencedIDs())
			}
			if ref, ok := m.refStore.Reference("paste"); !ok || !ref.Active() {
				t.Error("editing the draft deleted its session attachment")
			}
			m, _, _ = m.Update(tea.KeyPressMsg{Code: 'z', Mod: tea.ModCtrl}, false, false)
			if got := m.Value(); got != "before "+pasteLabel+" after" || !slices.Equal(m.ReferencedIDs(), []string{"paste"}) {
				t.Fatalf("Undo restored %q, refs=%v; want the original draft and attachment", got, m.ReferencedIDs())
			}
			m.LayoutShell(72, 4)
			buf := surface.NewCellBuffer(72, 4)
			m.PaintShell(buf, 0, 0, 72, 4, ui.PaintContext{})
			b := m.input.Bounds()
			for i, r := range pasteLabel {
				c := buf.Get(b.X+start+i, b.Y)
				if c.Char != r || !c.Attr.Has(cell.AttrBold) || !c.BG.Equal(m.tokens.Color(ui.ColorSelection)) {
					t.Fatalf("Undo did not restore chip cell %d: %+v", i, c)
				}
			}
		})
	}
}
