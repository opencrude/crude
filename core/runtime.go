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
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"crude/agent"
	"crude/buildinfo"
	"crude/coreprotocol"
	"crude/corewire"
	"crude/document"
	"crude/history"
	"crude/metrics"
	"crude/provider"
	"crude/types"
	"crude/types/control"
	"crude/types/inspect"
	"crude/workspace"
)

// Factory belongs to the application configuration owner. It is invoked once
// for each canonical project; it must return a newly configured, unshared owner.
type Factory func(root string) (*workspace.Workspace, error)

type Runtime struct {
	mu               sync.Mutex
	workspaces       map[string]*actor
	clients          map[string]*Connection
	epochs           map[string]uint64
	factory          Factory
	receipts         *receiptJournal
	closed           bool
	creating         map[string]*workspaceCreation
	factoryWG        sync.WaitGroup
	history          *history.Store
	historyPath      string
	uploads          *ReferenceUploads
	startup          StartupOwner
	draining         bool
	stopping         bool // reversible lifecycle reservation, under mu
	stopMu           sync.Mutex
	idleRelease      func() // retained until runtime resource shutdown joins
	lifetime         lifetimePolicy
	storageGates     map[string]*workspaceStorageGate
	deletionPreviews map[string]workspaceDeletionPreview
	// sessions index live sessions by session ID for resumption.
	sessions map[string]*Connection
	grace    time.Duration
	remoteMu sync.Mutex // serializes remote admission with live revocation
	remote   *RemoteAccess
	// Explicit reads retain bounded immutable evidence, never domain state.
	inspections  inspect.Store
	diagnosticWG sync.WaitGroup // registered under mu; joined before workspace DB close
}

