package problems

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

func TestProblemPreviewExpandsInPlaceAndKeepsFullCopy(t *testing.T) {
	for _, width := range []int{40, 80, 160} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			p := Problem{Path: "/w/main.go", Line: 7, Col: 2, Severity: Error, Source: "compiler",
				Message: strings.Repeat("This explanation includes the cause and the way to fix the problem. ", 32) + "FINAL_SENTENCE."}
			s, host := mount(t, width, 12, Model{Checked: true, Problems: []Problem{p}})
			before := strings.Join(paint(host, width, 12), "\n")
			if len(s.rows) != 4 || strings.Contains(before, "FINAL_SENTENCE") || !strings.Contains(before, "…") {
				t.Fatalf("long problem must show three preview lines and overflow: rows=%d\n%s", len(s.rows), before)
			}
			for _, y := range []int{listTop + 1, listTop + 2, listTop + 3} {
				if strings.TrimSpace(paint(host, width, 12)[y]) == "" {
					t.Fatalf("preview line %d is empty", y)
				}
			}
			host.Handle(tea.KeyPressMsg{Code: tea.KeyDown})
			host.Handle(tea.KeyPressMsg{Code: tea.KeyRight})
			host.Handle(tea.KeyPressMsg{Code: tea.KeyEnd})
			expanded := strings.Join(paint(host, width, 12), "\n")
			if !strings.Contains(expanded, "FINAL_SENTENCE.") || !s.scrollbar.Needed() {
				t.Fatalf("expanded message tail is not readable with the native scroll owner:\n%s", expanded)
			}
			if got := s.SelectionText(); got != Line(p) {
				t.Fatalf("a continuation row lost or duplicated copy text: %q", got)
			}
			if got := s.OpenSelected(); got == nil || got().(OpenMsg).Problem.Message != p.Message {
				t.Fatal("opening from a continuation row lost its problem")
			}
			host.Handle(tea.KeyPressMsg{Code: tea.KeyLeft})
			paint(host, width, 12)
			if len(s.rows) != 4 {
				t.Fatal("Left did not restore the three-line preview")
			}
			// The visible disclosure uses the same expansion path.
			host.Handle(tea.MouseClickMsg{X: margin, Y: listTop + 1, Button: tea.MouseLeft})
			result := host.Handle(tea.MouseReleaseMsg{X: margin, Y: listTop + 1, Button: tea.MouseLeft})
			if result.Cmd != nil || len(s.rows) <= 4 {
				t.Fatal("disclosure opened the file or failed to expand in place")
			}
			s.SetModel(Model{Checked: true, Problems: []Problem{p}})
			paint(host, width, 12)
			if len(s.rows) <= 4 {
				t.Fatal("an unchanged publication collapsed the message")
			}
			s.SetModel(Model{Checked: true})
			paint(host, width, 12)
			if len(s.rows) != 0 {
				t.Fatal("removed diagnostics kept stale disclosure state")
			}
		})
	}
}

func fixture() Model {
	return Model{
		Checked: true,
		Status:  Status{Text: "2 language servers running", Action: "language:settings", ActionLabel: "Language settings"},
		Problems: []Problem{
			{Path: "/w/b/util.go", Folder: "b", Line: 9, Col: 2, Severity: Warning, Message: "unused variable x", Source: "vet"},
			{Path: "/w/a/main.go", Folder: "a", Line: 30, Col: 4, Severity: Error, Message: "undefined: helper", Source: "compiler"},
			{Path: "/w/a/main.go", Folder: "a", Line: 4, Col: 12, Severity: Warning, Message: "shadowed  name\n  here", Source: "vet"},
			{Path: "/w/a/main.go", Folder: "a", Line: 7, Col: 1, Severity: Error, Message: "expected declaration", Source: "compiler"},
			{Path: "/w/z.go", Line: 1, Col: 1, Severity: Error, Message: "old error", Note: "Out of date"},
		},
	}
}

func mount(t *testing.T, w, h int, model Model) (*Screen, *ui.Host) {
	t.Helper()
	screen := New(theme.DefaultDark())
	host := ui.NewHost(theme.DefaultDark(), screen)
	screen.SetFocusHandler(func(id ui.ComponentID) { host.Focus(id) })
	screen.SetModel(model)
	host.Resize(w, h)
	host.Layout()
	host.Focus(ListID)
	return screen, host
}

