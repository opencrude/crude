// Package core owns retained workspace lifetimes and serialized domain ingress.
// It never imports a terminal renderer or uses a shell as a runtime scheduler.
package core

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"crude/agent"
	"crude/coreprotocol"
	"crude/document"
	"crude/history"
	"crude/provider"
	"crude/workspace"
)

const subscriberCapacity = 64

// Factory belongs to the application configuration owner. It is invoked once
// for each canonical project; it must return a newly configured, unshared owner.
type Factory func(root string) (*workspace.Workspace, error)

type Runtime struct {
	mu          sync.Mutex
	workspaces  map[string]*actor
	clients     map[string]*Connection
	epochs      map[string]uint64
	factory     Factory
	receipts    *receiptJournal
	closed      bool
	creating    map[string]*workspaceCreation
	factoryWG   sync.WaitGroup
	history     *history.Store
	historyPath string
	uploads     *ReferenceUploads
}

func New(factory Factory, receiptPath string) (*Runtime, error) {
	if factory == nil {
		return nil, errors.New("core workspace factory is required")
	}
	journal, err := openReceipts(receiptPath)
	if err != nil {
		return nil, err
	}
	return &Runtime{factory: factory, receipts: journal, workspaces: make(map[string]*actor), clients: make(map[string]*Connection), epochs: make(map[string]uint64), creating: make(map[string]*workspaceCreation), uploads: NewReferenceUploads(filepath.Join(filepath.Dir(receiptPath), "media-staging"))}, nil
}

// SetPromptHistory installs the one application-owned history before clients
// attach. Shells receive value snapshots, never another whole-file writer.
func (r *Runtime) SetPromptHistory(store *history.Store, path string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed || len(r.clients) != 0 || len(r.workspaces) != 0 || len(r.creating) != 0 {
		return errors.New("prompt history must be installed before serving clients")
	}
	if store == nil || path == "" {
		return errors.New("prompt history owner and path are required")
	}
	r.history, r.historyPath = store, path
	return nil
}

// Connection is one authenticated local/SSH transport lifetime. Its queue is
// bounded; a slow observer receives resynchronization, never domain backpressure.
type Connection struct {
	runtime   *Runtime
	Owner     coreprotocol.Owner
	mu        sync.Mutex
	attached  map[string]*actor
	events    chan coreprotocol.Change
	done      chan struct{}
	once      sync.Once
	publishMu sync.Mutex
	resync    bool
}

func (r *Runtime) Connect(clientID string) (*Connection, error) {
	if !validID(clientID) {
		return nil, errors.New("invalid client identity")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil, errors.New("core is closed")
	}
	if r.clients[clientID] != nil {
		return nil, errors.New("client identity already connected")
	}
	r.epochs[clientID]++
	c := &Connection{runtime: r, Owner: coreprotocol.Owner{ClientID: clientID, Epoch: r.epochs[clientID]}, attached: make(map[string]*actor), events: make(chan coreprotocol.Change, subscriberCapacity), done: make(chan struct{})}
	r.clients[clientID] = c
	return c, nil
}

func NewClientID() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(value[:])
}

func validID(id string) bool {
	if len(id) == 0 || len(id) > 256 {
		return false
	}
	for _, r := range id {
		if r < 33 || r > 126 {
			return false
		}
	}
	return true
}

func (c *Connection) Events() <-chan coreprotocol.Change { return c.events }
func (c *Connection) Done() <-chan struct{}              { return c.done }

func (c *Connection) Close() {
	c.once.Do(func() {
		close(c.done)
		c.runtime.mu.Lock()
		if c.runtime.clients[c.Owner.ClientID] == c {
			delete(c.runtime.clients, c.Owner.ClientID)
		}
		c.runtime.mu.Unlock()
		c.mu.Lock()
		owners := c.attached
		c.attached = make(map[string]*actor)
		c.mu.Unlock()
		for _, owner := range owners {
			owner.unsubscribe(c)
		}
	})
}

func (c *Connection) publish(change coreprotocol.Change) {
	c.publishMu.Lock()
	defer c.publishMu.Unlock()
	if c.resync {
		if len(c.events) != 0 {
			return
		}
		c.resync = false
	}
	select {
	case <-c.done:
		return
	default:
	}
	select {
	case c.events <- change:
		return
	default:
	}
	// Overflow invalidates the entire cursor. Empty the bounded backlog and
	// publish one explicit resync; never pretend that dropped deltas applied.
	for {
		select {
		case <-c.events:
		default:
			goto drained
		}
	}
drained:
	c.resync = true
	select {
	case c.events <- coreprotocol.Change{Resync: true}:
	default:
	}
}

