package core

import (
	"context"
	"reflect"
	"slices"
	"sync"
	"time"

	"crude/core/protocol"
	"crude/core/wire"
	"crude/types"
	"crude/workspace"
)

// workspaceTopicState belongs to the actor. It materializes only changed
// identities, once per publication, independent of subscriber count.
type workspaceTopicState struct {
	hub     topicHub
	pending coreprotocol.Change
	dirty   bool
	// A publication opportunity exists only while there are accepted facts to
	// publish. A periodic 16 ms ticker wakes every retained idle workspace.
	timer   *time.Timer
	ready   <-chan time.Time
	cursors map[string]uint64
	states  map[string]workspace.AgentSnapshot
	effects []workspace.TerminalEffectsMsg
}

func appendUnique(dst []string, values ...string) []string {
	for _, value := range values {
		if !slices.Contains(dst, value) {
			dst = append(dst, value)
		}
	}
	return dst
}

func (a *actor) queueTopic(change coreprotocol.Change) {
	if change.Status {
		a.queueState(coreprotocol.TopicStatus)
	}
	if change.Settings {
		a.queueState(coreprotocol.TopicSettings)
	}
	if change.LSP {
		a.queueState(coreprotocol.TopicLSP)
	}
	if len(change.Terminals) > 0 {
		a.queueState(coreprotocol.TopicTerminalInventory)
	}
	if change.Sessions {
		a.queueState(coreprotocol.TopicRegistry)
	}
	if len(change.Files) > 0 {
		a.queueState(coreprotocol.TopicGit)
	}
	if len(change.Agents)+len(change.Files)+len(change.Notices)+len(change.Reductions) == 0 && !change.Sessions && change.TerminalEffects == nil {
		return
	}
	p := &a.topic.pending
	if !a.topic.dirty {
		a.armTopicPublication()
	}
	a.topic.dirty = true
	p.Agents = appendUnique(p.Agents, change.Agents...)
	p.Files = append(p.Files, change.Files...)
	p.Notices = append(p.Notices, change.Notices...)
	p.Reductions = append(p.Reductions, change.Reductions...)
	p.Terminals = appendUnique(p.Terminals, change.Terminals...)
	p.Status, p.Settings, p.Sessions, p.LSP = p.Status || change.Status, p.Settings || change.Settings, p.Sessions || change.Sessions, p.LSP || change.LSP
	if change.TerminalEffects != nil {
		a.topic.effects = append(a.topic.effects, *change.TerminalEffects)
	}
}

func (a *actor) armTopicPublication() {
	if a.topic.timer == nil {
		a.topic.timer = time.NewTimer(16 * time.Millisecond)
	} else {
		a.topic.timer.Reset(16 * time.Millisecond)
	}
	a.topic.ready = a.topic.timer.C
}

func (a *actor) stopTopicPublication() {
	if a.topic.timer != nil {
		a.topic.timer.Stop()
	}
	a.topic.ready = nil
}

func stateMetadata(s workspace.AgentSnapshot) workspace.AgentSnapshot {
	s.Messages = nil
	s.ConversationRevision, s.LastTokenTime, s.RuntimeIdentity = 0, time.Time{}, types.RuntimeEventIdentity{}
	// The independently published Status Center owns observation timestamps.
	// Repeated tokens advance LastTokenTime in ConversationUpdate; a status
	// observation clock alone must not resend the complete agent metadata tuple.
	s.Activity.Status.UpdatedAt = time.Time{}
	return s
}