func New(factory Factory, receiptPath string) (*Runtime, error) {
	if factory == nil {
		return nil, errors.New("core workspace factory is required")
	}
	journal, err := openReceipts(receiptPath)
	if err != nil {
		return nil, err
	}
	return &Runtime{factory: factory, lifetime: lifetimePolicy{buildID: buildinfo.ExecutableID()}, receipts: journal, workspaces: make(map[string]*actor), clients: make(map[string]*Connection), epochs: make(map[string]uint64), creating: make(map[string]*workspaceCreation), sessions: make(map[string]*Connection), uploads: NewReferenceUploads(filepath.Join(filepath.Dir(receiptPath), "media-staging"))}, nil
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

// Connection is one client session. It outlives any one carrier for the
// session grace period. Domain replication belongs exclusively to topics;
// bounded private admission never delays the actor or evicts owner requests.
type Connection struct {
	sessionState
	runtime            *Runtime
	Owner              coreprotocol.Owner
	mu                 sync.Mutex
	attached           map[string]*actor
	privateEffects     chan coreprotocol.SessionEvent
	privateMu          sync.Mutex
	privateBytes       int
	done               chan struct{}
	ctx                context.Context
	cancel             context.CancelFunc
	closeComplete      chan struct{}
	closing            bool // protected by carrierMu
	controls           chan coreprotocol.ControlEnvelope
	controlRegistered  map[string]bool
	controlExternal    map[string]map[control.Effect]bool
	controlTargets     map[string]*control.ShellOwner
	controlReceipts    map[string]*controlReceipt
	controlOutstanding int
	controlBytes       int
	// Immutable after handshake admission. A resumed carrier never widens this
	// session's original authority.
	principal RemotePrincipal
	localHost bool
}

func (r *Runtime) Connect(clientID string) (*Connection, error) {
	if !validID(clientID) || strings.HasPrefix(clientID, "lsp:") {
		return nil, errors.New("invalid client identity")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed || r.draining || r.stopping {
		return nil, errors.New("core is closed")
	}
	if r.clients[clientID] != nil {
		return nil, errors.New("client identity already connected")
	}
	r.epochs[clientID]++
	c := &Connection{runtime: r, Owner: coreprotocol.Owner{ClientID: clientID, Epoch: r.epochs[clientID]}, attached: make(map[string]*actor), privateEffects: make(chan coreprotocol.SessionEvent, coreprotocol.SessionPrivateMaxOutstanding), done: make(chan struct{}), closeComplete: make(chan struct{})}
	c.ctx, c.cancel = context.WithCancel(context.Background())
	c.controls = make(chan coreprotocol.ControlEnvelope, coreprotocol.ControlMaxOutstanding)
	c.controlRegistered = make(map[string]bool)
	c.controlExternal = make(map[string]map[control.Effect]bool)
	c.controlTargets = make(map[string]*control.ShellOwner)
	c.controlReceipts = make(map[string]*controlReceipt)
	c.id, c.secret = randomSecret(), randomSecret()
	r.clients[clientID] = c
	r.sessions[c.id] = c
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

func (c *Connection) Done() <-chan struct{} { return c.done }

func (c *Connection) Close() {
	c.closeCarrier(nil)
}

// closeCarrier ends the session only if expected still owns it. A delayed FIN
// from a replaced carrier must never revoke the resumed owner's authority.
func (c *Connection) closeCarrier(expected corewire.Conn) {
	c.carrierMu.Lock()
	if c.closing {
		c.carrierMu.Unlock()
		<-c.closeComplete
		return
	}
	if expected != nil && c.carrier != expected {
		c.carrierMu.Unlock()
		return
	}
	c.closing = true
	c.cancel()
	close(c.done)
	if c.grace != nil {
		c.grace.Stop()
	}
	carrier, control := c.carrier, c.control
	c.carrier, c.control = nil, nil
	c.carrierMu.Unlock()
	defer close(c.closeComplete)
	c.mu.Lock()
	owners := c.attached
	c.attached = make(map[string]*actor)
	c.detachControlsLocked("")
	c.mu.Unlock()
	c.DetachCapabilityHosts("")
	for _, owner := range owners {
		c.stopWorkspaceTopics(owner.id)
		owner.unsubscribe(c)
	}
	c.runtime.mu.Lock()
	if c.runtime.clients[c.Owner.ClientID] == c {
		delete(c.runtime.clients, c.Owner.ClientID)
	}
	delete(c.runtime.sessions, c.id)
	for token, preview := range c.runtime.deletionPreviews {
		if preview.owner == c.Owner {
			delete(c.runtime.deletionPreviews, token)
		}
	}
	c.runtime.mu.Unlock()
	if carrier != nil {
		// Cleanup precedes the receipt. The peer consumes SessionEnd and
		// closes its carrier, proving the queued message reached it. A lost
		// or hostile peer cannot hold runtime shutdown indefinitely.
		timer := time.AfterFunc(time.Second, func() { _ = carrier.Close() })
		defer timer.Stop()
		if control != nil && send(control, &coreprotocol.SessionEnd{Reason: "session ended by the core"}) == nil {
			_ = control.CloseWrite()
			<-carrier.Done()
		}
		_ = carrier.Close()
	}
}

// offerClipboard admits the whole immutable offer or refuses it. It cannot
// evict an accepted offer, control request or capability-host request.
func (c *Connection) offerClipboard(workspaceID string, offer coreprotocol.TerminalClipboard) bool {
	c.privateMu.Lock()
	defer c.privateMu.Unlock()
	select {
	case <-c.done:
		return false
	default:
	}
	size := clipboardBytes(offer)
	if len(c.privateEffects) >= coreprotocol.SessionPrivateMaxOutstanding || size > coreprotocol.SessionPrivateMaxBytes-c.privateBytes {
		return false
	}
	offer.Clipboard = slices.Clone(offer.Clipboard)
	for i := range offer.Clipboard {
		offer.Clipboard[i] = bytes.Clone(offer.Clipboard[i])
	}
	c.privateBytes += size
	c.privateEffects <- coreprotocol.SessionEvent{WorkspaceID: workspaceID, TerminalClipboard: &offer}
	return true
}

func clipboardBytes(offer coreprotocol.TerminalClipboard) int {
	size := len(offer.TerminalID) + 32
	for _, value := range offer.Clipboard {
		size += len(value) + 8
	}
	return size
}

func (c *Connection) takePrivateEffect(event coreprotocol.SessionEvent) {
	c.privateMu.Lock()
	c.privateBytes -= clipboardBytes(*event.TerminalClipboard)
	c.privateMu.Unlock()
}

func (r *Runtime) Close() error {
	r.stopMu.Lock()
	defer r.stopMu.Unlock()
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
	var clientsClosed sync.WaitGroup
	for _, c := range clients {
		clientsClosed.Add(1)
		go func() { defer clientsClosed.Done(); c.Close() }()
	}
	clientsClosed.Wait()
	r.diagnosticWG.Wait()
	r.factoryWG.Wait()
	r.mu.Lock()
	actors := make([]*actor, 0, len(r.workspaces))
	for _, a := range r.workspaces {
		actors = append(actors, a)
	}
	r.mu.Unlock()
	for _, a := range actors {
		a.close()
	}
	if r.idleRelease != nil {
		r.idleRelease()
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
	if err := ctx.Err(); err != nil {
		return finish(fault(request, "cancelled", err.Error()))
	}
	c.runtime.mu.Lock()
	draining := c.runtime.draining || c.runtime.stopping
	c.runtime.mu.Unlock()
	if draining && request.Method != coreprotocol.CoreStatusMethod && request.Method != coreprotocol.GetReceipt {
		return finish(fault(request, "core_draining", "core is draining; only status and receipt observation remain available"))
	}
	select {
	case <-c.done:
		return finish(fault(request, "detached", "connection is closed"))
	default:
	}
	if len(request.Payload) > corewire.MaxMessage {
		return finish(fault(request, "invalid_request", "oversized request payload"))
	}
	if result, handled := c.dispatchRemote(ctx, request); handled {
		return finish(result)
	}
	if startupMethod(request.Method) {
		return c.dispatchStartup(ctx, request)
	}
	if request.Method == coreprotocol.Attach {
		return finish(c.attach(ctx, request))
	}
	if request.Method == coreprotocol.ApplicationPreferencesSave {
		var in coreprotocol.ApplicationPreferencesRequest
		if err := decodeRequest(request, &in); err != nil {
			return finish(fault(request, "invalid_request", err.Error()))
		}
		c.mu.Lock()
		host := c.localHost
		c.mu.Unlock()
		if !host {
			for _, edit := range in.Edits {
				if remotePreference(edit.Path) {
					return finish(fault(request, "forbidden", "remote admission settings require the authenticated local host"))
				}
			}
		}
	}
	if request.Method == coreprotocol.ControlShellRegister || request.Method == coreprotocol.ControlShellReply {
		return finish(c.controlMethod(request))
	}
	if request.Method == coreprotocol.CapabilityHostRegister || request.Method == coreprotocol.CapabilityHostReply {
		return finish(c.CapabilityHostMethod(request))
	}
	if request.Method == coreprotocol.QuestionControl {
		return c.QuestionControlMethod(ctx, request)
	}
	if request.Method == coreprotocol.SubmitControlInput {
		return c.ControlInputMethod(ctx, request)
	}
	if request.Method == coreprotocol.ControlClientRegister || request.Method == coreprotocol.ControlDescribe || request.Method == coreprotocol.ControlResolve || request.Method == coreprotocol.ControlExecute || request.Method == coreprotocol.ControlShells {
		return c.controlClientMethod(ctx, request)
	}
	if request.Method == coreprotocol.CoreMetricsMethod {
		var query metrics.Query
		if err := decodeRequest(request, &query); err != nil {
			return finish(fault(request, "invalid_request", err.Error()))
		}
		snapshot, err := metrics.TakeContext(ctx, query)
		if err != nil {
			return finish(fault(request, "metrics_unavailable", err.Error()))
		}
		return finish(response(request, snapshot))
	}
	if request.Method == coreprotocol.CorePerformanceInspect || request.Method == coreprotocol.CoreDiagnosticsInspect {
		return c.dispatchDiagnostics(ctx, request)
	}
	if request.Method == coreprotocol.CoreStatusMethod || request.Method == coreprotocol.CoreShutdownMethod {
		return finish(c.runtime.coreMethod(ctx, c, request))
	}
	if request.Method == coreprotocol.GetReceipt {
		var query coreprotocol.ReceiptRequest
		if err := decodeRequest(request, &query); err != nil {
			return finish(fault(request, "invalid_request", err.Error()))
		}
		receipt, err := c.runtime.receipts.lookup(ctx, c.Owner.ClientID, query.RequestID)
		if err != nil {
			return finish(fault(request, "receipt_unavailable", err.Error()))
		}
		return finish(response(request, receipt))
	}
	if workspaceDeletionMethod(request.Method) {
		return c.dispatchWorkspaceDeletion(ctx, request)
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
		c.detachControlsLocked(request.WorkspaceID)
		c.mu.Unlock()
		c.DetachCapabilityHosts(request.WorkspaceID)
		c.stopWorkspaceTopics(request.WorkspaceID)
		a.unsubscribe(c)
		return finish(response(request, coreprotocol.Empty{}))
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
	if err := decodeRequest(request, &input); err != nil {
		return fault(request, "invalid_request", err.Error())
	}
	root, err := inspectProject(input.Root)
	if err != nil {
		return fault(request, "project_folder_required", err.Error())
	}
	// Cover creation, subscription and RecordAccess, not just the factory wait.
	// A cancelled factory may outlive this lease; deletion also checks creating.
	release, err := c.runtime.lockWorkspaceStorage(ctx, workspace.WorkspaceID(root))
	if err != nil {
		return fault(request, "workspace_unavailable", err.Error())
	}
	defer release()
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
	// Attachment is the access boundary, including a return to a retained actor.
	// Persist off the actor loop; background work and catalog refresh never touch it.
	if err := a.accessDB.RecordAccess(ctx, time.Now()); err != nil {
		a.unsubscribe(c)
		return fault(request, "attach_failed", "record workspace access: "+err.Error())
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
	done    chan struct{}
	actor   *actor
	err     error
	savedID string
}

func (r *Runtime) workspace(ctx context.Context, id, root string) (*actor, error) {
	r.mu.Lock()
	if r.closed || r.draining || r.stopping {
		r.mu.Unlock()
		return nil, errors.New("core is closed")
	}
	if a := r.workspaces[id]; a != nil {
		r.mu.Unlock()
		return a, nil
	}
	creation := r.creating[id]
	if creation == nil {
		creation = &workspaceCreation{done: make(chan struct{}), savedID: workspace.WorkspaceID(root)}
		r.creating[id] = creation
		r.factoryWG.Add(1)
		go func() {
			defer r.factoryWG.Done()
			ws, err := r.factory(root)
			if err == nil && ws != nil {
				// Retained resources initialize before an actor or shell can read
				// them. This I/O belongs to the factory worker, never actor Update.
				initCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
				initErr := ws.InitializeCoreResources(initCtx)
				cancel()
				if initErr != nil {
					ws.Logger.Warn("CORE", "workspace_resources_failed", "error", initErr.Error())
					ws.Status.Notify(types.NotificationRecord{Severity: types.SeverityError, Source: "workspace", Summary: "Workspace resources need attention", Detail: "Saved Notices or capabilities could not be loaded. Inspect the workspace persistence and Skills/MCP settings before retrying."})
				}
				if ws.Capabilities != nil {
					ws.Capabilities.SetHostDispatcher(r)
				}
			}
			r.mu.Lock()
			if err == nil && r.closed {
				err = errors.New("core is closed")
			}
			if err == nil {
				creation.actor = newActor(id, ws, r.receipts)
				creation.actor.progress = r.publishProgress
				creation.actor.runtime = r
				r.workspaces[id] = creation.actor
			}
			creation.err = err
			if err == nil {
				delete(r.creating, id)
			}
			r.mu.Unlock()
			if err != nil && ws != nil {
				ws.CloseAllSessions()
				ws.Close()
			}
			if err != nil {
				// Failed construction still owns handles until cleanup has joined.
				// Deletion must keep seeing this exact opening resource lifetime.
				r.mu.Lock()
				delete(r.creating, id)
				r.mu.Unlock()
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
	// statusVersion is the status-center version last announced to shells.
	statusVersion uint64
	lspVersion    uint64
	lspViews      *lspViews
	// jobCancels cancels exactly one requester's in-flight job.
	jobCancels map[string]context.CancelFunc
	// progress delivers request-scoped output to the requesting connection.
	progress       func(coreprotocol.Owner, coreprotocol.Progress)
	runtime        *Runtime
	id             string
	ws             *workspace.Workspace
	accessDB       *workspace.WorkspaceDB // immutable lifetime handle; sql.DB fences concurrent close
	receipts       *receiptJournal
	commands       chan actorCommand
	ingress        chan workspace.AgentEvent
	serviceEvents  chan any
	stop           chan struct{}
	done           chan struct{}
	once           sync.Once
	subscribers    map[*Connection]struct{}
	revision       uint64
	ctx            context.Context
	cancel         context.CancelFunc
	jobs           chan actorJobResult
	inFlight       int
	lifecycle      bool
	settingsBusy   bool
	historyBusy    bool // owner-held restore exclusion through mutation and publication
	settingsQueue  []actorCommand
	controlQueue   []*actorControlCommand
	resourceLane   *resourceLane
	retiring       bool // actor-owned admission fence before off-actor shutdown
	idleFenced     bool
	controlWorkers atomic.Int32
	// Independent read admission shares this actor's existing resource lifetime.
	// No read may retain ws services after saved-workspace retirement closes them.
	diagnosticMu      sync.Mutex
	diagnosticClosing bool
	diagnosticReads   sync.WaitGroup
	// terminalLane orders PTY effects independently of documents, files and
	// language servers, so their slow work never delays a keystroke.
	terminalLane     *resourceLane
	fileWatch        *coreFileWatch
	stopGitFetch     func()
	documentReleases []document.Owner
	maintenance      actorMaintenance
	topic            workspaceTopicState
	states           workspaceStates
}

type actorCommand struct {
	control *actorControlCommand
	request *coreprotocol.Request
	client  *Connection
	attach  *Connection
	detach  *Connection
	result  chan coreprotocol.Response
	view    chan coreprotocol.WorkspaceSnapshot
	inspect func()
	// retire acknowledges the atomic quiescence/admission decision before the
	// loop exits. Its worker owns full off-actor resource shutdown afterward.
	retire func() bool
}

func newActor(id string, ws *workspace.Workspace, receipts *receiptJournal) *actor {
	ctx, cancel := context.WithCancel(context.Background())
	a := &actor{ctx: ctx, cancel: cancel, jobs: make(chan actorJobResult, 16), id: id, ws: ws, receipts: receipts, commands: make(chan actorCommand, 128), ingress: make(chan workspace.AgentEvent, 1024), stop: make(chan struct{}), done: make(chan struct{}), subscribers: make(map[*Connection]struct{}), revision: 1}
	a.accessDB = ws.WorkspaceDB
	a.resourceLane = newResourceLane(ctx, 32)
	a.terminalLane = newResourceLane(ctx, 32)
	a.lspViews = newLSPViews(ws.LiveDocuments)
	a.serviceEvents = make(chan any, 256)
	bus := workspace.NewEventBus()
	bus.SetSink(a)
	ws.SetEventBus(bus)
	ws.SetEventSender(a)
	a.startStateTopics()
	go a.run()
	// newActor runs in the workspace factory worker. Watcher setup/traversal
	// never executes on the actor or any shell's Update goroutine.
	watch, err := startCoreFileWatch(ws.RootPath, func(change coreprotocol.FileChange) { a.Send(change) })
	if err != nil {
		ws.Logger.Warn("CORE", "file_watch_failed", "error", err.Error())
	} else {
		a.fileWatch = watch
	}
	// Remote-tracking refs stay current for every attached shell; the fetch
	// moves .git refs, which the file watch above turns into a reconcile.
	a.stopGitFetch = ws.Git.StartAutoFetch()
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
	// Grid state has its own topic and pacing. PTY ingress must never wait on
	// the workspace actor or invalidate unrelated replicas for every read.
	if _, grid := message.(workspace.TerminalChangedMsg); grid {
		return
	}
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
	case coreprotocol.FileChange:
		a.broadcast(coreprotocol.Change{Files: []coreprotocol.FileChange{event}})
		return
	case workspace.DocumentChangedMsg:
		return
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
	if effects == nil || effects.Effects.TitleChanged {
		a.queueState(coreprotocol.TopicTerminalInventory)
	}
	if effects != nil {
		shared := *effects
		shared.Effects.Replies, shared.Effects.Clipboard = nil, nil
		if shared.Effects.TitleChanged || shared.Effects.CWDChanged || shared.Effects.Title != "" || shared.Effects.CWD != "" || shared.Effects.Bell {
			a.queueTopic(coreprotocol.Change{TerminalEffects: &shared})
		}
	}
	a.revision++
	if effects == nil || len(effects.Effects.Clipboard) == 0 || a.ws.Terminals == nil {
		return
	}
	for subscriber := range a.subscribers {
		lease, ok := a.ws.Terminals.ControllerLease(id, workspace.TerminalController{ClientID: subscriber.Owner.ClientID, Epoch: subscriber.Owner.Epoch})
		if !ok {
			continue
		}
		if !subscriber.offerClipboard(a.id, coreprotocol.TerminalClipboard{TerminalID: id, Generation: lease.Generation, Clipboard: effects.Effects.Clipboard}) {
			a.ws.Status.Notify(types.NotificationRecord{Severity: types.SeverityWarning, Source: "terminal", Summary: "Terminal clipboard offer refused", Detail: "The controlling shell's private inbox is full or the offer exceeds its 1 MiB limit. Copy again after the shell catches up."})
			a.queueState(coreprotocol.TopicStatus)
		}
	}
}

func (a *actor) run() {
	trackActorQueue(a, true)
	defer trackActorQueue(a, false)
	defer close(a.done)
	defer a.cancel()
	defer a.maintenance.stop()
	defer a.stopTopicPublication()
	// Capture the next broadcast before publishing the current version. A
	// mutation during publication closes that captured channel, so no generation
	// can be swallowed between a snapshot and the next select.
	_, statusChanged := a.ws.Status.Changed()
	_, lspChanged := a.ws.LSPMgr.Changed()
	a.publishStatusIfChanged()
	a.publishLSPIfChanged()
	a.refreshMaintenance()
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
			start := time.Now()
			a.finishJob(job)
			a.refreshMaintenance()
			method := ""
			if job.command.request != nil {
				method = job.command.request.Method
			}
			observeActorStep(start, "finish", method)
		case message := <-a.serviceEvents:
			start := time.Now()
			a.serviceEvent(message)
			observeActorStep(start, "service", message)
		case event := <-a.ingress:
			start := time.Now()
			if notice, ok := workspace.NoticeFromEvent(event); ok {
				a.notice(notice)
				observeActorStep(start, "notice", event)
				continue
			}
			if reduced := a.ws.ApplyAgentEvent(event); reduced.Accepted {
				a.reduced(reduced)
				a.refreshMaintenance()
			}
			observeActorStep(start, "agent", event)
		case <-statusChanged:
			_, statusChanged = a.ws.Status.Changed()
			a.publishStatusIfChanged()
		case <-lspChanged:
			_, lspChanged = a.ws.LSPMgr.Changed()
			a.publishLSPIfChanged()
		case now := <-a.maintenance.ready:
			a.maintenance.ready = nil
			a.drainDocumentReleases()
			if reduced := a.ws.AdvanceAgentState(now); reduced.Accepted {
				a.reduced(reduced)
			}
			a.refreshMaintenance()
			observeActorStep(now, "maintenance", nil)
		case <-a.topic.ready:
			_ = a.flushTopic()
		case command := <-a.commands:
			start := time.Now()
			retired := a.runCommand(command)
			observeActorStep(start, "request", commandKind(command))
			if retired {
				return
			}
			a.refreshMaintenance()
		}
	}
}

// actorMaintenance has no idle deadline. Only queued tool dwell transitions or
// a detached document release refused by the bounded resource lane need the
// existing 100 ms retry/advance bound. Conversation remains the pending owner;
// inspect it after semantic ingress/jobs, never from an idle polling clock.
type actorMaintenance struct {
	timer *time.Timer
	ready <-chan time.Time
}

func (m *actorMaintenance) stop() {
	if m.timer != nil {
		m.timer.Stop()
	}
	m.ready = nil
}

func (a *actor) refreshMaintenance() {
	pending := len(a.documentReleases) > 0
	if !pending && a.ws.AgentMgr != nil {
		for _, node := range a.ws.AgentMgr.AllNodes() {
			if node.State != nil && node.State.Messages.HasPendingToolLifecycles() {
				pending = true
				break
			}
		}
	}
	if !pending {
		a.maintenance.stop()
		return
	}
	if a.maintenance.ready != nil {
		return
	}
	if a.maintenance.timer == nil {
		a.maintenance.timer = time.NewTimer(100 * time.Millisecond)
	} else {
		a.maintenance.timer.Reset(100 * time.Millisecond)
	}
	a.maintenance.ready = a.maintenance.timer.C
}

func commandKind(command actorCommand) string {
	switch {
	case command.request != nil:
		return command.request.Method
	case command.retire != nil:
		return "retire"
	case command.control != nil:
		return "control"
	case command.inspect != nil:
		return "inspect"
	case command.attach != nil:
		return "attach"
	case command.detach != nil:
		return "detach"
	}
	return ""
}

// runCommand admits or answers one actor command and reports whether the
// actor retired.
func (a *actor) runCommand(command actorCommand) (retired bool) {
	if command.retire != nil {
		return command.retire()
	}
	if command.control != nil {
		if a.idleFenced {
			c := command.control
			err := errors.New("core shutdown owns workspace admission")
			switch {
			case c.resolve != nil:
				c.resolve <- controlResolveResult{err: err}
			case c.observe != nil:
				c.observe <- controlObserveResult{found: true, result: failedControl(*c.request, control.Unavailable, err.Error())}
			default:
				c.result <- failedControl(*c.request, control.Unavailable, err.Error())
			}
			return false
		}
		a.admitControl(command.control)
		return false
	}
	if command.inspect != nil {
		command.inspect()
		return false
	}
	if command.attach != nil {
		a.subscribers[command.attach] = struct{}{}
		command.view <- a.snapshot()
		return false
	}
	if command.detach != nil {
		delete(a.subscribers, command.detach)
		if a.ws.Terminals != nil {
			a.ws.Terminals.Detach(workspace.TerminalController{ClientID: command.detach.Owner.ClientID, Epoch: command.detach.Owner.Epoch})
		}
		if a.ws.LiveDocuments != nil {
			a.ws.LiveDocuments.Detach(document.Owner{ClientID: command.detach.Owner.ClientID, Epoch: command.detach.Owner.Epoch})
		}
		a.lspViews.Detach(document.Owner{ClientID: command.detach.Owner.ClientID, Epoch: command.detach.Owner.Epoch})
		a.documentReleases = append(a.documentReleases, document.Owner{ClientID: command.detach.Owner.ClientID, Epoch: command.detach.Owner.Epoch})
		a.drainDocumentReleases()
		return false
	}
	if !a.validClient(command.client) {
		command.result <- fault(*command.request, "detached", "request attachment is stale")
		return false
	}
	if serializedSettings(command.request.Method) && a.settingsBusy {
		if len(a.settingsQueue) >= 16 {
			command.result <- fault(*command.request, "busy", "settings queue is full")
		} else {
			a.settingsQueue = append(a.settingsQueue, command)
		}
		return false
	}
	if a.startJob(command) {
		return false
	}
	command.result <- a.execute(*command.request)
	return false
}

func (a *actor) changed(ids []string) {
	a.broadcast(coreprotocol.Change{Agents: slices.Clone(ids)})
}

// reduced publishes one accepted reduction with its effects, so every shell
// presents exactly what the single semantic reducer decided.
func (a *actor) reduced(reduction workspace.AgentReduction) {
	reduction.ChangedAgents = slices.Clone(reduction.ChangedAgents)
	a.broadcast(coreprotocol.Change{Agents: slices.Clone(reduction.ChangedAgents), Reductions: []workspace.AgentReduction{reduction}})
}

func (a *actor) notice(notice workspace.AgentNotice) {
	a.broadcast(coreprotocol.Change{Notices: []workspace.AgentNotice{notice}})
}

func (a *actor) broadcast(change coreprotocol.Change) {
	old := a.revision
	a.revision++
	change.WorkspaceID, change.BaseRevision, change.Revision = a.id, old, a.revision
	a.queueTopic(change)
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
	a.lspViews.Detach(document.Owner{ClientID: c.Owner.ClientID, Epoch: c.Owner.Epoch})
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
	a.once.Do(func() {
		a.diagnosticMu.Lock()
		a.diagnosticClosing = true
		a.diagnosticMu.Unlock()
		close(a.stop)
		a.fileWatch.Close()
		if a.stopGitFetch != nil {
			a.stopGitFetch()
		}
		<-a.done
		a.diagnosticReads.Wait()
		a.states.wg.Wait()
		a.resourceLane.close()
		a.terminalLane.close()
		for _, owner := range a.documentReleases {
			if a.ws.LiveDocuments != nil {
				a.ws.LiveDocuments.ReleaseDetached(owner)
				a.ws.LiveDocuments.ReleaseDetached(lspViewOwner(owner))
			}
		}
		a.ws.CloseAllSessions()
		a.ws.Close()
	})
}

// Detached epochs are fenced immediately; ordered final resource release must
// never make the actor wait for a disk save holding the document mutation lock.
func (a *actor) drainDocumentReleases() {
	for len(a.documentReleases) > 0 {
		owner := a.documentReleases[0]
		docs := a.ws.LiveDocuments
		if docs == nil {
			a.documentReleases = a.documentReleases[1:]
			continue
		}
		if !a.resourceLane.submit(func(context.Context) { docs.ReleaseDetached(owner); docs.ReleaseDetached(lspViewOwner(owner)) }) {
			return
		}
		a.documentReleases = a.documentReleases[1:]
	}
}

func decode(raw []byte, value coreprotocol.Target) error { return coreprotocol.Decode(raw, value) }

// decodeRequest decodes a request's payload in the encoding it carries.
func decodeRequest(request coreprotocol.Request, value coreprotocol.Target) error {
	if request.JSON {
		return decodeJSON(request.Payload, value)
	}
	return coreprotocol.Decode(request.Payload, value)
}

// decodeJSON strictly decodes one JSON document: unknown fields and trailing
// values are errors.
func decodeJSON(raw []byte, value any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(value); err != nil {
		return err
	}
	if d.More() {
		return errors.New("trailing JSON")
	}
	return nil
}

// payload encodes a request value in the encoding request already uses.
func payload[T any, P interface {
	*T
	coreprotocol.Value
}](request coreprotocol.Request, value T) ([]byte, error) {
	if request.JSON {
		return json.Marshal(value)
	}
	return coreprotocol.Marshal(P(&value))
}

// response encodes a result value. Any value whose pointer has a generated
// codec is accepted, so callers pass values as they hold them.
func response[T any, P interface {
	*T
	coreprotocol.Value
}](request coreprotocol.Request, value T) coreprotocol.Response {
	data, err := coreprotocol.Marshal(P(&value))
	if err != nil {
		return fault(request, "encode_failed", err.Error())
	}
	if len(data) > corewire.MaxMessage-1024 {
		return fault(request, "payload_limit", "result exceeds the message size limit")
	}
	return coreprotocol.Response{ID: request.ID, Payload: data, Value: value}
}

// wire boxes a result for a method that chooses its result type at run time;
// the compiler still checks that the type has a generated codec.
func wire[T any, P interface {
	*T
	coreprotocol.Value
}](value T) coreprotocol.Value {
	return P(&value)
}

// wired boxes the (result, error) pair a multiplexed method's call returns.
func wired[T any, P interface {
	*T
	coreprotocol.Value
}](value T, err error) (coreprotocol.Value, error) {
	return P(&value), err
}

// wiredList boxes a (slice, error) pair as the named collection L.
func wiredList[L ~[]E, E any, P interface {
	*L
	coreprotocol.Value
}](values []E, err error) (coreprotocol.Value, error) {
	list := L(values)
	return P(&list), err
}

// resultValue reads a typed result back from an in-core response.
func resultValue[T any](response coreprotocol.Response) (T, bool) {
	switch v := response.Value.(type) {
	case T:
		return v, true
	case *T:
		if v != nil {
			return *v, true
		}
	}
	var zero T
	return zero, false
}

// responseValue encodes a result boxed by wire or already a codec value.
func responseValue(request coreprotocol.Request, v coreprotocol.Value) coreprotocol.Response {
	data, err := coreprotocol.Marshal(v)
	if err != nil {
		return fault(request, "encode_failed", err.Error())
	}
	if len(data) > corewire.MaxMessage-1024 {
		return fault(request, "payload_limit", "result exceeds the message size limit")
	}
	return coreprotocol.Response{ID: request.ID, Payload: data, Value: v}
}

func fault(request coreprotocol.Request, code, message string) coreprotocol.Response {
	return coreprotocol.Response{ID: request.ID, Error: &coreprotocol.Fault{Code: code, Message: message}}
}

func (a *actor) execute(request coreprotocol.Request) coreprotocol.Response {
	if request.Method == coreprotocol.AgentChanges {
		var query coreprotocol.AgentChangesRequest
		if err := decodeRequest(request, &query); err != nil {
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
	if request.Method == coreprotocol.SessionSnapshots {
		return response(request, coreprotocol.SessionSnapshotList(a.sessionSnapshots()))
	}
	if result, ok := a.executeOwner(request); ok {
		return result
	}

	switch request.Method {
	case coreprotocol.NewSession, coreprotocol.OpenSession, coreprotocol.ForkSession, coreprotocol.SubmitInput, coreprotocol.SteerInput, coreprotocol.Decide, coreprotocol.CancelAgent, coreprotocol.TerminateAgent:
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
		if err := decodeRequest(request, &input); err != nil {
			return fault(request, "invalid_request", err.Error())
		}
		if strings.TrimSpace(input.Text) == "" {
			return fault(request, "invalid_input", "only nonempty human input is accepted")
		}
		_, accepted, err := a.ws.AdmitAgentSteering(input.AgentID, humanInput(request, input))
		if err != nil {
			return fault(request, "steering_failed", err.Error())
		}
		if accepted {
			a.changed([]string{input.AgentID})
		}
		return response(request, coreprotocol.DecisionResult{Accepted: accepted})
	case coreprotocol.Decide:
		var input coreprotocol.DecisionRequest
		if err := decodeRequest(request, &input); err != nil {
			return fault(request, "invalid_request", err.Error())
		}
		node := a.ws.AgentMgr.Get(input.AgentID)
		if node == nil || node.State == nil || input.BlockID == "" || node.State.PendingBlockID != input.BlockID {
			return fault(request, "stale_decision", "decision no longer owns this pending block")
		}
		var accepted bool
		if input.Reject != "" {
			accepted = a.ws.RejectQuestion(input.AgentID, input.BlockID, input.Reject)
		} else if input.Choice != "" {
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
		if err := decodeRequest(request, &input); err != nil {
			return fault(request, "invalid_request", err.Error())
		}
		node := a.ws.AgentMgr.Get(input.AgentID)
		if node == nil || node.State == nil || node.State.TurnIdentity.Generation != input.Generation {
			return fault(request, "stale_generation", "agent generation changed")
		}
		if request.Method == coreprotocol.CancelAgent && input.OperationID != node.State.ActiveCompaction.OperationID {
			return fault(request, "stale_operation", "agent compaction operation changed")
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
		Settings: coreprotocol.ShellSettings{Theme: settings.Theme, Editor: settings.Editor, UI: settings.UI}}
	view.Sessions = a.sessionSnapshots()
	return view
}

// sessionSnapshots projects the session set without transcripts.
func (a *actor) sessionSnapshots() []coreprotocol.SessionSnapshot {
	var out []coreprotocol.SessionSnapshot
	for _, owner := range a.ws.Sessions {
		s := coreprotocol.SessionSnapshot{ID: owner.SessionID, AgentID: owner.AgentID, CreatedAt: owner.CreatedAt}
		if owner.Settings != nil {
			effective := owner.Settings.Effective
			s.Settings = coreprotocol.SessionSettings{MainAgent: effective.SessionMainAgent, Model: effective.Model, ProviderID: effective.ProviderID, AccountID: effective.AccountID, ThinkingEffort: effective.ThinkingEffort, MultiAgent: effective.SessionMultiAgent}
		}
		if owner.RefStore != nil {
			s.References = owner.RefStore.ListReferences()
		}
		if backend := a.ws.BackendSession(owner.SessionID); backend != nil {
			full := publicSessionSet(backend.SettingsSnapshots())
			s.Full = &full
		}
		if err := a.ws.SessionConnectionIssue(owner.SessionID); err != nil {
			s.ConnectionIssue = err.Error()
		}
		out = append(out, s)
	}
	return out
}

// TODO: explicit typed service dispatch can share startJob's bounded worker and
// finishJob's serialized application; remote payloads must never carry callbacks.
type actorJobResult struct {
	command   actorCommand
	apply     func() coreprotocol.Response
	finish    func() // owner-only release after publication and immutable receipt
	lifecycle bool
	journal   bool
	admitted  bool
	settings  bool
}

func (a *actor) validClient(c *Connection) bool {
	if c == nil || a.retiring || a.idleFenced {
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
	target := control.Target{WorkspaceID: request.WorkspaceID, Shell: &control.ShellOwner{ClientID: request.Owner.ClientID, Epoch: request.Owner.Epoch}}
	return provider.UserInput{Text: input.Text, DisplayText: input.DisplayText, ReferenceIDs: slices.Clone(input.ReferenceIDs), ControlTarget: &target,
		ContextParts: slices.Clone(input.ContextParts), ActivePaths: slices.Clone(input.ActivePaths),
		Origin: provider.OriginHuman, InternalID: "client-" + request.Owner.ClientID + "-" + request.ID}
}

func (a *actor) bindInputExecution(input *provider.UserInput, agentID string) {
	if input.ControlTarget == nil {
		return
	}
	if owner := a.ws.SessionForAgent(agentID); owner != nil {
		input.ControlTarget.SessionID = owner.SessionID
		if backend := a.ws.BackendSession(owner.SessionID); backend != nil {
			input.ControlTarget.ExecutionID = backend.ExecutionSelection().ID
		}
	}
}

func (a *actor) startJob(command actorCommand) bool {
	request := *command.request
	historyRestore := request.Method == coreprotocol.HistoryApply
	if a.historyBusy && (request.Method == coreprotocol.NewSession || request.Method == coreprotocol.OpenSession || request.Method == coreprotocol.ForkSession || request.Method == coreprotocol.SubmitInput || request.Method == coreprotocol.SteerInput || request.Method == coreprotocol.AgentRecover || request.Method == coreprotocol.AgentCompact) {
		command.result <- fault(request, "restore_busy", "history restore owns admission; input and work are retained")
		return true
	}
	lifecycle := request.Method == coreprotocol.NewSession || request.Method == coreprotocol.OpenSession || request.Method == coreprotocol.ForkSession
	service, serviceKnown := a.prepareService(request)
	resource, resourceKnown := a.prepareResource(request)
	if resourceKnown {
		service, serviceKnown = resource, true
	}
	files, filesKnown := a.prepareFiles(request)
	if replace, known := a.prepareProjectReplace(request); known {
		files, filesKnown = replace, true
	}
	if clientFiles, known := a.prepareClientFiles(request); known {
		files, filesKnown = clientFiles, true
	}
	if filesKnown {
		service, serviceKnown = files, true
	}
	references, referencesKnown := a.prepareReferences(request)
	if referencesKnown {
		service, serviceKnown = references, true
	}
	settings, settingsApply, settingsKnown := a.prepareSettings(request)
	if preferences, apply, known := a.prepareWorkspacePreferences(request); known {
		settings, settingsApply, settingsKnown = preferences, apply, true
	}
	if settingsKnown {
		service, serviceKnown = settings, true
	}
	upload, uploadKnown := a.prepareUpload(request)
	if uploadKnown {
		service, serviceKnown = upload, true
	}
	// Owner preparation can reserve session work or reduce a lifecycle row.
	// Capacity and exact receipt replay checks must precede those effects.
	ownerKnown := ownerOperation(request.Method)
	lsp, lspKnown := a.prepareLSP(request)
	if lspKnown {
		service, serviceKnown = lsp, true
	}
	documentSave, documentSaveKnown := a.prepareDocumentSnapshotSave(request)
	if documentSaveKnown {
		service, serviceKnown = documentSave, true
	}
	if !lifecycle && !historyRestore && request.Method != coreprotocol.SubmitInput && request.Method != coreprotocol.ListSessions && !serviceKnown && !ownerKnown {
		return false
	}
	if a.inFlight >= 16 || lifecycle && a.lifecycle {
		command.result <- fault(request, "busy", "bounded workspace operation capacity is occupied; retry with a new request identity")
		return true
	}
	journal := request.Method != coreprotocol.ListSessions && (historyRestore || (!serviceKnown && !ownerKnown) || serviceMutation(request.Method) || resourceMutation(request.Method) || filesMutation(request.Method) || referenceMutation(request.Method) || settingsMutation(request.Method) || uploadMutation(request.Method) || ownerMutation(request.Method) || documentSnapshotSaveMutation(request.Method))
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
	if ownerKnown {
		service, serviceKnown = a.prepareOwner(request)
	}
	var historyApply func(coreprotocol.Response) error
	var historyFinish func()
	if historyRestore {
		service, historyApply, historyFinish = a.prepareHistoryApply(request)
		serviceKnown = true
	}
	// Each job has its own context so its requester can cancel exactly it.
	jobCtx, jobCancel := context.WithCancel(a.ctx)
	if a.jobCancels == nil {
		a.jobCancels = make(map[string]context.CancelFunc)
	}
	a.jobCancels[jobKey(request)] = jobCancel
	fail := func(result coreprotocol.Response) bool {
		a.finishJob(actorJobResult{command: command, apply: func() coreprotocol.Response { return result }, journal: journal, finish: historyFinish})
		return true
	}
	var run func() func() coreprotocol.Response
	if serviceKnown {
		run = func() func() coreprotocol.Response {
			result := service(jobCtx)
			return func() coreprotocol.Response {
				a.applyService(request, result)
				if resourceKnown {
					a.applyResource(request, result)
				}
				if historyApply != nil {
					if err := historyApply(result); err != nil {
						result.Error = &coreprotocol.Fault{Code: "publication_failed", Message: "restore committed; inspect original receipt and safety snapshot: " + err.Error()}
					}
				}
				if settingsApply != nil {
					settingsApply(result)
				}
				if (resourceKnown || filesKnown || referencesKnown || settingsKnown || uploadKnown || historyRestore) && journal && result.Error == nil {
					a.changed(nil)
				}
				if result.Error == nil {
					a.announceSettings(request.Method)
				}
				return result
			}
		}
	} else if lifecycle {
		op, err := a.prepareInvocation(request)
		if err != nil {
			return fail(fault(request, "session_failed", err.Error()))
		}
		a.lifecycle = true
		run = func() func() coreprotocol.Response {
			result := op.Run(a.ctx)
			return func() coreprotocol.Response {
				out := a.ws.ApplyAgentSession(result)
				if out.Error != "" {
					if out.NeedsAccount {
						return fault(request, "account_required", out.Error)
					}
					if out.NeedsModel {
						return fault(request, "model_required", out.Error)
					}
					return fault(request, "session_failed", out.Error)
				}
				a.bindControlSessions()
				a.changed([]string{out.OrchID})
				a.broadcast(coreprotocol.Change{Sessions: true})
				return response(request, *out)
			}
		}
	} else if request.Method == coreprotocol.SubmitInput {
		var input coreprotocol.InputRequest
		if err := decodeRequest(request, &input); err != nil {
			return fail(fault(request, "invalid_request", err.Error()))
		}
		if strings.TrimSpace(input.Text) == "" {
			return fail(fault(request, "invalid_input", "only nonempty human input is accepted"))
		}
		value := humanInput(request, input)
		a.bindInputExecution(&value, input.AgentID)
		// Enter has one authoritative route. Shell activity is a lagging mirror,
		// so only the execution owner decides whether this input steers its live
		// run or goes through fresh/queued admission. A storage error is never a
		// confirmed refusal and cannot fall through to another publication.
		steering, steered, err := a.ws.AdmitAgentSteering(input.AgentID, value)
		if err != nil {
			return fail(fault(request, "steering_failed", err.Error()))
		}
		if steered {
			steering.DraftRevision = input.DraftRevision
			a.changed([]string{input.AgentID})
			return fail(response(request, coreprotocol.InputResult{Status: workspace.InputAdmissionSteered, Receipt: steering}))
		}
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
			return func() coreprotocol.Response { return response(request, coreprotocol.SessionRegistry(out)) }
		}
	}
	a.inFlight++
	settingsExclusive := settingsKnown && serializedSettings(request.Method)
	if settingsExclusive {
		a.settingsBusy = true
	}
	work := func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				a.jobs <- actorJobResult{command: command, admitted: true, lifecycle: lifecycle, settings: settingsExclusive, journal: journal, finish: historyFinish,
					apply: func() coreprotocol.Response {
						return fault(request, "outcome_uncertain", fmt.Sprintf("operation panicked: %v; inspect receipt before retrying", recovered))
					}}
			}
		}()
		apply := run()
		a.jobs <- actorJobResult{command: command, apply: apply, lifecycle: lifecycle, journal: journal, admitted: true, settings: settingsExclusive, finish: historyFinish}
	}
	if resourceKnown || lspKnown || historyRestore || (filesKnown && filesMutation(request.Method)) {
		lane := a.resourceLane
		if terminalMethod(request.Method) {
			lane = a.terminalLane
		}
		if !lane.submit(func(context.Context) { work() }) {
			a.finishJob(actorJobResult{command: command, admitted: true, journal: journal, finish: historyFinish, apply: func() coreprotocol.Response { return fault(request, "cancelled", "resource owner is closing") }})
		}
	} else {
		go work()
	}
	return true
}

func (a *actor) finishJob(job actorJobResult) {
	if job.finish != nil {
		defer job.finish()
	}
	if job.command.request != nil {
		key := jobKey(*job.command.request)
		if cancel := a.jobCancels[key]; cancel != nil {
			cancel()
			delete(a.jobCancels, key)
		}
	}
	// Validation failures were never admitted as workers.
	if job.lifecycle {
		a.lifecycle = false
	}
	if job.apply == nil {
		return
	}
	result := applyActorJob(job)
	// Effects of an applied job (an accepted prompt starting a turn) reach
	// shells now, not on the next tick.
	a.publishStatusIfChanged()
	a.publishLSPIfChanged()
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
		for !a.settingsBusy && len(a.controlQueue) > 0 {
			command := a.controlQueue[0]
			a.controlQueue[0] = nil
			a.controlQueue = a.controlQueue[1:]
			a.admitControl(command)
		}
	}
}

// A publication fault cannot terminate the retained actor or label a possibly
// committed effect as refused. The original request remains the receipt owner.
func applyActorJob(job actorJobResult) (result coreprotocol.Response) {
	defer func() {
		if recovered := recover(); recovered != nil {
			var request coreprotocol.Request
			if job.command.request != nil {
				request = *job.command.request
			}
			result = fault(request, "outcome_uncertain", fmt.Sprintf("publication panicked: %v; inspect original receipt before retrying", recovered))
		}
	}()
	return job.apply()
}