func (r *Runtime) Close() error {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil
	}
	r.closed = true
	clients := make([]*Connection, 0, len(r.clients))
	for _, c := range r.clients {
		clients = append(clients, c)
	}
	r.mu.Unlock()
	r.factoryWG.Wait()
	r.mu.Lock()
	actors := make([]*actor, 0, len(r.workspaces))
	for _, a := range r.workspaces {
		actors = append(actors, a)
	}
	r.mu.Unlock()
	for _, c := range clients {
		c.Close()
	}
	for _, a := range actors {
		a.close()
	}
	return errors.Join(r.uploads.Close(), r.receipts.close())
}

func (r *Runtime) ClientCount() int { r.mu.Lock(); defer r.mu.Unlock(); return len(r.clients) }

func (c *Connection) Call(ctx context.Context, request coreprotocol.Request) coreprotocol.Response {
	result := c.Dispatch(ctx, request)
	select {
	case response := <-result:
		return response
	case <-ctx.Done():
		return fault(request, "outcome_pending", "request may have been admitted; inspect its receipt before retrying")
	case <-c.done:
		return fault(request, "outcome_uncertain", "connection closed; inspect its receipt before retrying")
	}
}

// Dispatch admits actor commands in caller order before returning. Network
// readers may await responses concurrently without reordering the admissions.
func (c *Connection) Dispatch(ctx context.Context, request coreprotocol.Request) <-chan coreprotocol.Response {
	ready := make(chan coreprotocol.Response, 1)
	finish := func(response coreprotocol.Response) <-chan coreprotocol.Response { ready <- response; return ready }
	request.Payload = bytes.Clone(request.Payload)
	if request.Owner != c.Owner || !validID(request.ID) {
		return finish(fault(request, "invalid_owner", "request owner or identity is invalid"))
	}
	select {
	case <-c.done:
		return finish(fault(request, "detached", "connection is closed"))
	default:
	}
	if len(request.Payload) > 8<<20 || !json.Valid(request.Payload) {
		return finish(fault(request, "invalid_request", "invalid or oversized request payload"))
	}
	if request.Method == coreprotocol.Attach {
		return finish(c.attach(ctx, request))
	}
	if request.Method == coreprotocol.GetReceipt {
		var query coreprotocol.ReceiptRequest
		if err := decode(request.Payload, &query); err != nil {
			return finish(fault(request, "invalid_request", err.Error()))
		}
		receipt, err := c.runtime.receipts.lookup(ctx, c.Owner.ClientID, query.RequestID)
		if err != nil {
			return finish(fault(request, "receipt_unavailable", err.Error()))
		}
		return finish(response(request, receipt))
	}
	c.mu.Lock()
	a := c.attached[request.WorkspaceID]
	c.mu.Unlock()
	if a == nil {
		return finish(fault(request, "not_attached", "workspace is not attached to this client"))
	}
	if request.Method == coreprotocol.Detach {
		c.mu.Lock()
		delete(c.attached, request.WorkspaceID)
		c.mu.Unlock()
		a.unsubscribe(c)
		return finish(response(request, struct{}{}))
	}
	select {
	case a.commands <- actorCommand{request: &request, client: c, result: ready}:
		return ready
	case <-ctx.Done():
		return finish(fault(request, "cancelled", ctx.Err().Error()))
	case <-a.stop:
		return finish(fault(request, "closed", "workspace closed"))
	}
}

func (c *Connection) attach(ctx context.Context, request coreprotocol.Request) coreprotocol.Response {
	var input coreprotocol.AttachRequest
	if err := decode(request.Payload, &input); err != nil {
		return fault(request, "invalid_request", err.Error())
	}
	root, err := filepath.Abs(input.Root)
	if err == nil {
		root, err = filepath.EvalSymlinks(root)
	}
	if err != nil {
		return fault(request, "invalid_workspace", err.Error())
	}
	key := sha256.Sum256([]byte(filepath.Clean(root)))
	id := hex.EncodeToString(key[:])
	r := c.runtime
	a, err := r.workspace(ctx, id, root)
	if err != nil {
		return fault(request, "workspace_unavailable", err.Error())
	}
	view, err := a.subscribe(ctx, c)
	if err != nil {
		return fault(request, "attach_failed", err.Error())
	}
	c.mu.Lock()
	select {
	case <-c.done:
		c.mu.Unlock()
		a.unsubscribe(c)
		return fault(request, "detached", "connection closed during attach")
	default:
	}
	c.attached[id] = a
	c.mu.Unlock()
	return response(request, coreprotocol.AttachResult{WorkspaceID: id, Epoch: c.Owner.Epoch, Revision: view.Revision, View: view})
}

