package core

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"crude/coreprotocol"
	"crude/corewire"
	"crude/types/inspect"
	"crude/workspace"
)

func diagnosticRead(t *testing.T, r *Runtime, c *Connection, store *inspect.Store, ws, view string) inspect.Page {
	t.Helper()
	p, err := r.InspectDiagnostics(t.Context(), store, c.Owner, ws, inspect.Request{View: view, Limit: 64, MaxBytes: inspect.MaxBytes})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestRuntimeDiagnosticsPagesPrivacyAndOwner(t *testing.T) {
	r := transportRuntime(t)
	var caller *Connection
	for i := range 140 {
		c, err := r.Connect(fmt.Sprintf("client-%03d", i))
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			caller = c
		}
	}
	caller.secret = "NEVER-RESUME-SECRET"
	store := &inspect.Store{}
	p, err := r.InspectDiagnostics(t.Context(), store, caller.Owner, "", inspect.Request{View: "clients"})
	if err != nil {
		t.Fatal(err)
	}
	if p.Returned > 12 || p.Total != 140 || p.Next == nil {
		t.Fatalf("bad first page %+v", p)
	}
	seen := map[string]bool{}
	for {
		raw, _ := json.Marshal(p)
		if len(raw) > inspect.DefaultBytes || strings.Contains(string(raw), "NEVER-") {
			t.Fatal("privacy/page byte bound failed")
		}
		for _, v := range p.Records {
			if seen[v.ID] {
				t.Fatal("duplicate page record")
			}
			seen[v.ID] = true
		}
		if p.Next == nil {
			break
		}
		p, err = r.InspectDiagnostics(t.Context(), store, caller.Owner, "", *p.Next)
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(seen) != 140 {
		t.Fatal(len(seen))
	}
	if _, err = r.InspectDiagnostics(t.Context(), store, coreprotocol.Owner{ClientID: caller.Owner.ClientID, Epoch: caller.Owner.Epoch + 1}, "", inspect.Request{}); err == nil {
		t.Fatal("stale caller admitted")
	}
	if _, err = r.InspectDiagnostics(t.Context(), store, caller.Owner, "", inspect.Request{Limit: 65}); err == nil {
		t.Fatal("oversize page admitted")
	}
	if _, err = r.InspectDiagnostics(t.Context(), store, caller.Owner, "foreign", inspect.Request{}); err == nil {
		t.Fatal("foreign workspace admitted")
	}
}

func TestRuntimeDiagnosticsRequestsCapacityAndPrivacy(t *testing.T) {
	r := transportRuntime(t)
	c, _ := r.Connect("mine")
	other, _ := r.Connect("other")
	c.commands = map[string]*command{}
	for i := range sessionCommandLimit {
		done := make(chan struct{})
		if i%2 == 0 {
			close(done)
		}
		id := fmt.Sprintf("own-%02d", i)
		cmd := &command{request: coreprotocol.Request{ID: id, Method: coreprotocol.Snapshot, Payload: []byte("NEVER-PAYLOAD")}, done: done, admitted: time.Now().Add(-time.Second)}
		c.commands[commandKey(cmd.request)] = cmd
	}
	other.commands = map[string]*command{"foreign": {request: coreprotocol.Request{ID: "NEVER-FOREIGN-REQUEST", Payload: []byte("NEVER-RESULT")}, done: make(chan struct{})}}
	if _, err := c.admit(t.Context(), coreprotocol.Request{ID: "overflow", Method: coreprotocol.Snapshot}); err != errCommandCapacity {
		t.Fatalf("capacity %v", err)
	}
	store := &inspect.Store{}
	p := diagnosticRead(t, r, c, store, "", "clients")
	var mine clientDiagnostic
	for _, entry := range p.Records {
		if entry.ID == "mine" {
			if err := json.Unmarshal(entry.Value, &mine); err != nil {
				t.Fatal(err)
			}
		}
	}
	if mine.Commands != 16 || mine.Executing != 8 || mine.AwaitingFinalACK != 8 || mine.CommandCapacity != 16 {
		t.Fatalf("wrong capacity %+v", mine)
	}
	p = diagnosticRead(t, r, c, store, "", "requests")
	raw, _ := json.Marshal(p)
	if p.Total != 16 || strings.Contains(string(raw), "NEVER-") {
		t.Fatalf("request privacy: %s", raw)
	}
	for _, cmd := range c.commands {
		c.forget(cmd)
		break
	}
	if c.diagnosticClient("").Commands != 15 {
		t.Fatal("ACK did not reduce pressure")
	}
	c.commandMu.Lock()
	c.commandBytes = sessionCommandBytes
	c.commandMu.Unlock()
	if _, err := c.admit(t.Context(), coreprotocol.Request{ID: "bytes", Method: coreprotocol.Snapshot, Payload: []byte{1}}); err != errCommandCapacity {
		t.Fatal("byte pressure not enforced")
	}
}

func TestRuntimeDiagnosticsCarrierLoss(t *testing.T) {
	r := transportRuntime(t)
	c, _ := r.Connect("carrier")
	a, b := net.Pipe()
	carrier := corewire.NewMux(a, corewire.CarrierLocal, true)
	peer := corewire.NewMux(b, corewire.CarrierLocal, false)
	t.Cleanup(func() { _ = carrier.Close(); _ = peer.Close() })
	c.bind(carrier, nil)
	if v := c.diagnosticClient(""); !v.CarrierPresent || v.Carrier != "local" || !v.GraceDeadline.IsZero() {
		t.Fatalf("live %+v", v)
	}
	c.unbind(carrier)
	v := c.diagnosticClient("")
	if v.CarrierPresent || v.GraceDeadline.Before(time.Now()) || r.ClientCount() != 1 {
		t.Fatalf("retention %+v", v)
	}
	c.bind(carrier, nil)
	if !c.diagnosticClient("").GraceDeadline.IsZero() {
		t.Fatal("resume retained old deadline")
	}
	// Avoid clean-close waiting on a peer control receipt in this direct fixture.
	c.unbind(carrier)
}

func TestRuntimeDiagnosticsStalledActorPartialAndScope(t *testing.T) {
	r := transportRuntime(t)
	c, _ := r.Connect("diagnostic")
	stalled := &actor{id: "a-stalled", commands: make(chan actorCommand, 128), stop: make(chan struct{})}
	healthy := &actor{id: "z-healthy", ws: &workspace.Workspace{}, commands: make(chan actorCommand, 128), stop: make(chan struct{}), revision: 9}
	stop, joined := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(joined)
		for {
			select {
			case cmd := <-healthy.commands:
				if cmd.inspect != nil {
					cmd.inspect()
				}
			case <-stop:
				return
			}
		}
	}()
	r.mu.Lock()
	r.workspaces[stalled.id] = stalled
	r.workspaces[healthy.id] = healthy
	r.mu.Unlock()
	t.Cleanup(func() {
		close(stop)
		<-joined
		r.mu.Lock()
		delete(r.workspaces, stalled.id)
		delete(r.workspaces, healthy.id)
		r.mu.Unlock()
	})
	store := &inspect.Store{}
	start := time.Now()
	p := diagnosticRead(t, r, c, store, "", "workspaces")
	if time.Since(start) > 500*time.Millisecond {
		t.Fatal("stalled actor monopolized diagnostics")
	}
	var source runtimeDiagnosticSource
	if err := json.Unmarshal(p.Source, &source); err != nil {
		t.Fatal(err)
	}
	if !source.Partial || p.Total != 2 {
		t.Fatalf("missing partial %+v", p)
	}
	for _, record := range p.Records {
		var v workspaceDiagnostic
		if err := json.Unmarshal(record.Value, &v); err != nil {
			t.Fatal(err)
		}
		if v.ID == healthy.id && (!v.Available || v.Revision != 9) {
			t.Fatalf("healthy lost %+v", v)
		}
		if v.ID == stalled.id && (v.Available || v.Unavailable != "actor_timeout") {
			t.Fatalf("timeout hidden %+v", v)
		}
	}
	c.mu.Lock()
	c.attached[healthy.id] = healthy
	c.mu.Unlock()
	t.Cleanup(func() { c.mu.Lock(); delete(c.attached, healthy.id); c.mu.Unlock() })
	p = diagnosticRead(t, r, c, store, healthy.id, "workspaces")
	if p.Total != 1 || p.Records[0].ID != healthy.id {
		t.Fatal("workspace scope escaped")
	}
	c.mu.Lock()
	delete(c.attached, healthy.id)
	c.mu.Unlock()
}

