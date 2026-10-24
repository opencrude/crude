package coreclient

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"crude/coreprotocol"
	"crude/corewire"
	captypes "crude/types/capability"
	"crude/types/control"
)

func TestTypedOutcomeRetainsOriginalIdentityAndState(t *testing.T) {
	for _, state := range []string{"pending", "uncertain"} {
		t.Run(state, func(t *testing.T) {
			ids := make(chan string, 1)
			acks := make(chan uint64, 1)
			f := &fakeCore{t: t, handle: func(open coreprotocol.Open, _ []byte, s corewire.Stream) {
				ids <- open.ID
				_ = sendValues(s, &coreprotocol.Reply{Final: true, Fault: &coreprotocol.Fault{Code: "outcome_" + state, Message: "inspect original receipt"}})
				body, err := corewire.NewReader(s, 64).Next()
				var ack coreprotocol.TopicAck
				if err == nil {
					err = coreprotocol.Decode(body, &ack)
				}
				if err != nil {
					t.Error(err)
				}
				acks <- ack.Seq
				_ = s.CloseWrite()
			}}
			client := dial(t, f)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, err := client.CallWith(ctx, "ws", "mutation", &coreprotocol.Empty{}, nil, CallOptions{})
			id := <-ids
			var uncertain *UncertainError
			var fault *coreprotocol.Fault
			if !errors.As(err, &uncertain) || uncertain.RequestID != id || id == "" || uncertain.State != state || !errors.As(err, &fault) || fault.Code != "outcome_"+state {
				t.Fatalf("identity/state/cause lost: %#v %v", uncertain, err)
			}
			if ack := <-acks; ack != 1 {
				t.Fatal("typed fault not acknowledged", ack)
			}
		})
	}
}

func TestUndecodableFinalResultPreservesIdentityWithoutAck(t *testing.T) {
	acked := make(chan bool, 1)
	f := &fakeCore{t: t, handle: func(_ coreprotocol.Open, _ []byte, s corewire.Stream) {
		// Empty is not the ReceiptRequest tuple the caller expects.
		_ = sendValues(s, &coreprotocol.Reply{Final: true}, &coreprotocol.Empty{})
		_, err := corewire.NewReader(s, 64).Next()
		acked <- err == nil
	}}
	c := dial(t, f)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var out coreprotocol.ReceiptRequest
	_, err := c.CallWith(ctx, "ws", "effect", &coreprotocol.Empty{}, &out, CallOptions{ID: "decode-original"})
	var uncertain *UncertainError
	if !errors.As(err, &uncertain) || uncertain.RequestID != "decode-original" {
		t.Fatal("decode error lost uncertainty identity", err)
	}
	if <-acked {
		t.Fatal("undecodable result was acknowledged")
	}
}

func deliveryEnvelope(owner coreprotocol.Owner) coreprotocol.ControlEnvelope {
	shell := control.ShellOwner{ClientID: owner.ClientID, Epoch: owner.Epoch}
	schema := control.Schema{ID: "empty", Revision: 1, Document: json.RawMessage(`{}`)}
	return coreprotocol.ControlEnvelope{Request: control.Request{ID: "effect", CommandID: "tree.set_visible", Caller: control.Caller{Kind: control.ExternalCaller, ClientID: "caller"}, Target: control.Target{WorkspaceID: "ws", Shell: &shell}, Input: json.RawMessage(`{}`)}, Descriptor: control.Descriptor{ID: "tree.set_visible", Scope: control.Shell, Effect: control.Write, Input: schema, Output: schema, Availability: control.Availability{Available: true}}}
}

func TestExactControlDuplicateReconcilesReceiptWithoutEffect(t *testing.T) {
	c := &Client{owner: coreprotocol.Owner{ClientID: "shell", Epoch: 1}, controls: make(chan coreprotocol.ControlEnvelope, 8), controlReceived: make(map[string]*receivedControl)}
	e := deliveryEnvelope(c.owner)
	if err := c.receiveControl("ws", e); err != nil {
		t.Fatal(err)
	}
	<-c.controls // one consumer owns the effect
	if err := c.receiveControl("ws", e.Clone()); err != nil {
		t.Fatal("exact duplicate rejected", err)
	}
	c.controlReceived[e.Request.ID].reply = []byte("immutable reply")
	if err := c.receiveControl("ws", e.Clone()); err != nil {
		t.Fatal("replied duplicate rejected", err)
	}
	select {
	case <-c.controls:
		t.Fatal("duplicate shell effect")
	default:
	}
	e.Request.Input = json.RawMessage(`{"changed":true}`)
	if err := c.receiveControl("ws", e); err == nil {
		t.Fatal("changed identity accepted")
	}
}

func TestExactHostDuplicateReconcilesReceiptWithoutEffect(t *testing.T) {
	owner := control.ShellOwner{ClientID: "shell", Epoch: 1}
	c := &Client{owner: coreprotocol.Owner{ClientID: owner.ClientID, Epoch: owner.Epoch}, hosts: newHostInbox()}
	r := captypes.HostRequest{ID: "host", WorkspaceID: "ws", Operation: "connect", Actor: captypes.RuntimeActor{ID: "agent", RunID: "run", WorkspaceID: "ws", RuntimeEpoch: 1, TurnGeneration: 1, ShellOwner: &owner}, Connection: captypes.ConnectionConfig{Host: "shell", Transport: "stdio", Directory: t.TempDir()}}
	r.Connection.SecretRefs = map[string]string{"A": "env:A", "B": "env:B", "C": "env:C"}
	if err := c.receiveCapabilityHost("ws", r); err != nil {
		t.Fatal(err)
	}
	delivered := <-c.hosts.requests
	delivered.Connection.SecretRefs["A"] = "consumer-owned"
	for i := 0; i < 16; i++ {
		if err := c.receiveCapabilityHost("ws", r); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case <-c.hosts.requests:
		t.Fatal("duplicate host effect")
	default:
	}
	r.Connection.Command = "changed"
	if err := c.receiveCapabilityHost("ws", r); err == nil {
		t.Fatal("changed host identity accepted")
	}
}