type workspaceCreation struct {
	done  chan struct{}
	actor *actor
	err   error
}

func (r *Runtime) workspace(ctx context.Context, id, root string) (*actor, error) {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil, errors.New("core is closed")
	}
	if a := r.workspaces[id]; a != nil {
		r.mu.Unlock()
		return a, nil
	}
	creation := r.creating[id]
	if creation == nil {
		creation = &workspaceCreation{done: make(chan struct{})}
		r.creating[id] = creation
		r.factoryWG.Add(1)
		go func() {
			defer r.factoryWG.Done()
			ws, err := r.factory(root)
			r.mu.Lock()
			if err == nil && r.closed {
				err = errors.New("core is closed")
			}
			if err == nil {
				creation.actor = newActor(id, ws, r.receipts)
				creation.actor.runtime = r
				r.workspaces[id] = creation.actor
			}
			creation.err = err
			delete(r.creating, id)
			r.mu.Unlock()
			if err != nil && ws != nil {
				ws.CloseAllSessions()
				ws.Close()
			}
			close(creation.done)
		}()
	}
	r.mu.Unlock()
	select {
	case <-creation.done:
		return creation.actor, creation.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

type actor struct {
	runtime       *Runtime
	id            string
	ws            *workspace.Workspace
	receipts      *receiptJournal
	commands      chan actorCommand
	ingress       chan workspace.AgentEvent
	serviceEvents chan any
	stop          chan struct{}
	done          chan struct{}
	once          sync.Once
	subscribers   map[*Connection]struct{}
	revision      uint64
	ctx           context.Context
	cancel        context.CancelFunc
	jobs          chan actorJobResult
	inFlight      int
	lifecycle     bool
	settingsBusy  bool
	settingsQueue []actorCommand
	resourceLane  *resourceLane
}

type actorCommand struct {
	request *coreprotocol.Request
	client  *Connection
	attach  *Connection
	detach  *Connection
	result  chan coreprotocol.Response
	view    chan coreprotocol.WorkspaceSnapshot
}

func newActor(id string, ws *workspace.Workspace, receipts *receiptJournal) *actor {
	ctx, cancel := context.WithCancel(context.Background())
	a := &actor{ctx: ctx, cancel: cancel, jobs: make(chan actorJobResult, 16), id: id, ws: ws, receipts: receipts, commands: make(chan actorCommand, 128), ingress: make(chan workspace.AgentEvent, 1024), stop: make(chan struct{}), done: make(chan struct{}), subscribers: make(map[*Connection]struct{}), revision: 1}
	a.resourceLane = newResourceLane(ctx, 32)
	a.serviceEvents = make(chan any, 256)
	bus := workspace.NewEventBus()
	bus.SetSink(a)
	ws.SetEventBus(bus)
	ws.SetEventSender(a)
	go a.run()
	return a
}

// Backend ingress never depends on a client connection or presentation cadence.
func (a *actor) SendAgentEvent(kind string, data any) { a.SendAgentEventWithID(kind, data, "") }
func (a *actor) SendAgentEventWithID(kind string, data any, id string) {
	a.enqueue(workspace.AgentEvent{Type: kind, Data: data, AgentID: id})
}
func (a *actor) SendAgentEventEnvelope(event agent.AgentEvent) {
	a.enqueue(workspace.AgentEvent{Type: event.Type, Data: event.Data, AgentID: event.AgentID, Identity: event.Identity})
}
func (a *actor) enqueue(event workspace.AgentEvent) {
	select {
	case <-a.stop:
		return
	default:
	}
	select {
	case a.ingress <- event:
	case <-a.stop:
	}
}

// Send receives service evidence independently of every shell. Only neutral
// typed facts are projected; PTY bytes remain in TerminalService.
func (a *actor) Send(message any) {
	select {
	case <-a.stop:
		return
	default:
	}
	select {
	case a.serviceEvents <- message:
	case <-a.stop:
	}
}

func (a *actor) serviceEvent(message any) {
	var id string
	var effects *workspace.TerminalEffectsMsg
	switch event := message.(type) {
	case workspace.TerminalChangedMsg:
		id = event.ID
	case workspace.TerminalExitedMsg:
		id = event.ID
	case workspace.TerminalTitleMsg:
		id = event.ID
	case workspace.TerminalEffectsMsg:
		id = event.ID
		effects = &event
	default:
		return
	}
	old := a.revision
	a.revision++
	for subscriber := range a.subscribers {
		change := coreprotocol.Change{WorkspaceID: a.id, BaseRevision: old, Revision: a.revision, Terminals: []string{id}}
		if effects != nil {
			copy := *effects
			copy.Effects.Replies = nil
			copy.Effects.Clipboard = nil
			if a.ws.Terminals != nil && a.ws.Terminals.ControlledBy(id, workspace.TerminalController{ClientID: subscriber.Owner.ClientID, Epoch: subscriber.Owner.Epoch}) {
				for _, value := range effects.Effects.Clipboard {
					copy.Effects.Clipboard = append(copy.Effects.Clipboard, bytes.Clone(value))
				}
			}
			change.TerminalEffects = &copy
		}
		subscriber.publish(change)
	}
}

func (a *actor) run() {
	defer close(a.done)
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-a.stop:
			a.cancel()
			// Producers observe stop before session shutdown; completed worker results
			// are still consumed so no admitted producer can deadlock waiting for us.
			for a.inFlight > 0 {
				a.finishJob(<-a.jobs)
			}
			return
		case job := <-a.jobs:
			a.finishJob(job)
		case message := <-a.serviceEvents:
			a.serviceEvent(message)
		case event := <-a.ingress:
			if reduced := a.ws.ApplyAgentEvent(event); reduced.Accepted {
				a.changed(reduced.ChangedAgents)
			}
		case now := <-tick.C:
			if reduced := a.ws.AdvanceAgentState(now); reduced.Accepted {
				a.changed(reduced.ChangedAgents)
			}
		case command := <-a.commands:
			if command.attach != nil {
				a.subscribers[command.attach] = struct{}{}
				command.view <- a.snapshot()
				continue
			}
			if command.detach != nil {
				delete(a.subscribers, command.detach)
				if a.ws.Terminals != nil {
					a.ws.Terminals.Detach(workspace.TerminalController{ClientID: command.detach.Owner.ClientID, Epoch: command.detach.Owner.Epoch})
				}
				if a.ws.LiveDocuments != nil {
					a.ws.LiveDocuments.Detach(document.Owner{ClientID: command.detach.Owner.ClientID, Epoch: command.detach.Owner.Epoch})
				}
				continue
			}
			if !a.validClient(command.client) {
				command.result <- fault(*command.request, "detached", "request attachment is stale")
				continue
			}
			if strings.HasPrefix(command.request.Method, "settings.") && a.settingsBusy {
				if len(a.settingsQueue) >= 16 {
					command.result <- fault(*command.request, "busy", "settings queue is full")
				} else {
					a.settingsQueue = append(a.settingsQueue, command)
				}
				continue
			}
			if a.startJob(command) {
				continue
			}
			command.result <- a.execute(*command.request)
		}
	}
}

