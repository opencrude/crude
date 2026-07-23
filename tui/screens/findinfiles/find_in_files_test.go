package findinfiles

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/fileio"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

func TestFindInFilesFilterPaintsQueryAndStatusAcrossThemes(t *testing.T) {
	for _, th := range []*theme.Theme{theme.DefaultDark(), theme.DefaultLight()} {
		for _, size := range []ui.Size{{W: 42, H: 14}, {W: 100, H: 24}} {
			screen := New(th)
			screen.Open(t.TempDir())
			component := ui.NewComponentWidget(th, screen, size.W, size.H)
			screen.SetFocusHandler(component.Focus)
			screen.FocusQuery()
			host := component.Host()
			host.Resize(size.W, size.H)
			host.Layout()
			buf := surface.NewCellBuffer(size.W, size.H)
			host.Paint(buf)
			painted := bufferText(buf)
			if !strings.Contains(painted, "Search file names and contents") {
				t.Fatalf("theme=%v size=%#v missing placeholder:\n%s", th == theme.DefaultLight(), size, painted)
			}
			if !strings.Contains(painted, "type to search") {
				t.Fatalf("theme=%v size=%#v missing hints:\n%s", th == theme.DefaultLight(), size, painted)
			}
		}
	}
}

func TestFindInFilesTypingSchedulesDebouncedSearch(t *testing.T) {
	screen, host := searchHost(t)
	screen.SetQuery("alpha")
	for _, r := range []string{"b", "e", "t", "a"} {
		host.Handle(tea.KeyPressMsg{Code: rune(r[0]), Text: r})
	}

	if got := screen.Query(); got != "alphabeta" {
		t.Fatalf("query=%q", got)
	}
	if screen.generation == 0 {
		t.Fatal("typing should bump generation")
	}
}

func TestFindInFilesNavigationClampsAndOpens(t *testing.T) {
	screen, host := searchHost(t)
	screen.SetQuery("x")
	screen.ScheduleSearch()
	results := []fileio.ProjectSearchMatch{
		{Path: "/root/a.go", RelativePath: "a.go", Line: 1, LineText: "alpha", NameOnly: true},
		{Path: "/root/b.go", RelativePath: "b.go", Line: 2, LineText: "beta", NameOnly: true},
		{Path: "/root/c.go", RelativePath: "c.go", Line: 3, LineText: "gamma", NameOnly: true},
	}
	screen.ApplySearchResults(screen.generation, results, nil, 12*time.Millisecond)
	if screen.Selected() != 0 {
		t.Fatalf("initial selection=%d", screen.Selected())
	}
	host.Handle(tea.KeyPressMsg{Code: tea.KeyDown})
	host.Handle(tea.KeyPressMsg{Code: tea.KeyDown})
	if screen.Selected() != 2 {
		t.Fatalf("after two down=%d", screen.Selected())
	}
	host.Handle(tea.KeyPressMsg{Code: tea.KeyDown})
	if screen.Selected() != 2 {
		t.Fatalf("clamp at end=%d", screen.Selected())
	}
	host.Handle(tea.KeyPressMsg{Code: tea.KeyUp})
	if screen.Selected() != 1 {
		t.Fatalf("up=%d", screen.Selected())
	}
	host.Handle(tea.KeyPressMsg{Code: tea.KeyHome})
	if screen.Selected() != 0 {
		t.Fatalf("home=%d", screen.Selected())
	}
	host.Handle(tea.KeyPressMsg{Code: tea.KeyEnd})
	if screen.Selected() != 2 {
		t.Fatalf("end=%d", screen.Selected())
	}
}

func TestFindInFilesEscAndEnterEmitOwnerScreenedMessages(t *testing.T) {
	screen, host := searchHost(t)
	screen.SetQuery("x")
	screen.ScheduleSearch()
	screen.ApplySearchResults(screen.generation, []fileio.ProjectSearchMatch{{
		Path: "/root/x.go", RelativePath: "x.go", Line: 5, LineText: "needle", NameOnly: true,
	}}, nil, 3*time.Millisecond)

	open := host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	if open.Cmd == nil {
		t.Fatal("Enter on result should emit a command")
	}
	if msg, ok := open.Cmd().(OpenMsg); !ok || msg.Match.Path != "/root/x.go" {
		t.Fatalf("open message=%#v", open)
	} else if msg.Screen != screen {
		t.Fatalf("open owner=%p, want %p", msg.Screen, screen)
	}

	close := host.Handle(tea.KeyPressMsg{Code: tea.KeyEsc})
	if close.Cmd == nil {
		t.Fatal("Esc should emit close command")
	}
	if msg, ok := close.Cmd().(CloseMsg); !ok || msg.Screen != screen {
		t.Fatalf("close message=%#v", close)
	}
}

