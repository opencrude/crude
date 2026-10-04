package launcher

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/ui"
	captypes "crude/types/capability"
)

func TestLauncherSharedEdgesMatchPaintedColumns(t *testing.T) {
	for _, width := range []int{40, 60, 80, 90, 120, 160, 220} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			s, h, buf := testScreen(t, width, 40, manyWorkspaces(30))
			s.SetCapabilities(captypes.Snapshot{})
			for _, failed := range []bool{false, true} {
				if failed {
					s.FinishLoad("/projects/current", nil, errors.New("storage unavailable"))
				}
				h.MarkLayout()
				buf.Clear()
				h.Paint(buf)
				newSession, openWorkspace := s.newSession.Bounds(), s.openWorkspace.Bounds()
				skills, mcp, search := s.skills.Bounds(), s.mcp.Bounds(), s.search.Bounds()
				if width < launcherCrampedWidth {
					if !skills.Empty() || !mcp.Empty() || newSession.X != openWorkspace.X || newSession.W != openWorkspace.W || search.X != newSession.X || search.W != newSession.W {
						t.Fatalf("cramped layout did not hide the left pane and fill one column: new=%+v workspace=%+v skills=%+v mcp=%+v search=%+v", newSession, openWorkspace, skills, mcp, search)
					}
				} else {
					if newSession.X != openWorkspace.X || openWorkspace.X != skills.X || skills.X != mcp.X || openWorkspace.W != skills.W || skills.W != mcp.W || search.X <= skills.X+skills.W || newSession.X+newSession.W != search.X+search.W {
						t.Fatalf("wide columns drifted: new=%+v workspace=%+v skills=%+v mcp=%+v search=%+v", newSession, openWorkspace, skills, mcp, search)
					}
					if buf.Get(skills.X, skills.Y).Char != 'S' || buf.Get(mcp.X, mcp.Y).Char != 'M' {
						t.Fatal("wide capability headings did not paint")
					}
				}
				if buf.Get(newSession.X, newSession.Y+1).Char != '│' || buf.Get(openWorkspace.X, openWorkspace.Y+1).Char != '│' {
					t.Fatal("action borders do not use their published geometry")
				}
				searchEnd := search.X + search.W - 1
				if search.W > 0 && buf.Get(searchEnd, search.Y).BG != s.tokens.Color(ui.ColorSubPaneBg) {
					t.Fatal("search surface does not use the published end column")
				}
				if failed && s.retry.Bounds().Y == search.Y {
					t.Fatal("Retry must not steal the search row's trailing columns")
				}
			}
		})
	}
}

func TestRecentSearchHasDedicatedRowAtEveryWidth(t *testing.T) {
	for _, width := range []int{80, 120, 160, 220} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			s, h, buf := testScreen(t, width, 40, manyWorkspaces(5))
			header, search, list := s.header.Bounds(), s.search.Bounds(), s.list.Bounds()
			if search.Y <= header.Y || search.H != 1 || search.X != header.X || search.W != header.W {
				t.Fatalf("search must own a full row below the heading: header=%+v search=%+v", header, search)
			}
			if search.Y+search.H >= list.Y || search.X <= s.openWorkspace.Bounds().W {
				t.Fatalf("search/results must stay separated in the right pane: search=%+v list=%+v", search, list)
			}
			row := strings.Split(bufferText(buf), "\n")[search.Y]
			if !strings.Contains(row, "Search workspaces") || strings.Contains(row, "Recent workspaces") {
				t.Fatalf("search shared heading paint: %s", row)
			}
			s.BeginLoad("/projects/current")
			h.MarkLayout()
			h.Paint(buf)
			if s.search.Bounds() != search {
				t.Fatal("refresh moved the search row")
			}
		})
	}
}

func TestRecentEntriesHaveQuietSeparators(t *testing.T) {
	s, h, buf := testScreen(t, 160, 40, manyWorkspaces(4))
	r := s.list.Bounds()
	lines := strings.Split(bufferText(buf), "\n")
	for _, row := range []struct {
		y    int
		text string
	}{{0, "ws-00"}, {1, "/projects/ws-00"}, {3, "ws-01"}, {4, "/projects/ws-01"}} {
		if !strings.Contains(lines[r.Y+row.y][r.X:], row.text) {
			t.Fatalf("row %d missing %q:\n%s", row.y, row.text, bufferText(buf))
		}
	}
	if strings.TrimSpace(lines[r.Y+2][r.X:]) != "" {
		t.Fatal("workspace entries lack a blank separating row")
	}
	if got := s.WorkspaceItemAt(1, 2); got != nil {
		t.Fatalf("separator targets a workspace: %+v", got)
	}
	if res := h.Handle(tea.MouseClickMsg{X: r.X + 1, Y: r.Y + 2, Button: tea.MouseLeft}); res.Cmd != nil {
		t.Fatal("clicking the separator opened a workspace")
	}
	for _, y := range []int{0, 1} {
		if got := s.WorkspaceItemAt(1, y); got == nil || got.Name != "ws-00" {
			t.Fatalf("name/path target changed: row=%d entry=%+v", y, got)
		}
	}
}