func (a *actor) changed(ids []string) {
	old := a.revision
	a.revision++
	for subscriber := range a.subscribers {
		select {
		case <-subscriber.done:
			delete(a.subscribers, subscriber)
			continue
		default:
		}
		subscriber.publish(coreprotocol.Change{WorkspaceID: a.id, BaseRevision: old, Revision: a.revision, Agents: slices.Clone(ids)})
	}
}

func (a *actor) subscribe(ctx context.Context, c *Connection) (coreprotocol.WorkspaceSnapshot, error) {
	ch := make(chan coreprotocol.WorkspaceSnapshot, 1)
	select {
	case a.commands <- actorCommand{attach: c, view: ch}:
	case <-ctx.Done():
		return coreprotocol.WorkspaceSnapshot{}, ctx.Err()
	case <-a.stop:
		return coreprotocol.WorkspaceSnapshot{}, errors.New("workspace closed")
	}
	select {
	case view := <-ch:
		return view, nil
	case <-ctx.Done():
		a.unsubscribe(c)
		return coreprotocol.WorkspaceSnapshot{}, ctx.Err()
	case <-a.stop:
		return coreprotocol.WorkspaceSnapshot{}, errors.New("workspace closed")
	}
}
func (a *actor) unsubscribe(c *Connection) {
	if a.runtime != nil && a.runtime.uploads != nil {
		a.runtime.uploads.Detach(c.Owner, a.id)
	}
	// Service fences happen independently of the bounded actor mailbox. They
	// invalidate delayed acquisitions, not merely currently held leases.
	if a.ws.Terminals != nil {
		a.ws.Terminals.Detach(workspace.TerminalController{ClientID: c.Owner.ClientID, Epoch: c.Owner.Epoch})
	}
	if a.ws.LiveDocuments != nil {
		a.ws.LiveDocuments.Detach(document.Owner{ClientID: c.Owner.ClientID, Epoch: c.Owner.Epoch})
	}
	select {
	case a.commands <- actorCommand{detach: c}:
	case <-a.stop:
	}
}
func (a *actor) call(ctx context.Context, c *Connection, request coreprotocol.Request) coreprotocol.Response {
	ch := make(chan coreprotocol.Response, 1)
	select {
	case a.commands <- actorCommand{request: &request, client: c, result: ch}:
	case <-ctx.Done():
		return fault(request, "cancelled", ctx.Err().Error())
	case <-a.stop:
		return fault(request, "closed", "workspace closed")
	}
	select {
	case result := <-ch:
		return result
	case <-ctx.Done():
		return fault(request, "outcome_pending", "request was admitted; inspect its receipt before retrying")
	case <-a.stop:
		return fault(request, "outcome_uncertain", "core closed; inspect its receipt before retrying")
	}
}
func (a *actor) close() {
	a.once.Do(func() { close(a.stop); <-a.done; a.resourceLane.close(); a.ws.CloseAllSessions(); a.ws.Close() })
}

