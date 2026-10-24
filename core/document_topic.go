package core

import (
	"context"
	"reflect"

	"crude/coreprotocol"
	"crude/corewire"
	"crude/document"
)

type documentTopicCursor struct {
	seq, revision uint64
	saved         []byte
	state         document.Snapshot
}
type documentTopicView struct{ current, previous documentTopicCursor }

func (c *Connection) serveDocumentTopic(ctx context.Context, stream corewire.Stream, reader *corewire.Reader, open coreprotocol.Open) {
	c.mu.Lock()
	a := c.attached[open.WorkspaceID]
	c.mu.Unlock()
	if a == nil || a.ws.LiveDocuments == nil {
		return
	}
	ctx, release := c.claimTopic(ctx, open.WorkspaceID+"/document/"+open.Key, func() { _ = stream.Close() })
	defer release()
	stop := context.AfterFunc(ctx, func() { _ = stream.Close() })
	defer stop()
	service := a.ws.LiveDocuments
	owner := document.Owner{ClientID: c.Owner.ClientID, Epoch: c.Owner.Epoch}
	dirty, unsubscribe, err := service.SubscribeDocument(owner, open.Key)
	if err != nil {
		return
	}
	defer unsubscribe()
	c.topicMu.Lock()
	if c.documentTopicViews == nil {
		c.documentTopicViews = make(map[string]*documentTopicView)
	}
	key := open.WorkspaceID + "/" + open.Key
	view := c.documentTopicViews[key]
	if view == nil {
		view = &documentTopicView{}
		c.documentTopicViews[key] = view
	}
	c.topicMu.Unlock()
	base := view.current
	if open.Since == 0 {
		base = documentTopicCursor{}
	} else if open.Since != base.seq {
		if open.Since == view.previous.seq {
			base = view.previous
		} else {
			base = documentTopicCursor{}
		}
	}
	revision, saved, previous := base.revision, base.saved, base.state
	seq := max(view.current.seq, open.Since)
	for {
		result, savepoint, err := service.TopicDocument(owner, open.Key, revision, saved)
		if err != nil {
			return
		}
		state := result.Snapshot
		state.Text, state.Saved = nil, nil
		if result.Reset || len(result.Changes) != 0 || savepoint || !reflect.DeepEqual(state, previous) {
			update := coreprotocol.DocumentUpdate{Base: revision, State: result.Snapshot, Changes: result.Changes, Savepoint: savepoint}
			if result.Reset {
				update.Snapshot = &result.Snapshot
				update.State = state
			}
			seq++
			next := documentTopicCursor{seq: seq, revision: result.Snapshot.Revision, saved: saved, state: state}
			if savepoint {
				next.saved = result.Snapshot.Saved
			}
			view.previous, view.current = base, next
			if send(stream, &coreprotocol.TopicHeader{Seq: seq, Reset: result.Reset}, &update) != nil {
				return
			}
			body, err := reader.Next()
			if err != nil {
				return
			}
			var ack coreprotocol.TopicAck
			if coreprotocol.Decode(body, &ack) != nil || ack.Seq != seq {
				return
			}
			c.mu.Lock()
			attached := c.attached[open.WorkspaceID] == a
			c.mu.Unlock()
			if !attached {
				return
			}
			revision, previous = result.Snapshot.Revision, state
			if savepoint {
				saved = result.Snapshot.Saved
			}
			base = next
		}
		select {
		case <-dirty:
		case <-ctx.Done():
			return
		case <-c.done:
			return
		case <-a.stop:
			return
		}
	}
}
