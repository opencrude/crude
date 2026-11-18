package coreprotocol

import (
	"encoding/json"
	"strings"
	"testing"

	"crude/types/control"
)

func TestControlTransportAcceptsLogicalResourceIdentitiesWithoutWideningRouting(t *testing.T) {
	d, ok := control.Lookup("shell.inspect")
	if !ok {
		t.Fatal("catalog command missing")
	}
	owner := control.ShellOwner{ClientID: "original-shell", Epoch: 1}
	e := ControlEnvelope{Request: control.Request{ID: "original", CommandID: d.ID, Caller: control.Caller{Kind: control.ExternalCaller, ClientID: "caller"}, Target: control.Target{WorkspaceID: "workspace", Shell: &owner, ResourceID: "floating/settings::actions/Apply agent 雪"}, Input: json.RawMessage(`{}`)}, Descriptor: d}
	if err := e.ValidateTransport("workspace", owner); err != nil {
		t.Fatal("valid logical Unicode/spaced control identity rejected", err)
	}
	for _, resource := range []string{"injected\ncontrol", strings.Repeat("x", control.MaxIDBytes+1)} {
		bad := e.Clone()
		bad.Request.Target.ResourceID = resource
		if err := bad.ValidateTransport("workspace", owner); err == nil {
			t.Fatal("invalid resource identity accepted")
		}
	}
	bad := e.Clone()
	bad.Request.ID = "routing id with space"
	if err := bad.ValidateTransport("workspace", owner); err == nil {
		t.Fatal("routing identity restriction was relaxed")
	}
	bad = e.Clone()
	bad.Request.Target.Shell.Epoch++
	if err := bad.ValidateTransport("workspace", owner); err == nil {
		t.Fatal("shell epoch ownership relaxed")
	}
}
