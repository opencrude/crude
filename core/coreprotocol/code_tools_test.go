package coreprotocol

import (
	"reflect"
	"testing"

	"crude/types/insight"
)

// The Settings table consumes the assembly with the inventory over the core
// protocol, not the in-process value returned by the service worker.
func TestCodeToolInventoryCarriesProjectAssemblyOverWire(t *testing.T) {
	want := ReadCodeToolInventoryResult{Inventory: insight.CodeToolInventory{
		Project: insight.AssemblyPlan{
			Generation: 17, State: insight.AssemblyReady, Limited: true,
			Languages: []insight.AssemblyLanguage{
				{ID: "fixture", Command: "fixture-server", Status: insight.ToolMissing},
				{ID: "other", Command: "other-server", Executable: "/user/bin/other", Source: insight.ToolSourcePath, Status: insight.ToolReady},
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
	want := ProjectToolsReviewRequest{Selection: insight.ProjectToolSetupRequest{
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
