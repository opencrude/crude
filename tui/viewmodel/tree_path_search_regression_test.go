package viewmodel

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"crude/tui/filetree"
	"crude/tui/screens/shelltree"
)

func TestTreePathSearchFindsRootFileThroughRealAsyncPath(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "README.md")
	if err := os.WriteFile(path, []byte("test"), 0o600); err != nil {
		t.Fatal(err)
	}
	app := newFullTestApp(t)
	model := filetree.NewFileTreeModel(root)
	app.Tree.Model = model
	app.sidebarScreen.SetModel(model)
	app.sidebarScreen.SetPathSearchHandler(app.beginTreePathSearch)
	cmd := app.beginTreePathSearch("README.md")
	if cmd == nil {
		t.Fatal("search did not start")
	}
	result, ok := cmd().(treePathSearchResultMsg)
	if !ok {
		t.Fatalf("result type=%T", cmd())
	}
	app.applyTreePathSearchResult(result)
	buf := app.sidebarScreen.PaintTestBuffer(40, 12)
	var painted strings.Builder
	for row := range 12 {
		painted.WriteString(shelltree.PlainRow(buf, row, 40))
		painted.WriteByte('\n')
	}
	if !strings.Contains(painted.String(), "README.md") {
		t.Fatalf("root file missing from search projection:\n%s", painted.String())
	}
}

func TestTreePathSearchDenseWorkspacePublishesLatestQuery(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	app := newFullTestApp(t)
	model := filetree.NewFileTreeModel(root)
	app.Tree.Model = model
	app.sidebarScreen.SetModel(model)
	app.sidebarScreen.SetPathSearchHandler(app.beginTreePathSearch)
	start := time.Now()
	cmd := app.beginTreePathSearch("README.md")
	result := cmd().(treePathSearchResultMsg)
	t.Logf("workspace catalog and rank: %s, query=%q, %d entries, %d matches", time.Since(start), result.query, len(result.catalog.Entries), len(result.matches))
	if len(result.matches) == 0 {
		t.Fatal("exact root filename had no ranked matches")
	}
	app.applyTreePathSearchResult(result)
	buf := app.sidebarScreen.PaintTestBuffer(40, 12)
	var painted strings.Builder
	for row := range 12 {
		painted.WriteString(shelltree.PlainRow(buf, row, 40))
		painted.WriteByte('\n')
	}
	if !strings.Contains(painted.String(), "README.md") {
		t.Fatalf("dense workspace result was not published:\n%s", painted.String())
	}
}
