package core

import (
	"bytes"
	"context"
	"crude/coreprotocol"
	"crude/corewire"
	"testing"
	"time"
)

// Every invocation has a finished producer and its latest unsent progress.
// Both branches are ready together: final may not overtake that pending note.
type progressOrderStream struct {
	messages []coreprotocol.Reply
	ack      bytes.Reader
	final    chan struct{}
}

func (s *progressOrderStream) Read(p []byte) (int, error) { <-s.final; return s.ack.Read(p) }
func (s *progressOrderStream) Write(p []byte) (int, error) {
	data, err := corewire.NewReader(bytes.NewReader(p), corewire.MaxMessage).Next()
	if err != nil {
		return 0, err
	}
	var reply coreprotocol.Reply
	if _, err = coreprotocol.DecodePrefix(data, &reply); err != nil {
		return 0, err
	}
	s.messages = append(s.messages, reply)
	if reply.Final {
		close(s.final)
	}
	return len(p), nil
}
func (*progressOrderStream) CloseWrite() error     { return nil }
func (*progressOrderStream) Close() error          { return nil }
func (*progressOrderStream) Class() corewire.Class { return corewire.ClassInteractive }
func TestPendingProgressPrecedesFinal(t *testing.T) {
	var wire bytes.Buffer
	raw, err := (&coreprotocol.TopicAck{Seq: 1}).MarshalMsg(nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = corewire.WriteMessage(&wire, raw); err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 64; attempt++ {
		c := &Connection{Owner: coreprotocol.Owner{ClientID: "probe", Epoch: 1}, done: make(chan struct{})}
		open := coreprotocol.Open{ID: "request", WorkspaceID: "workspace", Method: coreprotocol.FilesDelete}
		request := coreprotocol.Request{ID: open.ID, Owner: c.Owner, WorkspaceID: open.WorkspaceID, Method: open.Method}
		cmd := &command{request: request, enqueued: make(chan struct{}), done: make(chan struct{}), progress: make(chan string, 1), response: coreprotocol.Response{Revision: 42}}
		close(cmd.enqueued)
		cmd.progress <- "Deleting"
		close(cmd.done)
		c.commands = map[string]*command{commandKey(request): cmd}
		stream := &progressOrderStream{final: make(chan struct{})}
		stream.ack.Reset(wire.Bytes())
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		c.serveCommand(ctx, stream, corewire.NewReader(stream, 256), open, nil)
		cancel()
		if len(stream.messages) != 3 || !stream.messages[0].Admitted || stream.messages[1].Progress != "Deleting" || !stream.messages[2].Final || stream.messages[2].Revision != 42 {
			t.Fatalf("pending progress was lost or overtaken before final: %+v", stream.messages)
		}
		if len(c.commands) != 0 {
			t.Fatal("acknowledged final was not released")
		}
	}
}
