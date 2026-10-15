package viewmodel

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"crude/core"
	"crude/coreprotocol"
	"crude/shellport"
	"crude/tui/screens/termdock"
	"crude/tui/ui"
	captypes "crude/types/capability"
	"crude/types/control"
	"crude/workspace"
	"github.com/charmbracelet/x/ansi"
)

func localControlNode(t *testing.T, m *App, suffix string) control.Node {
	t.Helper()
	m.View()
	target := m.ws.(shellport.ShellControl).ControlIdentity()
	cursor := ""
	for {
		page, err := m.inspectShellControls(target, control.PageRequest{Limit: 128, Cursor: cursor})
		if err != nil {
			t.Fatal(err)
		}
		for _, node := range page.Nodes {
			if node.ID == suffix || strings.HasSuffix(node.ID, "::"+suffix) {
				return node
			}
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	t.Fatalf("missing local control %s", suffix)
	return control.Node{}
}

func invokeLocalControl(t *testing.T, runtime *core.Runtime, shells []remoteJourneyShell, shell remoteJourneyShell, suffix string, op ui.ControlOperation, expected control.Effect, tamper bool) control.Result {
	t.Helper()
	node := localControlNode(t, shell.app, suffix)
	intent := shellControlIntent{ComponentID: node.ID, Operation: op}
	d, _, err := shell.app.describeShellControl(intent, shell.app.ws.(shellport.ShellControl).ControlIdentity())
	if err != nil {
		t.Fatal(suffix, err)
	}
	if d.Effect != expected {
		t.Fatalf("%s effect %s, want %s", suffix, d.Effect, expected)
	}
	intent.ExpectedAction = d.ID
	return requestShellForTest(t, runtime, shells, shell, "shell.control", intent, func(e *coreprotocol.ControlEnvelope) {
		e.Descriptor = d
		e.Descriptor.ID = "shell.control"
		if tamper {
			e.Descriptor.Effect = control.Write
		}
		e.Request.Target.ResourceID = node.ID
		e.Request.Preconditions = control.Preconditions{Revision: &node.Revision, OpenEpoch: &node.OpenEpoch}
	})
}

func TestConnectedLocalMemorySearchInspectPinRetire(t *testing.T) {
	runtime := remoteJourneyRuntime(t, "")
	root := t.TempDir()
	one, two := newRemoteJourneyShell(t, runtime, root), newRemoteJourneyShell(t, runtime, root)
	shells := []remoteJourneyShell{one, two}
	two.app.Input.SetInput("peer memory draft")
	// Populate the real durable store; both shells still read through typed RPC.
	db, err := workspace.OpenWorkspaceDB(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	record := memoryBrowserTestRecord(t, db, "Semantic curation")
	requestShellForTest(t, runtime, shells, one, "panel.show", map[string]any{"id": termdock.PanelMemory}, nil)
	if r := invokeLocalControl(t, runtime, shells, one, "memorybrowser/search", ui.ControlOperation{Verb: "set_value", Value: json.RawMessage(`"Semantic"`)}, control.Write, false); r.State != control.Applied {
		t.Fatal("search", r)
	}
	if !strings.Contains(ansi.Strip(one.app.View().Content), "Semantic curation") {
		t.Fatal("search result not painted")
	}
	if r := invokeLocalControl(t, runtime, shells, one, "memorybrowser/list/control", ui.ControlOperation{Verb: "activate", ItemID: record.ID}, control.Read, true); r.State != control.Failed {
		t.Fatal("tampered read accepted", r)
	}
	if r := invokeLocalControl(t, runtime, shells, one, "memorybrowser/list/control", ui.ControlOperation{Verb: "activate", ItemID: record.ID}, control.Read, false); r.State != control.Applied {
		t.Fatal("inspect", r)
	}
	if !strings.Contains(ansi.Strip(one.app.View().Content), "Revision: 1") {
		t.Fatal("detail not painted")
	}
	activate := ui.ControlOperation{Verb: "activate"}
	if r := invokeLocalControl(t, runtime, shells, one, "memorybrowser/actions/menu", activate, control.Write, false); r.State != control.Applied {
		t.Fatal(r)
	}
	if r := invokeLocalControl(t, runtime, shells, one, "memorybrowser/action-list", ui.ControlOperation{Verb: "activate", ItemID: "memorybrowser.pin"}, control.Write, false); r.State != control.Applied {
		t.Fatal("pin", r)
	}
	detail, err := one.app.ws.InspectMemory(context.Background(), record.ID)
	if err != nil || !detail.Record.Pinned || detail.Record.Revision != 2 {
		t.Fatalf("pin durable: %+v %v", detail, err)
	}
	if r := invokeLocalControl(t, runtime, shells, one, "memorybrowser/actions/menu", activate, control.Write, false); r.State != control.Applied {
		t.Fatal(r)
	}
	if r := invokeLocalControl(t, runtime, shells, one, "memorybrowser/action-list", ui.ControlOperation{Verb: "activate", ItemID: "memorybrowser.retire"}, control.Write, false); r.State != control.Applied {
		t.Fatal(r)
	}
	if !strings.Contains(ansi.Strip(one.app.View().Content), "revision 2?") {
		t.Fatal("exact retire preview missing")
	}
	// Another shell curates the record after preview. The old revision must fail
	// without retiring that newer work or reporting an Applied receipt.
	if err := two.app.ws.PinMemory(context.Background(), record.ID, 2, false); err != nil {
		t.Fatal(err)
	}
	if r := invokeLocalControl(t, runtime, shells, one, "memorybrowser/actions/confirm-retire", activate, control.Delete, false); r.State != control.Failed {
		t.Fatal("stale retirement applied", r)
	}
	detail, err = one.app.ws.InspectMemory(context.Background(), record.ID)
	if err != nil || detail.Record.Revision != 3 || detail.Record.Pinned || detail.Record.Status == "superseded" {
		t.Fatalf("newer work lost: %+v %v", detail, err)
	}
	if r := invokeLocalControl(t, runtime, shells, one, "memorybrowser/actions/back", activate, control.Write, false); r.State != control.Applied {
		t.Fatal(r)
	}
	if r := invokeLocalControl(t, runtime, shells, one, "memorybrowser/actions/menu", activate, control.Write, false); r.State != control.Applied {
		t.Fatal(r)
	}
	if r := invokeLocalControl(t, runtime, shells, one, "memorybrowser/action-list", ui.ControlOperation{Verb: "activate", ItemID: "memorybrowser.reload"}, control.Read, false); r.State != control.Applied {
		t.Fatal(r)
	}
	if r := invokeLocalControl(t, runtime, shells, one, "memorybrowser/action-list", ui.ControlOperation{Verb: "activate", ItemID: "memorybrowser.retire"}, control.Write, false); r.State != control.Applied {
		t.Fatal(r)
	}
	if r := invokeLocalControl(t, runtime, shells, one, "memorybrowser/actions/confirm-retire", activate, control.Delete, false); r.State != control.Applied {
		t.Fatal("retire", r)
	}
	detail, err = one.app.ws.InspectMemory(context.Background(), record.ID)
	if err != nil || detail.Record.Revision != 4 || !strings.Contains(ansi.Strip(one.app.View().Content), "superseded") {
		t.Fatalf("retire not durable/painted: %+v %v", detail, err)
	}
	if two.app.Input.Value() != "peer memory draft" || two.app.memoryBrowserVisible() {
		t.Fatal("peer changed")
	}
}

func TestConnectedLocalBoardOverflowAndPickerNavigation(t *testing.T) {
	runtime := remoteJourneyRuntime(t, "")
	root := t.TempDir()
	one, two := newRemoteJourneyShell(t, runtime, root), newRemoteJourneyShell(t, runtime, root)
	shells := []remoteJourneyShell{one, two}
	two.app.Input.SetInput("peer board draft")
	requestShellForTest(t, runtime, shells, one, "panel.show", map[string]any{"id": termdock.PanelBoard}, nil)
	// A narrow real layout uses More rather than a second dispatch engine.
	one.app.width = 32
	one.app.Tree.Visible = false
	one.app.workspacePanelMaximized = true
	if r := invokeLocalControl(t, runtime, shells, one, "boardbrowser/more", ui.ControlOperation{Verb: "activate"}, control.Write, false); r.State != control.Applied {
		t.Fatal(r)
	}
	if r := invokeLocalControl(t, runtime, shells, one, "boardbrowser/action-list", ui.ControlOperation{Verb: "activate", ItemID: "board.refresh"}, control.Read, false); r.State != control.Applied {
		t.Fatal("refresh", r)
	}
	if !strings.Contains(ansi.Strip(one.app.View().Content), "Refresh") {
		t.Fatal("Board overflow not painted")
	}
	one.app.width = 120
	one.app.capabilitySnapshot = captypes.Snapshot{Settings: captypes.Settings{Connections: map[string]captypes.ConnectionConfig{"observed": {ID: "observed", Name: "Documentation"}}}, Entries: []captypes.Entry{{ID: "observed", Kind: captypes.KindMCP, Name: "Documentation", Installed: true}}}
	one.app.openCapabilityPickerTab(captypes.KindMCP, "")
	if r := invokeLocalControl(t, runtime, shells, one, "capability-picker/filter", ui.ControlOperation{Verb: "set_value", Value: json.RawMessage(`"docu"`)}, control.Write, false); r.State != control.Applied {
		t.Fatal(r)
	}
	localControlNode(t, one.app, "capability-picker/card/observed/switch")
	if paint := ansi.Strip(one.app.View().Content); !strings.Contains(paint, "Documentation") || !strings.Contains(paint, "Open a session") {
		t.Fatal("capability card or no-session state not painted", paint)
	}
	if _, _, err := one.app.resolveControlHost("boardbrowser/action-list", one.app.ws.(shellport.ShellControl).ControlIdentity()); err == nil {
		t.Fatal("modal allowed underlying owner")
	}
	old := localControlNode(t, one.app, "capability-picker/filter")
	one.app.overlayHost.Close(ui.OverlayCapabilityPicker)
	one.app.openCapabilityPicker()
	if _, _, err := one.app.resolveControlHost(old.ID, one.app.ws.(shellport.ShellControl).ControlIdentity()); err == nil {
		t.Fatal("picker reopen reused lifetime")
	}
	if two.app.Input.Value() != "peer board draft" || two.app.termDock.IsOpen() {
		t.Fatal("peer changed")
	}
}
