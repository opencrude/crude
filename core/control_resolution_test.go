package core

import (
	"encoding/json"
	"testing"

	"crude/coreprotocol"
	"crude/types/control"
)

func TestRepeatedControlResolutionOwnsDistinctReadReplies(t *testing.T) {
	r := transportRuntime(t)
	c, err := r.Connect("resolution-shell")
	if err != nil {
		t.Fatal(err)
	}
	c.mu.Lock()
	c.attached["ws"] = &actor{}
	c.controlRegistered["ws"] = true
	c.mu.Unlock()
	t.Cleanup(func() {
		c.mu.Lock()
		delete(c.attached, "ws")
		c.mu.Unlock()
	})
	a := &actor{id: "ws", runtime: r}
	request := shellEnvelope("original-mutation", "ws", c.Owner).Request
	request.CommandID = "shell.control"
	request.Input = json.RawMessage(`{"component_id":"field","operation":{"verb":"set_value","value":"8"},"expected_action":"tree.set_visible"}`)
	descriptor, _ := control.Lookup("tree.set_visible")
	seen := make(map[string]bool)
	for range 2 {
		ready := make(chan controlResolveResult, 1)
		a.resolveControlAsync(&actorControlCommand{request: &request, resolve: ready, ctx: t.Context()}, descriptor)
		var envelope coreprotocol.ControlEnvelope
		select {
		case envelope = <-c.Controls():
		case <-t.Context().Done():
			t.Fatal(t.Context().Err())
		}
		if seen[envelope.Request.ID] || envelope.Request.ID == request.ID {
			t.Fatalf("independent contextual resolution reused reply identity %q", envelope.Request.ID)
		}
		seen[envelope.Request.ID] = true
		output, _ := json.Marshal(map[string]any{"descriptor": descriptor})
		payload, _ := json.Marshal(control.Result{RequestID: envelope.Request.ID, OperationID: envelope.Request.ID, State: control.Applied, Output: output})
		response := c.controlMethod(coreprotocol.Request{ID: NewClientID(), Owner: c.Owner, WorkspaceID: "ws", Method: coreprotocol.ControlShellReply, Payload: payload})
		if response.Error != nil {
			t.Fatal(response.Error)
		}
		if resolved := <-ready; resolved.err != nil || resolved.descriptor.Effect != control.Write {
			t.Fatalf("resolution=%+v", resolved)
		}
	}
	if request.ID != "original-mutation" {
		t.Fatal("resolution changed original mutation/receipt identity")
	}
}
