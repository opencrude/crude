package memorybrowser

import (
	"fmt"
	"testing"

	"crude/memory"
	"crude/tui/ui"
	"crude/types/control"
)

func TestControlInventoryIncludesOffscreenNativeMemoryItems(t *testing.T) {
	records := make([]memory.ScoredRecord, 60)
	for i := range records {
		records[i].Record = memory.Record{ID: fmt.Sprintf("record-%d", i), Title: fmt.Sprintf("Knowledge %d", i), Revision: 1}
	}
	_, host := browserHost(t, records)
	cursor, found := "", false
	for {
		page, err := host.InspectControls(control.Target{}, control.PageRequest{Limit: 12, Cursor: cursor})
		if err != nil {
			t.Fatal(err)
		}
		for _, node := range page.Nodes {
			if node.ID == "memorybrowser/list/control/item/record-59" {
				found = true
			}
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	if !found {
		t.Fatal("offscreen native collection identity missing")
	}
	_, err := host.DescribeControl(ui.NewID(ListID, "control"), ui.ControlOperation{Verb: "activate", ItemID: "missing"})
	if err == nil {
		t.Fatal("unavailable record accepted")
	}
}
