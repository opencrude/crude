package core

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"crude/coreclient"
	"crude/coreprotocol"
	"crude/corewire"
	"crude/types"
	"crude/workspace"
)

func failureRuntime(t *testing.T) *Runtime {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
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

type failureFramer struct {
	*corewire.Framer
	raw net.Conn
}

func failureWire(t *testing.T, r *Runtime, id string) (*failureFramer, coreprotocol.Owner, <-chan error) {
	t.Helper()
	peer, server := net.Pipe()
	done := make(chan error, 1)
	go func() { done <- ServeConnection(t.Context(), r, server) }()
	f := corewire.NewFramer(t.Context(), peer)
	t.Cleanup(func() { _ = f.Close() })
	if _, err := f.NegotiateClient(id, corewire.MaxFrameSize); err != nil {
		t.Fatal(err)
	}
	ready, err := f.Read()
	if err != nil {
		t.Fatal(err)
	}
	var owner coreprotocol.Owner
	if err := json.Unmarshal(ready.Payload, &owner); err != nil {
		t.Fatal(err)
	}
	return &failureFramer{Framer: f, raw: peer}, owner, done
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
	raw, _ := json.Marshal(coreprotocol.AttachRequest{Root: root})
	if err := f.Write(corewire.Frame{Version: 1, Kind: corewire.KindRequest, RequestID: "attach", ClientID: owner.ClientID, Epoch: owner.Epoch, Method: coreprotocol.Attach, Payload: raw}); err != nil {
		t.Fatal(err)
	}
	attachedFrame, err := f.Read()
	if err != nil {
		t.Fatal(err)
	}
	var attached coreprotocol.AttachResult
	if err := json.Unmarshal(attachedFrame.Payload, &attached); err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(coreprotocol.FilePathRequest{Path: "once.txt"})
	frame := corewire.Frame{Version: 1, Kind: corewire.KindRequest, RequestID: "lost-create", ClientID: owner.ClientID, Epoch: owner.Epoch, WorkspaceID: attached.WorkspaceID, Method: coreprotocol.FilesCreate, Payload: raw}
	if err := f.Write(frame); err != nil {
		t.Fatal(err)
	}
	var receipt coreprotocol.Receipt
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		receipt, err = r.receipts.lookup(t.Context(), owner.ClientID, frame.RequestID)
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
	_ = f.Close()
	failureJoin(t, done)
	peer, server := net.Pipe()
	go func() { _ = ServeConnection(t.Context(), r, server) }()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	c, err := coreclient.Dial(ctx, peer, owner.ClientID)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if c.Owner().Epoch <= owner.Epoch {
		t.Fatal("reconnection did not advance owner epoch")
	}
	var recovered coreprotocol.Receipt
	if err := c.Call(ctx, "", coreprotocol.GetReceipt, coreprotocol.ReceiptRequest{RequestID: frame.RequestID}, &recovered); err != nil {
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
	replayed := callCore(t, reconnected, frame.RequestID, attached.WorkspaceID, frame.Method, coreprotocol.FilePathRequest{Path: "once.txt"})
	if string(replayed.Payload) != string(receipt.Response.Payload) {
		t.Fatal("repeat identity did not return immutable outcome")
	}
	// A repeated file create would fail or replace this newer content; receipt
	// recovery must preserve it, not merely return a successful-looking response.
	if err := os.WriteFile(filepath.Join(root, "once.txt"), []byte("later external content"), 0600); err != nil {
		t.Fatal(err)
	}
	callCore(t, reconnected, frame.RequestID, attached.WorkspaceID, frame.Method, coreprotocol.FilePathRequest{Path: "once.txt"})
	content, err := os.ReadFile(filepath.Join(root, "once.txt"))
	if err != nil || string(content) != "later external content" {
		t.Fatal("receipt recovery replayed the filesystem effect", err)
	}
}

func TestFailureAcceptanceMalformedAndForeignOwnerFramesReleaseOnlyTheirConnection(t *testing.T) {
	for _, kind := range []string{"zero-length", "oversized", "malformed-json", "duplicate-key", "foreign-client", "stale-epoch"} {
		t.Run(kind, func(t *testing.T) {
			r := transportRuntime(t)
			keeper, err := r.Connect("unrelated-owner")
			if err != nil {
				t.Fatal(err)
			}
			defer keeper.Close()
			f, owner, done := failureWire(t, r, "bad-frame")
			switch kind {
			case "foreign-client", "stale-epoch":
				if kind == "foreign-client" {
					owner.ClientID = keeper.Owner.ClientID
				} else {
					owner.Epoch++
				}
				err = f.Write(corewire.Frame{Version: 1, Kind: corewire.KindRequest, RequestID: "bad", ClientID: owner.ClientID, Epoch: owner.Epoch, Method: coreprotocol.CoreStatusMethod, Payload: json.RawMessage(`{}`)})
			default:
				var payload []byte
				var size uint32
				switch kind {
				case "oversized":
					size = corewire.MaxFrameSize + 1
				case "malformed-json":
					payload = []byte(`{"version":`)
				case "duplicate-key":
					payload = []byte(`{"version":1,"version":1,"kind":"request"}`)
				}
				if len(payload) > 0 {
					size = uint32(len(payload))
				}
				var header [4]byte
				binary.BigEndian.PutUint32(header[:], size)
				_, err = f.raw.Write(append(header[:], payload...))
			}
			if err != nil {
				t.Fatal(err)
			}
			failureJoin(t, done)
			if r.ClientCount() != 1 {
				t.Fatal("malformed peer retained or detached unrelated owner")
			}
			response := keeper.Call(t.Context(), coreprotocol.Request{ID: "still-live", Owner: keeper.Owner, Method: coreprotocol.CoreStatusMethod, Payload: json.RawMessage(`{}`)})
			if response.Error != nil {
				t.Fatal("malformed peer stopped retained core:", response.Error)
			}
		})
	}
}

func TestFailureAcceptanceSlowSubscriberResyncRequiresFreshSnapshot(t *testing.T) {
	r := failureRuntime(t)
	c, err := r.Connect("slow-reader")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	root := t.TempDir()
	var attached coreprotocol.AttachResult
	out := callCore(t, c, "attach", "", coreprotocol.Attach, coreprotocol.AttachRequest{Root: root})
	if err := json.Unmarshal(out.Payload, &attached); err != nil {
		t.Fatal(err)
	}
	// Hold the subscriber without draining. Overflow must invalidate its whole
	// cursor in bounded space and cannot block a real subsequent file mutation.
	for i := range subscriberCapacity + 10 {
		c.publish(coreprotocol.Change{WorkspaceID: attached.WorkspaceID, Revision: uint64(i + 1)})
	}
	if len(c.events) != 1 {
		t.Fatal("slow-reader backlog is not bounded to one resync")
	}
	if event := <-c.Events(); !event.Resync || event.Revision != 0 {
		t.Fatal("overflow silently presented a plausible delta", event)
	}
	callCore(t, c, "create-after-overflow", attached.WorkspaceID, coreprotocol.FilesCreate, coreprotocol.FilePathRequest{Path: "after-overflow.txt"})
	if _, err := os.Stat(filepath.Join(root, "after-overflow.txt")); err != nil {
		t.Fatal("slow observer blocked producer", err)
	}
	var fresh coreprotocol.WorkspaceSnapshot
	out = callCore(t, c, "fresh-snapshot", attached.WorkspaceID, coreprotocol.Snapshot, struct{}{})
	if err := json.Unmarshal(out.Payload, &fresh); err != nil || fresh.ID != attached.WorkspaceID || fresh.Root != root {
		t.Fatal("resync failed to recover exact attached authority", err)
	}
}