func decode(raw []byte, value any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(value); err != nil {
		return err
	}
	if d.Decode(new(any)) != io.EOF {
		return errors.New("trailing JSON")
	}
	return nil
}
func response(request coreprotocol.Request, value any) coreprotocol.Response {
	data, err := json.Marshal(value)
	if err != nil {
		return fault(request, "encode_failed", err.Error())
	}
	return coreprotocol.Response{ID: request.ID, Payload: data}
}
func fault(request coreprotocol.Request, code, message string) coreprotocol.Response {
	return coreprotocol.Response{ID: request.ID, Error: &coreprotocol.Fault{Code: code, Message: message}}
}

func (a *actor) execute(request coreprotocol.Request) coreprotocol.Response {
	if request.Method == coreprotocol.AgentChanges {
		var query coreprotocol.AgentChangesRequest
		if err := decode(request.Payload, &query); err != nil {
			return fault(request, "invalid_request", err.Error())
		}
		out := coreprotocol.AgentChangesResult{Revision: a.revision}
		known := make(map[string]bool)
		for _, state := range a.ws.AgentStateSnapshots().Agents {
			known[state.ID] = true
			node := a.ws.AgentMgr.Get(state.ID)
			out.Agents = append(out.Agents, coreprotocol.AgentDelta{State: state, Conversation: node.State.Messages.ChangesSince(query.Revisions[state.ID])})
		}
		for id := range query.Revisions {
			if !known[id] {
				out.Removed = append(out.Removed, id)
			}
		}
		return response(request, out)
	}
	if request.Method == coreprotocol.Snapshot {
		return response(request, a.snapshot())
	}

	switch request.Method {
	case coreprotocol.NewSession, coreprotocol.OpenSession, coreprotocol.SubmitInput, coreprotocol.SteerInput, coreprotocol.Decide, coreprotocol.CancelAgent, coreprotocol.TerminateAgent:
	default:
		return fault(request, "unknown_method", "method is not offered by this core")
	}
	prior, fresh, err := a.receipts.begin(context.Background(), request)
	if err != nil {
		return fault(request, "receipt_failed", err.Error())
	}
	if !fresh {
		if prior.State == "completed" {
			return prior.Response
		}
		return fault(request, "outcome_"+prior.State, "operation is not safe to replay; inspect its retained receipt")
	}
	result := a.mutate(request)
	result.Revision = a.revision
	if err := a.receipts.complete(context.Background(), request, result); err != nil {
		return fault(request, "outcome_uncertain", "effect may have completed; receipt publication failed: "+err.Error())
	}
	return result
}

