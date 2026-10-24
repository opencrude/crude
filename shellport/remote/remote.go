// Package remote is the connected shell's implementation of the shell port:
// a client of the per-user core. Shell reads come from replicas owned by the
// shell's Update goroutine; mutations are typed core requests.
package remote

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"time"

	"crude/coreclient"
	"crude/coreprotocol"
	"crude/provider"
	"crude/sessiondb"
	"crude/shellport"
	"crude/terminalvt"
	"crude/workspace"
)

// callTimeout bounds one ordinary core request.
const callTimeout = 30 * time.Second

// Dialer opens a fresh, negotiated connection to the core.
type Dialer func(ctx context.Context, clientID string) (*coreclient.Client, error)

// Options configures one attachment.
type Options struct {
	Root   string
	Dial   Dialer
	Logger workspace.Logger
	// Namespace separates private drafts for different remote core authorities.
	Namespace string
	// ClientID is stable for an exclusively held private profile. Empty allocates
	// a transient identity (tests and explicit non-profile attachments).
	ClientID string
}

// Workspace is one shell's attachment to a retained core workspace.
type Workspace struct {
	dial       Dialer
	logger     workspace.Logger
	namespace  string
	invocation coreprotocol.InvocationSelection

	// conn is the current connection; reconnect replaces it. Any goroutine may
	// issue a request through call().
	connMu         sync.Mutex
	client         *coreclient.Client
	id             string
	root           string
	clientID       string
	profileID      string
	controlEnabled bool
	controlClient  *coreclient.Client
	ctx            context.Context
	cancel         context.CancelFunc

	// view is the replica. Only the shell's Update goroutine touches it after
	// Attach returns (ApplyReplicaChanges and the read accessors).
	view *coreclient.WorkspaceView

	busMu     sync.Mutex
	bus       *workspace.EventBus
	busSet    chan struct{}
	startOnce sync.Once

	topicMu            sync.Mutex
	topics             map[string]*replicaTopic
	topicClients       map[string]*coreclient.Client // UI-owned revision domains
	topicCursors       map[string]uint64
	topicUpdated       chan struct{}
	auxMu              sync.RWMutex
	gitState           *coreprotocol.GitSnapshotResult
	gitError           error
	boardError         error
	boardState         *coreprotocol.BoardSnapshotResult
	initialized        bool
	liveDocumentMu     sync.Mutex
	liveDocumentTopics map[string]*liveDocumentTopic
	commands           commandQueue
	status             *statusReplica
	terminals          *terminalReplica
	settings           *settingsReplica
	settingsMu         sync.RWMutex
	lsp                *lspReplica
	documents          *documentService
	startup            coreprotocol.StartupResult
	registryMu         sync.RWMutex
	registry           []sessiondb.SessionMeta
	searchMu           sync.Mutex
	searchSeq          uint64
	searchCancel       context.CancelFunc

	// admissions holds submitted input between Prepare (Update goroutine) and
	// Commit (a command goroutine).
	admissionsMu sync.Mutex
	admissions   map[*workspace.InputAdmissionIntent]provider.UserInput

	stop     chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
}

var _ shellport.Workspace = (*Workspace)(nil)
var _ shellport.Replica = (*Workspace)(nil)

