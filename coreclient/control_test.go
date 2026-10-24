package coreclient

import (
	"encoding/json"
	"testing"
	"time"

	"crude/coreprotocol"
	"crude/corewire"
	"crude/types/control"
)

// Control requests arrive on the session topic, bound to this session's exact
// shell owner and workspace; a mismatched binding ends the session's client
// instead of being executed.
func TestControlPushBindingAndSeparateEvents(t *testing.T) {
	for _, mismatch := range []string{"", "workspace", "epoch"} {
		t.Run(mismatch, func(t *testing.T) {
			f := &fakeCore{t: t, handle: func(coreprotocol.Open, []byte, corewire.Stream) {}}
			f.session = func(s corewire.Stream) {
				owner := control.ShellOwner{ClientID: "test-client", Epoch: 7}
				workspace := "ws"
				switch mismatch {
				case "workspace":
					workspace = "other"
				case "epoch":
					owner.Epoch++
				}
				schema := control.Schema{ID: "empty", Revision: 1, Document: json.RawMessage(`{}`)}
				e := coreprotocol.ControlEnvelope{Request: control.Request{ID: "inspect", CommandID: "inspect", Caller: control.Caller{Kind: control.ExternalCaller, ClientID: "caller"}, Target: control.Target{WorkspaceID: "ws", Shell: &owner}, Input: json.RawMessage(`{}`)}, Descriptor: control.Descriptor{ID: "inspect", Scope: control.Shell, Effect: control.Read, Input: schema, Output: schema, Availability: control.Availability{Available: true}}}
				_ = sendValues(s, &coreprotocol.TopicHeader{Seq: 1}, &coreprotocol.SessionEvent{WorkspaceID: workspace, Control: &e})
			}
			c := dial(t, f)
			if mismatch != "" {
				select {
				case <-c.Done():
				case <-time.After(5 * time.Second):
					t.Fatal("invalid binding accepted")
				}
				return
			}
			select {
			case got := <-c.ControlRequests():
				if got.Request.ID != "inspect" {
					t.Fatal(got)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("control request not delivered")
			}
			select {
			case <-c.PrivateEffects():
				t.Fatal("control entered private effects")
			default:
			}
		})
	}
}
