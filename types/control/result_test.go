package control

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBoundResultPreservesEffectEvidence(t *testing.T) {
	request := Request{ID: "original"}
	for _, mutation := range []bool{false, true} {
		for _, output := range []json.RawMessage{json.RawMessage(`{"unfinished":`), json.RawMessage(`"` + strings.Repeat("x", MaxValueBytes) + `"`)} {
			in := Result{RequestID: request.ID, OperationID: "committed-operation", ResourceIDs: []string{"retained-resource"}, State: Committed, Output: output}
			out := BoundResult(request, in, mutation)
			want := Failed
			if mutation {
				want = Partial
			}
			if out.State != want || out.OperationID != in.OperationID || len(out.ResourceIDs) != 1 || out.ResourceIDs[0] != "retained-resource" || out.Fault == nil || len(out.Output) != 0 || out.Validate() != nil {
				t.Fatalf("invalid result lost evidence: %+v", out)
			}
		}
	}
}

func TestBoundResultValidAndMalformedMetadata(t *testing.T) {
	request := Request{ID: "original"}
	valid := Result{RequestID: request.ID, OperationID: "operation", State: Applied, Output: json.RawMessage(`{"ok":true}`)}
	if out := BoundResult(request, valid, true); string(out.Output) != string(valid.Output) || out.State != Applied || out.Fault != nil {
		t.Fatal(out)
	}
	valid.RequestID = "foreign"
	valid.ResourceIDs = []string{"bad\nidentity", "valid-resource"}
	out := BoundResult(request, valid, true)
	if out.RequestID != request.ID || out.State != Partial || len(out.ResourceIDs) != 1 || out.Validate() != nil {
		t.Fatal(out)
	}
}