func TestFindInFilesFirstQueryNavigationSynchronizesPreviewAndOpen(t *testing.T) {
	screen, host := searchHost(t)
	screen.SetQuery("x")
	screen.ScheduleSearch()
	screen.ApplySearchResults(screen.generation, []fileio.ProjectSearchMatch{
		{Path: "/root/a.go", RelativePath: "a.go", Line: 1, LineText: "alpha", NameOnly: true},
		{Path: "/root/b.go", RelativePath: "b.go", Line: 2, LineText: "beta", NameOnly: true},
	}, nil, time.Millisecond)

	host.Handle(tea.KeyPressMsg{Code: tea.KeyDown})
	if screen.Selected() != 1 {
		t.Fatalf("first down selected=%d, want 1", screen.Selected())
	}

	result := host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	if result.Cmd == nil {
		t.Fatal("Enter should emit an open command")
	}
	msg, ok := result.Cmd().(OpenMsg)
	if !ok || msg.Match.Path != "/root/b.go" || msg.Screen != screen {
		t.Fatalf("open message=%#v", msg)
	}
}

func TestFindInFilesRegexShortcutReschedulesSearch(t *testing.T) {
	screen, host := searchHost(t)
	screen.SetQuery("needle")
	screen.queryField.field.SetDebounce(0)
	host.Handle(tea.KeyPressMsg{Code: tea.KeyDown})

	result := host.Handle(tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl})
	if !screen.Regex() {
		t.Fatal("Ctrl+R should enable regex")
	}
	if result.Cmd == nil {
		t.Fatal("Ctrl+R should reschedule search")
	}
	debounced, ok := result.Cmd().(ui.SearchDebouncedMsg)
	if !ok {
		t.Fatalf("Ctrl+R produced %T, want SearchDebouncedMsg", result.Cmd())
	}
	requestCmd := screen.HandleSearchDebounced(debounced)
	if requestCmd == nil {
		t.Fatal("accepted debounce should emit a search request")
	}
	request, ok := requestCmd().(SearchRequestMsg)
	if !ok || !request.Options.Regex || request.Screen != screen {
		t.Fatalf("search request=%#v", request)
	}
}

func TestFindInFilesCaseToggleReschedulesSearch(t *testing.T) {
	screen, host := searchHost(t)
	screen.SetQuery("Needle")
	screen.queryField.field.SetDebounce(0)

	result := host.Handle(tea.MouseClickMsg{X: 0, Y: 0, Button: tea.MouseLeft})
	if !screen.CaseSensitive() {
		t.Fatal("case-sensitive toggle did not change state")
	}
	if result.Cmd == nil {
		t.Fatal("case-sensitive toggle should reschedule search")
	}
	debounced, ok := result.Cmd().(ui.SearchDebouncedMsg)
	if !ok {
		t.Fatalf("toggle produced %T, want SearchDebouncedMsg", result.Cmd())
	}
	requestCmd := screen.HandleSearchDebounced(debounced)
	request, ok := requestCmd().(SearchRequestMsg)
	if !ok || !request.Options.CaseSensitive || request.Screen != screen {
		t.Fatalf("search request=%#v", request)
	}

	close := host.Handle(tea.KeyPressMsg{Code: tea.KeyEsc})
	if close.Cmd == nil {
		t.Fatal("Escape on an option toggle should close Find in Files")
	}
	if msg, ok := close.Cmd().(CloseMsg); !ok || msg.Screen != screen {
		t.Fatalf("close message=%#v", close)
	}
}