func TestRuntimeDiagnosticsSlowACKBesideHealthy(t *testing.T) {
	r, root := resourceRuntime(t)
	slow, ws := topicResourceConnection(t, r, root, "slow-viewer")
	fast, _ := topicResourceConnection(t, r, root, "healthy-viewer")
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	var enteredOnce sync.Once
	subSlow := slow.Subscribe(t.Context(), ws, coreprotocol.TopicStatus, "", func(coreprotocol.TopicHeader, []byte) error {
		enteredOnce.Do(func() { close(entered) })
		select {
		case <-release:
			return nil
		case <-t.Context().Done():
			return t.Context().Err()
		}
	})
	defer func() { unblock(); subSlow.Close() }()
	fastReceived := make(chan struct{}, 4)
	subFast := fast.Subscribe(t.Context(), ws, coreprotocol.TopicStatus, "", func(coreprotocol.TopicHeader, []byte) error { fastReceived <- struct{}{}; return nil })
	defer subFast.Close()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("slow viewer not reached")
	}
	select {
	case <-fastReceived:
	case <-time.After(time.Second):
		t.Fatal("healthy viewer blocked")
	}
	r.mu.Lock()
	owner := r.clients[fast.Owner().ClientID]
	a := r.workspaces[ws]
	r.mu.Unlock()
	if err := a.inspect(t.Context(), func() { a.revision++ }); err != nil {
		t.Fatal(err)
	}
	// Receiving a second publication proves the healthy viewer's first applied ACK
	// reached the core. The slow viewer still holds its first applied ACK.
	slot := a.states.slots[coreprotocol.TopicStatus]
	entry, _ := slot.hub.latest()
	var state coreprotocol.WorkspaceState
	if err := coreprotocol.Decode(entry.body, &state); err != nil {
		t.Fatal(err)
	}
	state.Status.Version++
	body, err := coreprotocol.Marshal(&state)
	if err != nil {
		t.Fatal(err)
	}
	slot.hub.publishState(body)
	select {
	case <-fastReceived:
	case <-time.After(time.Second):
		t.Fatal("healthy ACK did not release next publication")
	}
	p := diagnosticRead(t, r, owner, &inspect.Store{}, ws, "replication")
	var slowPending, fastAck bool
	for _, record := range p.Records {
		if record.Kind != "subscription" {
			continue
		}
		var v struct {
			Owner       coreprotocol.Owner `json:"owner"`
			Observation topicObservation   `json:"observation"`
		}
		if err := json.Unmarshal(record.Value, &v); err != nil {
			t.Fatal(err)
		}
		if v.Observation.Topic != coreprotocol.TopicStatus {
			continue
		}
		if v.Owner.ClientID == "slow-viewer" {
			slowPending = !v.Observation.PendingSince.IsZero() && v.Observation.Ack < v.Observation.Sent
		}
		if v.Owner.ClientID == "healthy-viewer" {
			fastAck = v.Observation.Ack > 0
		}
	}
	if !slowPending || !fastAck {
		t.Fatalf("wrong attribution slow=%v healthy=%v %+v", slowPending, fastAck, p)
	}
}

