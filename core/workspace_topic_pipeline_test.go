package core

import (
	"errors"
	"testing"
	"time"

	"crude/core/protocol"
	"crude/core/wire"
	"crude/workspace"
)

type workspacePipelineFixture struct {
	wire *rawWire
	c    *Connection
	a    *actor
	root string
	id   string
}

func newWorkspacePipelineFixture(t *testing.T) workspacePipelineFixture {
	t.Helper()
	r := failureRuntime(t)
	w, owner, done := failureWire(t, r, "workspace-pipeline")
	t.Cleanup(func() {
		_ = w.conn.Close()
		failureJoin(t, done)
	})
	root := t.TempDir()
	_, body := w.result(t, w.send(t, "attach", "", coreprotocol.Attach, mustTestPayload(&coreprotocol.AttachRequest{Root: root})))
	var attached coreprotocol.AttachResult
	if err := coreprotocol.Decode(body, &attached); err != nil {
		t.Fatal(err)
	}
	r.mu.Lock()
	c := r.clients[owner.ClientID]
	r.mu.Unlock()
	c.mu.Lock()
	a := c.attached[attached.WorkspaceID]
	c.mu.Unlock()
	return workspacePipelineFixture{w, c, a, root, attached.WorkspaceID}
}

// Flush separate canonical publications without timing sleeps or test-only
// topic paths. Each publication carries a distinct body to check wire order.
func (f workspacePipelineFixture) publish(t *testing.T, start, count int) uint64 {
	t.Helper()
	var seq uint64
	var flushErr error
	err := f.a.inspect(t.Context(), func() {
		for i := start; i < start+count; i++ {
			f.a.broadcast(coreprotocol.Change{Notices: []workspace.AgentNotice{{Type: workspace.NoticeMemorySaved, Count: i}}})
			if flushErr = f.a.flushTopic(); flushErr != nil {
				return
			}
		}
		seq = f.a.topic.hub.sequence()
	})
	if err != nil || flushErr != nil {
		t.Fatal(err, flushErr)
	}
	return seq
}

type workspacePipelineFrame struct {
	header coreprotocol.TopicHeader
	update coreprotocol.WorkspaceUpdate
	err    error
}

type workspacePipelinePeer struct {
	stream corewire.Stream
	frames <-chan workspacePipelineFrame
}

func (f workspacePipelineFixture) open(t *testing.T, since uint64) workspacePipelinePeer {
	t.Helper()
	s, err := f.wire.conn.OpenStream(t.Context(), corewire.ClassInteractive)
	if err != nil {
		t.Fatal(err)
	}
	if err := send(s, &coreprotocol.Open{Kind: coreprotocol.StreamTopic, Topic: coreprotocol.TopicWorkspace, WorkspaceID: f.id, Since: since}); err != nil {
		_ = s.Close()
		t.Fatal(err)
	}
	frames := make(chan workspacePipelineFrame)
	stopping, joined := make(chan struct{}), make(chan struct{})
	t.Cleanup(func() {
		close(stopping)
		_ = s.Close()
		<-joined
	})
	go func() {
		defer close(joined)
		reader := corewire.NewReader(s, corewire.MaxMessage)
		for {
			var frame workspacePipelineFrame
			body, err := reader.Next()
			if err == nil {
				body, err = coreprotocol.DecodePrefix(body, &frame.header)
				if err == nil {
					err = coreprotocol.Decode(body, &frame.update)
				}
			}
			frame.err = err
			select {
			case frames <- frame:
			case <-stopping:
				return
			}
			if err != nil {
				return
			}
		}
	}()
	return workspacePipelinePeer{s, frames}
}

func (p workspacePipelinePeer) next(t *testing.T) workspacePipelineFrame {
	t.Helper()
	select {
	case frame := <-p.frames:
		return frame
	case <-time.After(4 * time.Second):
		t.Fatal("workspace topic did not deliver the next frame without another ACK")
		return workspacePipelineFrame{}
	}
}

func (p workspacePipelinePeer) quiet(t *testing.T, until string) {
	t.Helper()
	select {
	case frame := <-p.frames:
		t.Fatalf("received seq %d (reset=%v, err=%v) before %s", frame.header.Seq, frame.header.Reset, frame.err, until)
	case <-time.After(100 * time.Millisecond):
	}
}

func (p workspacePipelinePeer) ack(t *testing.T, seq uint64) {
	t.Helper()
	if err := send(p.stream, &coreprotocol.TopicAck{Seq: seq, DisplayIntervalNS: int64(20 * time.Millisecond)}); err != nil {
		t.Fatal(err)
	}
}

func (p workspacePipelinePeer) delta(t *testing.T, seq uint64, notice int) {
	t.Helper()
	frame := p.next(t)
	if frame.err != nil || frame.header.Reset || frame.header.Seq != seq || frame.update.Snapshot != nil ||
		len(frame.update.Notices) != 1 || frame.update.Notices[0].Count != notice {
		t.Fatalf("want delta seq=%d notice=%d, got header=%+v notices=%v snapshot=%v error=%v",
			seq, notice, frame.header, frame.update.Notices, frame.update.Snapshot != nil, frame.err)
	}
}

