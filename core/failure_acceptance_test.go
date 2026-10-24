package core

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"crude/buildinfo"
	"crude/coreclient"
	"crude/coreprotocol"
	"crude/corewire"
	"crude/types"
	"crude/workspace"
)

func failureRuntime(t *testing.T) *Runtime {
	t.Helper()
	setCoreTestHome(t, t.TempDir())
	r, err := New(func(root string) (*workspace.Workspace, error) {
		ws := workspace.NewWorkspace(root, "", "tester", types.Settings{}, coreTestLogger{})
		ws.SetAppDefaults(types.AppSettings{LocalHistory: types.LocalHistorySettings{Enabled: "off"}})
		return ws, nil
	}, filepath.Join(t.TempDir(), "receipts.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := r.Close(); err != nil {
			t.Error(err)
		}
	})
	return r
}

// rawWire speaks the protocol by hand: a carrier, a session handshake and
// command streams the test controls exactly.
type rawWire struct {
	conn  *corewire.Mux
	raw   net.Conn
	owner coreprotocol.Owner
}

func failureWire(t *testing.T, r *Runtime, id string) (*rawWire, coreprotocol.Owner, <-chan error) {
	t.Helper()
	peer, server := net.Pipe()
	done := make(chan error, 1)
	go func() { done <- ServeConnection(t.Context(), r, server) }()
	conn := corewire.NewMux(peer, corewire.CarrierLocal, true)
	t.Cleanup(func() { _ = conn.Close() })
	control, err := conn.OpenStream(t.Context(), corewire.ClassControl)
	if err != nil {
		t.Fatal(err)
	}
	hello := coreprotocol.Hello{Schema: coreprotocol.Schema, Build: buildinfo.ProtocolBuild, ClientID: id}
	body, _ := coreprotocol.Marshal(&hello)
	if err := corewire.WriteMessage(control, body); err != nil {
		t.Fatal(err)
	}
	reply, err := corewire.NewReader(control, 1<<16).Next()
	if err != nil {
		t.Fatal(err)
	}
	var welcome coreprotocol.Welcome
	if err := coreprotocol.Decode(reply, &welcome); err != nil || welcome.Fault != nil {
		t.Fatal(err, welcome.Fault)
	}
	return &rawWire{conn: conn, raw: peer, owner: welcome.Owner}, welcome.Owner, done
}

// send opens a command stream carrying exactly one request.
func (w *rawWire) send(t *testing.T, id, workspaceID, method string, payload []byte) corewire.Stream {
	t.Helper()
	s, err := w.conn.OpenStream(t.Context(), corewire.ClassInteractive)
	if err != nil {
		t.Fatal(err)
	}
	open := coreprotocol.Open{Kind: coreprotocol.StreamCommand, ID: id, WorkspaceID: workspaceID, Method: method}
	body, _ := coreprotocol.Marshal(&open)
	if err := corewire.WriteMessage(s, append(body, payload...)); err != nil {
		t.Fatal(err)
	}
	return s
}

// result reads a command's final reply.
func (w *rawWire) result(t *testing.T, s corewire.Stream) (coreprotocol.Reply, []byte) {
	t.Helper()
	reader := corewire.NewReader(s, corewire.MaxMessage)
	for {
		body, err := reader.Next()
		if err != nil {
			t.Fatal(err)
		}
		var reply coreprotocol.Reply
		rest, err := coreprotocol.DecodePrefix(body, &reply)
		if err != nil {
			t.Fatal(err)
		}
		if reply.Final {
			ack, _ := coreprotocol.Marshal(&coreprotocol.TopicAck{Seq: 1})
			if err := corewire.WriteMessage(s, ack); err != nil {
				t.Fatal(err)
			}
			_ = s.CloseWrite()
			_, _ = reader.Next()
			return reply, rest
		}
	}
}

func failureJoin(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("failed connection did not release its owner")
	}
}

