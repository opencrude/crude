package viewmodel

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"crude/shellport"
	"crude/tui/ui"
	"crude/tui/wm"
	"crude/types/control"
)

func TestConnectedShellInspectionPagesBeyond4096Rows(t *testing.T) {
	runtime := remoteJourneyRuntime(t, "")
	one := newRemoteJourneyShell(t, runtime, t.TempDir())
	m := one.app
	target := m.ws.(shellport.ShellControl).ControlIdentity()
	const count = 4357
	items := make([]ui.ListItem, count)
	for i := range items {
		items[i] = ui.ListItem{ID: fmt.Sprintf("item-%04d", i), Label: fmt.Sprintf("fixture-row-%04d", i), Payload: "private-payload-never-export"}
	}
	list := ui.NewList("fixture/list", ui.NewTokenSet(m.Theme))
	list.SetItems(items)
	text := ui.NewTextField("fixture/text", ui.NewTokenSet(m.Theme))
	text.SetValue("private-safe-looking-text")
	body := ui.NewStack("fixture/body", ui.Vertical, 0, list, text)
	open := func() {
		frame := ui.NewWindowFrameWidget(m.Theme, "fixture/frame", ui.FrameWindow, "Paging fixture", body, nil, false, 80, 20)
		window := wm.NewFloatingWindow(string(ui.OverlayOperationIssue), "Paging fixture", frame, 80, 20, wm.WithModal(true), wm.WithFocusable(true))
		m.overlayHost.Open(ui.OverlayOperationIssue, window, ui.ModalDialogPolicy(), nil)
		m.View()
	}
	open()
	seen := map[string]bool{}
	cursor, firstCursor := "", ""
	pages, offscreen, lastSize := 0, 0, 0
	for {
		page, err := m.inspectShellControls(target, control.PageRequest{Limit: 128, Cursor: cursor})
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Nodes) == 0 || len(page.Nodes) > 128 {
			t.Fatalf("unbounded/empty page: %d", len(page.Nodes))
		}
		pages++
		lastSize = len(page.Nodes)
		raw, err := json.Marshal(page)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), "private-payload-never-export") || strings.Contains(string(raw), "private-safe-looking-text") {
			t.Fatal("unapproved private values exported")
		}
		for _, node := range page.Nodes {
			if seen[node.ID] {
				t.Fatalf("duplicate page node %s", node.ID)
			}
			seen[node.ID] = true
			if strings.HasPrefix(node.Name, "fixture-row-") && !node.State.Visible {
				offscreen++
			}
		}
		if page.NextCursor == "" {
			break
		}
		if firstCursor == "" {
			firstCursor = page.NextCursor
			// The shell cursor must carry the bounded Host continuation rather
			// than an index into a truncated full-node shell export.
			encoded, err := base64.RawURLEncoding.DecodeString(firstCursor)
			if err != nil {
				t.Fatal(err)
			}
			var position struct{ Cursor string }
			if err := json.Unmarshal(encoded, &position); err != nil || !strings.Contains(position.Cursor, ":") {
				t.Fatalf("no underlying Host cursor: %s %v", encoded, err)
			}
		}
		cursor = page.NextCursor
		if pages > 100 {
			t.Fatal("paging did not terminate")
		}
	}
	rows := 0
	for id := range seen {
		if strings.Contains(id, "fixture/list/item/") {
			rows++
		}
	}
	// Accessible row labels independently establish complete membership.
	if rows != count {
		t.Fatalf("lost offscreen rows: %d/%d (nodes=%d)", rows, count, len(seen))
	}
	if pages <= 32 || offscreen < count-20 || lastSize == 128 {
		t.Fatalf("tail/virtualization regression: pages=%d offscreen=%d tail=%d", pages, offscreen, lastSize)
	}
	items[count-1].Label = "source-changed-at-tail"
	list.SetItems(items)
	if _, err := m.inspectShellControls(target, control.PageRequest{Limit: 128, Cursor: firstCursor}); err == nil {
		t.Fatal("offscreen source mutation retained shell cursor")
	}
	page, err := m.inspectShellControls(target, control.PageRequest{Limit: 128})
	if err != nil {
		t.Fatal(err)
	}
	m.overlayHost.Close(ui.OverlayOperationIssue)
	open()
	if _, err := m.inspectShellControls(target, control.PageRequest{Limit: 128, Cursor: page.NextCursor}); err == nil {
		t.Fatal("cursor crossed modal close/reopen")
	}
}