func (f workspacePipelineFixture) reset(t *testing.T, p workspacePipelinePeer) uint64 {
	t.Helper()
	frame := p.next(t)
	snapshot := frame.update.Snapshot
	if frame.err != nil || !frame.header.Reset || snapshot == nil || snapshot.ID != f.id || snapshot.Root != f.root {
		t.Fatalf("want reset for %s, got header=%+v snapshot=%v error=%v", f.id, frame.header, snapshot, frame.err)
	}
	return frame.header.Seq
}

func (p workspacePipelinePeer) closed(t *testing.T) {
	t.Helper()
	frame := p.next(t)
	if !errors.Is(frame.err, corewire.ErrReset) {
		t.Fatalf("workspace topic was not retired: header=%+v error=%v", frame.header, frame.err)
	}
}

// A 64-frame flight covers 1.024 s at the actor's maximum publication cadence.
// Keep this independent of the writer's implementation: no additional frame
// may arrive until an ACK frees a slot.
const workspacePipelineExpectedFlight = 64

func TestWorkspaceTopicPipelineSendsOrderedFlightWithoutACKs(t *testing.T) {
	f := newWorkspacePipelineFixture(t)
	p := f.open(t, 0)
	base := f.reset(t, p)
	f.publish(t, 1, workspacePipelineExpectedFlight+2)
	p.quiet(t, "the initial snapshot ACK")
	p.ack(t, base)
	for i := 1; i <= workspacePipelineExpectedFlight; i++ {
		p.delta(t, base+uint64(i), i)
	}
	p.quiet(t, "the first delta ACK")
	p.ack(t, base+1)
	p.delta(t, base+workspacePipelineExpectedFlight+1, workspacePipelineExpectedFlight+1)
	p.quiet(t, "the second delta ACK")
	f.c.topicMu.Lock()
	observed := *f.c.topicObservations[f.id+"/workspace"]
	f.c.topicMu.Unlock()
	if observed.Ack != base+1 || observed.Sent != base+workspacePipelineExpectedFlight+1 ||
		observed.PendingSince.IsZero() || !observed.PendingSince.Before(observed.SentAt) ||
		observed.DisplayIntervalNS != int64(20*time.Millisecond) {
		t.Fatalf("diagnostics lost the partial ACK or oldest pending publication: %+v", observed)
	}
	p.ack(t, base+2)
	p.delta(t, base+workspacePipelineExpectedFlight+2, workspacePipelineExpectedFlight+2)
}

func TestWorkspaceTopicPipelineRejectsInexactACK(t *testing.T) {
	for _, tc := range []struct {
		name   string
		offset uint64
	}{
		{"duplicate", 0},
		{"skipped", 2},
		{"unsent", 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newWorkspacePipelineFixture(t)
			p := f.open(t, 0)
			base := f.reset(t, p)
			p.ack(t, base)
			f.publish(t, 1, 2)
			p.delta(t, base+1, 1)
			p.delta(t, base+2, 2)
			p.ack(t, base+tc.offset)
			p.closed(t)
		})
	}
}

func TestWorkspaceTopicPipelineGapDrainsThenResets(t *testing.T) {
	f := newWorkspacePipelineFixture(t)
	p := f.open(t, 0)
	base := f.reset(t, p)
	p.ack(t, base)
	f.publish(t, 1, workspacePipelineExpectedFlight)
	for i := 1; i <= workspacePipelineExpectedFlight; i++ {
		p.delta(t, base+uint64(i), i)
	}
	// Evict the immediate successor of the current send cursor while ACKs are held.
	count := topicJournalEntries + 1
	head := f.publish(t, workspacePipelineExpectedFlight+1, count)
	for i := 1; i < workspacePipelineExpectedFlight; i++ {
		p.ack(t, base+uint64(i))
	}
	p.quiet(t, "the last in-flight ACK")
	p.ack(t, base+workspacePipelineExpectedFlight)
	reset := f.reset(t, p)
	if reset != head {
		t.Fatalf("reset cursor %d, want canonical head %d", reset, head)
	}
	notice := workspacePipelineExpectedFlight + count + 1
	f.publish(t, notice, 1)
	p.quiet(t, "the gap reset ACK")
	p.ack(t, reset)
	p.delta(t, reset+1, notice)
}

func TestWorkspaceTopicPipelineReplacementFencesOldOwner(t *testing.T) {
	f := newWorkspacePipelineFixture(t)
	f.publish(t, 1, 1) // Ensure the applied reset cursor is nonzero.
	old := f.open(t, 0)
	base := f.reset(t, old)
	old.ack(t, base)
	f.publish(t, 2, 2)
	old.delta(t, base+1, 2)
	old.delta(t, base+2, 3)
	// The replacement resumes from its own applied cursor, not the retired
	// writer's send cursor. Both frames were sent but neither was applied.
	replacement := f.open(t, base)
	replacement.delta(t, base+1, 2)
	replacement.delta(t, base+2, 3)
	old.closed(t)
	replacement.ack(t, base+1)
	reply, _ := f.wire.result(t, f.wire.send(t, "detach", f.id, coreprotocol.Detach, mustTestPayload(&coreprotocol.Empty{})))
	if reply.Fault != nil {
		t.Fatalf("workspace detach: %v", reply.Fault)
	}
	replacement.closed(t)
	f.open(t, base+1).closed(t)
}