// Attach connects a shell to the core workspace rooted at opts.Root. The
// returned replica already reflects the attach snapshot.
func Attach(ctx context.Context, opts Options) (*Workspace, error) {
	if opts.Dial == nil || opts.Root == "" {
		return nil, errors.New("remote attach needs a dialer and a project root")
	}
	if opts.Logger == nil {
		opts.Logger = nopLogger{}
	}
	clientID := opts.ClientID
	if clientID == "" {
		clientID = randomID()
	} else {
		clientID = fmt.Sprintf("%x", sha256.Sum256([]byte(clientID+"\x00"+opts.Root)))
	}
	client, err := opts.Dial(ctx, clientID)
	if err != nil {
		return nil, err
	}
	attached, err := attach(ctx, client, opts.Root)
	if err != nil {
		_ = client.Close()
		return nil, err
	}
	view, err := coreclient.NewWorkspaceView(client, attached.View)
	if err != nil {
		_ = client.Close()
		return nil, err
	}
	view.Logger = opts.Logger
	lifeCtx, lifeCancel := context.WithCancel(context.Background())
	w := &Workspace{
		dial: opts.Dial, logger: opts.Logger, namespace: opts.Namespace,
		client: client, id: attached.WorkspaceID, root: attached.View.Root,
		clientID: clientID, ctx: lifeCtx, cancel: lifeCancel,
		profileID: opts.ClientID,
		view:      view, busSet: make(chan struct{}),
		stop: make(chan struct{}), topics: make(map[string]*replicaTopic),
		topicClients: make(map[string]*coreclient.Client), topicCursors: make(map[string]uint64),
	}
	w.status = newStatusReplica(w)
	w.terminals = &terminalReplica{w: w}
	w.settings = &settingsReplica{w: w}
	w.lsp = newLSPReplica(w)
	w.documents = newDocumentService(w)
	if err := w.call(ctx, coreprotocol.ListSessions, &coreprotocol.Empty{}, (*coreprotocol.SessionRegistry)(&w.registry)); err != nil {
		lifeCancel()
		_ = client.Close()
		return nil, fmt.Errorf("load session registry: %w", err)
	}
	settings, err := w.settings.load(ctx)
	if err != nil {
		lifeCancel()
		_ = client.Close()
		return nil, fmt.Errorf("load workspace settings: %w", err)
	}
	w.settings.install(settings)
	if err := w.call(ctx, coreprotocol.WorkspaceStartup, &coreprotocol.Empty{}, &w.startup); err != nil {
		lifeCancel()
		_ = client.Close()
		return nil, fmt.Errorf("load startup diagnostics: %w", err)
	}
	if snapshot, err := w.status.fetch(); err == nil {
		w.status.install(snapshot)
	}
	if infos, err := w.terminals.fetch(ctx); err == nil {
		w.terminals.install(infos)
	}
	if snapshot, err := w.lsp.fetch(ctx); err == nil {
		w.lsp.install(snapshot)
	}
	w.admissions = make(map[*workspace.InputAdmissionIntent]provider.UserInput)
	w.commands.init()
	w.wg.Add(2)
	go w.collect(client)
	w.startTopics(client)
	go w.runCommands()
	return w, nil
}

func attach(ctx context.Context, client *coreclient.Client, root string) (coreprotocol.AttachResult, error) {
	var result coreprotocol.AttachResult
	err := client.Call(ctx, "", coreprotocol.Attach, coreprotocol.AttachRequest{Root: root}, &result)
	if err == nil && result.WorkspaceID == "" {
		err = errors.New("core returned no workspace")
	}
	return result, err
}

// Close detaches this shell. The core keeps the workspace and its work.
func (w *Workspace) Close() error {
	w.stopOnce.Do(func() {
		w.connMu.Lock()
		w.cancel()
		w.connMu.Unlock()
		close(w.stop)
		w.CancelProjectSearch()
		w.documents.Close()
	})
	w.connMu.Lock()
	client := w.client
	w.connMu.Unlock()
	var err error
	if client != nil {
		err = client.Close()
	}
	w.wg.Wait()
	return err
}

// conn returns the current connection and workspace identity.
func (w *Workspace) conn() (*coreclient.Client, string) {
	w.connMu.Lock()
	defer w.connMu.Unlock()
	return w.client, w.id
}

// rpc returns a request-only view bound to the current connection. Workers
// must never use the replica view, whose fields belong to the Update goroutine.
func (w *Workspace) rpc() *coreclient.WorkspaceView {
	client, id := w.conn()
	return &coreclient.WorkspaceView{Client: client, ID: id, RootPath: w.root}
}

