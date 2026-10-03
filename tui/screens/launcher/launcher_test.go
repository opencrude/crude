package launcher

import (
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/tui/uitest"
	"crude/workspace"
	"path/filepath"
	"strings"
	"testing"
)

func testScreen(t *testing.T, width, height int, workspaces []workspace.WorkspaceInfo) (*Screen, *ui.Host, *surface.CellBuffer) {
	t.Helper()
	s := New(theme.DefaultDark())
	s.SetData("/projects/current", workspaces)
	h := ui.NewHost(theme.DefaultDark(), s)
	s.SetFocusHandler(h.Focus)
	h.Resize(width, height)
	h.Layout()
	s.SyncFocus()
	buf := surface.NewCellBuffer(width, height)
	h.Paint(buf)
	return s, h, buf
}

func bufferText(buf *surface.CellBuffer) string {
	rows := make([]string, buf.Height())
	for y := 0; y < buf.Height(); y++ {
		var b strings.Builder
		for x := 0; x < buf.Width(); x++ {
			ch := buf.Get(x, y).Char
			if ch == 0 {
				ch = ' '
			}
			b.WriteRune(ch)
		}
		rows[y] = b.String()
	}
	return strings.Join(rows, "\n")
}

func TestPaintGoldenCellBufferContract(t *testing.T) {
	_, h, buf := testScreen(t, 110, 16, []workspace.WorkspaceInfo{{ID: "alpha", ProjectName: "alpha", ProjectDir: "/projects/alpha", SessionCount: 2}})
	h.Paint(buf)
	text := bufferText(buf)
	for _, label := range []string{"New Session", "New Workspace", "Recent workspaces", "alpha", "Skills", "MCP"} {
		if !strings.Contains(text, label) {
			t.Fatalf("missing %q:\n%s", label, text)
		}
	}
	uitest.CheckGolden(t, "launcher_dark_110x16", buf)
}
func TestLauncherCompositionKeepsOriginalActionsAndRightPane(t *testing.T) {
	s, _, buf := testScreen(t, 120, 32, manyWorkspaces(6))
	if s.newSession.Bounds() != (ui.Rect{W: 118, H: 4}) || s.openWorkspace.Bounds() != (ui.Rect{Y: 5, W: 40, H: 4}) {
		t.Fatal("wide action layout lost full-width New Session or left-column New Workspace")
	}
	text := bufferText(buf)
	for _, absent := range []string{"WORKSPACE", "Start a fresh conversation", "Settings", "Command Palette", "Help", "SHORTCUTS", "0 sessions"} {
		if strings.Contains(text, absent) {
			t.Fatalf("redundant content %q:\n%s", absent, text)
		}
	}
	r := s.list.Bounds()
	line := strings.Split(text, "\n")[r.Y]
	if r.X <= s.skills.Bounds().X+s.skills.Bounds().W || !strings.Contains(line, "ws-00") || strings.Contains(line, "ws-01") {
		t.Fatalf("recent entries must stay in the right pane: %s", line)
	}
	for _, width := range []int{40, 60} {
		screen, _, _ := testScreen(t, width, 24, manyWorkspaces(3))
		if screen.list.Bounds().W < width-5 {
			t.Fatalf("narrow collection squeezed: %+v", screen.list.Bounds())
		}
	}
}
func TestSetDataExcludesCurrentWorkspaceFromRecent(t *testing.T) {
	s := New(theme.DefaultDark())
	current := filepath.FromSlash("/projects/current")
	s.SetData(current, []workspace.WorkspaceInfo{{ID: workspace.WorkspaceID(current), ProjectDir: current}, {ID: "other", ProjectDir: "/projects/other"}})
	if len(s.workspaces) != 1 || s.workspaces[0].Name != "other" {
		t.Fatalf("recent=%+v", s.workspaces)
	}
}
func TestWorkspaceNamesNeverShowOpaqueHashes(t *testing.T) {
	_, _, buf := testScreen(t, 120, 30, []workspace.WorkspaceInfo{{ID: "84db4726b4a4528b", ProjectDir: "/projects/readable-name"}})
	if text := bufferText(buf); !strings.Contains(text, "readable-name") || strings.Contains(text, "84db4726b4a4528b") {
		t.Fatal(text)
	}
}
