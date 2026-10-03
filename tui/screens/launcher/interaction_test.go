package launcher

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/tui/uitest"
	captypes "crude/types/capability"
	"crude/workspace"
)

func TestRecentLoadingPaintsAnimationThenSettles(t *testing.T) {
	s, h, buf := testScreen(t, 120, 32, nil)
	s.BeginLoad("/projects/current")
	paint := func(frame int) string {
		s.SetAnimation(frame, 0)
		h.MarkLayout()
		buf.Clear()
		h.Paint(buf)
		return bufferText(buf)
	}
	first := paint(0)
	if !strings.Contains(first, "Loading recent workspaces") || strings.Contains(first, "No recent workspaces") {
		t.Fatalf("load was presented as empty:\n%s", first)
	}
	firstHash := uitest.GoldenHash(buf)
	second := paint(1600)
	if !strings.Contains(second, "Loading recent workspaces") || firstHash == uitest.GoldenHash(buf) {
		t.Fatal("pending load did not animate on the shared clock")
	}
	if s.TotalItems() != 2 {
		t.Fatal("loading placeholders became selectable workspace rows")
	}
	res := h.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	if res.Cmd == nil || res.Cmd().(ActivateMsg).Kind != ItemNewSession {
		t.Fatal("loading blocked the primary action")
	}
	s.FinishLoad("/projects/current", manyWorkspaces(2), nil)
	settled := paint(800)
	if strings.Contains(settled, "Loading") || !strings.Contains(settled, "ws-00") {
		t.Fatalf("load did not settle into real rows:\n%s", settled)
	}
	r := s.header.Bounds()
	before := make([]cell.Cell, 0, r.W*(buf.Height()-r.Y))
	for y := r.Y; y < buf.Height(); y++ {
		for x := r.X; x < r.X+r.W; x++ {
			before = append(before, buf.Get(x, y))
		}
	}
	paint(12000) // New Session still cycles; completed Recent content must not.
	i := 0
	for y := r.Y; y < buf.Height(); y++ {
		for x := r.X; x < r.X+r.W; x++ {
			if before[i] != buf.Get(x, y) {
				t.Fatal("completed listing kept animating")
			}
			i++
		}
	}
}

func TestRecentRefreshPreservesQuerySelectionViewportAndFocus(t *testing.T) {
	s, h, buf := testScreen(t, 120, 32, manyWorkspaces(40))
	h.Handle(tea.KeyPressMsg{Code: '/', Text: "/"})
	h.Handle(tea.PasteMsg{Content: "ws-"})
	for range 12 {
		h.Handle(tea.KeyPressMsg{Code: tea.KeyDown})
	}
	path, offset, focus := s.SelectedWorkspacePath(), s.ListScrollOffset(), h.Focused()
	s.BeginLoad("/projects/current")
	h.MarkLayout()
	h.Paint(buf)
	if !strings.Contains(bufferText(buf), "ws-12") {
		t.Fatal("refresh blanked a usable snapshot")
	}
	// Move two earlier rows without changing the selected resource's identity.
	data := manyWorkspaces(40)
	data[0], data[1] = data[1], data[0]
	s.FinishLoad("/projects/current", data, nil)
	h.MarkLayout()
	h.Layout()
	if s.Query() != "ws-" || s.SelectedWorkspacePath() != path || s.ListScrollOffset() != offset || h.Focused() != focus {
		t.Fatalf("refresh disturbed input: query=%q path=%q offset=%d focus=%q", s.Query(), s.SelectedWorkspacePath(), s.ListScrollOffset(), h.Focused())
	}
	s.FinishLoad("/projects/current", nil, errors.New("storage unavailable"))
	if s.Loading() || s.TotalItems() != 42 || s.Query() != "ws-" {
		t.Fatal("failed refresh discarded retained state")
	}
	s.BeginLoad("/projects/replacement")
	if s.Query() != "" || s.TotalItems() != 2 || s.ListScrollOffset() != 0 {
		t.Fatal("project switch inherited previous project state")
	}
}