func (a *actor) flushTopic() error {
	if !a.topic.dirty {
		a.stopTopicPublication()
		return nil
	}
	p := a.topic.pending
	if a.topic.cursors == nil {
		a.topic.cursors = make(map[string]uint64)
		a.topic.states = make(map[string]workspace.AgentSnapshot)
	}
	type publicationCursor struct {
		id       string
		revision uint64
		metadata *workspace.AgentSnapshot
		removed  bool
	}
	next := make([]publicationCursor, 0, len(p.Agents))
	u := coreprotocol.WorkspaceUpdate{Changes: coreprotocol.AgentChangesResult{Revision: a.revision}, Files: p.Files, Notices: p.Notices, TerminalEffects: a.topic.effects}
	// Text/status transitions are canonical state, not one effect per token.
	// Keep exact prompt/completion/error effects, but collapse repeated progress.
	for _, r := range p.Reductions {
		if r.Added || r.ChildCompleted || r.Effects.Prompt != nil || r.Effects.DismissPrompt || r.Effects.Done != nil || r.Effects.Error != nil {
			u.Reductions = append(u.Reductions, r)
		}
	}
	for _, id := range p.Agents {
		state, exists := a.ws.AgentStateSnapshot(id)
		if !exists {
			u.Changes.Removed = append(u.Changes.Removed, id)
			next = append(next, publicationCursor{id: id, removed: true})
			continue
		}
		conversation := a.ws.AgentMgr.Get(id).State.Messages
		delta := conversation.ChangesSince(a.topic.cursors[id])
		meta := stateMetadata(state)
		prior, known := a.topic.states[id]
		if !known || !reflect.DeepEqual(prior, meta) {
			u.Changes.Agents = append(u.Changes.Agents, coreprotocol.AgentDelta{State: state, Conversation: delta})
			next = append(next, publicationCursor{id: id, revision: delta.Revision, metadata: &meta})
		} else if delta.Revision != delta.BaseRevision || delta.Reset {
			u.Conversations = append(u.Conversations, coreprotocol.ConversationUpdate{AgentID: id, LastTokenTime: state.LastTokenTime, Delta: delta})
			next = append(next, publicationCursor{id: id, revision: delta.Revision})
		}
	}
	// Removing an agent can release a session root. Shells validate session
	// roots against the agent inventory, so a removal always carries the
	// session inventory it produced.
	if p.Sessions || len(u.Changes.Removed) > 0 {
		sessions := coreprotocol.SessionSnapshotList(a.sessionSnapshots())
		u.Sessions = &sessions
	}
	body, err := coreprotocol.Marshal(&u)
	if err != nil {
		a.ws.Logger.Warn("CORE", "topic_encode_failed", "error", err.Error())
		a.armTopicPublication()
		return err
	}
	a.topic.hub.publish(body)
	for _, cursor := range next {
		if cursor.removed {
			delete(a.topic.cursors, cursor.id)
			delete(a.topic.states, cursor.id)
			continue
		}
		a.topic.cursors[cursor.id] = cursor.revision
		if cursor.metadata != nil {
			a.topic.states[cursor.id] = *cursor.metadata
		}
	}
	a.topic.pending, a.topic.effects, a.topic.dirty = coreprotocol.Change{}, nil, false
	a.stopTopicPublication()
	return nil
}

func (c *Connection) workspaceTopicSnapshot(ctx context.Context, a *actor) (coreprotocol.WorkspaceUpdate, uint64, error) {
	var out coreprotocol.WorkspaceUpdate
	var seq uint64
	var flushErr error
	err := a.inspect(ctx, func() {
		flushErr = a.flushTopic()
		if flushErr != nil {
			return
		}
		snapshot := a.snapshot()
		out.Snapshot = &snapshot
		seq = a.topic.hub.sequence()
	})
	if err != nil {
		return out, 0, err
	}
	if flushErr != nil {
		return out, 0, flushErr
	}
	return out, seq, nil
}

// At the actor's 16 ms publication cadence, 64 entries cover about one second
// of ACK latency (including the nominal 750 ms GEO link). Only metadata is kept
// in flight here. Each sequential write uses the carrier's existing byte credit.
const workspaceTopicFlightLimit = 64