func paint(host *ui.Host, w, h int) []string {
	buf := surface.NewCellBuffer(w, h)
	host.Layout()
	host.Paint(buf)
	lines := make([]string, h)
	for y := range h {
		var b strings.Builder
		for x := range w {
			c := buf.Get(x, y)
			switch {
			case c.Width == 0 && c.Char == 0 && x > 0:
				continue
			case c.Char == 0:
				b.WriteByte(' ')
			default:
				b.WriteRune(c.Char)
			}
		}
		lines[y] = strings.TrimRight(b.String(), " ")
	}
	return lines
}

// Files with errors come first; inside a file errors come before warnings,
// then by line. A file row carries its counts; each problem shows message,
// source and position, with positions right-aligned in one column.
func TestProblemsAreGroupedAndOrdered(t *testing.T) {
	_, host := mount(t, 140, 14, fixture())
	lines := paint(host, 140, 14)
	want := []string{"main.go", "expected declaration", "undefined: helper", "shadowed name here", "z.go", "old error", "util.go", "unused variable x"}
	for i, text := range want {
		if !strings.Contains(lines[listTop+i], text) {
			t.Fatalf("row %d = %q, want %q\n%s", i, lines[listTop+i], text, strings.Join(lines, "\n"))
		}
	}
	if !strings.Contains(lines[listTop], "a") || !strings.Contains(lines[listTop], "✕ 2") || !strings.Contains(lines[listTop], "⚠ 1") {
		t.Fatalf("file row lacks folder or counts: %q", lines[listTop])
	}
	end := -1
	for _, i := range []int{1, 2, 3, 5, 7} {
		line := lines[listTop+i]
		if end >= 0 && len([]rune(line)) != end {
			t.Fatalf("positions are not right-aligned:\n%s", strings.Join(lines, "\n"))
		}
		end = len([]rune(line))
	}
	if !strings.Contains(lines[listTop+5], "Out of date") {
		t.Fatalf("an out-of-date problem does not say so: %q", lines[listTop+5])
	}
}

// The header shows counts and the language status with its next step, and no
// zero counters.
func TestHeaderShowsCountsAndLanguageStatus(t *testing.T) {
	screen, host := mount(t, 140, 8, fixture())
	screen.SetDockHeaderInset(10)
	host.MarkLayout()
	header := paint(host, 140, 8)[0]
	for _, text := range []string{"✕ 3 errors", "⚠ 2 warnings", "2 language servers running", "Language settings"} {
		if !strings.Contains(header, text) {
			t.Fatalf("header %q lacks %q", header, text)
		}
	}
	if strings.Contains(header, "information") || strings.Contains(header, "0 ") {
		t.Fatalf("header shows a zero counter: %q", header)
	}
	if len([]rune(header)) > 140-10 {
		t.Fatalf("header runs under the dock buttons: %q", header)
	}
	link := screen.link
	result := host.Handle(tea.MouseClickMsg{X: link.X + 1, Y: 0, Button: tea.MouseLeft})
	if result.Cmd == nil {
		t.Fatal("the status action is not clickable")
	}
	if msg, ok := result.Cmd().(ActionMsg); !ok || msg.Action != "language:settings" {
		t.Fatalf("status action sent %#v", result.Cmd())
	}
}

// Refresh sits before the status action in the header and on the narrow
// status row, and each link sends its own action.
func TestRefreshLinkRestartsLanguageServers(t *testing.T) {
	model := fixture()
	model.Refresh = true
	screen, host := mount(t, 160, 8, model)
	screen.SetDockHeaderInset(10)
	host.MarkLayout()
	header := paint(host, 160, 8)[0]
	if !strings.Contains(header, "2 language servers running · Refresh · Language settings") {
		t.Fatalf("header = %q", header)
	}
	for _, tc := range []struct {
		rect ui.Rect
		want string
	}{{screen.refresh, RefreshAction}, {screen.link, "language:settings"}} {
		result := host.Handle(tea.MouseClickMsg{X: tc.rect.X + 1, Y: tc.rect.Y, Button: tea.MouseLeft})
		if result.Cmd == nil {
			t.Fatalf("%s link is not clickable", tc.want)
		}
		if msg, ok := result.Cmd().(ActionMsg); !ok || msg.Action != tc.want {
			t.Fatalf("link sent %#v, want %s", msg, tc.want)
		}
	}

	narrow, narrowHost := mount(t, 81, 12, model)
	narrow.SetDockHeaderInset(10)
	narrowHost.MarkLayout()
	lines := paint(narrowHost, 81, 12)
	if !strings.Contains(lines[listTop], "· Refresh · Language settings") || strings.Contains(lines[0], "Refresh") {
		t.Fatalf("narrow status row = %q, header = %q", lines[listTop], lines[0])
	}
	result := narrowHost.Handle(tea.MouseClickMsg{X: narrow.refresh.X + 1, Y: narrow.refresh.Y, Button: tea.MouseLeft})
	if result.Cmd == nil {
		t.Fatal("Refresh on the status row is not clickable")
	}
	if msg, ok := result.Cmd().(ActionMsg); !ok || msg.Action != RefreshAction {
		t.Fatalf("status-row Refresh sent %#v", msg)
	}
}