func TestFindInFilesDoubleClickActivatesAndSingleClickJustSelects(t *testing.T) {
	screen, host := searchHost(t)
	screen.SetQuery("x")
	screen.ScheduleSearch()
	screen.ApplySearchResults(screen.generation, []fileio.ProjectSearchMatch{{
		Path: "/root/z.go", RelativePath: "z.go", Line: 9, LineText: "zed", NameOnly: true,
	}}, nil, 1*time.Millisecond)

	first := host.Handle(tea.MouseClickMsg{X: 1, Y: 2, Button: tea.MouseLeft})
	if first.Cmd != nil {
		t.Fatalf("first click emitted command=%#v", first)
	}
	if screen.Selected() != 0 {
		t.Fatalf("first click selected=%d", screen.Selected())
	}
	second := host.Handle(tea.MouseClickMsg{X: 1, Y: 2, Button: tea.MouseLeft})
	if second.Cmd == nil {
		t.Fatal("second click should emit a command")
	}
	if msg, ok := second.Cmd().(OpenMsg); !ok {
		t.Fatalf("double click message=%#v", msg)
	}
}

func TestFindInFilesOwnerScreeningRejectsStaleScreen(t *testing.T) {
	screen, host := searchHost(t)
	other := New(theme.DefaultDark())
	if screen.AcceptsSearch(other, 1) {
		t.Fatal("AcceptsSearch must reject a different screen")
	}
	screen.SetQuery("needle")
	if !screen.AcceptsSearch(screen, screen.generation) {
		t.Fatal("AcceptsSearch should accept matching screen+generation")
	}
	if screen.AcceptsSearch(screen, screen.generation+99) {
		t.Fatal("AcceptsSearch should reject stale generation")
	}
	screen.SetQuery("")
	if screen.AcceptsSearch(screen, screen.generation) {
		t.Fatal("AcceptsSearch should reject empty query")
	}
	_ = host
}

func TestFindInFilesStatusUpdatesFromResults(t *testing.T) {
	screen, _ := searchHost(t)
	screen.SetQuery("x")
	screen.ScheduleSearch()
	screen.ApplySearchResults(screen.generation, []fileio.ProjectSearchMatch{
		{Path: "/root/a.go", RelativePath: "a.go", Line: 1, LineText: "a", NameOnly: true},
		{Path: "/root/a.go", RelativePath: "a.go", Line: 2, LineText: "b", NameOnly: true},
		{Path: "/root/b.go", RelativePath: "b.go", Line: 3, LineText: "c", NameOnly: true},
	}, nil, 7*time.Millisecond)
	if !strings.Contains(screen.status.Text(), "3 results") {
		t.Fatalf("status=%q", screen.status.Text())
	}
	if !strings.Contains(screen.status.Text(), "2 files") {
		t.Fatalf("status=%q", screen.status.Text())
	}
}

func TestFindInFilesLayoutAllocatesListAndPreviewRectangles(t *testing.T) {
	th := theme.DefaultDark()
	screen := New(th)
	component := ui.NewComponentWidget(th, screen, 80, 24)
	screen.SetFocusHandler(component.Focus)
	host := component.Host()
	host.Resize(80, 24)
	host.Layout()
	if screen.list.Bounds().Empty() {
		t.Fatal("list should receive a non-empty rectangle")
	}
	if screen.preview.Bounds().Empty() {
		t.Fatal("preview should receive a non-empty rectangle when window is tall enough")
	}
	if screen.hints.Bounds().Empty() {
		t.Fatal("hints should receive a non-empty rectangle")
	}
}

func searchHost(t *testing.T) (*Screen, *ui.Host) {
	t.Helper()
	th := theme.DefaultDark()
	screen := New(th)
	screen.Open(t.TempDir())
	component := ui.NewComponentWidget(th, screen, 80, 24)
	host := component.Host()
	host.Resize(80, 24)
	host.Layout()
	screen.SetFocusHandler(component.Focus)
	screen.FocusQuery()
	return screen, host
}

func bufferText(buf *surface.CellBuffer) string {
	var result strings.Builder
	for y := 0; y < buf.Height(); y++ {
		for x := 0; x < buf.Width(); x++ {
			r := buf.Get(x, y).Char
			if r == 0 {
				r = ' '
			}
			result.WriteRune(r)
		}
		result.WriteByte('\n')
	}
	return filepath.ToSlash(result.String())
}
