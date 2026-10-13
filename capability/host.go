package capability

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"time"

	"crude/mcpclient"
	captypes "crude/types/capability"
	"crude/types/control"
)

func (s *Service) SetHostDispatcher(host captypes.HostDispatcher) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.host = host
}
func (s *Service) hostRequest(ctx context.Context, actor captypes.RuntimeActor, config captypes.ConnectionConfig, operation string, request captypes.HostRequest) (captypes.HostReply, error) {
	if err := s.validateRuntime(ctx, actor); err != nil {
		return captypes.HostReply{}, err
	}
	s.mu.RLock()
	host := s.host
	s.mu.RUnlock()
	if host == nil {
		return captypes.HostReply{}, errors.New("exact connected MCP shell host unavailable")
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return captypes.HostReply{}, err
	}
	request.ID = hex.EncodeToString(id[:])
	request.Actor = actor
	request.WorkspaceID = actor.WorkspaceID
	request.Connection = config
	request.Operation = operation
	if actor.ShellOwner == nil {
		return captypes.HostReply{}, errors.New("MCP shell host has no admitted shell owner")
	}
	if err := captypes.ValidateCapabilityHostRequest(request, actor.WorkspaceID, *actor.ShellOwner); err != nil {
		return captypes.HostReply{}, err
	}
	reply, err := host.DispatchHost(ctx, request)
	if err == nil && reply.Error != "" {
		err = errors.New(reply.Error)
	}
	if err == nil && reply.ID != request.ID {
		err = errors.New("MCP host response identity mismatch")
	}
	if err != nil && operation == "call" && request.Tool.Effect != captypes.EffectRead {
		reply.Result.Unknown = true
		reply.Result.IsError = true
	}
	return reply, err
}

// HostWorker owns a single connected shell epoch. Construction is passive.
// Handle executes on a worker, never Bubble Tea Update. Close cancels all leases.
type HostWorker struct {
	mu           sync.Mutex
	owner        control.ShellOwner
	workspace    string
	root         string
	ctx          context.Context
	cancel       context.CancelFunc
	managers     map[string]*mcpclient.Manager
	receipts     map[string]*workerReceipt
	closed       bool
	receiptBytes int
	outstanding  int
}
type workerReceipt struct {
	request []byte
	done    chan struct{}
	reply   captypes.HostReply
}

