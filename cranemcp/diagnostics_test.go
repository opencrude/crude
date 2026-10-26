package cranemcp

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"crude/core"
	"crude/coreprotocol"
	"crude/metrics"
	"crude/types"
	"crude/types/control"
	"crude/types/inspect"
	"crude/workspace"
)

func TestSDKDiagnosticsRealCoreReadPagingAndImmutableLaunchBinding(t *testing.T) {
	r, err := core.New(func(root string) (*workspace.Workspace, error) {
		return workspace.NewWorkspace(root, "", "tester", types.Settings{}, logger{}), nil
	}, filepath.Join(t.TempDir(), "receipts.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	root := t.TempDir()
	shell, ws := wire(t, r, "diagnostic-sdk-shell", root)
	peer, _ := wire(t, r, "diagnostic-sdk-peer", root)
	if err := shell.RegisterControlShell(t.Context(), ws); err != nil {
		t.Fatal(err)
	}
	if err := peer.RegisterControlShell(t.Context(), ws); err != nil {
		t.Fatal(err)
	}
	reader, _ := wire(t, r, "diagnostic-sdk-reader", root)
	owner := shell.Owner()
	bound := &control.ShellOwner{ClientID: owner.ClientID, Epoch: owner.Epoch}
	s, err := New(t.Context(), reader, ws, coreprotocol.ControlClientGrant{Effects: []control.Effect{control.Read}, Shell: bound})
	if err != nil {
		t.Fatal(err)
	}
	c := sdk(t, s)
	// Mutating the caller's original grant object cannot retarget this SDK launch.
	bound.ClientID, bound.Epoch = peer.Owner().ClientID, peer.Owner().Epoch
	discovery := call(t, c, "crude_commands", map[string]any{"query": "performance"})
	if discovery.IsError {
		t.Fatal(discovery)
	}
	raw, _ := json.Marshal(discovery.StructuredContent)
	var descriptors []control.Descriptor
	if err := json.Unmarshal(raw, &descriptors); err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, d := range descriptors {
		found[d.ID] = d.Effect == control.Read
	}
	if !found["performance.core"] || !found["performance.shell"] {
		t.Fatal("SDK did not discover distinct existing owners", found)
	}
	filter := "diagnostic.sdk." + t.Name()
	for i := range 21 {
		metrics.NewCounter(metrics.Meta{Name: fmt.Sprintf("%s.%02d", filter, i), Group: "sdk fixture", Title: "Observed SDK fixture work"}).Inc()
	}
	invoke := func(arguments any) inspect.Page {
		t.Helper()
		result := outcome(t, call(t, c, "crude_control", map[string]any{"command": "performance.core", "arguments": arguments}))
		if result.State != control.Committed || result.Fault != nil || len(result.Output) > 4096 || !json.Valid(result.Output) {
			t.Fatalf("SDK did not receive bounded real core evidence: %+v", result)
		}
		var page inspect.Page
		if err := json.Unmarshal(result.Output, &page); err != nil {
			t.Fatal(err)
		}
		return page
	}
	page := invoke(map[string]any{"page": inspect.Request{View: "series", Filter: filter, Limit: 4, MaxBytes: 4096}})
	var source struct {
		PID         int    `json:"pid"`
		Incarnation string `json:"incarnation"`
	}
	if err := json.Unmarshal(page.Source, &source); err != nil || source.PID != os.Getpid() || source.Incarnation == "" || page.Total != 21 || page.Next == nil {
		t.Fatalf("SDK core process/provenance/continuation missing: %+v (%v)", page, err)
	}
	next := invoke(map[string]any{"page": page.Next})
	if next.SnapshotID != page.SnapshotID || next.Offset != page.Returned || !reflect.DeepEqual(next.Source, page.Source) || next.Records[0].ID == page.Records[0].ID {
		t.Fatal("SDK continuation resampled or duplicated immutable evidence", next)
	}
	for _, command := range []string{"diagnostics.core", "diagnostics.workspace"} {
		result := outcome(t, call(t, c, "crude_control", map[string]any{"command": command, "arguments": map[string]any{}}))
		if result.State != control.Committed || result.Fault != nil || !json.Valid(result.Output) || len(result.Output) > inspect.DefaultBytes {
			t.Fatalf("SDK diagnostic Read unavailable: %+v", result)
		}
	}
	// Target shell is not a callable field. Reject before any request reaches a
	// shell; never depend on a fabricated shell observation for this assertion.
	if out := call(t, c, "crude_control", map[string]any{"command": "connection.inspect", "arguments": map[string]any{}, "target": map[string]any{"shell": bound}}); !out.IsError {
		t.Fatal("SDK accepted a replacement shell target", out)
	}
	if out := call(t, c, "crude_control", map[string]any{"command": "session.create", "arguments": map[string]any{}}); !out.IsError {
		result := outcome(t, out)
		if result.Fault == nil || result.Fault.Code != control.Denied {
			t.Fatal("SDK diagnostic read scope widened to execute", out)
		}
	}
	// The immutable registered grant is also enforced by the actual core, not
	// merely by SDK argument validation.
	if err := reader.Call(t.Context(), ws, coreprotocol.ControlClientRegister, &coreprotocol.ControlClientGrant{Effects: []control.Effect{control.Read}, Shell: bound}, nil); err == nil {
		t.Fatal("core allowed SDK launch shell to be rebound")
	}
}