// call performs one typed request on the current connection.
func (w *Workspace) call(ctx context.Context, method string, in coreprotocol.Value, out coreprotocol.Target) error {
	client, id := w.conn()
	if client == nil {
		return errDisconnected
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(w.ctx, cancel)
	defer stop()
	return client.Call(ctx, id, method, in, out)
}

// callWithProgress delivers the command's partial output as it arrives.
// Cancelling the await sends a typed cancellation for that request only,
// never a replay.
func (w *Workspace) callWithProgress(ctx context.Context, method string, in coreprotocol.Value, out coreprotocol.Target, onProgress func(string)) error {
	client, id := w.conn()
	if client == nil {
		return errDisconnected
	}
	requestID := randomID()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(w.ctx, cancel)
	defer stop()
	_, err := client.CallWith(ctx, id, method, in, out, coreclient.CallOptions{ID: requestID, Progress: onProgress})
	if err != nil && ctx.Err() != nil {
		cancelCtx, cancel := context.WithTimeout(context.Background(), callTimeout)
		defer cancel()
		_ = client.Call(cancelCtx, id, coreprotocol.CancelRequest, &coreprotocol.CancelRequestPayload{RequestID: requestID}, &coreprotocol.BoolResult{})
	}
	return err
}

var errDisconnected = errors.New("not connected to the crude core")

func (w *Workspace) post(batch any) {
	w.send(shellport.ReplicaChangesMsg{Owner: w, Batch: batch})
}

func (w *Workspace) send(msg any) {
	select {
	case <-w.busSet:
	case <-w.stop:
		return
	}
	w.busMu.Lock()
	bus := w.bus
	w.busMu.Unlock()
	if bus != nil {
		bus.Send(msg)
	}
}

// SetEventBus connects the shell's FIFO. Replica batches wait for it.
func (w *Workspace) SetEventBus(bus *workspace.EventBus) {
	w.busMu.Lock()
	w.bus = bus
	w.busMu.Unlock()
}

func (w *Workspace) StartReplica() {
	w.startOnce.Do(func() { close(w.busSet) })
	w.StartShellControl()
	w.StartCapabilityHost()
}

// collect drains one connection's private effects. When the connection fails it
// reconnects, re-attaches and requests a snapshot; work in the core continues.
func (w *Workspace) collect(client *coreclient.Client) {
	defer w.wg.Done()
	for {
		connected := true
		for connected {
			select {
			case effect, ok := <-client.PrivateEffects():
				if !ok {
					connected = false
					continue
				}
				w.post(privateEffectsBatch{client: client, effect: effect})
			case <-client.Done():
				connected = false
			case <-w.stop:
				return
			}
		}
		select {
		case <-w.stop:
			return
		default:
		}
		w.logger.Warn("REMOTE", "connection_lost", "error", fmt.Sprint(client.Err()))
		next, ok := w.reconnect()
		if !ok {
			return
		}
		client = next
	}
}

func (w *Workspace) reconnect() (*coreclient.Client, bool) {
	for delay := 100 * time.Millisecond; ; delay = min(delay*2, 5*time.Second) {
		select {
		case <-w.stop:
			return nil, false
		case <-time.After(delay):
		}
		ctx, cancel := context.WithTimeout(w.ctx, callTimeout)
		client, err := w.dial(ctx, w.clientID)
		if err == nil {
			var attached coreprotocol.AttachResult
			attached, err = attach(ctx, client, w.root)
			if err == nil {
				cancel()
				w.connMu.Lock()
				w.client, w.id = client, attached.WorkspaceID
				w.startControlLocked(client, attached.WorkspaceID)
				w.startCapabilityHostLocked(client, attached.WorkspaceID)
				w.connMu.Unlock()
				w.logger.Info("REMOTE", "reconnected", "workspace", attached.WorkspaceID)
				w.startTopics(client)
				return client, true
			}
			_ = client.Close()
		}
		cancel()
		w.logger.Warn("REMOTE", "reconnect_failed", "error", err.Error())
	}
}

// Each topic has one cancellation-owned generation. Explicit refreshes reset the
// subscription, never fetch state; cancellation fences already queued UI batches.
type replicaTopic struct {
	cancel  context.CancelFunc
	applied bool // protected by topicMu; set only after Update installs a value
}
type privateEffectsBatch struct {
	client *coreclient.Client
	effect coreclient.PrivateEffect
}

type publicationBatch struct {
	client *coreclient.Client
	ctx    context.Context
	topic  string
	header coreprotocol.TopicHeader
	update coreprotocol.WorkspaceUpdate
	state  coreprotocol.WorkspaceState
	done   chan error
	owner  *replicaTopic
}

var replicaTopics = []string{coreprotocol.TopicWorkspace, coreprotocol.TopicStatus,
	coreprotocol.TopicSettings, coreprotocol.TopicLSP, coreprotocol.TopicTerminalInventory,
	coreprotocol.TopicRegistry, coreprotocol.TopicGit, coreprotocol.TopicBoard}

func (w *Workspace) startTopics(client *coreclient.Client) {
	w.topicMu.Lock()
	for _, topic := range w.topics {
		topic.cancel()
	}
	w.topics = make(map[string]*replicaTopic)
	w.topicMu.Unlock()
	for _, name := range replicaTopics {
		w.wg.Add(1)
		go w.followTopic(client, name)
	}
	w.startDocumentTopics(client)
}

func (w *Workspace) followTopic(client *coreclient.Client, name string) {
	defer w.wg.Done()
	for {
		ctx, cancel := context.WithCancel(w.ctx)
		token := &replicaTopic{cancel: cancel}
		w.connMu.Lock()
		current := w.client == client
		id := w.id
		w.topicMu.Lock()
		if current && ctx.Err() == nil {
			w.topics[name] = token
			w.notifyTopicUpdated()
		}
		w.topicMu.Unlock()
		w.connMu.Unlock()
		if !current || ctx.Err() != nil {
			cancel()
			return
		}
		failures := 0
		sub := client.Subscribe(ctx, id, name, "", func(header coreprotocol.TopicHeader, body []byte) error {
			batch := publicationBatch{client: client, ctx: ctx, topic: name, header: header, done: make(chan error, 1), owner: token}
			var err error
			if name == coreprotocol.TopicWorkspace {
				err = coreprotocol.Decode(body, &batch.update)
			} else {
				err = coreprotocol.Decode(body, &batch.state)
			}
			if err != nil {
				w.reportFailure("Decode "+name, err)
				failures++
				if failures > 1 {
					return fmt.Errorf("repeated malformed %s: %v", name, err)
				}
				return coreclient.ErrTopicReset
			}
			w.post(batch)
			select {
			case err := <-batch.done:
				if errors.Is(err, coreclient.ErrTopicReset) {
					failures++
					if failures > 1 {
						return fmt.Errorf("repeated rejected %s", name)
					}
				} else if err == nil {
					failures = 0
				}
				return err
			case <-ctx.Done():
				return ctx.Err()
			}
		})
		<-sub.Done()
		reset := ctx.Err() != nil && w.ctx.Err() == nil
		cancel()
		if !reset {
			return
		}
	}
}

func (w *Workspace) resetTopic(name string) {
	w.topicMu.Lock()
	if topic := w.topics[name]; topic != nil {
		topic.cancel()
	}
	w.topicMu.Unlock()
}

// notifyTopicUpdated runs with topicMu held. Waiters observe subscription
// replacement and UI application, never network delivery alone.
func (w *Workspace) notifyTopicUpdated() {
	if w.topicUpdated != nil {
		close(w.topicUpdated)
		w.topicUpdated = nil
	}
}

// refreshTopicApplied waits off Update for a post-request subscription value.
// Reset fences queued pre-save publications; only the topic installer remains a
// replica writer. Another reset/reconnect may supersede ours without losing the
// waiter, and attachment cancellation releases it during shutdown.
func (w *Workspace) refreshTopicApplied(ctx context.Context, name string) error {
	w.topicMu.Lock()
	before := w.topics[name]
	if before != nil {
		before.cancel()
	}
	for {
		current := w.topics[name]
		if current != nil && current != before && current.applied {
			w.topicMu.Unlock()
			return nil
		}
		if w.topicUpdated == nil {
			w.topicUpdated = make(chan struct{})
		}
		updated := w.topicUpdated
		w.topicMu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-updated:
		}
		w.topicMu.Lock()
	}
}
func (w *Workspace) requestResync() { w.resetTopic(coreprotocol.TopicWorkspace) }