// The client writes a real framed mutation, then deliberately never reads its
// response. Completion is established through the journal, not a timing sleep.
// Reconnect recovers the original identity and must not execute FilesCreate twice.
func TestFailureAcceptanceLostWireResponseRecoversReceiptWithoutReplay(t *testing.T) {
	r := failureRuntime(t)
	root := t.TempDir()
	f, owner, done := failureWire(t, r, "lost-response")
	_, payload := f.result(t, f.send(t, "attach", "", coreprotocol.Attach, mustTestPayload(coreprotocol.AttachRequest{Root: root})))
	var attached coreprotocol.AttachResult
	if err := coreprotocol.Decode(payload, &attached); err != nil {
		t.Fatal(err)
	}
	frame := coreprotocol.Request{ID: "lost-create", WorkspaceID: attached.WorkspaceID, Method: coreprotocol.FilesCreate}
	f.send(t, frame.ID, frame.WorkspaceID, frame.Method, mustTestPayload(coreprotocol.FilePathRequest{Path: "once.txt"}))
	var receipt coreprotocol.Receipt
	var err error
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		receipt, err = r.receipts.lookup(t.Context(), owner.ClientID, frame.ID)
		if err == nil && receipt.State == "completed" {
			break
		}
		select {
		case <-deadline.C:
			t.Fatalf("mutation never reached durable completion: %+v %v", receipt, err)
		case <-tick.C:
		}
	}
	if receipt.Response.Error != nil {
		t.Fatal(receipt.Response.Error)
	}
	if _, err := os.Stat(filepath.Join(root, "once.txt")); err != nil {
		t.Fatal("accepted effect missing:", err)
	}
	_ = f.raw.Close()
	failureJoin(t, done)
	peer, server := net.Pipe()
	go func() { _ = ServeConnection(t.Context(), r, server) }()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	c, err := coreclient.Dial(ctx, coreclient.SingleCarrier(peer), owner.ClientID)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if c.Owner().Epoch <= owner.Epoch {
		t.Fatal("reconnection did not advance owner epoch")
	}
	var recovered coreprotocol.Receipt
	if err := c.Call(ctx, "", coreprotocol.GetReceipt, &coreprotocol.ReceiptRequest{RequestID: frame.ID}, &recovered); err != nil {
		t.Fatal(err)
	}
	if recovered.State != "completed" || string(recovered.Response.Payload) != string(receipt.Response.Payload) {
		t.Fatalf("lost response not recovered exactly: %+v", recovered)
	}
	// Ordinary client.Call allocates a new identity, so use the retained original
	// request explicitly through the core connection to test deduplication.
	r.mu.Lock()
	reconnected := r.clients[owner.ClientID]
	r.mu.Unlock()
	callCore(t, reconnected, "reattach", "", coreprotocol.Attach, coreprotocol.AttachRequest{Root: root})
	replayed := callCore(t, reconnected, frame.ID, attached.WorkspaceID, frame.Method, coreprotocol.FilePathRequest{Path: "once.txt"})
	if string(replayed.Payload) != string(receipt.Response.Payload) {
		t.Fatal("repeat identity did not return immutable outcome")
	}
	// A repeated file create would fail or replace this newer content; receipt
	// recovery must preserve it, not merely return a successful-looking response.
	if err := os.WriteFile(filepath.Join(root, "once.txt"), []byte("later external content"), 0600); err != nil {
		t.Fatal(err)
	}
	callCore(t, reconnected, frame.ID, attached.WorkspaceID, frame.Method, coreprotocol.FilePathRequest{Path: "once.txt"})
	content, err := os.ReadFile(filepath.Join(root, "once.txt"))
	if err != nil || string(content) != "later external content" {
		t.Fatal("receipt recovery replayed the filesystem effect", err)
	}
}

// A peer that breaks framing or flow control loses its carrier; one that
// sends a malformed message loses only that stream. Neither affects any other
// session or the retained core.
func TestFailureAcceptanceMalformedPeersReleaseOnlyTheirCarrier(t *testing.T) {
	for _, kind := range []string{"unknown-frame", "oversized-frame", "credit-overrun", "malformed-message", "forged-resume"} {
		t.Run(kind, func(t *testing.T) {
			r := transportRuntime(t)
			keeper, err := r.Connect("unrelated-owner")
			if err != nil {
				t.Fatal(err)
			}
			defer keeper.Close()
			f, _, done := failureWire(t, r, "bad-peer")
			carrierEnds := true
			switch kind {
			case "unknown-frame":
				_, err = f.raw.Write([]byte{1, 0x7f, 0})
			case "oversized-frame":
				_, err = f.raw.Write([]byte{1, 2, 0xff, 0xff, 0x7f})
			case "credit-overrun":
				var frames []byte
				frames = append(frames, 0x63, 1, 1, byte(corewire.ClassBulk))
				chunk := make([]byte, 16<<10)
				for range 17 {
					frames = append(frames, 0x63, 2, 0x80, 0x80, 0x01)
					frames = append(frames, chunk...)
				}
				_, err = f.raw.Write(frames)
			case "malformed-message":
				carrierEnds = false
				s, openErr := f.conn.OpenStream(t.Context(), corewire.ClassInteractive)
				if openErr != nil {
					t.Fatal(openErr)
				}
				err = corewire.WriteMessage(s, []byte{0xc1, 0xc1})
				if _, readErr := corewire.NewReader(s, 1<<16).Next(); readErr == nil {
					t.Fatal("malformed message got a reply")
				}
			case "forged-resume":
				carrierEnds = false
				_, _, forgedDone := forgedResume(t, r, "bad-peer")
				failureJoin(t, forgedDone)
			}
			// A violating write may fail because the core already hung up.
			if err != nil && !carrierEnds {
				t.Fatal(err)
			}
			if carrierEnds {
				failureJoin(t, done)
			} else {
				reply, _ := f.result(t, f.send(t, "after", "", coreprotocol.CoreStatusMethod, emptyPayload))
				if reply.Fault != nil {
					t.Fatal("isolated failure broke its own session:", reply.Fault)
				}
			}
			response := keeper.Call(t.Context(), coreprotocol.Request{ID: "still-live", Owner: keeper.Owner, Method: coreprotocol.CoreStatusMethod, Payload: emptyPayload})
			if response.Error != nil {
				t.Fatal("malformed peer stopped retained core:", response.Error)
			}
		})
	}
}

