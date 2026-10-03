package termdock

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/screens/boardbrowser"
	"crude/tui/screens/memorybrowser"
	"crude/tui/screens/shellterm"
	"crude/tui/screens/statusoverview"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/workspace"
)

func TestDockUsesNumberedTerminalTabsAndFullWidthBody(t *testing.T) {
	th := theme.DefaultDark()
	dock := New(th)
	first := shellterm.New(th, "one", "one")
	second := shellterm.New(th, "two", "two")
	t.Cleanup(first.Close)
	t.Cleanup(second.Close)
	dock.Mount("one", first)
	dock.Mount("two", second)

	host := ui.NewHost(th, dock)
	host.Resize(80, 12)
	host.Layout()
	buf := surface.NewCellBuffer(80, 12)
	host.Paint(buf)

	if got := second.Bounds().Y; got != dividerH+ui.StripHeight {
		t.Fatalf("terminal body starts at y=%d want %d", got, dividerH+ui.StripHeight)
	}
	if MinRenderableHeight != dividerH+ui.StripHeight+workspace.MinTerminalRows {
		t.Fatalf("MinRenderableHeight=%d want divider+strip+PTY rows", MinRenderableHeight)
	}
	if second.Bounds().W != 80 {
		t.Fatalf("terminal body width=%d want full dock width", second.Bounds().W)
	}
	painted := readCellsRange(buf, dividerH, 0, 80)
	for _, want := range []string{"one 1", "two 2", "≡", "+", "⇱", "✕", "↕", "⌄"} {
		if !strings.Contains(painted, want) {
			t.Fatalf("terminal tab/action row missing %q: %q", want, painted)
		}
	}
	if dock.header.Bounds().Y != dock.tabs.Bounds().Y || dock.tabs.Bounds().X+dock.tabs.Bounds().W >= dock.header.Bounds().X {
		t.Fatalf("tabs=%+v and actions=%+v must share one non-overlapping row", dock.tabs.Bounds(), dock.header.Bounds())
	}
	if buf.Get(79, dividerH+1).Char != '─' {
		t.Fatal("terminal tab baseline does not span the entire dock")
	}
}

func readCellsRange(buf *surface.CellBuffer, y, start, w int) string {
	var b strings.Builder
	for x := start; x < start+w; x++ {
		c := buf.Get(x, y)
		if c.Char != 0 && c.Width != 0 {
			b.WriteRune(c.Char)
		} else {
			b.WriteByte(' ')
		}
	}
	return b.String()
}

func TestDockSearchSharesHeaderWithPanelActions(t *testing.T) {
	for _, panelID := range []string{PanelWorkspace, PanelProblems, PanelNotices} {
		t.Run(panelID, func(t *testing.T) {
			th := theme.DefaultDark()
			dock := New(th)
			panel := statusoverview.NewDocked(th, statusoverview.Projection{Active: statusoverview.ViewTabs})
			dock.SetPanel(panelID, panel)
			dock.ShowPanel(panelID)
			host := ui.NewHost(th, dock)
			for _, width := range []int{80, 120} {
				host.Resize(width, 12)
				host.Layout()
				search, ok := host.ScreenRect(statusoverview.SearchID)
				if !ok || len(dock.actions) != 3 {
					t.Fatal("missing dock search or panel actions")
				}
				header := dock.header.Bounds()
				if search.Y != header.Y {
					t.Fatalf("search y=%d and actions y=%d occupy separate rows", search.Y, header.Y)
				}
				if search.X+search.W >= header.X {
					t.Fatalf("search=%+v overlaps panel controls=%+v", search, header)
				}
				buf := surface.NewCellBuffer(width, 12)
				host.Paint(buf)
				row := readCellsRange(buf, search.Y, 0, width)
				for _, want := range []string{"Search agents", "…", "↕", "⌄"} {
					if !strings.Contains(row, want) {
						t.Fatalf("dock header missing %q: %q", want, row)
					}
				}
				if panel.ListBounds().W <= search.W {
					t.Fatal("reserving header controls reduced the body width")
				}
				for index, action := range dock.actions {
					result := host.Handle(tea.MouseClickMsg{X: header.X + action.rect.X + 1, Y: header.Y, Button: tea.MouseLeft})
					if !result.Handled || result.Action == nil || result.Action.ID != dock.actions[index].id {
						t.Fatalf("header action %s did not route through Host: %+v", action.id, result)
					}
					host.Handle(tea.MouseReleaseMsg{X: header.X + action.rect.X + 1, Y: header.Y, Button: tea.MouseLeft})
				}
			}
		})
	}
}

