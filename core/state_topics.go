package core

import (
	"bytes"
	"context"
	"sync"
	"time"

	"crude/collaboration"
	"crude/coreprotocol"
	"crude/corewire"
	"crude/types"
	"crude/workspace"
)

var workspaceStateTopics = [...]string{coreprotocol.TopicStatus, coreprotocol.TopicSettings, coreprotocol.TopicLSP, coreprotocol.TopicTerminalInventory, coreprotocol.TopicRegistry, coreprotocol.TopicGit, coreprotocol.TopicBoard}

// A state slot has one latest immutable publication and one conflated pending
// derivation. Each family has its own joined worker: a slow Git read can never
// delay a status update, registry, terminal inventory or command.
type stateSlot struct {
	mu        sync.Mutex
	wanted    uint64
	published uint64
	changed   chan struct{}
	pending   func(context.Context) coreprotocol.WorkspaceState
	wake      chan struct{}
	hub       topicHub
}

type workspaceStates struct {
	slots  map[string]*stateSlot // immutable after actor construction
	wg     sync.WaitGroup
	shells []types.TerminalShell
}

func (a *actor) startStateTopics() {
	// Zero is reserved for a fresh subscriber; an empty log still has an exact
	// resume boundary, without a fabricated publication or a reset on reconnect.
	a.topic.hub.seq = 1
	a.states.slots = make(map[string]*stateSlot, len(workspaceStateTopics))
	a.states.shells = workspace.DetectTerminalShells() // factory worker, before actor publication
	for _, topic := range workspaceStateTopics {
		slot := &stateSlot{wake: make(chan struct{}, 1)}
		a.states.slots[topic] = slot
		a.states.wg.Add(1)
		go func() {
			defer a.states.wg.Done()
			for {
				select {
				case <-a.ctx.Done():
					return
				case <-slot.wake:
				}
				slot.mu.Lock()
				run, gen := slot.pending, slot.wanted
				slot.pending = nil
				slot.mu.Unlock()
				if run == nil {
					continue
				}
				ctx, cancel := context.WithTimeout(a.ctx, 30*time.Second)
				value := run(ctx)
				cancel()
				body, err := coreprotocol.Marshal(&value)
				if err != nil {
					failure := stateFailure("state_encode_failed", err)
					body, _ = coreprotocol.Marshal(&failure)
				}
				slot.mu.Lock()
				if gen == slot.wanted && a.ctx.Err() == nil {
					slot.hub.publishState(body)
					slot.published = gen
					// Generation completion is independent of byte deduplication.
					if slot.changed != nil {
						close(slot.changed)
						slot.changed = nil
					}
				}
				slot.mu.Unlock()
				if err != nil {
					a.ws.Logger.Warn("CORE", "state_encode_failed", "error", err.Error())
				}
			}
		}()
	}
	if board := a.ws.Collaboration; board != nil {
		changed, unsubscribe := board.Subscribe()
		a.states.wg.Add(1)
		go func() {
			defer a.states.wg.Done()
			defer unsubscribe()
			for {
				select {
				case <-a.ctx.Done():
					return
				case _, ok := <-changed:
					if !ok {
						return
					}
					if a.inspect(a.ctx, func() { a.queueState(coreprotocol.TopicBoard) }) != nil {
						return
					}
				}
			}
		}()
	}
}

func (a *actor) queueState(topic string) {
	slot := a.states.slots[topic]
	if slot == nil {
		return
	}
	var run func(context.Context) coreprotocol.WorkspaceState
	switch topic {
	case coreprotocol.TopicStatus:
		var value coreprotocol.StatusSnapshot
		if status := a.ws.Status; status != nil {
			value = coreprotocol.StatusSnapshot{Version: status.Version(), Agents: status.Agents(), Issues: status.Issues(), Notifications: status.Notifications(true)}
		}
		run = func(context.Context) coreprotocol.WorkspaceState { return coreprotocol.WorkspaceState{Status: &value} }
	case coreprotocol.TopicSettings:
		value, defaults := a.captureSettings()
		value.TerminalShells = append([]types.TerminalShell(nil), a.states.shells...)
		run = func(context.Context) coreprotocol.WorkspaceState {
			return coreprotocol.WorkspaceState{Settings: &value, AgentDefaults: &defaults}
		}
	case coreprotocol.TopicLSP:
		manager := a.ws.LSPMgr
		run = func(context.Context) coreprotocol.WorkspaceState {
			value := lspSnapshot(manager)
			return coreprotocol.WorkspaceState{LSP: &value}
		}
	case coreprotocol.TopicTerminalInventory:
		infos := coreprotocol.TerminalInfos(a.ws.TermMgr.List())
		run = func(context.Context) coreprotocol.WorkspaceState {
			return coreprotocol.WorkspaceState{Terminals: &infos}
		}
	case coreprotocol.TopicRegistry:
		root, db, logger := a.ws.RootPath, a.ws.WorkspaceDB, a.ws.Logger
		run = func(context.Context) coreprotocol.WorkspaceState {
			reader := &workspace.Workspace{RootPath: root, WorkspaceDB: db, Logger: logger}
			value := coreprotocol.SessionRegistry(reader.ListSessions())
			return coreprotocol.WorkspaceState{Registry: &value}
		}
	case coreprotocol.TopicGit:
		git := a.ws.Git
		run = func(ctx context.Context) coreprotocol.WorkspaceState {
			value := coreprotocol.GitSnapshotResult{}
			if git != nil {
				snapshot, err := git.Snapshot(ctx)
				if err != nil {
					return stateFailure("git_failed", err)
				}
				value.Snapshot = snapshot
			}
			return coreprotocol.WorkspaceState{Git: &value}
		}
	case coreprotocol.TopicBoard:
		board := a.ws.Collaboration
		run = func(ctx context.Context) coreprotocol.WorkspaceState {
			if board == nil {
				return coreprotocol.WorkspaceState{Board: &coreprotocol.BoardSnapshotResult{}}
			}
			page, err := board.Page(ctx, collaboration.PageQuery{Limit: collaboration.DefaultPageLimit})
			if err != nil {
				return stateFailure("board_failed", err)
			}
			agents, err := board.ListAgents(ctx, collaboration.AgentQuery{IncludeClosed: true, Limit: collaboration.MaxAgentLimit})
			if err != nil {
				return stateFailure("board_failed", err)
			}
			unread, err := board.HumanUnread(ctx)
			if err != nil {
				return stateFailure("board_failed", err)
			}
			value := workspace.BoardSnapshot{WorkspaceID: board.WorkspaceID(), Page: page, Unread: unread}
			for _, agent := range agents {
				value.Participants = append(value.Participants, workspace.BoardParticipant{Agent: agent})
			}
			return coreprotocol.WorkspaceState{Board: &coreprotocol.BoardSnapshotResult{Snapshot: value}}
		}
	default:
		return
	}
	slot.mu.Lock()
	slot.wanted++
	slot.pending = run
	slot.mu.Unlock()
	select {
	case slot.wake <- struct{}{}:
	default:
	}
}