// ApplyReplicaChanges validates and commits on Update. Completing done only after
// this transaction lets the subscription ACK the exact UI-applied cursor off UI.
func (w *Workspace) ApplyReplicaChanges(msg shellport.ReplicaChangesMsg) shellport.ReplicaApplied {
	var applied shellport.ReplicaApplied
	if msg.Owner != w {
		return applied
	}
	if private, ok := msg.Batch.(privateEffectsBatch); ok {
		w.connMu.Lock()
		defer w.connMu.Unlock()
		if private.client == w.client && w.ctx.Err() == nil {
			if private.effect.WorkspaceID != w.id {
				return applied
			}
			if private.effect.Error != "" {
				applied.Follow = append(applied.Follow, shellport.ReplicaFailureMsg{Owner: w, Operation: "Terminal clipboard", Error: private.effect.Error})
			} else if offer := private.effect.TerminalClipboard; offer != nil && w.terminals.ownsClipboard(*offer, private.client.Owner()) {
				applied.Follow = append(applied.Follow, workspace.TerminalEffectsMsg{ID: offer.TerminalID, Effects: terminalvt.Effects{Clipboard: offer.Clipboard}})
			}
		}
		return applied
	}
	batch, ok := msg.Batch.(publicationBatch)
	if !ok {
		return applied
	}
	var err error
	defer func() { batch.done <- err }()
	w.connMu.Lock()
	defer w.connMu.Unlock()
	w.topicMu.Lock()
	defer w.topicMu.Unlock()
	current := batch.client == w.client
	if !current || batch.ctx.Err() != nil {
		err = context.Canceled
		return applied
	}
	if previous, exists := w.topicCursors[batch.topic]; exists && w.topicClients[batch.topic] == batch.client && !batch.header.Reset && (batch.header.Seq <= previous || batch.topic == coreprotocol.TopicWorkspace && batch.header.Seq != previous+1) {
		err = fmt.Errorf("topic cursor mismatch: have %d, next %d", previous, batch.header.Seq)
	} else if batch.topic != coreprotocol.TopicWorkspace {
		applied, err = w.applyState(batch)
	} else {
		applied, err = w.applyPublication(batch)
	}
	if err == nil {
		w.topicCursors[batch.topic] = batch.header.Seq
		w.topicClients[batch.topic] = batch.client
		if batch.owner != nil && w.topics[batch.topic] == batch.owner && batch.state.Fault == nil {
			batch.owner.applied = true
			w.notifyTopicUpdated()
		}
	}
	if err != nil {
		applied = shellport.ReplicaApplied{Follow: []any{shellport.ReplicaFailureMsg{Owner: w, Operation: "Apply " + batch.topic, Error: err.Error()}}}
		err = coreclient.ErrTopicReset
	}
	return applied
}