func TestDockCompactTabsSelectAndRetainTerminals(t *testing.T) {
	th := theme.DefaultDark()
	dock := New(th)
	for index := range 12 {
		id := fmt.Sprintf("t%d", index+1)
		screen := shellterm.New(th, id, "Long terminal title 👩🏽‍💻 / workspace")
		t.Cleanup(screen.Close)
		dock.Mount(id, screen)
	}
	host := ui.NewHost(th, dock)
	host.Resize(60, 12)
	host.Layout()
	if dock.tabs.ScrollOffset() == 0 {
		t.Fatal("overflow did not scroll to the active numbered tab")
	}
	buf := surface.NewCellBuffer(60, 12)
	host.Paint(buf)
	if row := readCellsRange(buf, dividerH, 0, 60); !strings.Contains(row, "T 12") {
		t.Fatalf("active numbered terminal is not visible: %q", row)
	}
	dock.SetActive("t1")
	dock.SetAttention("t2", true)
	host.MarkLayout()
	host.Layout()
	host.Paint(buf)
	row := readCellsRange(buf, dividerH, 0, 60)
	if !strings.Contains(row, "T 1") || !strings.Contains(row, "● T 2") {
		t.Fatalf("compact labels lost numbers or attention: %q", row)
	}
	secondX := -1
	for column := range dock.tabs.Bounds().W {
		if index, _ := dock.tabs.HitTest(column); index == 1 {
			secondX = column
			break
		}
	}
	if secondX < 0 {
		t.Fatal("second numbered tab has no visible hit target")
	}
	result := host.Handle(tea.MouseClickMsg{X: secondX, Y: dividerH, Button: tea.MouseLeft})
	if !result.Handled || dock.ActiveID() != "t2" || dock.HasAttention("t2") || host.Focused() != dock.Active().ID() {
		t.Fatalf("terminal tab click failed to activate/focus/clear attention: %+v", result)
	}
	host.Handle(tea.MouseReleaseMsg{X: secondX, Y: dividerH, Button: tea.MouseLeft})
	host.Focus(TabsID)
	host.Handle(tea.KeyPressMsg{Code: tea.KeyRight})
	if dock.ActiveID() != "t3" || len(dock.IDs()) != 12 {
		t.Fatal("keyboard tab selection changed terminal ownership")
	}
	dock.SetTitle("t3", "renamed")
	dock.Unmount("t2")
	host.MarkLayout()
	host.Layout()
	host.Paint(buf)
	if row := readCellsRange(buf, dividerH, 0, 60); !strings.Contains(row, "T 2") || dock.Number("t3") != 2 {
		t.Fatalf("terminal numbering/title did not refresh after removal: %q", row)
	}
}

func TestDockDenseTerminalWheelKeepsSelectionAndCompactNumbers(t *testing.T) {
	th := theme.DefaultDark()
	dock := New(th)
	for number := 1; number <= 16; number++ {
		id := fmt.Sprint(number)
		terminal := shellterm.New(th, id, "Terminal")
		t.Cleanup(terminal.Close)
		dock.Mount(id, terminal)
	}
	host := ui.NewHost(th, dock)
	host.Resize(80, 12)
	host.Layout()
	before := dock.tabs.ScrollOffset()
	for range 16 {
		host.Handle(tea.MouseWheelMsg{X: 10, Y: dividerH, Button: tea.MouseWheelLeft})
	}
	if before == 0 || dock.tabs.ScrollOffset() != 0 || dock.ActiveID() != "16" {
		t.Fatal("scroll did not reveal first terminals independently of the active PTY")
	}
	dock.SetTitle("16", "changed")
	host.MarkLayout()
	host.Layout()
	buffer := surface.NewCellBuffer(80, 12)
	host.Paint(buffer)
	row := readCellsRange(buffer, dividerH, 0, 80)
	if !strings.Contains(row, "T 1") || !strings.Contains(row, "T 2") || strings.Contains(row, "Termi…") || !strings.Contains(row, "‹") || !strings.Contains(row, "›") {
		t.Fatalf("dense tabs lost compact numbers or manual scroll controls: %q", row)
	}
	if dock.tabs.ScrollOffset() != 0 {
		t.Fatal("title refresh snapped the manually scrolled strip back to active")
	}
	host.Resize(240, 12)
	host.Layout()
	wide := surface.NewCellBuffer(240, 12)
	host.Paint(wide)
	if row := readCellsRange(wide, dividerH, 0, 240); !strings.Contains(row, "Terminal 1") || !strings.Contains(row, "changed 16") {
		t.Fatalf("wide tabs did not restore full suffix numbering: %q", row)
	}
}

