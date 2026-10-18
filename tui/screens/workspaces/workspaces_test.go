package workspaces

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"crude/tui/screens/activitylist"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/workspace"
)

func fixture(t *testing.T, w, h int) (*Screen, *ui.Host) {
	t.Helper()
	s := New(theme.DefaultDark())
	host := ui.NewHost(theme.DefaultDark(), s)
	s.SetFocusHandler(host.Focus)
	s.SetData("C:/alpha", []workspace.WorkspaceInfo{{ProjectDir: "C:/alpha", ProjectName: "alpha"}, {ProjectDir: "D:/second/beta", ProjectName: "beta"}})
	host.Resize(w, h)
	host.Layout()
	host.Focus(SearchID)
	return s, host
}
func text(buf *surface.CellBuffer) string {
	var b strings.Builder
	for y := 0; y < buf.Height(); y++ {
		for x := 0; x < buf.Width(); x++ {
			c := buf.Get(x, y)
			if c.Width > 0 {
				b.WriteRune(c.Char)
			}
		}
		b.WriteByte('\n')
	}
	return b.String()
}
func TestPaintGoldenCellBufferContract(t *testing.T) {
	for _, size := range [][2]int{{48, 20}, {24, 14}} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			s, host := fixture(t, size[0], size[1])
			b := surface.NewCellBuffer(size[0], size[1])
			host.Paint(b)
			for _, label := range []string{"New Workspace…", "Refresh", "alpha", "beta", "Current", "Search"} {
				if !strings.Contains(text(b), label) {
					t.Fatalf("missing %q:\n%s", label, text(b))
				}
			}
			s.SetTheme(theme.DefaultDark())
			host.MarkLayout()
			host.Layout()
			host.Paint(b)
			if s.ViewState().Selection != ActionNew {
				t.Fatal("theme changed selection")
			}
		})
	}
}
func TestWorkspacePointerTargetsAndNativeCommands(t *testing.T) {
	s, host := fixture(t, 40, 18)
	list, _ := host.ScreenRect(ListID)
	for _, row := range []int{2, 3} {
		res := host.Handle(tea.MouseClickMsg{X: list.X + 2, Y: list.Y + row, Button: tea.MouseLeft})
		if row == 2 && res.Action != nil {
			t.Fatal("first resource click must only select")
		}
		if row == 2 {
			host.Handle(tea.MouseReleaseMsg{Button: tea.MouseLeft})
			continue
		}
		if res.Action == nil || res.Action.ID != ActionOpen {
			t.Fatalf("row %d action=%#v", row, res.Action)
		}
		req, ok := res.Action.Payload.(Request)
		if !ok || req.Screen != s || req.Path != "C:/alpha" {
			t.Fatalf("row %d request=%#v", row, res.Action.Payload)
		}
		host.Handle(tea.MouseReleaseMsg{Button: tea.MouseLeft})
	}
	res := host.Handle(tea.MouseClickMsg{X: list.X + 2, Y: list.Y + 4, Button: tea.MouseLeft})
	if res.Action != nil {
		t.Fatal("separator opened a workspace")
	}
	for _, spec := range []struct {
		row    int
		action string
	}{{0, ActionNew}, {1, ActionRefresh}} {
		res := host.Handle(tea.MouseClickMsg{X: list.X + 1, Y: list.Y + spec.row, Button: tea.MouseLeft})
		if res.Action == nil || res.Action.ID != spec.action {
			t.Fatalf("%s action=%#v", spec.action, res.Action)
		}
		if req, ok := res.Action.Payload.(Request); !ok || req.Screen != s {
			t.Fatalf("command owner=%#v", res.Action.Payload)
		}
		host.Handle(tea.MouseReleaseMsg{Button: tea.MouseLeft})
	}
}
func TestFilterPasteEnterAndEscape(t *testing.T) {
	s, host := fixture(t, 40, 18)
	host.Handle(tea.PasteMsg{Content: "second"})
	host.Layout()
	if got := s.Query(); got != "second" {
		t.Fatalf("query=%q", got)
	}
	b := surface.NewCellBuffer(40, 18)
	host.Paint(b)
	if strings.Contains(text(b), "alpha") || !strings.Contains(text(b), "beta") {
		t.Fatalf("filter painting:\n%s", text(b))
	}
	res := host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	req, ok := res.Action.Payload.(Request)
	if !ok || req.Path != "D:/second/beta" {
		t.Fatalf("enter request=%#v", res.Action)
	}
	host.Handle(tea.KeyPressMsg{Code: tea.KeyEsc})
	host.Layout()
	if s.Query() != "" || len(s.list.inner.Items()) != 4 {
		t.Fatal("Escape did not restore catalog")
	}
}
func TestRefreshAndRelayoutPreserveBrowsing(t *testing.T) {
	s, host := fixture(t, 32, 15)
	infos := make([]workspace.WorkspaceInfo, 50)
	for i := range infos {
		infos[i] = workspace.WorkspaceInfo{ProjectDir: fmt.Sprintf("C:/project-%02d", i)}
	}
	s.SetData("C:/project-00", infos)
	host.Layout()
	host.Focus(ListID)
	for i := 0; i < 12; i++ {
		host.Handle(tea.KeyPressMsg{Code: tea.KeyDown})
	}
	before := s.ViewState()
	s.SetStatus(true, "")
	s.SetData("C:/project-00", infos)
	host.Layout()
	if got := s.ViewState(); got != before {
		t.Fatalf("refresh changed browsing: %#v -> %#v", before, got)
	}
	s.SetStatus(false, "offline")
	host.Layout()
	if got := s.ViewState(); got != before {
		t.Fatalf("error lost browse: %#v", got)
	}
	list, _ := host.ScreenRect(ListID)
	host.Handle(tea.MouseWheelMsg{X: list.X + 2, Y: list.Y + 1, Button: tea.MouseWheelDown})
	offset := s.ViewState().Offset
	for i := 0; i < 10; i++ {
		host.MarkLayout()
		host.Layout()
	}
	if s.ViewState().Offset != offset {
		t.Fatal("relayout snapped wheel offset")
	}
	host.Resize(18, 8)
	host.Layout()
	host.Resize(32, 15)
	host.Layout()
	if got := s.ViewState(); got.Selection != before.Selection {
		t.Fatal("resize lost selected identity")
	}
}
func TestNarrowBoundsStayWithinParent(t *testing.T) {
	for w := 0; w < 25; w++ {
		for h := 0; h < 10; h++ {
			s := New(theme.DefaultDark())
			s.Layout(ui.LayoutContext{}, ui.Rect{W: w, H: h})
			for _, c := range s.Children() {
				r := c.Bounds()
				if r.X < 0 || r.Y < 0 || r.W < 0 || r.H < 0 || r.X+r.W > w || r.Y+r.H > h {
					t.Fatalf("%dx%d %s bounds=%#v", w, h, c.ID(), r)
				}
			}
		}
	}
}