func NewHostWorker(ctx context.Context, owner control.ShellOwner, workspace, root string) *HostWorker {
	ctx, cancel := context.WithCancel(ctx)
	return &HostWorker{owner: owner, workspace: workspace, root: root, ctx: ctx, cancel: cancel, managers: map[string]*mcpclient.Manager{}, receipts: map[string]*workerReceipt{}}
}
func (w *HostWorker) Handle(ctx context.Context, r captypes.HostRequest) captypes.HostReply {
	reply := captypes.HostReply{ID: r.ID}
	if err := captypes.ValidateCapabilityHostRequest(r, w.workspace, w.owner); err != nil {
		reply.Error = err.Error()
		return reply
	}
	if r.Operation == "call" && !actorAllows(r.Actor, r.Tool.Name, r.Tool.Effect) {
		reply.Error = "MCP call exceeds exact actor ceiling"
		return reply
	}
	if r.Operation == "connect" && !actorAllows(r.Actor, "capability_activate", captypes.EffectExecute) {
		reply.Error = "MCP launch exceeds exact actor ceiling"
		return reply
	}
	encoded, _ := json.Marshal(r)
	w.mu.Lock()
	if w.closed || w.ctx.Err() != nil {
		w.mu.Unlock()
		reply.Error = "MCP shell lease ended"
		return reply
	}
	if prior, exists := w.receipts[r.ID]; exists {
		w.mu.Unlock()
		if !bytes.Equal(prior.request, encoded) {
			reply.Error = "MCP request identity changed"
			return reply
		}
		select {
		case <-prior.done:
			w.mu.Lock()
			reply = prior.reply
			w.mu.Unlock()
			return reply
		case <-ctx.Done():
			reply.Error = ctx.Err().Error()
			reply.Result.Unknown = r.Operation == "call" && r.Tool.Effect != captypes.EffectRead
			return reply
		}
	}
	if len(w.receipts) >= 256 || w.outstanding >= 16 || w.receiptBytes+len(encoded) > 4<<20 {
		w.mu.Unlock()
		reply.Error = "MCP shell receipt capacity exhausted"
		return reply
	}
	receipt := &workerReceipt{request: encoded, done: make(chan struct{})}
	w.receipts[r.ID] = receipt
	w.receiptBytes += len(encoded)
	w.outstanding++
	key := taskKey(r.Actor) + "\x00" + r.Connection.ID
	manager := w.managers[key]
	if manager == nil {
		manager = mcpclient.New(w.root)
		w.managers[key] = manager
	}
	w.mu.Unlock()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(w.ctx, cancel)
	defer stop()
	// Resolve env: references on this shell only. Never forward core credential bytes.
	config := r.Connection
	config.Host = "core"
	manager.Configure(map[string]captypes.ConnectionConfig{config.ID: config})
	var err error
	switch r.Operation {
	case "connect":
		if r.Authorized {
			err = manager.ConnectAuthorized(ctx, config.ID, config)
		} else {
			err = manager.Connect(ctx, config.ID)
		}
	case "prepare":
		for _, state := range manager.States() {
			if state.ID == config.ID && state.State == "connected" && state.Cached {
				err = manager.Refresh(ctx, config.ID)
			}
		}
	case "inventory":
		err = manager.Refresh(ctx, config.ID)
	case "call":
		if r.Grant != nil {
			reply.Result, err = manager.CallAuthorized(ctx, r.Tool, r.Arguments, config, *r.Grant)
		} else {
			reply.Result, err = manager.Call(ctx, r.Tool, r.Arguments)
		}
	case "readResource":
		reply.Result, err = manager.ReadResource(ctx, config.ID, r.URI)
	case "getPrompt":
		reply.Result, err = manager.GetPrompt(ctx, config.ID, r.Prompt, r.PromptArguments)
	case "disconnect":
		err = manager.Disconnect(config.ID)
		w.mu.Lock()
		if w.managers[key] == manager {
			delete(w.managers, key)
		}
		w.mu.Unlock()
		manager.Close()
	}
	reply.States = manager.States()
	if err != nil {
		reply.Error = err.Error()
	}
	w.mu.Lock()
	if w.closed || w.ctx.Err() != nil {
		reply.Error = "MCP shell lease ended; outcome must not be replayed"
		reply.Result.Unknown = r.Operation == "call" && r.Tool.Effect != captypes.EffectRead
		reply.Result.IsError = true
	}
	replyBytes, _ := json.Marshal(reply)
	if w.receiptBytes+len(replyBytes) > 4<<20 {
		reply = captypes.HostReply{ID: r.ID, Result: captypes.Result{Unknown: r.Operation == "call" && r.Tool.Effect != captypes.EffectRead, IsError: true}, Error: "MCP retained reply exceeds shell receipt budget"}
		replyBytes, _ = json.Marshal(reply)
	}
	w.receiptBytes += len(replyBytes)
	receipt.reply = reply
	w.receipts[r.ID] = receipt
	close(receipt.done)
	w.outstanding--
	if captypes.HostRequestReadOnly(r) {
		delete(w.receipts, r.ID)
		w.receiptBytes -= len(encoded) + len(replyBytes)
	}
	w.mu.Unlock()
	return reply
}
func (w *HostWorker) Close() {
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return
	}
	w.closed = true
	w.cancel()
	managers := w.managers
	w.managers = map[string]*mcpclient.Manager{}
	w.mu.Unlock()
	for _, manager := range managers {
		manager.Close()
	}
}

type actorClient interface {
	Connect(context.Context, string) error
	ConnectAuthorized(context.Context, string, captypes.ConnectionConfig) error
	Refresh(context.Context, string) error
	States() []captypes.ConnectionState
	Call(context.Context, captypes.Tool, map[string]any) (captypes.Result, error)
	CallAuthorized(context.Context, captypes.Tool, map[string]any, captypes.ConnectionConfig, captypes.ToolGrant) (captypes.Result, error)
	ReadResource(context.Context, string, string) (captypes.Result, error)
	GetPrompt(context.Context, string, string, map[string]string) (captypes.Result, error)
}

// routedClient composes local and exact-shell connections under one resolver.
type routedClient struct {
	s     *Service
	actor captypes.RuntimeActor
	local *mcpclient.Manager
}