func stateFailure(code string, err error) coreprotocol.WorkspaceState {
	return coreprotocol.WorkspaceState{Fault: &coreprotocol.Fault{Code: code, Message: err.Error()}}
}

func (h *topicHub) publishState(body []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.entries) > 0 && bytes.Equal(h.entries[0].body, body) {
		return
	}
	h.seq++
	if h.wake != nil {
		close(h.wake)
	}
	h.wake = make(chan struct{})
	h.entries = []topicEntry{{h.seq, body}}
	h.bytes = len(body)
}

func (h *topicHub) latest() (topicEntry, <-chan struct{}) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.wake == nil {
		h.wake = make(chan struct{})
	}
	if len(h.entries) == 0 {
		return topicEntry{}, h.wake
	}
	return h.entries[len(h.entries)-1], h.wake
}

func (c *Connection) serveStateTopic(ctx context.Context, s corewire.Stream, reader *corewire.Reader, open coreprotocol.Open) {
	c.mu.Lock()
	a := c.attached[open.WorkspaceID]
	c.mu.Unlock()
	if a == nil {
		return
	}
	slot := a.states.slots[open.Topic]
	if slot == nil {
		return
	}
	ctx, release := c.claimTopic(ctx, open.WorkspaceID+"/"+open.Topic, func() { _ = s.Close() })
	defer release()
	stop := context.AfterFunc(ctx, func() { _ = s.Close() })
	defer stop()
	var required uint64
	if err := a.inspect(ctx, func() {
		if current, _ := slot.hub.latest(); current.body == nil {
			a.queueState(open.Topic)
		}
		slot.mu.Lock()
		required = slot.wanted
		slot.mu.Unlock()
	}); err != nil {
		return
	}
	// A reset subscribed after a command must not acknowledge an old snapshot
	// merely because its replacement is still deriving. Keep one publication
	// owner off the actor, and await the source boundary captured above.
	for {
		slot.mu.Lock()
		if slot.published >= required {
			slot.mu.Unlock()
			break
		}
		if slot.changed == nil {
			slot.changed = make(chan struct{})
		}
		changed := slot.changed
		slot.mu.Unlock()
		select {
		case <-changed:
		case <-ctx.Done():
			return
		case <-a.stop:
			return
		case <-c.done:
			return
		}
	}
	var since uint64
	for {
		entry, wake := slot.hub.latest()
		if entry.body == nil || entry.seq == since {
			select {
			case <-wake:
				continue
			case <-ctx.Done():
				return
			case <-a.stop:
				return
			case <-c.done:
				return
			}
		}
		m := corewire.NewMessage()
		m.B, _ = (&coreprotocol.TopicHeader{Seq: entry.seq, Reset: true}).MarshalMsg(m.B)
		m.B = append(m.B, entry.body...)
		err := m.Send(s)
		m.Release()
		if err != nil {
			return
		}
		observeTopicSent(ctx, entry.seq, "latest_state")
		body, err := reader.Next()
		if err != nil {
			return
		}
		var ack coreprotocol.TopicAck
		if coreprotocol.Decode(body, &ack) != nil || ack.Seq != entry.seq {
			return
		}
		observeTopicAck(ctx, ack.Seq, ack.DisplayIntervalNS)
		since = entry.seq
		display := clampDisplay(ack.DisplayIntervalNS)
		_, rtt := c.Carrier()
		interval := max(display, min(rtt/2, 250*time.Millisecond))
		timer := time.NewTimer(interval)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return
		case <-a.stop:
			timer.Stop()
			return
		}
	}
}
