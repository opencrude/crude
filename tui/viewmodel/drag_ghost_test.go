package viewmodel

import (
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"crude/tui/ui"
)

// frameText is the complete presented frame, including the drag ghost that
// is drawn above every surface.
func frameText(app *App) string {
	app.forceFrame()
	return ansi.Strip(app.View().RenderContent())
}

func frameRow(app *App, y int) string {
	rows := strings.Split(frameText(app), "\n")
	if y < 0 || y >= len(rows) {
		return ""
	}
	return rows[y]
}

// A file dragged from the tree carries a ghost with its name at the pointer
// and the drop it would make; the pane zone under the pointer is outlined,
// and release opens the file exactly there.
func TestTreeFileDragShowsGhostAndOpensInPreviewedSplit(t *testing.T) {
	app, root := treeActionsApp(t)
	group := app.Win.ActiveGroup()
	body := app.mainPanes.groups[group].body
	x, y := body.X+body.W-3, body.Y+body.H/2
	treeDrag(t, app, "notes.txt", x, y)

	drop := app.mainPanes.drop
	if drop == nil || drop.edge != "right" || drop.path != filepath.Join(root, "notes.txt") {
		t.Fatalf("right edge did not propose a split for the file: %+v", drop)
	}
	ghost := app.dragGhost().Rect(app.width, app.height)
	if !strings.Contains(frameRow(app, ghost.Y), "notes.txt") || !strings.Contains(frameRow(app, ghost.Y+1), "Open to the right") {
		t.Fatalf("ghost rows: %q / %q", frameRow(app, ghost.Y), frameRow(app, ghost.Y+1))
	}
	if abs(ghost.X-x) > 40 || abs(ghost.Y-y) > 3 {
		t.Fatalf("ghost %+v does not follow the pointer at %d,%d", ghost, x, y)
	}
	if c := app.mainSurf.Buffer().Get(drop.rect.X, drop.rect.Y); c.Char != '╭' {
		t.Fatalf("previewed pane is not outlined: %q", c.Char)
	}
	if len(app.Win.GroupIDs()) != 1 {
		t.Fatal("the preview changed the layout before release")
	}

	treeRelease(t, app, x, y)
	ids := app.Win.GroupIDs()
	if len(ids) != 2 || app.Win.ActiveGroup() == group {
		t.Fatalf("release did not open a split: %v", ids)
	}
	if tab := app.Win.GroupActiveTab(app.Win.ActiveGroup()); tab == nil || tab.Title != app.editorTabTitle(filepath.Join(root, "notes.txt")) {
		t.Fatalf("new split does not show the file: %+v", tab)
	}
	if text := frameText(app); strings.Contains(text, "Open to the right") || app.mainPanes.drop != nil {
		t.Fatal("ghost or preview stayed after the drop")
	}
}

// Esc ends a tree drag over a pane: the ghost and preview disappear and the
// release opens nothing.
func TestTreeFileDragEscapeLeavesNothing(t *testing.T) {
	app, _ := treeActionsApp(t)
	group := app.Win.ActiveGroup()
	body := app.mainPanes.groups[group].body
	x, y := body.X+body.W/2, body.Y+body.H/2
	treeDrag(t, app, "notes.txt", x, y)
	if !strings.Contains(frameText(app), "Open here") {
		t.Fatal("the middle of a pane did not offer to open the file there")
	}
	tabs := app.Win.Tabs()
	_, _ = app.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	if text := frameText(app); strings.Contains(text, "Open here") || app.mainPanes.drop != nil {
		t.Fatal("Esc left the ghost or the preview")
	}
	treeRelease(t, app, x, y)
	if len(app.Win.Tabs()) != len(tabs) || len(app.Win.GroupIDs()) != 1 {
		t.Fatal("release after Esc opened the file")
	}
}

// Inside the tree the ghost states the move; a folder taken out of the tree
// says where it can go instead of offering a pane.
func TestTreeDragGhostStatesMoveAndBlockedDrops(t *testing.T) {
	app, _ := treeActionsApp(t)
	tx, ty := editorTreePoint(t, app, "target")
	treeDrag(t, app, "src", tx, ty)
	ghost := app.dragGhost()
	if ghost.Label != "src" || ghost.Intent != "Move to target" || ghost.Blocked || ghost.Icon == "" {
		t.Fatalf("in-tree ghost: %+v", ghost)
	}
	body := app.mainPanes.groups[app.Win.ActiveGroup()].body
	_, _ = app.Update(tea.MouseMotionMsg{X: body.X + body.W/2, Y: body.Y + body.H/2, Button: tea.MouseLeft})
	ghost = app.dragGhost()
	if !ghost.Blocked || ghost.Intent != "Drop on a folder to move" || app.mainPanes.drop != nil {
		t.Fatalf("folder over a pane: %+v drop=%+v", ghost, app.mainPanes.drop)
	}
	if !strings.Contains(frameText(app), "Drop on a folder to move") {
		t.Fatal("blocked guidance is not painted")
	}
	treeRelease(t, app, body.X+body.W/2, body.Y+body.H/2)
	if (app.dragGhost() != ui.DragGhost{}) {
		t.Fatal("ghost outlived the drag")
	}
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// A pane too narrow to split opens a dropped file in itself from any zone,
// so the ghost never says a visible pane cannot take the file.
func TestTreeFileDropOnNarrowPaneOpensThere(t *testing.T) {
	app, root := treeActionsApp(t)
	_, _ = app.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	_ = app.View()
	group := app.Win.ActiveGroup()
	body := app.mainPanes.groups[group].body
	x, y := body.X+body.W-2, body.Y+body.H/2
	treeDrag(t, app, "notes.txt", x, y)
	if ghost := app.dragGhost(); ghost.Blocked || ghost.Intent != "Open here" {
		t.Fatalf("narrow pane edge: %+v", ghost)
	}
	treeRelease(t, app, x, y)
	if tab := app.Win.GroupActiveTab(group); len(app.Win.GroupIDs()) != 1 || tab == nil || tab.Title != app.editorTabTitle(filepath.Join(root, "notes.txt")) {
		t.Fatalf("file did not open in the narrow pane: groups=%v tab=%+v", app.Win.GroupIDs(), tab)
	}
}
