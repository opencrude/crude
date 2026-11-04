package coreprotocol

import (
	"reflect"
	"testing"

	"crude/types"
)

// The Settings table consumes the assembly with the inventory over the core
// protocol, not the in-process value returned by the service worker.
func TestCodeToolInventoryCarriesProjectAssemblyOverWire(t *testing.T) {
	want := ReadCodeToolInventoryResult{Inventory: types.CodeToolInventory{
		Project: types.AssemblyPlan{
			Generation: 17, State: types.AssemblyReady, Limited: true,
			Languages: []types.AssemblyLanguage{
				{ID: "fixture", Command: "fixture-server", Status: types.ToolMissing},
				{ID: "other", Command: "other-server", Executable: "/user/bin/other", Source: types.ToolSourcePath, Status: types.ToolReady},
			},
		},
	}}
	wire, err := want.MarshalMsg(nil)
	if err != nil {
		t.Fatal(err)
	}
	var got ReadCodeToolInventoryResult
	if rest, err := got.UnmarshalMsg(wire); err != nil || len(rest) != 0 {
		t.Fatalf("decode inventory: trailing=%d error=%v", len(rest), err)
	}
	if !reflect.DeepEqual(got.Inventory.Project, want.Inventory.Project) {
		t.Fatalf("project assembly lost on wire: got %+v, want %+v", got.Inventory.Project, want.Inventory.Project)
	}
}

func TestProjectToolReviewRequestCarriesSingleToolOverWire(t *testing.T) {
	want := ProjectToolsReviewRequest{Selection: types.ProjectToolSetupRequest{
		Explicit: true, LanguageID: "fixture", AssemblyGeneration: 23,
	}}
	wire, err := want.MarshalMsg(nil)
	if err != nil {
		t.Fatal(err)
	}
	var got ProjectToolsReviewRequest
	if rest, err := got.UnmarshalMsg(wire); err != nil || len(rest) != 0 {
		t.Fatalf("decode setup request: trailing=%d error=%v", len(rest), err)
	}
	if got != want {
		t.Fatalf("setup request lost its scope: got %+v, want %+v", got, want)
	}
}