func TestRuntimeDiagnosticsTopicGenerationAndBounds(t *testing.T) {
	c := &Connection{}
	old, release := c.claimTopic(context.Background(), "ws/document/private/path")
	observeTopicSent(old, 7, "document_snapshot")
	release()
	next, done := c.claimTopic(context.Background(), "ws/document/private/path")
	defer done()
	observeTopicSent(next, 8, "")
	observeTopicAck(old, 8, 0)
	c.topicMu.Lock()
	value := *c.topicObservations["ws/document/private/path"]
	c.topicMu.Unlock()
	if value.Ack != 0 || value.Generation != 2 || strings.Contains(value.ResourceID, "private") {
		t.Fatalf("stale generation/privacy %+v", value)
	}
	observeTopicAck(next, 8, 0)
	var releases []func()
	for i := range diagnosticTopicLimit + 4 {
		_, release := c.claimTopic(context.Background(), fmt.Sprintf("ws/terminal/%d", i))
		releases = append(releases, release)
	}
	for _, release := range releases {
		release()
	}
	c.topicMu.Lock()
	defer c.topicMu.Unlock()
	if len(c.topicObservations) > diagnosticTopicLimit || c.topicObservationDropped == 0 {
		t.Fatal("observation capacity unbounded")
	}
}

func TestRuntimeDiagnosticsQueueAggregateAndHubCapacity(t *testing.T) {
	a, b := &actor{commands: make(chan actorCommand, 16)}, &actor{jobs: make(chan actorJobResult, 8)}
	baseline := sampleActorQueues()
	trackActorQueue(a, true)
	trackActorQueue(b, true)
	defer trackActorQueue(a, false)
	defer trackActorQueue(b, false)
	for range 13 {
		a.commands <- actorCommand{}
	}
	if total := sampleActorQueues(); total < baseline+13 {
		t.Fatal("idle actor masked backlog")
	}
	for range 5 {
		b.jobs <- actorJobResult{}
	}
	trackActorQueue(a, false)
	if sampleActorQueues() < baseline+5 {
		t.Fatal("sampler lost another actor's live queue")
	}
	trackActorQueue(b, false)
	var hub topicHub
	for range topicJournalEntries + 10 {
		hub.publish([]byte{1})
	}
	v := hub.observation(false)
	if v.Entries != 256 || v.EntryCapacity != 256 || v.Evictions != 10 || v.ByteCapacity != 8<<20 {
		t.Fatalf("hub capacity %+v", v)
	}
}