// forgedResume tries to take over a session without its secret.
func forgedResume(t *testing.T, r *Runtime, id string) (*corewire.Mux, coreprotocol.Welcome, <-chan error) {
	t.Helper()
	peer, server := net.Pipe()
	done := make(chan error, 1)
	go func() { done <- ServeConnection(t.Context(), r, server) }()
	conn := corewire.NewMux(peer, corewire.CarrierLocal, true)
	t.Cleanup(func() { _ = conn.Close() })
	r.mu.Lock()
	session := r.clients[id]
	r.mu.Unlock()
	control, err := conn.OpenStream(t.Context(), corewire.ClassControl)
	if err != nil {
		t.Fatal(err)
	}
	hello := coreprotocol.Hello{Schema: coreprotocol.Schema, Build: buildinfo.ProtocolBuild, ClientID: id, Resume: &coreprotocol.SessionResume{SessionID: session.id, Secret: "guessed"}}
	body, _ := coreprotocol.Marshal(&hello)
	if err := corewire.WriteMessage(control, body); err != nil {
		t.Fatal(err)
	}
	reply, err := corewire.NewReader(control, 1<<16).Next()
	if err != nil {
		t.Fatal(err)
	}
	var welcome coreprotocol.Welcome
	if err := coreprotocol.Decode(reply, &welcome); err != nil || welcome.Fault == nil {
		t.Fatalf("forged resume admitted: %+v %v", welcome, err)
	}
	_ = conn.Close()
	return conn, welcome, done
}

func TestFailureAcceptanceEvictedTopicCursorResetsWithoutBlockingMutation(t *testing.T) {
	r := failureRuntime(t)
	wire, owner, _ := failureWire(t, r, "slow-reader")
	root := t.TempDir()
	var attached coreprotocol.AttachResult
	_, payload := wire.result(t, wire.send(t, "attach", "", coreprotocol.Attach, mustTestPayload(coreprotocol.AttachRequest{Root: root})))
	if err := coreprotocol.Decode(payload, &attached); err != nil {
		t.Fatal(err)
	}
	r.mu.Lock()
	c := r.clients[owner.ClientID]
	r.mu.Unlock()
	c.mu.Lock()
	a := c.attached[attached.WorkspaceID]
	c.mu.Unlock()
	if err := a.inspect(t.Context(), func() {
		for range topicJournalEntries + 10 {
			a.broadcast(coreprotocol.Change{Notices: []workspace.AgentNotice{{}}})
			if err := a.flushTopic(); err != nil {
				t.Error(err)
				return
			}
		}
	}); err != nil {
		t.Fatal(err)
	}
	callCore(t, c, "create-after-overflow", attached.WorkspaceID, coreprotocol.FilesCreate, coreprotocol.FilePathRequest{Path: "after-overflow.txt"})
	if _, err := os.Stat(filepath.Join(root, "after-overflow.txt")); err != nil {
		t.Fatal("slow observer blocked producer", err)
	}
	stream, err := wire.conn.OpenStream(t.Context(), corewire.ClassInteractive)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	open, _ := coreprotocol.Marshal(&coreprotocol.Open{Kind: coreprotocol.StreamTopic, Topic: coreprotocol.TopicWorkspace, WorkspaceID: attached.WorkspaceID, Since: 1})
	if err := corewire.WriteMessage(stream, open); err != nil {
		t.Fatal(err)
	}
	body, err := corewire.NewReader(stream, corewire.MaxMessage).Next()
	if err != nil {
		t.Fatal(err)
	}
	var header coreprotocol.TopicHeader
	rest, err := coreprotocol.DecodePrefix(body, &header)
	if err != nil {
		t.Fatal(err)
	}
	var update coreprotocol.WorkspaceUpdate
	if err := coreprotocol.Decode(rest, &update); err != nil {
		t.Fatal(err)
	}
	if !header.Reset || header.Seq <= topicJournalEntries || update.Snapshot == nil || update.Snapshot.ID != attached.WorkspaceID || update.Snapshot.Root != root {
		t.Fatal("evicted cursor did not recover exact authority through a real topic reset", header, update.Snapshot)
	}
	if len(c.privateEffects) != 0 {
		t.Fatal("domain mutation leaked to private session inbox")
	}
}
