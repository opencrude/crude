package help

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

func TestPaintGoldenCellBufferContract(t *testing.T) {
	_, host := helpHost(t)
	buf := paintHost(host, 72, 18)
	text := bufferText(buf)
	if countNonEmptyCells(buf) == 0 {
		t.Fatal("help paint produced no non-empty cells")
	}
	for _, label := range []string{"Search help", "Using Crude", "Getting started", "Chat"} {
		if !strings.Contains(text, label) {
			t.Fatalf("missing label %q:\n%s", label, text)
		}
	}
	if strings.Contains(text, "❯") {
		t.Fatalf("help must not use the chat prompt glyph:\n%s", text)
	}
}

func TestHelpFilterNarrowsCatalog(t *testing.T) {
	screen, host := helpHost(t)
	host.Focus(SearchID)
	for _, r := range "yolo" {
		host.Handle(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	host.Layout()
	text := bufferText(paintHost(host, 72, 18))
	if !strings.Contains(text, "/yolo") && !strings.Contains(text, "Safety") {
		t.Fatalf("filter yolo should keep safety/yolo content:\n%s", text)
	}
	if strings.Contains(text, "Getting started") {
		t.Fatalf("filter yolo should hide unrelated getting-started article:\n%s", text)
	}
	if screen.Query() != "yolo" {
		t.Fatalf("query=%q want yolo", screen.Query())
	}
}

func TestHelpEscCloses(t *testing.T) {
	_, host := helpHost(t)
	host.Focus(ListID)
	result := host.Handle(tea.KeyPressMsg{Code: tea.KeyEsc})
	if result.Cmd == nil {
		t.Fatal("Esc should emit CancelMsg")
	}
	msg := result.Cmd()
	if _, ok := msg.(CancelMsg); !ok {
		t.Fatalf("Esc cmd=%T want CancelMsg", msg)
	}
}

func TestHelpArrowsMoveWithoutClosing(t *testing.T) {
	screen, host := helpHost(t)
	host.Focus(ListID)
	start := screen.list.list.SelectedIndex()
	host.Handle(tea.KeyPressMsg{Code: tea.KeyDown})
	host.Handle(tea.KeyPressMsg{Code: tea.KeyDown})
	if screen.list.list.SelectedIndex() <= start {
		t.Fatalf("Down should move selection, start=%d now=%d", start, screen.list.list.SelectedIndex())
	}
	result := host.Handle(tea.KeyPressMsg{Code: tea.KeyUp})
	if result.Cmd != nil {
		t.Fatalf("Up must not close help: %#v", result)
	}
}

func TestHelpCatalogUsesLiveCommandsAndShortcuts(t *testing.T) {
	items := BuildItems(60, "", []Command{{Name: "help", Label: "help", Desc: "Open help"}}, []Shortcut{
		{Category: "Help", Keys: "Ctrl+Alt+Z", Label: "Custom Help", Description: "from registry"},
	})
	plain := itemsText(items)
	if !strings.Contains(plain, "/help") {
		t.Fatalf("catalog missing slash command:\n%s", plain)
	}
	if !strings.Contains(plain, "Ctrl+Alt+Z") || !strings.Contains(plain, "Custom Help") {
		t.Fatalf("catalog missing live shortcut:\n%s", plain)
	}
}

func helpHost(t *testing.T) (*Screen, *ui.Host) {
	t.Helper()
	th := theme.DefaultDark()
	screen := New(th)
	screen.Open(
		[]Command{
			{Name: "help", Label: "help", Desc: "Open help"},
			{Name: "yolo", Label: "yolo", Desc: "Disable ALL safety checks (DANGEROUS)"},
			{Name: "settings", Label: "settings", Desc: "Open session AI settings"},
		},
		[]Shortcut{
			{Category: "Help", Keys: "F1", Label: "Help", Description: "open this window"},
			{Category: "Tools", Keys: "Ctrl+Alt+P", Label: "Command Palette", Description: "search and run commands"},
		},
	)
	component := ui.NewComponentWidget(th, screen, 72, 18)
	screen.SetFocusHandler(component.Focus)
	screen.FocusList()
	host := component.Host()
	host.Resize(72, 18)
	host.Layout()
	return screen, host
}

func paintHost(host *ui.Host, w, h int) *surface.CellBuffer {
	host.Resize(w, h)
	host.Layout()
	buf := surface.NewCellBuffer(w, h)
	host.Paint(buf)
	return buf
}

func bufferText(buf *surface.CellBuffer) string {
	var b strings.Builder
	for y := 0; y < buf.Height(); y++ {
		for x := 0; x < buf.Width(); x++ {
			r := buf.Get(x, y).Char
			if r == 0 {
				r = ' '
			}
			b.WriteRune(r)
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func countNonEmptyCells(buf *surface.CellBuffer) int {
	n := 0
	for y := 0; y < buf.Height(); y++ {
		for x := 0; x < buf.Width(); x++ {
			c := buf.Get(x, y)
			if c.Char != 0 && c.Width != 0 {
				n++
			}
		}
	}
	return n
}

func itemsText(items []ui.VirtItem) string {
	var b strings.Builder
	for _, item := range items {
		b.WriteString(item.Label)
		b.WriteByte(' ')
		b.WriteString(item.Detail)
		b.WriteByte('\n')
	}
	return b.String()
}