func (c *Connection) serveWorkspaceTopic(ctx context.Context, s corewire.Stream, reader *corewire.Reader, open coreprotocol.Open) {
	c.mu.Lock()
	a := c.attached[open.WorkspaceID]
	c.mu.Unlock()
	if a == nil {
		return
	}
	var closeOnce sync.Once
	closeStream := func() { closeOnce.Do(func() { _ = s.Close() }) }
	ctx, release := c.claimTopic(ctx, open.WorkspaceID+"/workspace", closeStream)
	defer release()
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(ctx, closeStream)
	offers := make(chan uint64, workspaceTopicFlightLimit)
	acks := make(chan coreprotocol.TopicAck, workspaceTopicFlightLimit)
	readerDone := make(chan struct{})
	defer func() {
		cancel()
		stop()
		closeStream()
		<-readerDone // join the reader before releasing the stream's claim
	}()
	reader.SetLimit(256)
	go func() {
		defer close(readerDone)
		defer cancel() // interrupt a blocked writer on EOF or invalid ACK
		for {
			body, err := reader.Next()
			var ack coreprotocol.TopicAck
			if err != nil || coreprotocol.Decode(body, &ack) != nil {
				return
			}
			select {
			case seq := <-offers:
				if ack.Seq != seq {
					return
				}
			default:
				return // no publication offered for this ACK
			}
			// Each validated ACK consumes one of the bounded offers. The
			// reader stays able to detect closure while a write lacks credit.
			acks <- ack
		}
	}()

	since := open.Since
	resetReason := ""
	if since == 0 {
		resetReason = "initial_snapshot"
	}
	resetPending := false
	var sendTimes [workspaceTopicFlightLimit]time.Time
	first, outstanding := 0, 0
	write := func(seq uint64, reason string, body []byte, snapshot *coreprotocol.WorkspaceUpdate) bool {
		m := corewire.NewMessage()
		defer m.Release()
		m.B, _ = (&coreprotocol.TopicHeader{Seq: seq, Reset: reason != ""}).MarshalMsg(m.B)
		if snapshot != nil {
			var err error
			m.B, err = snapshot.MarshalMsg(m.B)
			if err != nil {
				return false
			}
		} else {
			m.B = append(m.B, body...)
		}
		if ctx.Err() != nil {
			return false
		}
		// The peer can receive the last byte and ACK before Write returns.
		// Register the exact sequence first, but commit its credit and accept
		// its ACK only after the whole write succeeds.
		offers <- seq
		if m.Send(s) != nil {
			return false
		}
		now := time.Now()
		sendTimes[(first+outstanding)%workspaceTopicFlightLimit] = now
		outstanding++
		since = seq
		observeTopicFlightSent(ctx, seq, reason, now, sendTimes[first])
		return true
	}
	applyAck := func(ack coreprotocol.TopicAck) {
		sendTimes[first] = time.Time{}
		first = (first + 1) % workspaceTopicFlightLimit
		outstanding--
		resetPending = false
		observeTopicFlightAck(ctx, ack.Seq, ack.DisplayIntervalNS, sendTimes[first])
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-c.done:
			return
		case <-a.stop:
			return
		default:
		}
		c.mu.Lock()
		attached := c.attached[open.WorkspaceID] == a
		c.mu.Unlock()
		if !attached {
			return
		}
		select {
		case ack := <-acks:
			applyAck(ack)
			continue
		default:
		}

		var wake <-chan struct{}
		if !resetPending && outstanding < workspaceTopicFlightLimit {
			if resetReason == "" {
				entry, ready, covered := a.topic.hub.next(since)
				if !covered {
					resetReason = "journal_gap"
				} else if entry.body != nil {
					if !write(entry.seq, "", entry.body, nil) {
						return
					}
					continue
				} else {
					wake = ready
				}
			}
			// An evicted successor ends this flight. Drain sent entries
			// before capturing the snapshot, then await its exact ACK.
			if resetReason != "" && outstanding == 0 {
				u, seq, err := c.workspaceTopicSnapshot(ctx, a)
				if err != nil || !write(seq, resetReason, nil, &u) {
					return
				}
				resetReason, resetPending = "", true
				continue
			}
		}
		select {
		case ack := <-acks:
			applyAck(ack)
		case <-wake:
		case <-ctx.Done():
			return
		case <-c.done:
			return
		case <-a.stop:
			return
		}
	}
}
