package workspace

import "testing"

func TestTabViewCarriesNeutralProjectionData(t *testing.T) {
	view := TabView{ID: "root", Title: "agent:root", DisplayTitle: "Main Agent", Kind: "orchestrator", Closable: true}
	if view.ID != "root" || !view.Closable || view.Active {
		t.Fatalf("neutral tab data = %+v", view)
	}
}