func (a *actor) mutate(request coreprotocol.Request) coreprotocol.Response {
	switch request.Method {
	case coreprotocol.SteerInput:
		var input coreprotocol.InputRequest
		if err := decode(request.Payload, &input); err != nil {
			return fault(request, "invalid_request", err.Error())
		}
		if strings.TrimSpace(input.Text) == "" {
			return fault(request, "invalid_input", "only nonempty human input is accepted")
		}
		accepted := a.ws.SteerAgent(input.AgentID, humanInput(request, input))
		if accepted {
			a.changed([]string{input.AgentID})
		}
		return response(request, coreprotocol.DecisionResult{Accepted: accepted})
	case coreprotocol.Decide:
		var input coreprotocol.DecisionRequest
		if err := decode(request.Payload, &input); err != nil {
			return fault(request, "invalid_request", err.Error())
		}
		node := a.ws.AgentMgr.Get(input.AgentID)
		if node == nil || node.State == nil || input.BlockID == "" || node.State.PendingBlockID != input.BlockID {
			return fault(request, "stale_decision", "decision no longer owns this pending block")
		}
		var accepted bool
		if input.Choice != "" {
			if input.Choice != "allow" && input.Choice != "deny" && input.Choice != "always" {
				return fault(request, "invalid_decision", "invalid permission choice")
			}
			accepted = a.ws.ResolveConfirmation(input.AgentID, input.BlockID, input.Choice)
		} else {
			accepted = a.ws.ResolveQuestion(input.AgentID, input.BlockID, input.Answers, input.Questions, input.Cancelled)
		}
		if accepted {
			a.changed([]string{input.AgentID})
		}
		return response(request, coreprotocol.DecisionResult{Accepted: accepted})
	case coreprotocol.CancelAgent, coreprotocol.TerminateAgent:
		var input coreprotocol.AgentRequest
		if err := decode(request.Payload, &input); err != nil {
			return fault(request, "invalid_request", err.Error())
		}
		node := a.ws.AgentMgr.Get(input.AgentID)
		if node == nil || node.State == nil || node.State.TurnIdentity.Generation != input.Generation {
			return fault(request, "stale_generation", "agent generation changed")
		}
		if request.Method == coreprotocol.TerminateAgent {
			a.ws.CloseAgent(input.AgentID)
			a.ws.ForgetAgentInventory(input.AgentID)
		} else {
			a.ws.BeginAgentCancellation(input.AgentID)
			a.ws.CancelAgent(input.AgentID)
		}
		a.changed([]string{input.AgentID})
		return response(request, coreprotocol.DecisionResult{Accepted: true})
	}
	return fault(request, "unknown_method", fmt.Sprintf("unknown operation %q", request.Method))
}

func (a *actor) snapshot() coreprotocol.WorkspaceSnapshot {
	settings := a.ws.LoadAppSettings()
	view := coreprotocol.WorkspaceSnapshot{ID: a.id, Root: a.ws.RootPath, Username: a.ws.Username, StartedAt: a.ws.StartTime, Revision: a.revision, Agents: a.ws.SnapshotAgents().Agents,
		Settings: coreprotocol.ShellSettings{Theme: settings.Theme, Editor: settings.Editor, UI: settings.UI, Font: settings.Font}}
	for _, owner := range a.ws.Sessions {
		s := coreprotocol.SessionSnapshot{ID: owner.SessionID, AgentID: owner.AgentID, CreatedAt: owner.CreatedAt}
		if owner.Settings != nil {
			effective := owner.Settings.Effective
			s.Settings = coreprotocol.SessionSettings{MainAgent: effective.SessionMainAgent, Model: effective.Model, ProviderID: effective.ProviderID, AccountID: effective.AccountID, ThinkingEffort: effective.ThinkingEffort, MultiAgent: effective.SessionMultiAgent}
		}
		if owner.RefStore != nil {
			s.References = owner.RefStore.ListReferences()
		}
		view.Sessions = append(view.Sessions, s)
	}
	return view
}

// TODO: explicit typed service dispatch can share startJob's bounded worker and
// finishJob's serialized application; remote payloads must never carry callbacks.
type actorJobResult struct {
	command   actorCommand
	apply     func() coreprotocol.Response
	lifecycle bool
	journal   bool
	admitted  bool
	settings  bool
}