func (w *Workspace) applyPublication(batch publicationBatch) (shellport.ReplicaApplied, error) {
	var applied shellport.ReplicaApplied
	u := batch.update
	if batch.header.Reset != (u.Snapshot != nil) {
		return applied, fmt.Errorf("snapshot/reset boundary mismatch")
	}
	known := make(map[string]bool)
	blocks := make(map[string]string)
	for _, node := range w.view.AgentMgr.AllNodes() {
		known[node.ID] = true
		blocks[node.ID] = node.State.PendingBlockID
	}
	var ids []string
	prompts := make(map[string]workspace.AgentSnapshot)
	if u.Snapshot != nil {
		if len(u.Changes.Agents)+len(u.Changes.Removed)+len(u.Conversations) != 0 || u.Sessions != nil {
			return applied, fmt.Errorf("snapshot mixed with incremental state")
		}
		if err := w.view.InstallPublicationSnapshot(*u.Snapshot, batch.client != w.view.Client); err != nil {
			return applied, err
		}
		w.view.Client = batch.client
		applied.Initial = !w.initialized
		w.initialized = true
		for _, a := range u.Snapshot.Agents {
			ids = append(ids, a.ID)
			prompts[a.ID] = a
		}
		applied.SessionsChanged = true
		applied.Follow = append(applied.Follow, shellport.ClientFilesChangedMsg{Owner: w, Changes: []coreprotocol.FileChange{{Resync: true}}})
	} else {
		if !w.initialized {
			return applied, fmt.Errorf("publication before snapshot")
		}
		var err error
		ids, err = w.view.ApplyPublication(u)
		if err != nil {
			return applied, err
		}
		for _, delta := range u.Changes.Agents {
			prompts[delta.State.ID] = delta.State
		}
		applied.SessionsChanged = u.Sessions != nil
	}
	// Effects and follow-up invalidations are emitted only after successful commit.
	applied.Notices = u.Notices
	applied.Reductions = u.Reductions
	for _, id := range ids {
		node := w.view.AgentMgr.Get(id)
		previous := blocks[id]
		if previous != "" && (node == nil || node.State.PendingBlockID != previous) {
			applied.Reductions = append(applied.Reductions, workspace.AgentReduction{Accepted: true, AgentID: id, Effects: workspace.AgentEffects{DismissPrompt: true, DismissBlockID: previous}})
		}
	}
	for _, node := range w.view.AgentMgr.Orchestrators() {
		if applied.Initial || !known[node.ID] {
			applied.Attached = append(applied.Attached, node.ID)
		}
	}
	for _, id := range ids {
		if node := w.view.AgentMgr.Get(id); node != nil {
			if prompt := prompts[id].Prompt; prompt != nil && (applied.Initial || prompt.BlockID != blocks[id]) {
				applied.Reductions = append(applied.Reductions, workspace.AgentReduction{Accepted: true, AgentID: id, Effects: workspace.AgentEffects{Prompt: prompt}})
			}
		}
	}
	if len(ids) > 0 {
		applied.Reductions = append(applied.Reductions, workspace.AgentReduction{Accepted: true, EvidenceOnly: true, ChangedAgents: ids})
	}
	if len(u.Files) > 0 {
		applied.Follow = append(applied.Follow, shellport.ClientFilesChangedMsg{Owner: w, Changes: u.Files})
	}
	for _, effect := range u.TerminalEffects {
		applied.Follow = append(applied.Follow, effect)
	}
	return applied, nil
}

