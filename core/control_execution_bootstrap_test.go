package core

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"crude/coreprotocol"
	"crude/types/control"
	"crude/workspace"
	"crude/worktree"
)

// Both entry points must transport only the token, while the existing
// integration owner still commits the oversized full plan and safety evidence.
func TestControlExecutionBootstrap(t *testing.T) {
	for _, framed := range []bool{false, true} {
		t.Run(fmt.Sprintf("framed=%t", framed), func(t *testing.T) {
			a := controlExecutionFixture(t)
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			owner := coreprotocol.Owner{ClientID: "bootstrap", Epoch: 1}
			sequence := 0
			invoke := func(request control.Request) control.Result {
				run, _, ok := a.prepareControlDomain(request, owner)
				if !ok {
					t.Fatal("missing route", request.CommandID)
				}
				return run(ctx)
			}
			if framed {
				r, err := New(func(string) (*workspace.Workspace, error) { return a.ws, nil }, filepath.Join(t.TempDir(), "receipts.db"))
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = r.Close() })
				c := controlWireClient(t, r, "bootstrap")
				var attached coreprotocol.AttachResult
				if err := c.Call(ctx, "", coreprotocol.Attach, coreprotocol.AttachRequest{Root: a.ws.RootPath}, &attached); err != nil {
					t.Fatal(err)
				}
				if err := c.Call(ctx, attached.WorkspaceID, coreprotocol.ControlClientRegister, &coreprotocol.ControlClientGrant{Effects: []control.Effect{control.Read, control.Write, control.Execute}}, nil); err != nil {
					t.Fatal(err)
				}
				invoke = func(request control.Request) control.Result {
					request.Target.WorkspaceID = attached.WorkspaceID
					payload, err := coreprotocol.Marshal(coreprotocol.Ptr(request))
					if err != nil {
						t.Fatal(err)
					}
					response, err := c.CallRequest(ctx, coreprotocol.Request{ID: request.ID, Owner: c.Owner(), WorkspaceID: attached.WorkspaceID, Method: coreprotocol.ControlExecute, Payload: payload})
					if err != nil || response.Error != nil {
						t.Fatalf("framed %s: %v %v", request.CommandID, err, response.Error)
					}
					var result control.Result
					if err := coreprotocol.Decode(response.Payload, &result); err != nil {
						t.Fatal(err)
					}
					return result
				}
			}
			call := func(command, id string, input any) control.Result {
				t.Helper()
				sequence++
				raw, err := json.Marshal(input)
				if err != nil {
					t.Fatal(err)
				}
				return invoke(control.Request{ID: fmt.Sprintf("bootstrap-%d", sequence), CommandID: command, Target: control.Target{ResourceID: id}, Input: raw})
			}
			decode := func(result control.Result, output any) {
				t.Helper()
				if result.Fault != nil || result.State != control.Committed {
					t.Fatalf("not committed: %+v", result)
				}
				if err := json.Unmarshal(result.Output, output); err != nil {
					t.Fatal(err)
				}
			}
			put := func(root, text string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(root, "disk.txt"), []byte(text), 0644); err != nil {
					t.Fatal(err)
				}
			}
			assertDisk := func(want string) {
				t.Helper()
				data, err := os.ReadFile(filepath.Join(a.ws.RootPath, "disk.txt"))
				if err != nil || string(data) != want {
					t.Fatalf("destination changed: bytes=%d want=%d err=%v", len(data), len(want), err)
				}
			}
			var execution worktree.Context
			decode(call("execution.create", "", map[string]any{"mode": "worktree"}), &execution)
			large := strings.Repeat("child source\n", 6000)
			put(execution.Root, large)
			full, err := a.ws.Executions.Review(ctx, execution.ID)
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(full)
			if err != nil || len(encoded) <= control.MaxValueBytes {
				t.Fatalf("fixture is not oversized: %d %v", len(encoded), err)
			}
			reviewed := call("execution.review", execution.ID, map[string]any{"id": execution.ID})
			var token worktree.PlanDelta
			decode(reviewed, &token)
			if len(reviewed.Output) > 1024 || bytes.Contains(reviewed.Output, []byte(`"data"`)) || token.ContextID != execution.ID || token.Owner != execution.Owner || token.Digest != full.Digest || token.SourceDigest != full.SourceDigest || token.Digest == "" || token.SourceDigest == "" || len(token.Changes) != 0 {
				t.Fatal("review did not return the exact compact token")
			}
			t.Logf("full review=%d bytes; compact review=%d bytes", len(encoded), len(reviewed.Output))
			apply := func(plan worktree.PlanDelta) control.Result {
				return call("execution.apply", execution.ID, map[string]any{"plan": plan})
			}
			for _, change := range []func(*worktree.PlanDelta){
				func(p *worktree.PlanDelta) { p.Owner.RunID = "forged" },
				func(p *worktree.PlanDelta) { p.SourceDigest = "" },
				func(p *worktree.PlanDelta) { p.Changes = []worktree.Change{{Path: "disk.txt"}} },
				func(p *worktree.PlanDelta) { p.DiscardDigest = "not-integration" },
			} {
				bad := token
				change(&bad)
				if out := apply(bad); out.Fault == nil || out.Fault.Code != control.Invalid {
					t.Fatalf("invalid token admitted: %+v", out)
				}
			}
			stale := func(plan worktree.PlanDelta) {
				t.Helper()
				if out := apply(plan); out.State != control.Failed || out.Fault == nil || out.Fault.Code != control.Stale {
					t.Fatalf("stale review admitted: %+v", out)
				}
			}
			bad := token
			bad.Digest = "changed"
			stale(bad)
			bad = token
			bad.SourceDigest = "changed"
			stale(bad)
			put(execution.Root, "newer child source\n")
			stale(token)
			assertDisk("human dirty\n")
			put(execution.Root, large)
			put(a.ws.RootPath, "newer human destination\n")
			stale(token)
			assertDisk("newer human destination\n")
			put(a.ws.RootPath, "human dirty\n")
			var receipt worktree.Receipt
			applied := apply(token)
			decode(applied, &receipt)
			if receipt.PlanDigest != token.Digest || receipt.OperationID == "" || receipt.SafetySnapshotID == "" {
				t.Fatal("missing authoritative safety receipt", receipt)
			}
			assertDisk(large)
			status, err := a.ws.Executions.Integration(receipt.OperationID)
			if err != nil || status.State != "committed" || len(status.Plan.Changes) != 1 || !bytes.Equal(status.Plan.Changes[0].After.Data, []byte(large)) {
				t.Fatal("full plan not retained by existing integration owner", err)
			}
			// Receipt lookup precedes both source and destination preimages, and
			// must not overwrite subsequent intentional work or allocate a new op.
			put(execution.Root, "later source\n")
			put(a.ws.RootPath, "later destination\n")
			duplicate := apply(token)
			var recovered worktree.Receipt
			decode(duplicate, &recovered)
			if recovered != receipt || !bytes.Equal(duplicate.Output, applied.Output) {
				t.Fatal("lost reply did not recover the exact committed receipt")
			}
			assertDisk("later destination\n")
			statuses, err := a.ws.Executions.Integrations()
			if err != nil || len(statuses) != 1 {
				t.Fatal("receipt lookup created another integration", len(statuses), err)
			}
			// Model an interrupted journal in this private fixture. Unknown
			// effects need explicit recovery, even with stale current preimages.
			if _, err := a.ws.WorkspaceDB.DB.ExecContext(ctx, `UPDATE workspace_integrations SET payload=json_set(payload,'$.state','prepared') WHERE id=?`, receipt.OperationID); err != nil {
				t.Fatal(err)
			}
			uncertain := apply(token)
			if uncertain.State != control.Uncertain || uncertain.Fault == nil || uncertain.Fault.Code != control.UnknownOutcome || !strings.Contains(uncertain.Fault.Message, "explicit recovery") {
				t.Fatalf("uncertain effect was re-reviewed/replayed: %+v", uncertain)
			}
			assertDisk("later destination\n")
		})
	}
}
