package coreclient

import (
	"encoding/json"
	"testing"

	"crude/coreprotocol"
	"crude/corewire"
	"crude/types/control"
)

func TestControlPushBindingAndSeparateEvents(t *testing.T) {
	for _, mismatch := range []string{"", "workspace", "request", "epoch"} {
		t.Run(mismatch, func(t *testing.T) {
			c, f := fakePeer(t)
			owner := control.ShellOwner{ClientID: c.Owner().ClientID, Epoch: c.Owner().Epoch}
			schema := control.Schema{ID: "empty", Revision: 1, Document: json.RawMessage(`{}`)}
			e := coreprotocol.ControlEnvelope{Request: control.Request{ID: "inspect", CommandID: "inspect", Caller: control.Caller{Kind: control.ExternalCaller, ClientID: "caller"}, Target: control.Target{WorkspaceID: "ws", Shell: &owner}, Input: json.RawMessage(`{}`)}, Descriptor: control.Descriptor{ID: "inspect", Scope: control.Shell, Effect: control.Read, Input: schema, Output: schema, Availability: control.Availability{Available: true}}}
			frame := corewire.Frame{Version: 1, Kind: corewire.KindEvent, Method: coreprotocol.ControlRequest, ClientID: c.Owner().ClientID, Epoch: c.Owner().Epoch, WorkspaceID: "ws", RequestID: "inspect"}
			switch mismatch {
			case "workspace":
				frame.WorkspaceID = "other"
			case "request":
				frame.RequestID = "other"
			case "epoch":
				owner.Epoch++
			}
			frame.Payload, _ = json.Marshal(e)
			if err := f.Write(frame); err != nil {
				t.Fatal(err)
			}
			if mismatch != "" {
				<-c.Done()
				if c.Err() == nil {
					t.Fatal("invalid binding accepted")
				}
				return
			}
			got := <-c.ControlRequests()
			if got.Request.ID != "inspect" {
				t.Fatal(got)
			}
			select {
			case <-c.Events():
				t.Fatal("control entered lossy events")
			default:
			}
		})
	}
}