func (w *Workspace) applyState(batch publicationBatch) (shellport.ReplicaApplied, error) {
	var applied shellport.ReplicaApplied
	s := batch.state
	if s.Fault != nil {
		if s.Registry != nil || s.Settings != nil || s.AgentDefaults != nil || s.Status != nil || s.LSP != nil || s.Terminals != nil || s.Git != nil || s.Board != nil {
			return applied, fmt.Errorf("state fault mixed with value")
		}
		w.auxMu.Lock()
		switch batch.topic {
		case coreprotocol.TopicGit:
			w.gitError = s.Fault
		case coreprotocol.TopicBoard:
			w.boardError = s.Fault
		}
		w.auxMu.Unlock()
		applied.Follow = append(applied.Follow, shellport.ReplicaFailureMsg{Owner: w, Operation: batch.topic, Error: s.Fault.Error()})
		return applied, nil
	}
	count := 0
	for _, present := range []bool{s.Registry != nil, s.Settings != nil, s.Status != nil, s.LSP != nil, s.Terminals != nil, s.Git != nil, s.Board != nil} {
		if present {
			count++
		}
	}
	if count != 1 || s.AgentDefaults != nil && batch.topic != coreprotocol.TopicSettings {
		return applied, fmt.Errorf("state topic requires exactly one projection")
	}
	valid := false
	switch batch.topic {
	case coreprotocol.TopicRegistry:
		if s.Registry != nil {
			w.registryMu.Lock()
			w.registry = *s.Registry
			w.registryMu.Unlock()
			valid = true
		}
	case coreprotocol.TopicSettings:
		if s.Settings != nil && s.AgentDefaults != nil {
			if w.settings.install(settingsSnapshot{fetch: *s.Settings, defaults: *s.AgentDefaults}) {
				applied.Follow = append(applied.Follow, shellport.SettingsChangedMsg{})
			}
			valid = true
		}
	case coreprotocol.TopicStatus:
		if s.Status != nil {
			if previous := w.topicClients[batch.topic]; previous != nil && previous != batch.client {
				w.status.resetRevision()
			}
			if w.status.install(*s.Status) {
				applied.Follow = append(applied.Follow, workspace.StatusCenterHydratedMsg{})
			}
			valid = true
		}
	case coreprotocol.TopicLSP:
		if s.LSP != nil {
			applied.Follow = append(applied.Follow, w.lsp.install(*s.LSP)...)
			valid = true
		}
	case coreprotocol.TopicTerminalInventory:
		if s.Terminals != nil {
			applied.Follow = append(applied.Follow, w.terminals.install(*s.Terminals)...)
			valid = true
		}
	case coreprotocol.TopicGit:
		if s.Git != nil {
			w.auxMu.Lock()
			changed := w.gitState == nil || w.gitError != nil || !reflect.DeepEqual(w.gitState, s.Git)
			w.gitState = s.Git
			w.gitError = nil
			w.auxMu.Unlock()
			if changed {
				applied.Follow = append(applied.Follow, shellport.GitSnapshotChangedMsg{Owner: w, Snapshot: s.Git.Snapshot})
			}
			valid = true
		}
	case coreprotocol.TopicBoard:
		if s.Board != nil {
			w.auxMu.Lock()
			w.boardState = s.Board
			w.boardError = nil
			w.auxMu.Unlock()
			valid = true
		}
	}
	if !valid {
		return shellport.ReplicaApplied{}, fmt.Errorf("wrong projection for %s", batch.topic)
	}

	return applied, nil
}

type nopLogger struct{}

func (nopLogger) Info(string, string, ...any)  {}
func (nopLogger) Warn(string, string, ...any)  {}
func (nopLogger) Error(string, string, ...any) {}
func (nopLogger) Debug(string, string, ...any) {}
