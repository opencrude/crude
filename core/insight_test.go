package core

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"crude/coreprotocol"
	"crude/document"
	"crude/types"
)

func TestInsightTopicThreeShellsAndSlowAckDoesNotBlockText(t *testing.T) {
	r, root := resourceRuntime(t)
	path := filepath.Join(root, "shared.go")
	if err := os.WriteFile(path, []byte("package main\n"), 0600); err != nil {
		t.Fatal(err)
	}
	clients := []*resourceWire{
		newResourceWire(t, r, "insight-a"),
		newResourceWire(t, r, "insight-b"),
		newResourceWire(t, r, "insight-c"),
	}
	var workspaceID string
	var snapshot document.Snapshot
	for _, c := range clients {
		var attach coreprotocol.AttachResult
		c.call(t, "", coreprotocol.Attach, coreprotocol.AttachRequest{Root: root}, &attach)
		if workspaceID != "" && workspaceID != attach.WorkspaceID {
			t.Fatal("shells attached different canonical workspaces")
		}
		workspaceID = attach.WorkspaceID
		c.call(t, workspaceID, coreprotocol.DocumentOpen, coreprotocol.DocumentOpenRequest{Path: path}, &snapshot)
	}
	type publication struct {
		header coreprotocol.TopicHeader
		view   types.DocumentInsight
		ack    chan struct{}
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	streams := make([]chan publication, len(clients))
	for i, c := range clients {
		streams[i] = make(chan publication, 8)
		out := streams[i]
		sub := c.c.Subscribe(ctx, workspaceID, coreprotocol.TopicInsight, snapshot.ID, func(h coreprotocol.TopicHeader, body []byte) error {
			var update coreprotocol.InsightUpdate
			if err := coreprotocol.Decode(body, &update); err != nil {
				return err
			}
			p := publication{h, update.View, make(chan struct{})}
			select {
			case out <- p:
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
		defer func() { cancel(); sub.Close() }()
	}
	next := func(index int) publication {
		t.Helper()
		select {
		case p := <-streams[index]:
			return p
		case <-time.After(5 * time.Second):
			t.Fatalf("shell %d received no insight", index)
			return publication{}
		}
	}
	current := func(index int, revision uint64) publication {
		t.Helper()
		for {
			p := next(index)
			if p.view.Revision == revision && p.view.State == types.InsightCurrent {
				return p
			}
			if p.view.State != types.InsightPending || p.view.Syntax != nil {
				t.Fatalf("unexpected insight: %+v", p.view)
			}
			close(p.ack)
		}
	}
	initial := make([]publication, len(clients))
	for i := range clients {
		initial[i] = current(i, 1)
		if initial[i].view.Generation == "" || initial[i].view.Syntax == nil || initial[i].view.Syntax.HasError {
			t.Fatalf("shell %d missing clean native result", i)
		}
		if i > 0 && !reflect.DeepEqual(initial[0].view, initial[i].view) {
			t.Fatal("shells did not receive the identical canonical analysis/version")
		}
	}
	// Keep shell A's insight ACK held while its text stream and both peers work.
	close(initial[1].ack)
	close(initial[2].ack)
	texts := make(chan coreprotocol.DocumentUpdate, 8)
	textSub := clients[0].c.Subscribe(ctx, workspaceID, coreprotocol.TopicDocument, snapshot.ID, func(_ coreprotocol.TopicHeader, body []byte) error {
		var update coreprotocol.DocumentUpdate
		if err := coreprotocol.Decode(body, &update); err != nil {
			return err
		}
		select {
		case texts <- update:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	defer func() { cancel(); textSub.Close() }()
	select {
	case initialText := <-texts:
		if initialText.Snapshot == nil || initialText.State.Revision != 1 {
			t.Fatalf("missing initial canonical text: %+v", initialText)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("held insight ACK blocked initial text")
	}
	var receipt document.Receipt
	clients[1].call(t, workspaceID, coreprotocol.DocumentEdit, coreprotocol.DocumentEditRequest{
		ID: snapshot.ID, Revision: 1, Edits: []document.Edit{{Start: 13, End: 13, Text: "func main( {\n"}},
	}, &receipt)
	if !receipt.Accepted || receipt.Revision != 2 {
		t.Fatal(receipt)
	}
	for {
		select {
		case update := <-texts:
			if update.State.Revision == 2 {
				if update.Snapshot != nil || len(update.Changes) != 1 || update.Changes[0].Edits[0].Text != "func main( {\n" {
					t.Fatalf("text publication did not carry the accepted edit: %+v", update)
				}
				goto textApplied
			}
		case <-time.After(5 * time.Second):
			t.Fatal("held insight ACK blocked canonical text publication")
		}
	}
textApplied:
	b, c := current(1, 2), current(2, 2)
	if !reflect.DeepEqual(b.view, c.view) || b.view.Syntax == nil || !b.view.Syntax.HasError || len(b.view.Syntax.Problems) == 0 {
		t.Fatal("peers lost identical syntax findings while another shell stalled")
	}
	close(b.ack)
	close(c.ack)
	select {
	case <-streams[0]:
		t.Fatal("insight topic ignored its own applied ACK")
	default:
	}
	close(initial[0].ack)
	a := current(0, 2)
	if !reflect.DeepEqual(a.view, b.view) || a.header.Seq <= initial[0].header.Seq {
		t.Fatal("slow shell did not converge to the latest complete canonical result")
	}
	close(a.ack)
}
