package core

import (
	"context"

	"crude/coreprotocol"
	"crude/corewire"
	"crude/document"
)

func (c *Connection) serveInsightTopic(ctx context.Context, stream corewire.Stream, reader *corewire.Reader, open coreprotocol.Open) {
	c.mu.Lock()
	a := c.attached[open.WorkspaceID]
	c.mu.Unlock()
	if a == nil || a.ws.LiveDocuments == nil {
		return
	}
	ctx, release := c.claimTopic(ctx, open.WorkspaceID+"/insight/"+open.Key, func() { _ = stream.Close() })
	defer release()
	stop := context.AfterFunc(ctx, func() { _ = stream.Close() })
	defer stop()
	service := a.ws.LiveDocuments.Insight
	owner := document.Owner{ClientID: c.Owner.ClientID, Epoch: c.Owner.Epoch}
	dirty, unsubscribe, err := service.Subscribe(owner, open.Key)
	if err != nil {
		return
	}
	defer unsubscribe()
	// Every carrier restart gets the latest complete derived projection. It
	// never replays mutations or resurrects source from a cached text snapshot.
	version := uint64(0)
	seq := open.Since
	for {
		view, err := service.Snapshot(owner, open.Key)
		if err != nil {
			return
		}
		if view.Version != version {
			seq++
			if send(stream, &coreprotocol.TopicHeader{Seq: seq, Reset: version == 0}, &coreprotocol.InsightUpdate{View: view}) != nil {
				return
			}
			observeTopicSent(ctx, seq, "insight_snapshot")
			body, err := reader.Next()
			var ack coreprotocol.TopicAck
			if err != nil || coreprotocol.Decode(body, &ack) != nil || ack.Seq != seq {
				return
			}
			observeTopicAck(ctx, ack.Seq, ack.DisplayIntervalNS)
			version = view.Version
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
