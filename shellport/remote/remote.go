// Package remote is the connected shell's implementation of the shell port:
// a client of the per-user core. Shell reads come from replicas owned by the
// shell's Update goroutine; mutations are typed core requests.
package remote

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"crude/coreclient"
	"crude/coreprotocol"
	"crude/provider"
	"crude/sessiondb"
	"crude/shellport"
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
	connMu    sync.Mutex
	client    *coreclient.Client
	id        string
	root      string
	clientID  string
	profileID string
	ctx       context.Context
	cancel    context.CancelFunc

	// view is the replica. Only the shell's Update goroutine touches it after
	// Attach returns (ApplyReplicaChanges and the read accessors).
	view *coreclient.WorkspaceView

	busMu     sync.Mutex
	bus       *workspace.EventBus
	busSet    chan struct{}
	startOnce sync.Once

	pending      pendingChanges
	resync       chan struct{}
	commands     commandQueue
	status       *statusReplica
	terminals    *terminalReplica
	settings     *settingsReplica
	settingsMu   sync.RWMutex
	lsp          *lspReplica
	documents    *documentService
	startup      coreprotocol.StartupResult
	registryMu   sync.RWMutex
	registry     []sessiondb.SessionMeta
	searchMu     sync.Mutex
	searchSeq    uint64
	searchCancel context.CancelFunc
	progressMu   sync.Mutex
	progress     map[string]func(string)

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
		resync: make(chan struct{}, 1), stop: make(chan struct{}),
	}
	w.status = newStatusReplica(w)
	w.terminals = &terminalReplica{w: w}
	w.settings = &settingsReplica{w: w}
	w.lsp = newLSPReplica(w)
	w.documents = newDocumentService(w)
	w.progress = make(map[string]func(string))
	if err := w.call(ctx, coreprotocol.ListSessions, struct{}{}, &w.registry); err != nil {
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
	if err := w.call(ctx, coreprotocol.WorkspaceStartup, struct{}{}, &w.startup); err != nil {
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
	w.pending.init()
	w.commands.init()
	w.wg.Add(3)
	go w.collect(client)
	go w.fetch(attached.View)
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
		w.cancel()
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
func (w *Workspace) call(ctx context.Context, method string, in, out any) error {
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

// callWithProgress registers the exact request before admission. Cancelling an
// await sends a typed cancellation for that request only, never a replay.
func (w *Workspace) callWithProgress(ctx context.Context, method string, in, out any, onProgress func(string)) error {
	client, id := w.conn()
	if client == nil {
		return errDisconnected
	}
	payload, err := json.Marshal(in)
	if err != nil {
		return err
	}
	requestID := randomID()
	w.progressMu.Lock()
	w.progress[requestID] = onProgress
	w.progressMu.Unlock()
	defer func() { w.progressMu.Lock(); delete(w.progress, requestID); w.progressMu.Unlock() }()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(w.ctx, cancel)
	defer stop()
	result, err := client.CallRequest(ctx, coreprotocol.Request{ID: requestID, Owner: client.Owner(), WorkspaceID: id, Method: method, Payload: payload})
	if err != nil {
		if ctx.Err() != nil {
			cancelCtx, cancel := context.WithTimeout(context.Background(), callTimeout)
			defer cancel()
			_ = client.Call(cancelCtx, id, coreprotocol.CancelRequest, coreprotocol.CancelRequestPayload{RequestID: requestID}, &coreprotocol.BoolResult{})
		}
		return err
	}
	if result.Error != nil {
		return result.Error
	}
	if out != nil {
		return json.Unmarshal(result.Payload, out)
	}
	return nil
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

func (w *Workspace) StartReplica() { w.startOnce.Do(func() { close(w.busSet) }) }

// pendingChanges coalesces change events while a fetch is in flight, so the
// connection's bounded event queue is always drained promptly.
type pendingChanges struct {
	mu          sync.Mutex
	ready       chan struct{}
	agents      map[string]bool
	documents   map[string]bool
	files       map[string]coreprotocol.FileChange
	filesResync bool
	reductions  []workspace.AgentReduction
	notices     []workspace.AgentNotice
	terminals   []coreprotocol.Change
	status      bool
	settings    bool
	sessions    bool
	lsp         bool
	resync      bool
}

func (p *pendingChanges) init() {
	p.ready = make(chan struct{}, 1)
	p.agents = make(map[string]bool)
	p.documents = make(map[string]bool)
	p.files = make(map[string]coreprotocol.FileChange)
}

func (p *pendingChanges) add(change coreprotocol.Change) {
	p.mu.Lock()
	if change.Resync {
		p.resync = true
	}
	for _, id := range change.Agents {
		p.agents[id] = true
	}
	for _, id := range change.Documents {
		p.documents[id] = true
	}
	for _, file := range change.Files {
		if file.Resync || len(p.files) >= 1024 {
			p.filesResync = true
			clear(p.files)
		} else if !p.filesResync {
			p.files[file.Path] = file
		}
	}
	p.reductions = append(p.reductions, change.Reductions...)
	p.notices = append(p.notices, change.Notices...)
	if len(change.Terminals) != 0 {
		p.terminals = append(p.terminals, change)
	}
	if change.Status {
		p.status = true
	}
	p.settings = p.settings || change.Settings
	p.sessions = p.sessions || change.Sessions
	p.lsp = p.lsp || change.LSP
	p.mu.Unlock()
	select {
	case p.ready <- struct{}{}:
	default:
	}
}

type pendingBatch struct {
	agents     []string
	documents  []string
	files      []coreprotocol.FileChange
	reductions []workspace.AgentReduction
	notices    []workspace.AgentNotice
	terminals  []coreprotocol.Change
	status     bool
	settings   bool
	sessions   bool
	lsp        bool
	resync     bool
}

func (p *pendingChanges) take() pendingBatch {
	p.mu.Lock()
	defer p.mu.Unlock()
	batch := pendingBatch{reductions: p.reductions, notices: p.notices, terminals: p.terminals, status: p.status, resync: p.resync, settings: p.settings, sessions: p.sessions, lsp: p.lsp}
	for id := range p.agents {
		batch.agents = append(batch.agents, id)
	}
	slices.Sort(batch.agents)
	for id := range p.documents {
		batch.documents = append(batch.documents, id)
	}
	slices.Sort(batch.documents)
	if p.filesResync {
		batch.files = []coreprotocol.FileChange{{Resync: true}}
	} else {
		for _, file := range p.files {
			batch.files = append(batch.files, file)
		}
		slices.SortFunc(batch.files, func(a, b coreprotocol.FileChange) int { return strings.Compare(a.Path, b.Path) })
	}
	p.agents = make(map[string]bool)
	p.documents = make(map[string]bool)
	clear(p.files)
	p.filesResync = false
	p.reductions, p.notices, p.terminals, p.status, p.resync = nil, nil, nil, false, false
	p.settings, p.sessions, p.lsp = false, false, false
	return batch
}

// collect drains one connection's events. When the connection fails it
// reconnects, re-attaches and requests a snapshot; work in the core continues.
func (w *Workspace) collect(client *coreclient.Client) {
	defer w.wg.Done()
	for {
		for change := range client.Events() {
			if change.Progress != nil {
				w.progressMu.Lock()
				fn := w.progress[change.Progress.RequestID]
				w.progressMu.Unlock()
				if fn != nil {
					fn(change.Progress.Text)
				}
				continue
			}
			w.pending.add(change)
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
				w.connMu.Unlock()
				w.logger.Info("REMOTE", "reconnected", "workspace", attached.WorkspaceID)
				w.pending.add(coreprotocol.Change{Resync: true})
				return client, true
			}
			_ = client.Close()
		}
		cancel()
		w.logger.Warn("REMOTE", "reconnect_failed", "error", err.Error())
	}
}

// Batches posted into the shell FIFO.
type attachBatch struct {
	snapshot coreprotocol.WorkspaceSnapshot
}
type snapshotBatch struct {
	snapshot coreprotocol.WorkspaceSnapshot
	client   *coreclient.Client
}
type deltaBatch struct {
	changes          coreprotocol.AgentChangesResult
	documents        []string
	documentsResync  bool
	files            []coreprotocol.FileChange
	fetched          bool
	reductions       []workspace.AgentReduction
	notices          []workspace.AgentNotice
	terminals        []coreprotocol.Change
	status           *statusSnapshot
	settings         *settingsSnapshot
	sessions         []coreprotocol.SessionSnapshot
	sessionsFetched  bool
	registry         []sessiondb.SessionMeta
	registryFetched  bool
	terminalInfos    []workspace.TerminalInfo
	terminalsFetched bool
	lsp              *coreprotocol.LSPSnapshot
}

// fetch turns coalesced change notifications into replica batches. Its
// conversation cursors are exactly what it delivered, in FIFO order, so they
// match the replica once the shell applies each batch.
func (w *Workspace) fetch(initial coreprotocol.WorkspaceSnapshot) {
	defer w.wg.Done()
	cursors := snapshotCursors(initial)
	w.post(attachBatch{snapshot: initial})
	if status, err := w.status.fetch(); err == nil {
		w.post(deltaBatch{status: &status})
	}
	for {
		select {
		case <-w.stop:
			return
		case <-w.resync:
			w.pending.add(coreprotocol.Change{Resync: true})
			continue
		case <-w.pending.ready:
		}
		batch := w.pending.take()
		if batch.resync {
			ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
			var snapshot coreprotocol.WorkspaceSnapshot
			err := w.call(ctx, coreprotocol.Snapshot, struct{}{}, &snapshot)
			cancel()
			if err != nil {
				w.logger.Warn("REMOTE", "snapshot_failed", "error", err.Error())
				continue
			}
			client, _ := w.conn()
			cursors = snapshotCursors(snapshot)
			w.post(snapshotBatch{snapshot: snapshot, client: client})
			batch.agents = nil
			batch.status = true
			batch.settings, batch.sessions, batch.lsp = true, true, true
			batch.terminals = append(batch.terminals, coreprotocol.Change{})
			batch.files = []coreprotocol.FileChange{{Resync: true}}
		}
		out := deltaBatch{reductions: batch.reductions, notices: batch.notices, terminals: batch.terminals,
			documents: batch.documents, documentsResync: batch.resync, files: batch.files}
		newAgent := false
		for _, id := range batch.agents {
			if _, known := cursors[id]; !known {
				newAgent = true
			}
		}
		if batch.sessions || newAgent {
			ctx, cancel := context.WithTimeout(w.ctx, callTimeout)
			err := w.call(ctx, coreprotocol.SessionSnapshots, struct{}{}, &out.sessions)
			cancel()
			if err == nil {
				out.sessionsFetched = true
			} else {
				w.requestResync()
			}
			ctx, cancel = context.WithTimeout(w.ctx, callTimeout)
			err = w.call(ctx, coreprotocol.ListSessions, struct{}{}, &out.registry)
			cancel()
			out.registryFetched = err == nil
		}
		if len(batch.agents) != 0 || len(batch.reductions) != 0 {
			ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
			err := w.call(ctx, coreprotocol.AgentChanges, coreprotocol.AgentChangesRequest{Revisions: maps.Clone(cursors)}, &out.changes)
			cancel()
			if err != nil {
				w.logger.Warn("REMOTE", "changes_failed", "error", err.Error())
				w.requestResync()
				out.reductions = nil
			} else {
				out.fetched = true
				for _, delta := range out.changes.Agents {
					cursors[delta.State.ID] = delta.Conversation.Revision
				}
				for _, id := range out.changes.Removed {
					delete(cursors, id)
				}
			}
		}
		if batch.status {
			if status, err := w.status.fetch(); err == nil {
				out.status = &status
			}
		}
		if batch.settings {
			ctx, cancel := context.WithTimeout(w.ctx, callTimeout)
			snapshot, err := w.settings.load(ctx)
			cancel()
			if err == nil {
				out.settings = &snapshot
			} else {
				w.requestResync()
			}
		}
		if len(batch.terminals) != 0 {
			ctx, cancel := context.WithTimeout(w.ctx, callTimeout)
			infos, err := w.terminals.fetch(ctx)
			cancel()
			if err == nil {
				out.terminalInfos, out.terminalsFetched = infos, true
			}
		}
		if batch.lsp {
			ctx, cancel := context.WithTimeout(w.ctx, callTimeout)
			snapshot, err := w.lsp.fetch(ctx)
			cancel()
			if err == nil {
				out.lsp = &snapshot
			}
		}
		w.post(out)
	}
}

func (w *Workspace) requestResync() {
	select {
	case w.resync <- struct{}{}:
	default:
	}
}

func snapshotCursors(snapshot coreprotocol.WorkspaceSnapshot) map[string]uint64 {
	cursors := make(map[string]uint64, len(snapshot.Agents))
	for _, agent := range snapshot.Agents {
		cursors[agent.ID] = agent.ConversationRevision
	}
	return cursors
}

// ApplyReplicaChanges installs one batch on the shell's Update goroutine.
func (w *Workspace) ApplyReplicaChanges(msg shellport.ReplicaChangesMsg) shellport.ReplicaApplied {
	var applied shellport.ReplicaApplied
	if msg.Owner != w {
		return applied
	}
	switch batch := msg.Batch.(type) {
	case attachBatch:
		applied = w.installSnapshot(batch.snapshot, true)
	case snapshotBatch:
		if batch.client != nil {
			w.view.Client = batch.client
		}
		applied = w.installSnapshot(batch.snapshot, false)
	case deltaBatch:
		applied.Notices = batch.notices
		if len(batch.files) != 0 {
			applied.Follow = append(applied.Follow, shellport.ClientFilesChangedMsg{Owner: w, Changes: slices.Clone(batch.files)})
		}
		if batch.documentsResync || len(batch.documents) != 0 {
			// An empty ID list means reconcile every open document after reconnect.
			applied.Follow = append(applied.Follow, shellport.LiveDocumentsChangedMsg{Owner: w, IDs: slices.Clone(batch.documents)})
		}
		if batch.registryFetched {
			w.registryMu.Lock()
			w.registry = batch.registry
			w.registryMu.Unlock()
		}
		if batch.sessionsFetched {
			applied.SessionsChanged = true
			if err := w.view.InstallSessions(batch.sessions); err != nil {
				w.requestResync()
			}
		}
		if batch.fetched {
			known := make(map[string]bool)
			blocks := make(map[string]string)
			for _, node := range w.view.AgentMgr.AllNodes() {
				blocks[node.ID] = node.State.PendingBlockID
			}
			for _, node := range w.view.AgentMgr.Orchestrators() {
				known[node.ID] = true
			}
			if _, err := w.view.ApplyChanges(batch.changes); err != nil {
				w.logger.Warn("REMOTE", "replica_rejected_changes", "error", err.Error())
				w.requestResync()
			} else {
				applied.Reductions = batch.reductions
				for _, delta := range batch.changes.Agents {
					id, previous := delta.State.ID, blocks[delta.State.ID]
					if previous != "" && (delta.State.Prompt == nil || delta.State.Prompt.BlockID != previous) {
						applied.Reductions = append(applied.Reductions, workspace.AgentReduction{Accepted: true, AgentID: id, Effects: workspace.AgentEffects{DismissPrompt: true, DismissBlockID: previous}})
					}
					if prompt := delta.State.Prompt; prompt != nil && prompt.BlockID != previous {
						applied.Reductions = append(applied.Reductions, workspace.AgentReduction{Accepted: true, AgentID: id, Effects: workspace.AgentEffects{Prompt: prompt}})
					}
				}
				for _, node := range w.view.AgentMgr.Orchestrators() {
					if !known[node.ID] {
						applied.Attached = append(applied.Attached, node.ID)
					}
				}
				applied.Reductions = append(applied.Reductions, workspace.AgentReduction{Accepted: true, EvidenceOnly: true, ChangedAgents: batchAgentIDs(batch.changes)})
			}
		}
		if batch.settings != nil && w.settings.install(*batch.settings) {
			applied.Follow = append(applied.Follow, shellport.SettingsChangedMsg{})
		}
		if batch.terminalsFetched {
			applied.Follow = append(applied.Follow, w.terminals.install(batch.terminalInfos)...)
		}
		if batch.lsp != nil {
			applied.Follow = append(applied.Follow, w.lsp.install(*batch.lsp)...)
		}
		if batch.status != nil {
			if w.status.install(*batch.status) {
				applied.Follow = append(applied.Follow, workspace.StatusCenterHydratedMsg{})
			}
		}
		for _, change := range batch.terminals {
			applied.Follow = append(applied.Follow, terminalMessages(change)...)
		}
	}
	return applied
}

// installSnapshot reconciles a snapshot and reports what the shell must
// surface: every root on attach, only unseen roots on resync, and every
// pending decision (prompt de-duplication is by exact block identity).
func (w *Workspace) installSnapshot(snapshot coreprotocol.WorkspaceSnapshot, initial bool) shellport.ReplicaApplied {
	var applied shellport.ReplicaApplied
	applied.Initial = initial
	known := make(map[string]bool)
	for _, node := range w.view.AgentMgr.AllNodes() {
		known[node.ID] = true
	}
	if err := w.view.InstallSnapshot(snapshot); err != nil {
		w.logger.Warn("REMOTE", "snapshot_rejected", "error", err.Error())
		return applied
	}
	for _, agent := range snapshot.Agents {
		if agent.ParentID == "" && (initial || !known[agent.ID]) {
			applied.Attached = append(applied.Attached, agent.ID)
		}
		reduction := workspace.AgentReduction{Accepted: true, AgentID: agent.ID, ChangedAgents: []string{agent.ID}}
		if agent.Prompt != nil {
			prompt := *agent.Prompt
			reduction.Effects.Prompt = &prompt
		}
		applied.Reductions = append(applied.Reductions, reduction)
	}
	return applied
}

func batchAgentIDs(changes coreprotocol.AgentChangesResult) []string {
	ids := make([]string, 0, len(changes.Agents))
	for _, delta := range changes.Agents {
		ids = append(ids, delta.State.ID)
	}
	return ids
}

type nopLogger struct{}

func (nopLogger) Info(string, string, ...any)  {}
func (nopLogger) Warn(string, string, ...any)  {}
func (nopLogger) Error(string, string, ...any) {}
func (nopLogger) Debug(string, string, ...any) {}
