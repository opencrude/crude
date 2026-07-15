package widgets

import (
	"fmt"
	"strings"
	"testing"

	"crude/tui/rendering"
	"crude/tui/theme"
)

func testWorkspaceEntries(count int) []WorkspaceEntry {
	entries := make([]WorkspaceEntry, count)
	for i := range entries {
		entries[i] = WorkspaceEntry{
			Name:         fmt.Sprintf("workspace-%02d", i),
			Path:         fmt.Sprintf("/projects/workspace-%02d", i),
			SessionCount: i,
		}
	}
	return entries
}

func TestWelcomeLauncherWorkspaceViewport(t *testing.T) {
	items := BuildLauncherItems(testWorkspaceEntries(24), nil)
	paint := RenderWelcomeLauncher("/projects/current", items, 2, 0, false, "", theme.DefaultDark(), 110, 16, 0, 0)
	plain := stripAnsi(strings.Join(paint.Lines, "\n"))

	if !strings.Contains(plain, "New Workspace") {
		t.Fatalf("expected renamed workspace action, got:\n%s", plain)
	}
	if strings.Contains(plain, "Open Folder") {
		t.Fatalf("legacy Open Folder label should not be rendered, got:\n%s", plain)
	}
	if !strings.Contains(plain, "Search workspaces") {
		t.Fatalf("expected persistent workspace search, got:\n%s", plain)
	}
	if paint.WorkspaceScrollMax <= 0 {
		t.Fatalf("expected independently scrollable workspace viewport, max=%d", paint.WorkspaceScrollMax)
	}
	for i, line := range paint.Lines {
		if got := rendering.VisibleWidth(line); got != 110 {
			t.Fatalf("launcher line %d width=%d, want 110", i, got)
		}
	}
	if paint.WorkspaceViewportH != paint.WorkspaceListY1-paint.WorkspaceListY0+1 {
		t.Fatalf("viewport geometry mismatch: %+v", paint)
	}

	workspaceHits := 0
	for _, hit := range paint.Hits {
		if hit.ItemIdx >= 2 {
			workspaceHits++
			if hit.Y0 != hit.Y1 {
				t.Fatalf("workspace item %d should occupy one row, got y=%d..%d", hit.ItemIdx, hit.Y0, hit.Y1)
			}
			if hit.X1 != paint.WorkspacePaneX1-1 {
				t.Fatalf("workspace row should fill viewport width before scrollbar, x1=%d pane=%d", hit.X1, paint.WorkspacePaneX1)
			}
		}
	}
	if workspaceHits != paint.WorkspaceViewportH {
		t.Fatalf("expected %d visible workspace hits, got %d", paint.WorkspaceViewportH, workspaceHits)
	}

	last := RenderWelcomeLauncher("/projects/current", items, 2, 999, false, "", theme.DefaultDark(), 110, 16, 0, 0)
	if plainLast := stripAnsi(strings.Join(last.Lines, "\n")); !strings.Contains(plainLast, "workspace-23") {
		t.Fatalf("expected final workspace after scrolling, got:\n%s", plainLast)
	}
}

func TestWelcomeLauncherWorkspaceSearch(t *testing.T) {
	items := BuildLauncherItems(testWorkspaceEntries(24), nil)
	paint := RenderWelcomeLauncher("/projects/current", items, 25, 0, true, "workspace-23", theme.DefaultDark(), 110, 16, 0, 0)
	plain := stripAnsi(strings.Join(paint.Lines, "\n"))

	if paint.WorkspaceVisibleCount != 1 {
		t.Fatalf("expected one filtered workspace, got %d", paint.WorkspaceVisibleCount)
	}
	if !strings.Contains(plain, "workspace-23") || strings.Contains(plain, "workspace-22") {
		t.Fatalf("workspace search did not constrain the viewport:\n%s", plain)
	}
	if paint.WorkspaceSearchY >= paint.WorkspaceListY0 {
		t.Fatalf("search must remain fixed above scrolling rows: search=%d list=%d", paint.WorkspaceSearchY, paint.WorkspaceListY0)
	}
}