func TestEmptyStatesSpeakPlainly(t *testing.T) {
	for _, tc := range []struct {
		model Model
		want  string
	}{
		{Model{}, "No results yet."},
		{Model{Checked: true}, "No problems."},
	} {
		_, host := mount(t, 100, 6, tc.model)
		if header := paint(host, 100, 6)[0]; !strings.Contains(header, tc.want) {
			t.Fatalf("header %q, want %q", header, tc.want)
		}
	}
}

// A double-click opens a problem; one click selects it. A file row folds on one click.
func TestClickOpensProblemAndFoldsFile(t *testing.T) {
	screen, host := mount(t, 140, 14, fixture())
	y := listTop + 2 // undefined: helper
	host.Handle(tea.MouseClickMsg{X: 10, Y: y, Button: tea.MouseLeft})
	result := host.Handle(tea.MouseReleaseMsg{X: 10, Y: y, Button: tea.MouseLeft})
	if result.Cmd != nil {
		t.Fatal("single click opened a problem instead of selecting it")
	}
	host.Handle(tea.MouseClickMsg{X: 10, Y: y, Button: tea.MouseLeft})
	result = host.Handle(tea.MouseReleaseMsg{X: 10, Y: y, Button: tea.MouseLeft})
	if result.Cmd == nil {
		t.Fatal("double-click did not open the problem")
	}
	if msg, ok := result.Cmd().(OpenMsg); !ok || msg.Problem.Message != "undefined: helper" {
		t.Fatalf("click opened %#v", result.Cmd())
	}
	host.Handle(tea.MouseClickMsg{X: 10, Y: listTop, Button: tea.MouseLeft})
	host.Handle(tea.MouseReleaseMsg{X: 10, Y: listTop, Button: tea.MouseLeft})
	if lines := paint(host, 140, 14); !strings.Contains(lines[listTop+1], "z.go") {
		t.Fatalf("file row did not fold:\n%s", strings.Join(lines, "\n"))
	}
	// An out-of-date problem cannot be opened.
	screen.list.cursor, screen.list.anchor = 2, 2
	if cmd := screen.OpenSelected(); cmd != nil {
		t.Fatal("an out-of-date problem opened")
	}
}

// Text is selectable: a drag or Shift+arrows select rows and Ctrl+C copies
// them as plain lines; a file row stands for its problems.
func TestDragAndShiftSelectRowsForCopy(t *testing.T) {
	screen, host := mount(t, 140, 14, fixture())
	host.Handle(tea.MouseClickMsg{X: 10, Y: listTop + 1, Button: tea.MouseLeft})
	host.Handle(tea.MouseMotionMsg{X: 10, Y: listTop + 3, Button: tea.MouseLeft})
	if result := host.Handle(tea.MouseReleaseMsg{X: 10, Y: listTop + 3, Button: tea.MouseLeft}); result.Cmd != nil {
		t.Fatal("a drag opened a problem")
	}
	text := screen.SelectionText()
	want := "/w/a/main.go:7:1  error  expected declaration  (compiler)\n" +
		"/w/a/main.go:30:4  error  undefined: helper  (compiler)\n" +
		"/w/a/main.go:4:12  warning  shadowed  name\n  here  (vet)"
	if text != want {
		t.Fatalf("drag copy =\n%s\nwant\n%s", text, want)
	}
	if result := host.Handle(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}); result.Cmd == nil {
		t.Fatal("Ctrl+C did not copy")
	}
	host.Handle(tea.KeyPressMsg{Code: tea.KeyHome})
	host.Handle(tea.KeyPressMsg{Code: tea.KeyDown, Mod: tea.ModShift})
	// Messages are copied exactly, line breaks included.
	if n := strings.Count(screen.SelectionText(), "/w/a/main.go:"); n != 3 {
		t.Fatalf("file row plus Shift+Down copied %d problems, want the file's 3", n)
	}
}

// The pointer highlights the row under it, as in every list.
func TestHoverFollowsPointer(t *testing.T) {
	screen, host := mount(t, 140, 14, fixture())
	host.Handle(tea.MouseMotionMsg{X: 10, Y: listTop + 4})
	if screen.list.hover != 4 {
		t.Fatalf("hover = %d, want 4", screen.list.hover)
	}
	host.Handle(tea.MouseMotionMsg{X: 10, Y: listTop + 6})
	if screen.list.hover != 6 {
		t.Fatalf("hover = %d, want 6", screen.list.hover)
	}
}