func (a *actor) validClient(c *Connection) bool {
	if c == nil {
		return false
	}
	select {
	case <-c.done:
		return false
	default:
	}
	_, ok := a.subscribers[c]
	c.mu.Lock()
	attached := c.attached[a.id] == a
	c.mu.Unlock()
	c.runtime.mu.Lock()
	current := c.runtime.clients[c.Owner.ClientID] == c && c.runtime.epochs[c.Owner.ClientID] == c.Owner.Epoch
	c.runtime.mu.Unlock()
	return ok && current && attached
}

func humanInput(request coreprotocol.Request, input coreprotocol.InputRequest) provider.UserInput {
	return provider.UserInput{Text: input.Text, ReferenceIDs: slices.Clone(input.ReferenceIDs), ActivePaths: slices.Clone(input.ActivePaths), Origin: provider.OriginHuman, InternalID: "client-" + request.Owner.ClientID + "-" + request.ID}
}

func (a *actor) startJob(command actorCommand) bool {
	request := *command.request
	lifecycle := request.Method == coreprotocol.NewSession || request.Method == coreprotocol.OpenSession
	service, serviceKnown := a.prepareService(request)
	resource, resourceKnown := a.prepareResource(request)
	if resourceKnown {
		service, serviceKnown = resource, true
	}
	files, filesKnown := a.prepareFiles(request)
	if filesKnown {
		service, serviceKnown = files, true
	}
	references, referencesKnown := a.prepareReferences(request)
	if referencesKnown {
		service, serviceKnown = references, true
	}
	settings, settingsApply, settingsKnown := a.prepareSettings(request)
	if settingsKnown {
		service, serviceKnown = settings, true
	}
	upload, uploadKnown := a.prepareUpload(request)
	if uploadKnown {
		service, serviceKnown = upload, true
	}
	if !lifecycle && request.Method != coreprotocol.SubmitInput && request.Method != coreprotocol.ListSessions && !serviceKnown {
		return false
	}
	if a.inFlight >= 16 || lifecycle && a.lifecycle {
		command.result <- fault(request, "busy", "bounded workspace operation capacity is occupied; retry with a new request identity")
		return true
	}
	journal := request.Method != coreprotocol.ListSessions && (!serviceKnown || serviceMutation(request.Method) || resourceMutation(request.Method) || filesMutation(request.Method) || referenceMutation(request.Method) || settingsMutation(request.Method) || uploadMutation(request.Method))
	if journal {
		prior, fresh, err := a.receipts.begin(a.ctx, request)
		if err != nil {
			command.result <- fault(request, "receipt_failed", err.Error())
			return true
		}
		if !fresh {
			if prior.State == "completed" {
				command.result <- prior.Response
			} else {
				command.result <- fault(request, "outcome_"+prior.State, "operation cannot be replayed")
			}
			return true
		}
	}
	fail := func(result coreprotocol.Response) bool {
		a.finishJob(actorJobResult{command: command, apply: func() coreprotocol.Response { return result }, journal: journal})
		return true
	}
	var run func() func() coreprotocol.Response
	if serviceKnown {
		run = func() func() coreprotocol.Response {
			result := service(a.ctx)
			return func() coreprotocol.Response {
				a.applyService(request, result)
				if filesKnown {
					if err := a.applyFiles(request, result); err != nil {
						return fault(request, "publication_failed", err.Error())
					}
				}
				if settingsApply != nil {
					settingsApply(result)
				}
				if (resourceKnown || filesKnown || referencesKnown || settingsKnown || uploadKnown) && journal && result.Error == nil {
					a.changed(nil)
				}
				return result
			}
		}
	} else if lifecycle {
		var id string
		if request.Method == coreprotocol.OpenSession {
			var input coreprotocol.SessionRequest
			if err := decode(request.Payload, &input); err != nil {
				return fail(fault(request, "invalid_request", err.Error()))
			}
			if input.SessionID == "" {
				return fail(fault(request, "invalid_request", "session identity is required"))
			}
			id = input.SessionID
		} else {
			if err := decode(request.Payload, &struct{}{}); err != nil {
				return fail(fault(request, "invalid_request", err.Error()))
			}
		}
		op, err := a.ws.PrepareAgentSession(id)
		if err != nil {
			return fail(fault(request, "session_failed", err.Error()))
		}
		a.lifecycle = true
		run = func() func() coreprotocol.Response {
			result := op.Run(a.ctx)
			return func() coreprotocol.Response {
				out := a.ws.ApplyAgentSession(result)
				if out.Error != "" {
					return fault(request, "session_failed", out.Error)
				}
				a.changed([]string{out.OrchID})
				return response(request, out)
			}
		}
	} else if request.Method == coreprotocol.SubmitInput {
		var input coreprotocol.InputRequest
		if err := decode(request.Payload, &input); err != nil {
			return fail(fault(request, "invalid_request", err.Error()))
		}
		if strings.TrimSpace(input.Text) == "" {
			return fail(fault(request, "invalid_input", "only nonempty human input is accepted"))
		}
		value := humanInput(request, input)
		intent, err := a.ws.PrepareAgentInputAdmission(input.AgentID, value, input.DraftRevision)
		if err != nil {
			return fail(fault(request, "input_rejected", err.Error()))
		}
		owner := a.ws.SessionForAgent(input.AgentID)
		backend := a.ws.BackendSession(owner.SessionID)
		var labels []string
		if owner.RefStore != nil {
			for _, id := range input.ReferenceIDs {
				if ref, ok := owner.RefStore.GetReference(id); ok {
					labels = append(labels, ref.DisplayName())
				}
			}
		}
		run = func() func() coreprotocol.Response {
			result := backend.CommitInputAdmission(a.ctx, intent)
			return func() coreprotocol.Response {
				if a.ws.ApplyAgentInputAdmission(intent, result, value, labels) {
					a.changed([]string{input.AgentID})
				}
				out := coreprotocol.InputResult{Status: result.Status, Receipt: result.Receipt}
				if result.Err != nil {
					out.Error = result.Err.Error()
				}
				return response(request, out)
			}
		}
	} else {
		// Listing touches only the captured registry and filesystem. The workspace
		// root and persistence lifetime stay fixed until all workers have joined.
		root, db, logger := a.ws.RootPath, a.ws.WorkspaceDB, a.ws.Logger
		run = func() func() coreprotocol.Response {
			reader := &workspace.Workspace{RootPath: root, WorkspaceDB: db, Logger: logger}
			out := reader.ListSessions()
			return func() coreprotocol.Response { return response(request, out) }
		}
	}
	a.inFlight++
	if settingsKnown {
		a.settingsBusy = true
	}
	work := func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				a.jobs <- actorJobResult{command: command, admitted: true, lifecycle: lifecycle, settings: settingsKnown,
					apply: func() coreprotocol.Response {
						return fault(request, "outcome_uncertain", fmt.Sprintf("operation panicked: %v; inspect receipt before retrying", recovered))
					}}
			}
		}()
		apply := run()
		a.jobs <- actorJobResult{command: command, apply: apply, lifecycle: lifecycle, journal: journal, admitted: true, settings: settingsKnown}
	}
	if resourceKnown {
		if !a.resourceLane.submit(func(context.Context) { work() }) {
			a.finishJob(actorJobResult{command: command, admitted: true, journal: journal, apply: func() coreprotocol.Response { return fault(request, "cancelled", "resource owner is closing") }})
		}
	} else {
		go work()
	}
	return true
}

func (a *actor) finishJob(job actorJobResult) {
	// Validation failures were never admitted as workers.
	if job.lifecycle {
		a.lifecycle = false
	}
	if job.apply == nil {
		return
	}
	result := job.apply()
	result.Revision = a.revision
	if job.journal {
		if err := a.receipts.complete(context.Background(), *job.command.request, result); err != nil {
			result = fault(*job.command.request, "outcome_uncertain", "effect may have completed; receipt publication failed: "+err.Error())
		}
	}
	job.command.result <- result
	if job.admitted {
		a.inFlight--
	}
	if job.settings {
		a.settingsBusy = false
		for len(a.settingsQueue) > 0 {
			command := a.settingsQueue[0]
			a.settingsQueue[0] = actorCommand{}
			a.settingsQueue = a.settingsQueue[1:]
			if a.ctx.Err() != nil || !a.validClient(command.client) {
				command.result <- fault(*command.request, "detached", "settings owner is no longer attached")
				continue
			}
			if !a.startJob(command) {
				command.result <- a.execute(*command.request)
			}
			if a.settingsBusy {
				break
			}
		}
	}
}
