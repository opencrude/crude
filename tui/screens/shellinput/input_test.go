package shellinput

import (
	"strings"
	"testing"

	"crude/tui/input"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/tui/uitest"
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
	// Byte-identical regression: catches a shifted input, a wrong padding
	// cell, or a color change that property checks cannot see.
	uitest.CheckGolden(t, "shellinput_dark_48x2", buf)
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
		if strings.Contains(plain, "hello") {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected input prompt/content in painted buffer")
	}
}

func TestShellInputUsesShellBackground(t *testing.T) {
	th := theme.DefaultDark()
	tokens := ui.NewTokenSet(th)
	shellBG := tokens.Color(ui.ColorBg)
	popupBG := tokens.Color(ui.ColorPopupBg)
	if shellBG.Equal(popupBG) {
		t.Fatal("dark theme must distinguish shell bg from popup bg")
	}

	m := input.NewModel(th)
	m.SetWidth(40)
	m.SetInput("hello")
	s := New(th)
	s.Bind(&m)
	buf := s.PaintTestBuffer(40, s.ContentHeight(40))

	for y := 0; y < buf.Height(); y++ {
		for x := 0; x < buf.Width(); x++ {
			c := buf.Get(x, y)
			if c.Char != 'h' {
				continue
			}
			if c.BG.Equal(popupBG) {
				t.Fatal("chat prompt used popup background")
			}
			if !c.BG.Equal(shellBG) {
				t.Fatalf("chat prompt bg=%v want shell %v", c.BG, shellBG)
			}
			return
		}
	}
	t.Fatal("did not find input text to check background")
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

func TestShellInputSlashListUsesShellBackground(t *testing.T) {
	th := theme.DefaultDark()
	tokens := ui.NewTokenSet(th)
	shellBG := tokens.Color(ui.ColorBg)
	popupBG := tokens.Color(ui.ColorPopupBg)
	if shellBG.Equal(popupBG) {
		t.Fatal("dark theme must distinguish shell bg from popup bg")
	}

	m := input.NewModel(th)
	m.SetWidth(48)
	m.SetInput("/s")
	if !m.SlashActive() {
		t.Fatal("expected slash mode for /s")
	}
	s := New(th)
	s.Bind(&m)
	buf := s.PaintTestBuffer(48, m.ShellHeight(48))

	for y := 0; y < buf.Height(); y++ {
		plain := PlainRow(buf, y, 48)
		if !strings.Contains(plain, "settings") {
			continue
		}
		for x := 0; x < buf.Width(); x++ {
			c := buf.Get(x, y)
			if c.Char == 0 {
				continue
			}
			if c.BG.Equal(popupBG) {
				t.Fatal("slash command list used popup background")
			}
		}
		return
	}
	t.Fatal("did not find slash command row to check background")
}
