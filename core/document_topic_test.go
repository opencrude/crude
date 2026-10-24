package core

import (
	"context"
	"crude/coreclient"
	"crude/corewire"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"crude/coreprotocol"
	"crude/document"
)

func TestDocumentTopicConnectedAckOrdersDeltasAndSavepoint(t *testing.T) {
	r, root := resourceRuntime(t)
	path := filepath.Join(root, "shared.txt")
	if err := os.WriteFile(path, []byte("abc"), 0600); err != nil {
		t.Fatal(err)
	}
	a, b := newResourceWire(t, r, "doc-author"), newResourceWire(t, r, "doc-peer")
	var attach coreprotocol.AttachResult
	a.call(t, "", coreprotocol.Attach, coreprotocol.AttachRequest{Root: root}, &attach)
	var peer coreprotocol.AttachResult
	b.call(t, "", coreprotocol.Attach, coreprotocol.AttachRequest{Root: root}, &peer)
	var snapshot document.Snapshot
	a.call(t, attach.WorkspaceID, coreprotocol.DocumentOpen, coreprotocol.DocumentOpenRequest{Path: path}, &snapshot)
	b.call(t, attach.WorkspaceID, coreprotocol.DocumentOpen, coreprotocol.DocumentOpenRequest{Path: path}, &snapshot)
	type publication struct {
		header coreprotocol.TopicHeader
		update coreprotocol.DocumentUpdate
		ack    chan struct{}
	}
	updates := make(chan publication, 4)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	sub := b.c.Subscribe(ctx, attach.WorkspaceID, coreprotocol.TopicDocument, snapshot.ID, func(header coreprotocol.TopicHeader, body []byte) error {
		var update coreprotocol.DocumentUpdate
		if err := coreprotocol.Decode(body, &update); err != nil {
			return err
		}
		p := publication{header: header, update: update, ack: make(chan struct{})}
		select {
		case updates <- p:
		case <-ctx.Done():
			return ctx.Err()
		}
		select {
		case <-p.ack:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	defer sub.Close()
	next := func() publication {
		t.Helper()
		select {
		case p := <-updates:
			return p
		case <-time.After(5 * time.Second):
			t.Fatal("no pushed document")
			return publication{}
		}
	}
	initial := next()
	if !initial.header.Reset || initial.update.Snapshot == nil || string(initial.update.Snapshot.Text) != "abc" {
		t.Fatalf("bad initial: %+v", initial)
	}
	var receipt document.Receipt
	a.call(t, attach.WorkspaceID, coreprotocol.DocumentEdit, coreprotocol.DocumentEditRequest{ID: snapshot.ID, Revision: 1, Edits: []document.Edit{{Start: 0, End: 0, Text: "X"}}}, &receipt)
	select {
	case <-updates:
		t.Fatal("published before UI-applied ack")
	default:
	}
	close(initial.ack)
	delta := next()
	if delta.header.Reset || delta.update.Base != 1 || delta.update.State.Revision != 2 || len(delta.update.State.Text) != 0 || len(delta.update.State.Saved) != 0 || len(delta.update.Changes) != 1 || delta.update.Changes[0].Edits[0].Text != "X" {
		t.Fatalf("bad delta: %+v", delta)
	}
	close(delta.ack)
	a.call(t, attach.WorkspaceID, coreprotocol.DocumentSave, coreprotocol.DocumentRevisionRequest{ID: snapshot.ID, Revision: 2}, &snapshot)
	save := next()
	if !save.update.Savepoint || save.update.State.Dirty || string(save.update.State.Saved) != "Xabc" || len(save.update.Changes) != 0 || len(save.update.State.Text) != 0 {
		t.Fatalf("bad savepoint: %+v", save)
	}
	close(save.ack)
}

// A carrier cut is not a cold attachment: retain the session owner and resume
// the document journal from the last UI-applied publication, without a reset.
func TestDocumentTopicCarrierResumeUsesJournal(t *testing.T) {
	r, root := resourceRuntime(t)
	path := filepath.Join(root, "resume.txt")
	if err := os.WriteFile(path, []byte("abc"), 0600); err != nil {
		t.Fatal(err)
	}
	carriers := make(chan net.Conn, 8)
	dial := func(ctx context.Context) (corewire.Conn, error) {
		server, client := net.Pipe()
		carriers <- server
		go func() { _ = ServeConnection(t.Context(), r, server) }()
		return corewire.NewMux(client, corewire.CarrierLocal, true), nil
	}
	client, err := coreclient.Dial(t.Context(), dial, "document-resume")
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	call := func(method string, in coreprotocol.Value, out coreprotocol.Target, ws string) {
		t.Helper()
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()
		if err := client.Call(ctx, ws, method, in, out); err != nil {
			t.Fatal(err)
		}
	}
	var attach coreprotocol.AttachResult
	call(coreprotocol.Attach, &coreprotocol.AttachRequest{Root: root}, &attach, "")
	var snapshot document.Snapshot
	call(coreprotocol.DocumentOpen, &coreprotocol.DocumentOpenRequest{Path: path}, &snapshot, attach.WorkspaceID)
	type publication struct {
		header coreprotocol.TopicHeader
		update coreprotocol.DocumentUpdate
	}
	updates := make(chan publication, 8)
	sub := client.Subscribe(t.Context(), attach.WorkspaceID, coreprotocol.TopicDocument, snapshot.ID, func(h coreprotocol.TopicHeader, body []byte) error {
		var u coreprotocol.DocumentUpdate
		if err := coreprotocol.Decode(body, &u); err != nil {
			return err
		}
		updates <- publication{h, u}
		return nil
	})
	defer sub.Close()
	next := func() publication {
		t.Helper()
		select {
		case p := <-updates:
			return p
		case <-time.After(5 * time.Second):
			t.Fatal("missing document publication")
			return publication{}
		}
	}
	initial := next()
	if !initial.header.Reset {
		t.Fatal("initial was not reset")
	}
	// Receipt and pushed delta prove the initial ACK was accepted before cut.
	var receipt document.Receipt
	call(coreprotocol.DocumentEdit, &coreprotocol.DocumentEditRequest{ID: snapshot.ID, Revision: 1, Edits: []document.Edit{{Start: 0, End: 0, Text: "X"}}}, &receipt, attach.WorkspaceID)
	first := next()
	if first.header.Reset || first.update.State.Revision != 2 {
		t.Fatalf("bad first delta %+v", first)
	}
	owner := client.Owner()
	if err := (<-carriers).Close(); err != nil {
		t.Fatal(err)
	}
	call(coreprotocol.DocumentEdit, &coreprotocol.DocumentEditRequest{ID: snapshot.ID, Revision: 2, Edits: []document.Edit{{Start: 4, End: 4, Text: "Y"}}}, &receipt, attach.WorkspaceID)
	// A cut can lose the previous ACK. Replay may include that same delta; it
	// must still be a contiguous journal suffix, never a full snapshot.
	for {
		p := next()
		if p.header.Reset || p.update.Snapshot != nil || len(p.update.State.Text) != 0 {
			t.Fatalf("warm resume reset document: %+v", p)
		}
		if p.update.State.Revision == 3 {
			if len(p.update.Changes) == 0 || p.update.Changes[len(p.update.Changes)-1].Edits[0].Text != "Y" {
				t.Fatalf("missing accepted edit %+v", p)
			}
			break
		}
	}
	if client.Owner() != owner {
		t.Fatal("carrier resume changed document owner")
	}
}
