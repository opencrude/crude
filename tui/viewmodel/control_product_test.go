package viewmodel

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"crude/coreprotocol"
	"crude/shellport"
	"crude/tui/ui"
	"crude/types/control"
	"github.com/charmbracelet/x/ansi"
)

// Direct semantic entry while parent integrates dispatch; real detached connected
// shells still own all rendering and replicated resources in these tests.
func productRequest(t *testing.T, m *App, id, resource, session string, input any, adjust func(*control.Request)) control.Result {
	t.Helper()
	d, ok := productDescriptor(id)
	if !ok {
		t.Fatal(id)
	}
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	p := m.ws.(shellport.ShellControl)
	s := m.inspectShell()
	r := control.Request{ID: "product-test", CommandID: id, Caller: control.Caller{Kind: control.ExternalCaller, ClientID: "test"}, Target: p.ControlIdentity(), Input: raw, Preconditions: control.Preconditions{Revision: &s.Revision, OpenEpoch: &s.OpenEpoch}}
	r.Target.ResourceID, r.Target.SessionID = resource, session
	if strings.HasPrefix(id, "composer.") || id == "capability.picker" {
		rev, epoch := m.Input.DraftRevision(), m.Input.DraftEpoch()
		r.Preconditions = control.Preconditions{Revision: &rev, OpenEpoch: &epoch}
	}
	if adjust != nil {
		adjust(&r)
	}
	var result control.Result
	msg := shellport.ControlRequestMsg{Owner: p, Envelope: coreprotocol.ControlEnvelope{Descriptor: d, Request: r}, Reply: func(_ context.Context, got control.Result) error { result = got; return nil }}
	cmd, handled := m.handleProductControl(msg)
	if !handled || cmd == nil {
		t.Fatal("unhandled", id)
	}
	var run func(tea.Cmd)
	run = func(cmd tea.Cmd) {
		if cmd == nil {
			return
		}
		switch got := cmd().(type) {
		case tea.BatchMsg:
			for _, next := range got {
				run(next)
			}
		case shellControlDoneMsg:
			next, ok := m.finishProductControl(got)
			if !ok {
				t.Fatal("unexpected completion")
			}
			if m.shellControlPending > 0 {
				m.shellControlPending--
			}
			run(next)
		}
	}
	run(cmd)
	return result
}

func TestProductTabMoveConnectedShellIsolation(t *testing.T) {
	runtime := remoteJourneyRuntime(t, "")
	root := t.TempDir()
	one, two := newRemoteJourneyShell(t, runtime, root), newRemoteJourneyShell(t, runtime, root)
	m := one.app
	a := m.Win.OpenTab("a", "", true).ID
	b := m.Win.OpenTab("b", "", true).ID
	group := m.Win.SplitGroup(m.Win.ActiveGroup(), ui.Horizontal, false)
	result := productRequest(t, m, "tab.move", fmt.Sprint(a), "", map[string]any{"tab_id": a, "group_id": group, "before_tab_id": 0}, nil)
	if result.State != control.Applied || m.Win.GroupOf(a) != group || m.Win.TabCount() != 2 || two.app.Win.TabCount() != 0 {
		t.Fatal(result)
	}
	m.View()
	if m.mainPanes.groups[group] == nil {
		t.Fatal("moved group not rendered")
	}
	result = productRequest(t, m, "tab.move", fmt.Sprint(b), "", map[string]any{"tab_id": b, "group_id": group, "before_tab_id": a}, func(r *control.Request) { zero := uint64(0); r.Preconditions.Revision = &zero })
	if result.State != control.Failed || result.Fault.Code != control.Stale || m.Win.GroupOf(b) == group {
		t.Fatal("stale move", result)
	}
	m.openApplicationSettings()
	result = productRequest(t, m, "tab.move", fmt.Sprint(b), "", map[string]any{"tab_id": b, "group_id": group, "before_tab_id": a}, nil)
	if result.State != control.Failed || result.Fault.Code != control.Denied {
		t.Fatal("modal bypass", result)
	}
}

func TestProductWindowMovementPreimageAndPaint(t *testing.T) {
	runtime := remoteJourneyRuntime(t, "")
	one := newRemoteJourneyShell(t, runtime, t.TempDir())
	m := one.app
	result := productRequest(t, m, "project.search_open", "", "", map[string]any{"replace": false}, nil)
	if result.State != control.Admitted || !strings.Contains(ansi.Strip(m.View().Content), "Find in Files") {
		t.Fatal("search did not paint", result)
	}
	w := m.overlayHost.Window(ui.OverlayFindInFiles)
	x, y := w.Position()
	width, height := w.Size()
	epoch := w.Content.(*ui.WindowFrameWidget).Host().ControlLifetime()
	in := map[string]any{"id": w.ID, "epoch": epoch, "expected_x": x, "expected_y": y, "expected_width": width, "expected_height": height, "x": 0, "y": 0}
	result = productRequest(t, m, "window.move", w.ID, "", in, nil)
	if result.State != control.Applied {
		t.Fatal(result)
	}
	m.View()
	nx, ny := w.Position()
	if nx != 0 || ny != 0 {
		t.Fatal("window did not move")
	}
	result = productRequest(t, m, "window.move", w.ID, "", in, nil)
	if (x != 0 || y != 0) && (result.State != control.Failed || result.Fault.Code != control.Stale) {
		t.Fatal("changed preimage accepted", result)
	}
}