func TestDockHeaderAndTabsStayInsideNarrowSlots(t *testing.T) {
	th := theme.DefaultDark()
	dock := New(th)
	terminal := shellterm.New(th, "t1", "Terminal")
	t.Cleanup(terminal.Close)
	dock.Mount("t1", terminal)
	for width := 1; width <= 80; width++ {
		dock.Layout(ui.LayoutContext{}, ui.Rect{X: 3, Y: 2, W: width, H: MinRenderableHeight})
		for _, child := range dock.Children() {
			bounds := child.Bounds()
			if bounds.X < 0 || bounds.Y < 0 || bounds.W < 0 || bounds.H < 0 || bounds.X+bounds.W > width || bounds.Y+bounds.H > MinRenderableHeight {
				t.Fatalf("%s bounds=%+v exceed narrow dock width=%d", child.ID(), bounds, width)
			}
		}
		buf := surface.NewCellBuffer(width+6, MinRenderableHeight+4)
		buf.Fill(cell.NewCell('~', cell.NoColor, cell.NoColor, 0))
		dock.Paint(buf, 0, 0, ui.PaintContext{})
		for _, child := range dock.Children() {
			if child.ID() != terminal.ID() {
				ui.PaintTree(child, buf, 3, 2, ui.PaintContext{})
			}
		}
		for row := range buf.Height() {
			for column := range buf.Width() {
				if column >= 3 && column < width+3 && row >= 2 && row < MinRenderableHeight+2 {
					continue
				}
				if buf.Get(column, row).Char != '~' {
					t.Fatalf("dock width=%d paints outside slot at %d,%d", width, column, row)
				}
			}
		}
	}
}

func TestDockRetainedPanelsShareTabsAndHostControls(t *testing.T) {
	for _, panelID := range []string{PanelMemory, PanelBoard} {
		t.Run(panelID, func(t *testing.T) {
			th := theme.DefaultDark()
			dock := New(th)
			var panel ui.Component
			tabID, searchID := memorybrowser.TabsID, memorybrowser.SearchID
			if panelID == PanelMemory {
				panel = memorybrowser.New(th)
			} else {
				panel = boardbrowser.New(th)
				tabID, searchID = "boardbrowser/tabs", "boardbrowser/filter"
			}
			dock.SetPanel(panelID, panel)
			dock.ShowPanel(panelID)
			host := ui.NewHost(th, dock)
			host.Resize(120, 20)
			host.Layout()
			tabs, ok := host.ScreenRect(tabID)
			if !ok {
				t.Fatal("panel tabs are not mounted")
			}
			header := dock.header.Bounds()
			if tabs.Y != header.Y || tabs.X+tabs.W > header.X {
				t.Fatalf("panel tabs=%+v do not share the reserved header=%+v", tabs, header)
			}
			buffer := surface.NewCellBuffer(120, 20)
			host.Paint(buffer)
			row := readCellsRange(buffer, header.Y, 0, 120)
			if !strings.Contains(row, "…") || !strings.Contains(row, "↕") || !strings.Contains(row, "⌄") {
				t.Fatalf("panel children erased shared host controls: %q", row)
			}
			host.Focus(tabID)
			host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
			if host.Focused() != searchID {
				t.Fatalf("panel tabs entered %s rather than search %s", host.Focused(), searchID)
			}
			result := host.Handle(tea.MouseClickMsg{X: header.X + 7, Y: header.Y, Button: tea.MouseLeft})
			if result.Action == nil || result.Action.ID != "workspace.panel.hide" {
				t.Fatalf("reserved panel hide control did not route: %+v", result)
			}
		})
	}
}