// Typing in the filter narrows the list; Tab moves between filter and list.
func TestFilterNarrowsAndTabMovesFocus(t *testing.T) {
	_, host := mount(t, 140, 14, fixture())
	host.Handle(tea.KeyPressMsg{Code: tea.KeyTab})
	if host.Focused() != FilterID {
		t.Fatalf("Tab from the list focused %q", host.Focused())
	}
	for _, r := range "unused" {
		host.Handle(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	lines := paint(host, 140, 14)
	if !strings.Contains(lines[listTop], "util.go") || !strings.Contains(lines[listTop+1], "unused variable x") || lines[listTop+2] != "" {
		t.Fatalf("filter did not narrow:\n%s", strings.Join(lines, "\n"))
	}
	host.Handle(tea.KeyPressMsg{Code: tea.KeyDown})
	if host.Focused() != ListID {
		t.Fatalf("Down from the filter focused %q", host.Focused())
	}
}

// A refresh keeps the selected problem selected.
func TestRefreshKeepsSelection(t *testing.T) {
	screen, host := mount(t, 140, 14, fixture())
	host.Handle(tea.KeyPressMsg{Code: tea.KeyDown})
	host.Handle(tea.KeyPressMsg{Code: tea.KeyDown})
	model := fixture()
	model.Problems = append(model.Problems, Problem{Path: "/w/0.go", Line: 1, Col: 1, Severity: Error, Message: "new"})
	screen.SetModel(model)
	if p, ok := screen.Selected(); !ok || p.Message != "undefined: helper" {
		t.Fatalf("selection moved to %+v", p)
	}
}

// When the status does not fit beside the filter and counts, it takes its own
// row above the problems in full and is never cut short.
func TestNarrowStatusTakesItsOwnRow(t *testing.T) {
	model := fixture()
	model.Status = Status{Text: "Language servers do not run in restricted mode.", Attention: true, Action: "language:trust", ActionLabel: "Trust folder"}
	screen, host := mount(t, 81, 12, model)
	screen.SetDockHeaderInset(10)
	host.MarkLayout()
	lines := paint(host, 81, 12)
	if strings.Contains(lines[0], "Language servers") {
		t.Fatalf("a status that does not fit stayed in the header: %q", lines[0])
	}
	if !strings.Contains(lines[listTop], "Language servers do not run in restricted mode. · Trust folder") {
		t.Fatalf("status row = %q", lines[listTop])
	}
	if !strings.Contains(lines[listTop+1], "main.go") {
		t.Fatalf("problems do not start below the status row:\n%s", strings.Join(lines, "\n"))
	}
	link := screen.link
	result := host.Handle(tea.MouseClickMsg{X: link.X + 1, Y: link.Y, Button: tea.MouseLeft})
	if result.Cmd == nil {
		t.Fatal("the status action on its own row is not clickable")
	}
	if msg, ok := result.Cmd().(ActionMsg); !ok || msg.Action != "language:trust" {
		t.Fatalf("status action sent %#v", msg)
	}
	// A click on the first problem row still selects that problem.
	host.Handle(tea.MouseClickMsg{X: 10, Y: listTop + 2, Button: tea.MouseLeft})
	host.Handle(tea.MouseReleaseMsg{X: 10, Y: listTop + 2, Button: tea.MouseLeft})
	if p, ok := screen.Selected(); !ok || p.Message != "expected declaration" {
		t.Fatalf("click under the status row selected %+v", p)
	}
}

// Only two clicks on the same current problem may navigate. Selection,
// stale diagnostics and a drag must not trigger navigation.
func TestProblemDoubleClickRequiresSameCurrentRow(t *testing.T) {
	screen, host := mount(t, 140, 14, fixture())
	click := func(y int) ui.EventResult {
		host.Handle(tea.MouseClickMsg{X: 10, Y: y, Button: tea.MouseLeft})
		return host.Handle(tea.MouseReleaseMsg{X: 10, Y: y, Button: tea.MouseLeft})
	}
	if click(listTop+1).Cmd != nil || click(listTop+2).Cmd != nil {
		t.Fatal("different rows opened on single clicks")
	}
	screen.list.lastClickAt = time.Now().Add(-time.Second)
	if click(listTop+2).Cmd != nil {
		t.Fatal("an expired click opened a problem")
	}
	if click(listTop+2).Cmd == nil {
		t.Fatal("double click did not open the selected problem")
	}
	for range 2 {
		if click(listTop+5).Cmd != nil {
			t.Fatal("out-of-date problem opened")
		}
	}
}
