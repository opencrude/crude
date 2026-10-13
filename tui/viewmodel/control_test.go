package viewmodel

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"crude/core"
	"crude/coreprotocol"
	"crude/shellport"
	"crude/tui/screens/termdock"
	"crude/tui/ui"
	"crude/types/control"
	"github.com/charmbracelet/x/ansi"
)

func requestShellForTest(t *testing.T, runtime *core.Runtime, shells []remoteJourneyShell, shell remoteJourneyShell, id string, input any, adjust func(*coreprotocol.ControlEnvelope)) control.Result {
	t.Helper()
	port := shell.app.ws.(shellport.ShellControl)
	d, ok := ShellCommandDescriptor(id)
	if !ok && id != "shell.control" {
		t.Fatal(id)
	}
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	seq := shell.app.paneFileOpenSeq + uint64(time.Now().UnixNano())
	e := coreprotocol.ControlEnvelope{Descriptor: d, Request: control.Request{ID: fmt.Sprintf("test-%d", seq), CommandID: id, Target: port.ControlIdentity(), Caller: control.Caller{Kind: control.ExternalCaller, ClientID: "test-controller"}, Input: raw}}
	if adjust != nil {
		adjust(&e)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()
	type outcome struct {
		result control.Result
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		for {
			result, err := runtime.RequestShellControl(ctx, e.Request.Target.WorkspaceID, *e.Request.Target.Shell, e)
			if err != nil && strings.Contains(err.Error(), "not registered") {
				select {
				case <-ctx.Done():
					done <- outcome{result, err}
					return
				case <-time.After(time.Millisecond):
					continue
				}
			}
			done <- outcome{result, err}
			return
		}
	}()
	var got outcome
	finished := false
	remoteJourneyUntil(t, shells, func() bool {
		select {
		case got = <-done:
			finished = true
		default:
		}
		return finished
	})
	if got.err != nil {
		t.Fatal(got.err)
	}
	return got.result
}

func TestConnectedShellControlWorkspacePanels(t *testing.T) {
	runtime := remoteJourneyRuntime(t, "")
	one := newRemoteJourneyShell(t, runtime, t.TempDir())
	shells := []remoteJourneyShell{one}
	for _, id := range []string{termdock.PanelProblems, termdock.PanelNotices, termdock.PanelWorkspace, termdock.PanelMemory, termdock.PanelBoard} {
		result := requestShellForTest(t, runtime, shells, one, "panel.show", map[string]any{"id": id}, nil)
		if result.State != control.Applied || !one.app.termDock.IsOpen() || one.app.termDock.ActivePanel() != id {
			t.Fatalf("panel %s did not apply: %+v", id, result)
		}
		one.app.View()
		if one.app.termDock.Panel(id) == nil {
			t.Fatal("no mounted content", id)
		}
	}
	result := requestShellForTest(t, runtime, shells, one, "panel.show", map[string]any{"id": "nonexistent"}, nil)
	if result.State != control.Failed || one.app.termDock.ActivePanel() != termdock.PanelBoard {
		t.Fatal("missing panel falsely applied", result)
	}
}

func TestConnectedShellControlExactOwnerAndPaint(t *testing.T) {
	runtime := remoteJourneyRuntime(t, "")
	root := t.TempDir()
	one, two := newRemoteJourneyShell(t, runtime, root), newRemoteJourneyShell(t, runtime, root)
	shells := []remoteJourneyShell{one, two}
	one.app.Input.SetInput("private composer should never escape")
	before := one.app.inspectShell()
	if before.Revision != one.app.inspectShell().Revision {
		t.Fatal("unchanged inspection advanced revision")
	}
	result := requestShellForTest(t, runtime, shells, one, "shell.inspect", struct{}{}, nil)
	if result.State != control.Applied || strings.Contains(string(result.Output), "private composer") {
		t.Fatal(result)
	}
	result = requestShellForTest(t, runtime, shells, one, "tree.set_visible", map[string]any{"visible": false}, nil)
	if result.State != control.Applied || one.app.Tree.Visible || !two.app.Tree.Visible {
		t.Fatal("shell-local visibility failed", result)
	}
	one.app.View()
	two.app.View()
	if one.app.computeLayout().SidebarW >= two.app.computeLayout().SidebarW {
		t.Fatal("painted sidebar did not disappear")
	}
	result = requestShellForTest(t, runtime, shells, one, "tree.set_width", map[string]any{"columns": 32, "unexpected": true}, nil)
	if result.State != control.Failed {
		t.Fatal("unknown input accepted", result)
	}
	result = requestShellForTest(t, runtime, shells, one, "tree.set_visible", map[string]any{"visible": true}, func(e *coreprotocol.ControlEnvelope) { e.Request.Preconditions.Revision = &before.Revision })
	if result.State != control.Failed || result.Fault.Code != control.Stale || one.app.Tree.Visible {
		t.Fatal("stale revision mutated shell", result)
	}
	result = requestShellForTest(t, runtime, shells, one, "settings.open", map[string]any{"scope": "application"}, nil)
	if result.State != control.Applied || !strings.Contains(ansi.Strip(one.app.View().Content), "Settings") {
		t.Fatal("settings did not paint", result)
	}
	result = requestShellForTest(t, runtime, shells, one, "tree.set_visible", map[string]any{"visible": true}, nil)
	if result.State != control.Failed || result.Fault.Code != control.Denied || one.app.Tree.Visible {
		t.Fatal("modal bypass", result)
	}
}

func TestConnectedShellControlFileMountAndDirtyDecision(t *testing.T) {
	runtime := remoteJourneyRuntime(t, "")
	root := t.TempDir()
	path := filepath.Join(root, "control.txt")
	if err := os.WriteFile(path, []byte("real connected editor content\nsecond line\n"), 0600); err != nil {
		t.Fatal(err)
	}
	one, two := newRemoteJourneyShell(t, runtime, root), newRemoteJourneyShell(t, runtime, root)
	shells := []remoteJourneyShell{one, two}
	result := requestShellForTest(t, runtime, shells, one, "file.open", map[string]any{"path": path, "line": 1, "column": 2}, nil)
	if result.State != control.Applied || len(result.ResourceIDs) != 2 || one.app.EditorState.Editor == nil || two.app.EditorState.Editor.IsOpen() {
		t.Fatal("file receipt is not exact mount", result)
	}
	if !strings.Contains(ansi.Strip(one.app.View().Content), "real connected editor content") {
		t.Fatal("opened content not painted")
	}
	one.app.EditorState.Editor.InsertRune('X')
	tab := one.app.Win.ActiveTab()
	result = requestShellForTest(t, runtime, shells, one, "tab.close", map[string]any{"tab_id": tab.ID}, nil)
	if result.State != control.PendingDecision || one.app.DirtyPromptPath == "" || one.app.Win.IndexOf(tab.ID) < 0 {
		t.Fatal("dirty close falsely succeeded", result)
	}
}

func TestConnectedShellControlDescribeAndExecuteButton(t *testing.T) {
	runtime := remoteJourneyRuntime(t, "")
	one := newRemoteJourneyShell(t, runtime, t.TempDir())
	shells := []remoteJourneyShell{one}
	path := filepath.Join(one.app.ws.RootPath(), "generic.txt")
	if err := os.WriteFile(path, []byte("generic tab content"), 0600); err != nil {
		t.Fatal(err)
	}
	requestShellForTest(t, runtime, shells, one, "file.open", map[string]any{"path": path}, nil)
	tab := one.app.Win.ActiveTab()
	one.app.View()
	page, err := one.app.rootHost.InspectControls(one.app.ws.(shellport.ShellControl).ControlIdentity(), control.PageRequest{Limit: 128})
	if err != nil {
		t.Fatal(err)
	}
	id := ""
	for _, node := range page.Nodes {
		for _, action := range node.Actions {
			if action.ID == "tabs.close" {
				id = node.ID
			}
		}
	}
	if id == "" {
		t.Fatal("real tab strip was not inspectable")
	}
	intent := shellControlIntent{ComponentID: id, Operation: ui.ControlOperation{Verb: "close", ItemID: uiTabID(tab.ID)}}
	result := requestShellForTest(t, runtime, shells, one, "shell.describe_control", intent, nil)
	if result.State != control.Applied {
		t.Fatal(result)
	}
	var described struct {
		Descriptor control.Descriptor `json:"descriptor"`
		Node       control.Node       `json:"node"`
	}
	if err := json.Unmarshal(result.Output, &described); err != nil {
		t.Fatal(err)
	}
	intent.ExpectedAction = described.Descriptor.ID
	result = requestShellForTest(t, runtime, shells, one, "shell.control", intent, func(e *coreprotocol.ControlEnvelope) {
		e.Descriptor = described.Descriptor
		e.Descriptor.ID = "shell.control"
		e.Request.Target.ResourceID = id
		e.Request.Preconditions = control.Preconditions{Revision: &described.Node.Revision, OpenEpoch: &described.Node.OpenEpoch}
	})
	if result.State != control.Applied || one.app.Win.IndexOf(tab.ID) >= 0 {
		t.Fatal("real tab strip Close did not execute", result)
	}
	result = requestShellForTest(t, runtime, shells, one, "shell.control", intent, func(e *coreprotocol.ControlEnvelope) {
		e.Descriptor = described.Descriptor
		e.Descriptor.ID = "shell.control"
		e.Request.Target.ResourceID = id
		e.Request.Preconditions = control.Preconditions{Revision: &described.Node.Revision, OpenEpoch: &described.Node.OpenEpoch}
	})
	if result.State != control.Failed {
		t.Fatal("unmounted control preimage accepted", result)
	}
}