func TestSidebarStatesMatchAgentsAndDoNotLeakHover(t *testing.T) {
	s, host := fixture(t, 48, 20)
	agents := activitylist.New(theme.DefaultDark())
	agents.SetItems([]ui.ListItem{{ID: "new", Icon: "+", Label: "New Agent", Command: true}})
	ah := ui.NewHost(theme.DefaultDark(), agents)
	ah.Resize(48, 20)
	ah.Layout()
	list, _ := host.ScreenRect(ListID)
	alist, _ := ah.ScreenRect(activitylist.ListID)
	for _, focused := range []bool{false, true} {
		if focused {
			host.Focus(ListID)
			ah.Focus(activitylist.ListID)
		} else {
			host.Focus(SearchID)
			ah.Focus(activitylist.FilterID)
		}
		b, ab := surface.NewCellBuffer(48, 20), surface.NewCellBuffer(48, 20)
		host.Paint(b)
		ah.Paint(ab)
		for x := 0; x < list.W; x++ {
			if b.Get(list.X+x, list.Y).BG != ab.Get(alist.X+x, alist.Y).BG {
				t.Fatalf("focus %v: command row colors diverge at %d", focused, x)
			}
		}
	}
	// The resource's two content rows share one state; blank separators never fill.
	host.Handle(tea.KeyPressMsg{Code: tea.KeyDown})
	host.Handle(tea.KeyPressMsg{Code: tea.KeyDown})
	b := surface.NewCellBuffer(48, 20)
	host.Paint(b)
	if b.Get(2, list.Y+2).BG != s.tokens.TreeCursorBg() || b.Get(2, list.Y+3).BG != s.tokens.TreeCursorBg() || b.Get(2, list.Y+4).BG != s.tokens.Color(ui.ColorBg) {
		t.Fatal("resource focus band/spacer inconsistent")
	}
	// A refresh can't leave a previously hovered row highlighted after pointer exit.
	host.Handle(tea.MouseMotionMsg{X: list.X + 2, Y: list.Y + 5})
	host.Handle(tea.MouseMotionMsg{X: 47, Y: 19})
	host.Paint(b)
	if b.Get(2, list.Y+5).BG != s.tokens.Color(ui.ColorBg) {
		t.Fatal("hover stuck after leaving list")
	}
}