func (c routedClient) config(id string) captypes.ConnectionConfig {
	c.s.mu.RLock()
	defer c.s.mu.RUnlock()
	return captypes.Clone(c.s.settings).Connections[id]
}
func (c routedClient) remote(ctx context.Context, id, op string, r captypes.HostRequest) (captypes.HostReply, error) {
	reply, err := c.s.hostRequest(ctx, c.actor, c.config(id), op, r)
	if err == nil {
		c.s.mu.Lock()
		if c.s.hostStates[taskKey(c.actor)] == nil {
			c.s.hostStates[taskKey(c.actor)] = map[string]captypes.ConnectionState{}
		}
		for _, state := range reply.States {
			c.s.hostStates[taskKey(c.actor)][state.ID] = state
		}
		c.s.mu.Unlock()
	}
	return reply, err
}
func (c routedClient) Connect(ctx context.Context, id string) error {
	if c.config(id).Host != "shell" {
		return c.local.Connect(ctx, id)
	}
	_, err := c.remote(ctx, id, "connect", captypes.HostRequest{})
	return err
}
func (c routedClient) ConnectAuthorized(ctx context.Context, id string, config captypes.ConnectionConfig) error {
	if config.Host != "shell" {
		return c.local.ConnectAuthorized(ctx, id, config)
	}
	if !reflect.DeepEqual(config, c.config(id)) {
		return errors.New("MCP launch preimage changed")
	}
	_, err := c.remote(ctx, id, "connect", captypes.HostRequest{Authorized: true})
	return err
}
func (c routedClient) Refresh(ctx context.Context, id string) error {
	if c.config(id).Host != "shell" {
		return c.local.Refresh(ctx, id)
	}
	_, err := c.remote(ctx, id, "inventory", captypes.HostRequest{})
	return err
}
func (c routedClient) States() []captypes.ConnectionState {
	states := c.local.States()
	c.s.mu.RLock()
	defer c.s.mu.RUnlock()
	for i, state := range states {
		if c.s.settings.Connections[state.ID].Host == "shell" {
			if remote, ok := c.s.hostStates[taskKey(c.actor)][state.ID]; ok {
				states[i] = remote
			}
		}
	}
	return states
}
func (c routedClient) Call(ctx context.Context, t captypes.Tool, args map[string]any) (captypes.Result, error) {
	if c.config(t.ConnectionID).Host != "shell" {
		return c.local.Call(ctx, t, args)
	}
	r, e := c.remote(ctx, t.ConnectionID, "call", captypes.HostRequest{Tool: t, Arguments: args})
	return r.Result, e
}
func (c routedClient) CallAuthorized(ctx context.Context, t captypes.Tool, args map[string]any, config captypes.ConnectionConfig, g captypes.ToolGrant) (captypes.Result, error) {
	if config.Host != "shell" {
		return c.local.CallAuthorized(ctx, t, args, config, g)
	}
	if !reflect.DeepEqual(config, c.config(t.ConnectionID)) {
		return captypes.Result{}, errors.New("MCP connection changed")
	}
	r, e := c.remote(ctx, t.ConnectionID, "call", captypes.HostRequest{Tool: t, Arguments: args, Grant: &g})
	return r.Result, e
}
func (c routedClient) ReadResource(ctx context.Context, id, uri string) (captypes.Result, error) {
	if c.config(id).Host != "shell" {
		return c.local.ReadResource(ctx, id, uri)
	}
	r, e := c.remote(ctx, id, "readResource", captypes.HostRequest{URI: uri})
	return r.Result, e
}
func (c routedClient) GetPrompt(ctx context.Context, id, name string, args map[string]string) (captypes.Result, error) {
	if c.config(id).Host != "shell" {
		return c.local.GetPrompt(ctx, id, name, args)
	}
	r, e := c.remote(ctx, id, "getPrompt", captypes.HostRequest{Prompt: name, PromptArguments: args})
	return r.Result, e
}

func (s *Service) prepareHosts(ctx context.Context, actor captypes.RuntimeActor) error {
	s.mu.RLock()
	var configs []captypes.ConnectionConfig
	for id := range s.hostStates[taskKey(actor)] {
		configs = append(configs, captypes.Clone(s.settings).Connections[id])
	}
	s.mu.RUnlock()
	for _, config := range configs {
		if config.Host != "shell" {
			continue
		}
		c := routedClient{s: s, actor: actor}
		if _, err := c.remote(ctx, config.ID, "prepare", captypes.HostRequest{}); err != nil {
			s.mu.Lock()
			delete(s.hostStates[taskKey(actor)], config.ID)
			s.mu.Unlock()
			return err
		}
	}
	return nil
}

func (s *Service) revokeHostRun(actor captypes.RuntimeActor) {
	s.mu.RLock()
	host := s.host
	var configs []captypes.ConnectionConfig
	for id := range s.hostStates[taskKey(actor)] {
		configs = append(configs, captypes.Clone(s.settings).Connections[id])
	}
	s.mu.RUnlock()
	if host == nil || actor.ShellOwner == nil {
		return
	}
	for _, config := range configs {
		var id [16]byte
		if _, err := rand.Read(id[:]); err != nil {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_, _ = host.DispatchHost(ctx, captypes.HostRequest{ID: hex.EncodeToString(id[:]), WorkspaceID: actor.WorkspaceID, Actor: actor, Operation: "disconnect", Connection: config})
		cancel()
	}
}
