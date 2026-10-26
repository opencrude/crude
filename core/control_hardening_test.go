package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"crude/coreclient"
	"crude/coreprotocol"
	"crude/document"
	"crude/types"
	"crude/types/control"
	"crude/workspace"
)

func TestControlGatewayReadRevisionAndBoundedMutationReceipt(t *testing.T) {
	r, err := New(func(root string) (*workspace.Workspace, error) {
		return workspace.NewWorkspace(root, "", "test", types.Settings{}, coreTestLogger{}), nil
	}, filepath.Join(t.TempDir(), "receipts.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	c := controlWireClient(t, r, "hardening-client")
	ctx := t.Context()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "large.txt"), []byte(strings.Repeat("a", control.MaxValueBytes)), 0600); err != nil {
		t.Fatal(err)
	}
	var attached coreprotocol.AttachResult
	if err := c.Call(ctx, "", coreprotocol.Attach, &coreprotocol.AttachRequest{Root: root}, &attached); err != nil {
		t.Fatal(err)
	}
	if err := c.Call(ctx, attached.WorkspaceID, coreprotocol.ControlClientRegister, &coreprotocol.ControlClientGrant{Effects: []control.Effect{control.Read, control.Write}}, nil); err != nil {
		t.Fatal(err)
	}
	a := r.actors()[0]
	revision := func() uint64 {
		var value uint64
		if err := a.inspect(ctx, func() { value = a.revision }); err != nil {
			t.Fatal(err)
		}
		return value
	}
	execute := func(id, command string, target control.Target, input any) control.Result {
		t.Helper()
		raw, _ := json.Marshal(input)
		request := control.Request{ID: id, CommandID: command, Target: target, Input: raw}
		var out control.Result
		if _, err := c.CallWith(ctx, attached.WorkspaceID, coreprotocol.ControlExecute, &request, &out, coreclient.CallOptions{ID: id}); err != nil {
			t.Fatal(err)
		}
		if out.Validate() != nil {
			t.Fatalf("unbounded result: %+v", out)
		}
		return out
	}
	before := revision()
	for _, command := range []string{"session.list", "terminal.list"} {
		out := execute(command, command, control.Target{}, struct{}{})
		if out.Fault != nil || revision() != before {
			t.Fatalf("read %s mutated workspace revision: %d -> %d, %+v", command, before, revision(), out)
		}
	}
	opened := execute("open-large", "document.open", control.Target{}, map[string]any{"path": "large.txt"})
	// Opening a document acquires an actual view and may publish LSP state; it
	// is not the pure inspection fixture above. Its oversized data must still
	// settle explicitly and preserve the exact opened resource.
	if opened.State != control.Failed || opened.Fault == nil || len(opened.Output) != 0 || len(opened.ResourceIDs) != 1 {
		t.Fatalf("large read falsely successful or lost resource: %+v", opened)
	}
	id := opened.ResourceIDs[0]
	snap, err := a.ws.LiveDocuments.Snapshot(id)
	if err != nil {
		t.Fatal(err)
	}
	target := control.Target{ResourceID: id}
	input := coreprotocol.DocumentEditRequest{ID: id, Revision: snap.Revision, Group: "hardening", Edits: []document.Edit{{Start: 0, End: 1, Text: "b"}}}
	edit := execute("large-edit", "document.edit", target, input)
	if edit.Fault != nil {
		t.Fatal(edit)
	}
	save := coreprotocol.DocumentRevisionRequest{ID: id, Revision: snap.Revision + 1}
	changed := execute("large-save", "document.save", target, save)
	if changed.State != control.Partial || changed.Fault == nil || len(changed.Output) != 0 || len(changed.ResourceIDs) != 1 || changed.ResourceIDs[0] != id {
		t.Fatalf("committed mutation evidence lost: %+v", changed)
	}
	after, err := a.ws.LiveDocuments.Snapshot(id)
	if err != nil || after.Revision != snap.Revision+1 || !strings.HasPrefix(string(after.Text), "b") || after.Dirty {
		t.Fatal("mutation did not commit", err)
	}
	replayed := execute("large-save", "document.save", target, save)
	if !reflect.DeepEqual(changed, replayed) {
		t.Fatalf("receipt changed: %+v / %+v", changed, replayed)
	}
	final, _ := a.ws.LiveDocuments.Snapshot(id)
	if final.Revision != after.Revision {
		t.Fatal("receipt observation repeated mutation")
	}
}

func TestControlShellOversizeReplyCompletesOriginalWaiter(t *testing.T) {
	r := transportRuntime(t)
	c, err := r.Connect("hardening-shell")
	if err != nil {
		t.Fatal(err)
	}
	c.mu.Lock()
	c.attached["ws"], c.controlRegistered["ws"] = &actor{}, true
	c.mu.Unlock()
	t.Cleanup(func() { c.mu.Lock(); delete(c.attached, "ws"); c.mu.Unlock() })
	for _, mutation := range []bool{false, true} {
		e := shellEnvelope(NewClientID(), "ws", c.Owner)
		if mutation {
			e.Descriptor.Effect = control.Write
		}
		done := make(chan control.Result, 1)
		go func() {
			out, err := r.RequestShellControl(t.Context(), "ws", *e.Request.Target.Shell, e)
			if err != nil {
				t.Error(err)
			}
			done <- out
		}()
		<-c.Controls()
		result := control.Result{RequestID: e.Request.ID, OperationID: "committed-operation", State: control.Applied, ResourceIDs: []string{"retained-resource"}, Output: json.RawMessage(`"` + strings.Repeat("x", control.MaxValueBytes) + `"`)}
		raw, _ := coreprotocol.Marshal(&result)
		reply := c.controlMethod(coreprotocol.Request{ID: NewClientID(), Owner: c.Owner, WorkspaceID: "ws", Method: coreprotocol.ControlShellReply, Payload: raw})
		if reply.Error != nil {
			t.Fatal(reply.Error)
		}
		if mutation {
			duplicate := c.controlMethod(coreprotocol.Request{ID: NewClientID(), Owner: c.Owner, WorkspaceID: "ws", Method: coreprotocol.ControlShellReply, Payload: raw})
			if duplicate.Error != nil {
				t.Fatal("exact rejected-output acknowledgement is not idempotent", duplicate.Error)
			}
			result.Output = json.RawMessage(`"different oversized ` + strings.Repeat("x", control.MaxValueBytes) + `"`)
			changed, _ := coreprotocol.Marshal(&result)
			duplicate = c.controlMethod(coreprotocol.Request{ID: NewClientID(), Owner: c.Owner, WorkspaceID: "ws", Method: coreprotocol.ControlShellReply, Payload: changed})
			if duplicate.Error == nil {
				t.Fatal("changed invalid output rewrote immutable receipt")
			}
		}
		select {
		case out := <-done:
			want := control.Failed
			if mutation {
				want = control.Partial
			}
			if out.State != want || out.Fault == nil || out.OperationID != result.OperationID || len(out.ResourceIDs) != 1 || coreprotocol.ValidateControlResult(out) != nil {
				t.Fatal(out)
			}
		case <-t.Context().Done():
			t.Fatal("invalid shell output stranded original waiter")
		}
	}
}
