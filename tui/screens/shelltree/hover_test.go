package shelltree_test

import (
	"os"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/filetree"
	"crude/tui/screens/shelltree"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

func TestHoverPaintUsesModelHoverIndex(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"a.go", "b.go", "c.go"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("package p\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	th := theme.DefaultDark()
	model := filetree.NewFileTreeModel(dir)
	screen := shelltree.New(th, model)
	host := ui.NewHost(th, screen)
	host.Resize(36, 8)
	host.Layout()

	// Hover the second visible file row (workspace root is not painted).
	nodes := model.VisibleNodes()
	if len(nodes) < 3 {
		t.Fatalf("need >=3 visible nodes, got %d", len(nodes))
	}
	host.Handle(tea.MouseMotionMsg{X: 2, Y: 2})

	if model.Hover.Index < 0 {
		t.Fatal("model hover index should be set after motion")
	}
	if screen.TreeControl() == nil {
		t.Fatal("missing tree control")
	}
	if got := screen.TreeControl().HoverIndex(); got != model.Hover.Index {
		t.Fatalf("ui.Tree hoverIdx=%d want model %d", got, model.Hover.Index)
	}

	buf := surface.NewCellBuffer(36, 8)
	host.Paint(buf)
	hoverBG := ui.NewTokenSet(th).TreeHoverBg()
	found := false
	for y := 0; y < buf.Height(); y++ {
		for x := 0; x < buf.Width(); x++ {
			if bg := buf.Get(x, y).BG; !bg.IsDefault() && bg.Equal(hoverBG) {
				found = true
				break
			}
		}
	}
	if !found {
		t.Fatal("expected TreeHoverBg painted after mouse motion")
	}
}
