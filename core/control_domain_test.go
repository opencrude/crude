package core

import (
	"context"
	"crude/workspace"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"crude/coreprotocol"
	"crude/types/control"
)

func TestControlCatalogNestedDTOProperties(t *testing.T) {
	for id, dto := range map[string]any{"terminal.create": workspace.TerminalCreateOpts{}, "terminal.input": coreprotocol.TerminalInputRequest{}, "files.search": coreprotocol.FileSearchRequest{}, "git.review": coreprotocol.GitCompareRequest{}, "git.stage_reviewed": coreprotocol.GitStageReviewedRequest{}, "git.discard_reviewed": coreprotocol.DiscardGitReviewedRequest{}, "history.plan": coreprotocol.HistoryPlanRequest{}, "history.apply": coreprotocol.HistoryApplyRequest{}} {
		d, ok := control.Lookup(id)
		if !ok {
			t.Fatal(id)
		}
		var got map[string]any
		if err := json.Unmarshal(d.Input.Document, &got); err != nil {
			t.Fatal(err)
		}
		want := control.StructSchema(reflect.TypeOf(dto))
		var check func(map[string]any, map[string]any)
		check = func(g, w map[string]any) {
			t.Helper()
			gp, _ := g["properties"].(map[string]any)
			wp, _ := w["properties"].(map[string]any)
			if len(gp) != len(wp) {
				t.Fatalf("%s properties differ: %v / %v", id, gp, wp)
			}
			for name, raw := range wp {
				gg, exists := gp[name].(map[string]any)
				if !exists {
					t.Fatalf("%s missing %s", id, name)
				}
				ww := raw.(map[string]any)
				if ww["type"] == "object" {
					check(gg, ww)
				}
				if ww["type"] == "array" {
					check(gg["items"].(map[string]any), ww["items"].(map[string]any))
				}
			}
		}
		check(got, want)
	}
}

func TestControlPublicationFailurePreservesCommittedEvidence(t *testing.T) {
	in := control.Request{ID: "publication"}
	mutations, publications := 0, 0
	run, apply, _ := wrapControlOperation(in, func(context.Context) coreprotocol.Response {
		mutations++
		return coreprotocol.Response{ID: in.ID, Payload: json.RawMessage(`{"id":"created-file"}`)}
	}, func(coreprotocol.Response) error { publications++; return errors.New("replica failed") })
	result := run(context.Background())
	result.ResourceIDs = []string{"created-file"}
	result = apply(result)
	if result.State != control.Partial || result.Fault == nil || result.Fault.Code != control.OperationFailed || len(result.Output) == 0 || len(result.ResourceIDs) != 1 {
		t.Fatalf("lost committed effect: %#v", result)
	}
	apply(result)
	run(context.Background())
	if mutations != 1 || publications != 1 {
		t.Fatal("publication failure replayed effects")
	}
}

func TestControlDomainFailsClosedBeforeOwnerEffects(t *testing.T) {
	a := &actor{}
	for _, in := range []control.Request{
		{ID: "missing-owner", CommandID: "document.edit", Input: json.RawMessage(`{"id":"one","revision":3,"edits":[]}`)},
		{ID: "wrong-resource", CommandID: "document.edit", Target: control.Target{ResourceID: "two"}, Input: json.RawMessage(`{"id":"one","revision":3,"edits":[]}`)},
	} {
		owner := coreprotocol.Owner{ClientID: "trusted", Epoch: 1}
		if in.ID == "missing-owner" {
			owner = coreprotocol.Owner{}
		}
		run, _, handled := a.prepareControlDomain(in, owner)
		if !handled {
			t.Fatal("missing route")
		}
		got := run(context.Background())
		if got.State != control.Failed || got.Fault.Code != control.Invalid || got.OperationID != in.ID {
			t.Fatalf("unsafe admission: %#v", got)
		}
	}
	if _, _, handled := a.prepareControlDomain(control.Request{CommandID: "core.call"}, coreprotocol.Owner{}); handled {
		t.Fatal("arbitrary RPC dispatched")
	}
}