func TestLauncherTallCapabilitySpacing(t *testing.T) {
	s, h, _ := testScreen(t, 160, 40, nil)
	s.SetCapabilities(captypes.Snapshot{})
	h.MarkLayout()
	h.Layout()
	action, skills, mcp := s.openWorkspace.Bounds(), s.skills.Bounds(), s.mcp.Bounds()
	if skills.Y != action.Y+action.H+1 || mcp.Y != skills.Y+skills.H || skills.H != mcp.H {
		t.Fatalf("capabilities do not split the left pane evenly: skills=%+v mcp=%+v", skills, mcp)
	}
	if skills.X != 0 || mcp.X != 0 || mcp.Y+mcp.H != 40 {
		t.Fatal("capabilities do not consume the complete left-column height")
	}
	h.Resize(110, 16)
	h.Layout()
	if s.skills.Bounds().H != s.mcp.Bounds().H || s.mcp.Bounds().Y+s.mcp.Bounds().H != 16 {
		t.Fatal("short wide layout lost the equal capability split")
	}
}

func TestLauncherWideRestoresFullWidthSessionAndLeftWorkspace(t *testing.T) {
	s, h, buf := testScreen(t, 120, 32, nil)
	newSession, openWorkspace := s.newSession.Bounds(), s.openWorkspace.Bounds()
	if newSession != (ui.Rect{W: 118, H: 4}) || openWorkspace != (ui.Rect{Y: 5, W: 40, H: 4}) {
		t.Fatalf("wide action geometry changed: new=%+v workspace=%+v", newSession, openWorkspace)
	}
	for _, label := range []string{"New Session", s.cwd, "📁", "New Workspace", "Open a folder as a workspace", "enter ↵"} {
		if !strings.Contains(bufferText(buf), label) {
			t.Fatalf("original action content missing %q:\n%s", label, bufferText(buf))
		}
	}
	if strings.Contains(bufferText(buf), "- or -") {
		t.Fatal("rejected wide choice label still paints")
	}
	firstBorder := buf.Get(newSession.X, newSession.Y)
	for i, icon := range []string{"✨", "🌟", "✨", "💫", "✨", "⭐"} {
		s.SetAnimation(i*10000, 0)
		h.MarkLayout()
		buf.Clear()
		h.Paint(buf)
		if !strings.Contains(strings.Split(bufferText(buf), "\n")[1], icon) || buf.Get(newSession.X, newSession.Y) != firstBorder {
			t.Fatalf("icon cycle %d changed shape or missed %s", i, icon)
		}
	}
}

func TestLauncherCrampedActionsStackAndHideCapabilities(t *testing.T) {
	s, _, buf := testScreen(t, 60, 28, manyWorkspaces(2))
	if s.newSession.Bounds() != (ui.Rect{X: 2, W: 56, H: 4}) || s.openWorkspace.Bounds() != (ui.Rect{X: 2, Y: 5, W: 56, H: 4}) {
		t.Fatalf("cramped actions do not stack with one blank row: new=%+v workspace=%+v", s.newSession.Bounds(), s.openWorkspace.Bounds())
	}
	if !s.skills.Bounds().Empty() || !s.mcp.Bounds().Empty() || s.list.Bounds().W != 56 {
		t.Fatalf("cramped layout did not hide the left pane or fill recent workspaces: skills=%+v mcp=%+v list=%+v", s.skills.Bounds(), s.mcp.Bounds(), s.list.Bounds())
	}
	text := bufferText(buf)
	if strings.Contains(text, "Skills") || strings.Contains(text, "MCP") || strings.Contains(text, "- or -") {
		t.Fatalf("hidden wide-only content still painted:\n%s", text)
	}
}

func TestLauncherGuideStaysAtRightPaneBottom(t *testing.T) {
	for _, count := range []int{0, 1, 20} {
		s, _, _ := testScreen(t, 160, 40, manyWorkspaces(count))
		paneBottom := s.divider.Bounds().Y + s.divider.Bounds().H
		if hints := s.hints.Bounds(); hints.H != 1 || hints.Y+hints.H != paneBottom {
			t.Fatalf("workspace guide floated for %d resources: hints=%+v paneBottom=%d", count, hints, paneBottom)
		}
	}
}
