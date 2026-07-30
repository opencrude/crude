package shellinput

import (
	"strings"
	"testing"

	"crude/tui/cell"
	"crude/tui/input"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

func TestPaintGoldenCellBufferContract(t *testing.T) {
	th := theme.DefaultDark()
	m := input.NewModel(th)
	m.SetWidth(48)
	m.SetInput("hello world")

	s := New(th)
	s.Bind(&m)
	host := ui.NewHost(th, s)
	host.Resize(48, s.ContentHeight(48))
	host.Layout()
	buf := surface.NewCellBuffer(48, s.ContentHeight(48))
	host.Paint(buf)

	if nonEmpty := countNonEmptyCells(buf); nonEmpty == 0 {
		t.Fatal("shell input paint produced no non-empty cells")
	}
	plain := bufferPlain(buf)
	for _, label := range []string{"hello world", "─"} {
		if !strings.Contains(plain, label) {
			t.Fatalf("missing shell input label %q:\n%s", label, plain)
		}
	}
	wantAccent := ui.NewTokenSet(th).Color(ui.ColorAccent)
	if !bufferHasFG(buf, wantAccent) {
		t.Fatal("shell input paint missing theme accent foreground")
	}
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

func bufferHasFG(buf *surface.CellBuffer, want cell.Color) bool {
	for y := 0; y < buf.Height(); y++ {
		for x := 0; x < buf.Width(); x++ {
			if fg := buf.Get(x, y).FG; !fg.IsDefault() && fg.Equal(want) {
				return true
			}
		}
	}
	return false
}

func bufferPlain(buf *surface.CellBuffer) string {
	var rows []string
	for y := 0; y < buf.Height(); y++ {
		rows = append(rows, PlainRow(buf, y, buf.Width()))
	}
	return strings.Join(rows, "\n")
}

func TestShellInputPaintsSeparatorAndPrompt(t *testing.T) {
	th := theme.DefaultDark()
	m := input.NewModel(th)
	m.SetWidth(40)
	m.SetInput("hello")

	s := New(th)
	s.Bind(&m)
	h := s.ContentHeight(40)
	buf := s.PaintTestBuffer(40, h)
	row0 := PlainRow(buf, 0, 40)
	if row0 == "" || !strings.HasPrefix(row0, "─") {
		t.Fatalf("expected separator row, got %q", row0)
	}
	found := false
	for y := 1; y < buf.Height(); y++ {
		plain := PlainRow(buf, y, 40)
		if strings.Contains(plain, "hello") || strings.Contains(plain, "❯") {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected input prompt/content in painted buffer")
	}
}

func TestShellInputSlashPopupIncreasesHeight(t *testing.T) {
	th := theme.DefaultDark()
	m := input.NewModel(th)
	m.SetWidth(48)
	base := m.ShellHeight(48)

	m.SetInput("/s")
	if !m.SlashActive() {
		t.Fatal("expected slash mode for /s")
	}
	h := m.ShellHeight(48)
	if h <= base {
		t.Fatalf("slash popup should increase shell height: base=%d got=%d cmds=%d",
			base, h, len(m.SlashFiltered()))
	}
	if len(m.SlashFiltered()) == 0 {
		t.Fatal("expected filtered slash commands")
	}
}
