package workspace

import (
	"reflect"
	"testing"
)

func TestToolLayoutPersistsIndependentNavigationAndPanelState(test *testing.T) {
	owner := &Workspace{WorkspaceDB: openTestWorkspaceDB(test)}
	layout := DefaultToolLayout()
	layout.Rail = []string{"mcp", "files", "git"}
	layout.Views["mcp"] = ToolViewState{Width: 38, Filter: "documentation", Selection: "docs", Offset: 12}
	layout.Views["skills"] = ToolViewState{Width: 29, Filter: "review", Selection: "vendor:review", Offset: 4}
	layout.PanelHeights["terminal"], layout.PanelHeights["memory"] = 12, 22
	if err := owner.SaveToolLayout(layout); err != nil {
		test.Fatal(err)
	}
	resumed := &Workspace{WorkspaceDB: owner.WorkspaceDB}
	loaded, err := resumed.LoadToolLayout()
	if err != nil || !reflect.DeepEqual(loaded, layout) {
		test.Fatalf("presentation round-trip: %+v %v", loaded, err)
	}
	layout.Rail = []string{"files", "terminals"}
	if err := owner.SaveToolLayout(layout); err == nil {
		test.Fatal("removed terminal rail shortcut persisted")
	}
	loaded, _ = resumed.LoadToolLayout()
	if len(loaded.Rail) != 3 || loaded.PanelHeights["memory"] != 22 {
		test.Fatal("invalid save changed valid presentation")
	}
}