func TestRecentErrorRetryAndEmptyAreDistinct(t *testing.T) {
	s, h, buf := testScreen(t, 100, 24, nil)
	s.BeginLoad("/projects/current")
	s.FinishLoad("/projects/current", nil, errors.New("read workspace directory: permission denied"))
	h.MarkLayout()
	h.Paint(buf)
	text := bufferText(buf)
	if !strings.Contains(text, "Couldn’t load") || !strings.Contains(text, "Retry") || strings.Contains(text, "Loading") {
		t.Fatalf("error missing recovery:\n%s", text)
	}
	r, ok := h.ScreenRect(RetryID)
	if !ok || r.Empty() {
		t.Fatal("retry has no hit target")
	}
	res := h.Handle(tea.MouseClickMsg{X: r.X + 1, Y: r.Y, Button: tea.MouseLeft})
	if res.Cmd == nil {
		t.Fatal("retry click produced no command")
	}
	msg, ok := res.Cmd().(CommandMsg)
	if !ok || msg.Screen != s || msg.Action != "launcher.retry" {
		t.Fatalf("retry=%+v", msg)
	}
	s.BeginLoad("/projects/current")
	s.FinishLoad("/projects/current", nil, nil)
	h.MarkLayout()
	buf.Clear()
	h.Paint(buf)
	text = bufferText(buf)
	if !strings.Contains(text, "No recent workspaces") || strings.Contains(text, "Retry") || strings.Contains(text, "Loading") {
		t.Fatalf("empty state=%s", text)
	}
}

func TestHoverDoesNotRedirectKeyboardActivation(t *testing.T) {
	s, h, _ := testScreen(t, 120, 32, manyWorkspaces(12))
	h.Handle(tea.KeyPressMsg{Code: tea.KeyDown})
	h.Handle(tea.KeyPressMsg{Code: tea.KeyDown})
	selected, focus, offset := s.SelectedWorkspacePath(), h.Focused(), s.ListScrollOffset()
	for _, r := range []ui.Rect{s.newSession.Bounds(), s.openWorkspace.Bounds(), s.list.Bounds()} {
		h.Handle(tea.MouseMotionMsg{X: r.X + 1, Y: r.Y + 1})
	}
	if selected != s.SelectedWorkspacePath() || focus != h.Focused() || offset != s.ListScrollOffset() {
		t.Fatal("hover stole keyboard selection/focus/scroll")
	}
	res := h.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	if res.Cmd == nil || res.Cmd().(ActivateMsg).WorkspacePath != selected {
		t.Fatal("Enter followed hover, not keyboard selection")
	}
}

func TestWorkspaceNameAndPathRowsShareExactHitIdentity(t *testing.T) {
	s, h, buf := testScreen(t, 120, 32, manyWorkspaces(60))
	s.SetListScroll(16) // the path row of ws-05; scroll is rows, never item indices
	h.MarkLayout()
	buf.Clear()
	h.Paint(buf)
	r := s.list.Bounds()
	if got := s.WorkspaceItemAt(0, 0); got == nil || got.Name != "ws-05" {
		t.Fatalf("partial first row maps to %#v", got)
	}
	for y := 0; y < r.H; y++ {
		entry := s.WorkspaceItemAt(0, y)
		if entry == nil {
			continue
		}
		res := h.Handle(tea.MouseClickMsg{X: r.X + 2, Y: r.Y + y, Button: tea.MouseLeft})
		if res.Cmd == nil || res.Cmd().(ActivateMsg).WorkspacePath != entry.Path {
			t.Fatalf("paint/hit mismatch at row %d", y)
		}
		h.Handle(tea.MouseReleaseMsg{X: r.X + 2, Y: r.Y + y, Button: tea.MouseLeft})
	}
	search := s.search.Bounds()
	before := bufferText(buf)
	h.Handle(tea.MouseWheelMsg{X: r.X + 2, Y: r.Y + 1, Button: tea.MouseWheelDown})
	h.MarkLayout()
	buf.Clear()
	h.Paint(buf)
	if before == bufferText(buf) || search != s.search.Bounds() {
		t.Fatal("scroll did not move painted rows independently of search")
	}
}

func TestKeyboardReachesUtilityAndCapabilityActions(t *testing.T) {
	s, h, _ := testScreen(t, 120, 32, manyWorkspaces(2))
	s.SetCapabilities(captypes.Snapshot{})
	h.MarkLayout()
	h.Layout()
	s.SyncFocus()
	var visited []ui.ComponentID
	for range 7 {
		visited = append(visited, h.Focused())
		h.Handle(tea.KeyPressMsg{Code: tea.KeyTab})
	}
	want := []ui.ComponentID{NewSessionID, NewWorkspaceID, SkillsID, MCPID, SearchID, WorkspaceListID, NewSessionID}
	if !reflect.DeepEqual(visited, want) {
		t.Fatalf("focus order=%v want %v", visited, want)
	}
	h.Handle(tea.KeyPressMsg{Code: tea.KeyTab}) // links
	res := h.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	if res.Cmd == nil || res.Cmd().(CommandMsg).Action != "skills.manage" {
		t.Fatal("Skills are not keyboard reachable")
	}
	h.Handle(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	if h.Focused() != NewWorkspaceID {
		t.Fatal("reverse traversal did not return to Open Workspace")
	}
}

func TestZeroResultsCannotOpenHiddenWorkspaceButActionsStillWork(t *testing.T) {
	s, h, _ := testScreen(t, 100, 24, manyWorkspaces(2))
	h.Handle(tea.KeyPressMsg{Code: '/', Text: "/"})
	h.Handle(tea.PasteMsg{Content: "does-not-exist"})
	if res := h.Handle(tea.KeyPressMsg{Code: tea.KeyEnter}); res.Cmd != nil {
		t.Fatal("zero-result Enter activated a hidden row")
	}
	r := s.openWorkspace.Bounds()
	res := h.Handle(tea.MouseClickMsg{X: r.X + 1, Y: r.Y, Button: tea.MouseLeft})
	if res.Cmd == nil || res.Cmd().(ActivateMsg).Kind != ItemOpenFolder {
		t.Fatal("filter disabled Open Workspace")
	}
}

func TestLauncherClipsEveryThemeAndSize(t *testing.T) {
	for _, th := range []*theme.Theme{theme.DefaultDark(), theme.DefaultLight(), theme.DefaultHighContrast()} {
		for _, size := range []ui.Size{{W: 1, H: 1}, {W: 20, H: 3}, {W: 40, H: 12}, {W: 50, H: 20}, {W: 80, H: 24}, {W: 120, H: 40}, {W: 200, H: 50}} {
			t.Run(fmt.Sprintf("%s-%dx%d", theme.PresetName(th), size.W, size.H), func(t *testing.T) {
				s := New(th)
				s.SetData("/projects/日本語-👨‍👩‍👧", []workspace.WorkspaceInfo{{ProjectName: "日本語-👨‍👩‍👧-long-worktree", ProjectDir: "/projects/日本語-👨‍👩‍👧-long-worktree", SessionCount: 3}})
				s.SetCapabilities(captypes.Snapshot{})
				ctx := ui.LayoutContext{Theme: th}
				s.Layout(ctx, ui.Rect{X: 3, Y: 2, W: size.W, H: size.H})
				buf := surface.NewCellBuffer(size.W+6, size.H+4)
				mark := cell.Cell{Char: '!', Width: 1}
				buf.Fill(mark)
				ui.PaintTree(s, buf, 0, 0, ui.PaintContext{Theme: th})
				for y := 0; y < buf.Height(); y++ {
					for x := 0; x < buf.Width(); x++ {
						if !(ui.Rect{X: 3, Y: 2, W: size.W, H: size.H}).Contains(x, y) && buf.Get(x, y) != mark {
							t.Fatalf("paint escaped bounds at %d,%d", x, y)
						}
					}
				}
			})
		}
	}
}

func BenchmarkLauncherDenseFrame(b *testing.B) {
	s := New(theme.DefaultDark())
	s.SetData("/projects/current", manyWorkspaces(1000))
	h := ui.NewHost(theme.DefaultDark(), s)
	h.Resize(160, 40)
	buf := surface.NewCellBuffer(160, 40)
	h.Paint(buf)
	revision := s.list.list.StructureRevision()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s.SetAnimation(i*400, 0)
		h.MarkLayout()
		h.Paint(buf)
	}
	b.StopTimer()
	if revision != s.list.list.StructureRevision() {
		b.Fatal("idle frames rebuilt the workspace index")
	}
}
