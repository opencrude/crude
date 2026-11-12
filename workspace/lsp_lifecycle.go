package workspace

import (
	"context"
	"crude/internal/fileuri"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"sync"
	"time"

	"crude/fileio"
	"crude/insight/governor"
	ttypes "crude/types"
)

type lspClientState struct {
	ticket      *governor.Ticket
	lease       *governor.Lease
	epoch       uint64
	instance    string
	rootURI     string
	cmd         string
	args        []string
	spec        ttypes.LSPServerSpec
	status      LSPStatus
	err         string
	client      LSPClient
	assistance  ttypes.LSPAssistanceCapabilities
	teardownErr error
	cancel      context.CancelFunc
	closing     bool
	waiting     bool
	stopped     bool
	lastUsed    time.Time
}

type lspDocument struct {
	source             ttypes.InsightSource
	boundary           ttypes.InsightBoundary
	wireVersion        int
	syncedContext      uint64
	syncPending        bool
	pendingPublication *ttypes.InsightDiagnosticPublication
	text               string
	key                string
	language           string
	opened             bool
	revision           uint64
	syncedRevision     uint64
	// editor is the DocumentService view; probes counts agent requests that
	// borrowed this document. Either keeps the server view alive.
	editor bool
	probes int
}

type lspClosedDocument struct {
	path   string
	client LSPClient
}

func NewLSPManager(logger Logger, runtimes ...LSPRuntime) *LSPManager {
	m := &LSPManager{
		logger: logger, settings: ttypes.DefaultLSPSettings(), runtime: defaultLSPRuntime{},
		analysisSettings: ttypes.DefaultWorkspaceAnalysisSettings(), analysisIdentity: fileio.DirectoryIdentity,
		states: make(map[string]*lspClientState), documents: make(map[string]*lspDocument),
		updateTimers: make(map[string]*time.Timer), wake: make(chan struct{}, 1),
		docSymbols: make(map[string][]LSPSymbol), stateChanged: make(chan struct{}), contextGeneration: 1,
	}
	if len(runtimes) > 0 {
		m.runtime = runtimes[0]
	}
	m.recomputeActiveStatusLocked()
	return m
}

func (m *LSPManager) SetRuntime(runtime LSPRuntime) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	for _, state := range m.states {
		m.retireLocked(state)
	}
	for _, doc := range m.documents {
		doc.key, doc.opened = "", false
	}
	m.runtime = runtime
	m.settingsGeneration++
	m.advanceContextLocked("Language service configuration changed.")
	m.requestAssemblyLocked()
	if len(m.states) > 0 || len(m.documents) > 0 {
		m.signalLocked()
	}
	m.mu.Unlock()
}

// SetWorkspaceRoot is passive and performs no scan, process launch or disk I/O.
func (m *LSPManager) SetWorkspaceRoot(root string) {
	m.mu.Lock()
	m.setWorkspaceRootLocked(root)
	m.mu.Unlock()
}

func (m *LSPManager) setWorkspaceRootLocked(root string) {
	root = filepath.Clean(root)
	if root == m.workspaceRoot {
		return
	}
	m.workspaceRoot = root
	m.discovery = nil
	m.projects = nil
	m.settingsGeneration++
	m.advanceContextLocked("Project root changed; retained context is stale.")
	for _, state := range m.states {
		m.retireLocked(state)
	}
	for _, doc := range m.documents {
		doc.key, doc.opened = "", false
	}
	if len(m.states) > 0 {
		m.signalLocked()
	}
	m.requestAssemblyLocked()
}

func (m *LSPManager) Settings() ttypes.LSPSettings {
	m.mu.Lock()
	defer m.mu.Unlock()
	return ttypes.CloneLSPSettings(m.settings)
}

func (m *LSPManager) SetSettings(settings ttypes.LSPSettings) {
	settings = ttypes.NormalizeLSP(ttypes.CloneLSPSettings(settings))
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	if reflect.DeepEqual(m.settings, settings) {
		m.mu.Unlock()
		return
	}
	viewOnly := sameLSPProducerSettings(m.settings, settings)
	if m.settings.InlayHints != settings.InlayHints {
		m.inlayGeneration++
	}
	if m.settings.Completions != settings.Completions || m.settings.AutoComplete != settings.AutoComplete {
		m.completionGeneration++
	}
	m.settings = settings
	if m.analysisGovernor != nil {
		m.analysisGovernorError = ""
		if err := m.analysisGovernor.ConfigureWorkspace(m.analysisWorkspace, settings.MaxServers); err != nil {
			m.analysisGovernorError = err.Error()
		}
	}
	if viewOnly {
		m.bumpVersionLocked()
		m.mu.Unlock()
		return
	}
	m.settingsGeneration++
	m.advanceContextLocked("Language service configuration changed.")
	// Changed launch policy invalidates the old process identity, never mutates
	// a live connection's environment/settings beneath its outstanding requests.
	for _, state := range m.states {
		m.retireLocked(state)
	}
	for _, doc := range m.documents {
		doc.key, doc.opened = "", false
	}
	m.cancelUpdatesLocked()
	// Settings can change which language owns a file and which server runs.
	// The old producers' launch specs are stale: drop them now, so nothing
	// starts with them, and let the new plan name the current ones.
	m.setProjectProducersLocked(nil)
	m.discovery = nil
	m.requestAssemblyLocked()
	m.recomputeActiveStatusLocked()
	m.invalidateDiagnosticsLocked("Language service was stopped or reconfigured.")
	if len(m.states) > 0 || len(m.documents) > 0 {
		m.signalLocked()
	}
	m.mu.Unlock()
}

// Visibility/severity and request feature gates do not change a producer's
// source or launch configuration. They must not restart it or restamp facts.
func sameLSPProducerSettings(a, b ttypes.LSPSettings) bool {
	// Quota changes govern new admission, never forcibly evict live producers.
	a.MaxServers = b.MaxServers
	a.Diagnostics, a.Completions, a.Hover, a.Definition = b.Diagnostics, b.Completions, b.Hover, b.Definition
	a.References, a.Outline, a.Rename = b.References, b.Outline, b.Rename
	a.DiagInEditor, a.DiagInTree, a.DiagInStatus = b.DiagInEditor, b.DiagInTree, b.DiagInStatus
	a.MinSeverity, a.AutoComplete = b.MinSeverity, b.AutoComplete
	a.CompletionDelayMS, a.CompletionAccept = b.CompletionDelayMS, b.CompletionAccept
	a.InlayHints = b.InlayHints
	return reflect.DeepEqual(a, b)
}

// StartForWorkspace explicitly resumes intelligence for retained editor buffers.
// It never fabricates didOpen messages for the contents of the workspace.
func (m *LSPManager) StartForWorkspace(root string) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.setWorkspaceRootLocked(root)
	m.paused, m.manualStart = false, true
	for _, state := range m.states {
		state.stopped = false
		if state.status == LSPFailed {
			m.retireLocked(state)
		}
	}
	// Starting again re-checks the project and its tools, so a server the
	// user installed since the last plan is found.
	m.discovery = nil
	m.requestAssemblyLocked()
	m.recomputeActiveStatusLocked()
	if len(m.documents) > 0 || len(m.states) > 0 {
		m.signalLocked()
	}
	m.mu.Unlock()
}

func (m *LSPManager) signalLocked() {
	if !m.workerRunning {
		m.workerRunning = true
		m.workerDone = make(chan struct{})
		go m.runWorker(m.workerDone)
	}
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

// One worker serializes starts and joins stops before spending the released
// process slot. This bounds actual live processes, not merely registry entries.
func (m *LSPManager) runWorker(done chan struct{}) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	defer func() { m.mu.Lock(); m.workerRunning = false; close(done); m.mu.Unlock() }()
	for {
		for m.reconcile() {
		}
		m.mu.Lock()
		closed := m.closed
		// Close can retire owners after reconcile's final idle result but
		// before this check. Only exit once that same terminal state is drained.
		retiring := len(m.closedDocuments) != 0
		if closed {
			for _, state := range m.states {
				retiring = retiring || state.closing
			}
		}
		m.mu.Unlock()
		if closed {
			if retiring {
				continue
			}
			return
		}
		// Observe each exact ticket before sleeping. Capacity changes wake the
		// existing joined worker, never a detached waiter or a busy retry loop.
		m.mu.Lock()
		cases := []reflect.SelectCase{
			{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(m.wake)},
			{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(ticker.C)},
		}
		for _, state := range m.states {
			if state.ticket != nil && state.lease == nil && !state.closing {
				observation, changed := state.ticket.Observe()
				switch observation.State {
				case governor.Queued:
					cases = append(cases, reflect.SelectCase{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(changed)})
				case governor.Granted:
					// It changed since reconcile's observation. Do not lose the
					// grant by waiting on its new (not yet closed) broadcast.
					m.signalLocked()
				}
			}
		}
		m.mu.Unlock()
		reflect.Select(cases)
	}
}

func (m *LSPManager) reconcile() bool {
	if event, ok := m.reviewWatch.nextReplay(); ok {
		if event.ready != nil {
			select {
			case <-event.ctx.Done():
				return true
			case <-event.ready:
			}
		}
		m.notifyWatchedFilesChanged(event.ctx, event.path, event.kind, false, event.invalidated)
		return true
	}
	m.mu.Lock()
	if len(m.closedDocuments) > 0 {
		doc := m.closedDocuments[0]
		m.closedDocuments = m.closedDocuments[1:]
		m.mu.Unlock()
		m.documentSend.Lock()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		doc.client.CloseFile(ctx, doc.path)
		cancel()
		m.documentSend.Unlock()
		return true
	}
	// Always finish an old lifetime before admitting another launch.
	for key, state := range m.states {
		if state.ticket != nil && state.lease == nil && !m.keyWantedLocked(key) {
			m.retireLocked(state)
		}
		if !state.closing {
			continue
		}
		client := state.client
		m.mu.Unlock()
		var stopErr error
		if client != nil {
			stopErr = client.Stop()
		}
		m.mu.Lock()
		if m.states[key] == state {
			if m.retainUnverifiedStopLocked(key, state, stopErr) {
				m.mu.Unlock()
				return true
			}
			if state.stopped {
				state.client, state.closing, state.status = nil, false, LSPIdle
			} else {
				delete(m.states, key)
			}
			for _, doc := range m.documents {
				if doc.key == key {
					if !state.stopped {
						doc.key = ""
					}
					doc.opened = false
					doc.syncedRevision = 0
				}
			}
			m.clearServerDiagnosticsLocked(key)
			m.recomputeActiveStatusLocked()
		}
		m.mu.Unlock()
		return true
	}
	if m.closed || m.paused || m.analysisErrorLocked() != nil || m.runtime == nil || !m.anyWantedLocked() {
		m.mu.Unlock()
		return false
	}
	// A disconnected server is not silently restarted every tick or keypress.
	for key, state := range m.states {
		if state.client == nil || state.teardownErr != nil {
			continue
		}
		select {
		case <-state.client.Done():
			client := state.client
			state.status, state.err = LSPFailed, "Language server connection lost; use Restart Language Servers."
			m.clearServerDiagnosticsLocked(key)
			m.recomputeActiveStatusLocked()
			m.mu.Unlock()
			stopErr := client.Stop()
			m.mu.Lock()
			if m.states[key] == state && !m.retainUnverifiedStopLocked(key, state, stopErr) {
				state.client = nil
				m.recomputeActiveStatusLocked()
			}
			m.mu.Unlock()
			return true
		default:
		}
		if m.documentCountLocked(key) == 0 && !m.projectWantedLocked(key) && time.Since(state.lastUsed) >= time.Duration(m.settings.IdleTimeoutSeconds)*time.Second {
			m.retireLocked(state)
			m.mu.Unlock()
			return true
		}
	}
	// Resolve only documents that actually exist in the editor. Root probing is
	// outside both the UI goroutine and the state lock.
	paths := make([]string, 0, len(m.documents))
	for path := range m.documents {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		doc := m.documents[path]
		if doc.key != "" || !m.wantedLocked(doc) {
			continue
		}
		spec, ok := m.resolveServerLocked(path)
		if !ok {
			continue
		}
		root, generation := m.workspaceRoot, m.settingsGeneration
		if !m.checkAnalysisRootLocked() {
			m.mu.Unlock()
			return true
		}
		if m.documents[path] != doc || generation != m.settingsGeneration || !m.wantedLocked(doc) {
			m.mu.Unlock()
			return true
		}
		m.mu.Unlock()
		rootURI := resolveLSPRoot(path, root, spec.RootMarkers, spec.WorkspaceRootMarkers)
		key := lspServerKey(rootURI, spec)
		m.mu.Lock()
		if m.documents[path] != doc || generation != m.settingsGeneration || m.paused || m.closed || !m.wantedLocked(doc) {
			m.mu.Unlock()
			return true
		}
		doc.key, doc.language = key, spec.LanguageID
		if m.states[key] == nil {
			m.states[key] = &lspClientState{rootURI: rootURI, cmd: spec.Command, args: spec.Args, spec: spec, status: LSPIdle, lastUsed: time.Now()}
		}
		m.recomputeActiveStatusLocked()
		m.mu.Unlock()
		return true
	}
	// A project producer starts without a document. It shares the state key,
	// and therefore the process, with documents that resolve to the same root
	// and launch configuration.
	for _, key := range m.projectKeysLocked() {
		if m.states[key] != nil || !m.projectWantedLocked(key) {
			continue
		}
		producer := m.projects[key]
		if !m.checkAnalysisRootLocked() {
			m.mu.Unlock()
			return true
		}
		if m.projects[key].Root != producer.Root || !m.projectWantedLocked(key) || m.states[key] != nil {
			m.mu.Unlock()
			return true
		}
		m.states[key] = &lspClientState{rootURI: fileuri.FromPath(producer.Root), cmd: producer.Spec.Command, args: producer.Spec.Args,
			spec: producer.Spec, status: LSPIdle, lastUsed: time.Now()}
		m.recomputeActiveStatusLocked()
		m.mu.Unlock()
		return true
	}
	// Prioritize the active document, then stable order. No eager fan-out.
	if doc := m.documents[m.activeFilePath]; doc != nil {
		paths = append([]string{m.activeFilePath}, paths...)
	}
	for _, path := range paths {
		doc := m.documents[path]
		state := m.states[doc.key]
		if state == nil || state.stopped || !m.wantedLocked(doc) || len(doc.text) > m.settings.MaxDocumentBytes {
			continue
		}
		if doc.opened && (doc.syncedRevision == doc.revision && doc.syncedContext == m.contextGeneration || m.updateTimers[path] != nil) {
			continue
		}
		if state.status == LSPConnected && state.client != nil {
			m.mu.Unlock()
			m.syncDocument(path, doc)
			return true
		}
		if m.startStateLocked(doc.key, state, func() bool { return m.documents[path] == doc && m.wantedLocked(doc) }) {
			return true
		}
	}
	for _, key := range m.projectKeysLocked() {
		state := m.states[key]
		if state == nil || state.stopped || state.status == LSPConnected || !m.projectWantedLocked(key) {
			continue
		}
		if m.startStateLocked(key, state, func() bool { return m.projectWantedLocked(key) }) {
			return true
		}
	}
	m.mu.Unlock()
	return false
}

// startStateLocked admits and launches one idle or waiting server. It returns
// true after it released m.mu (the caller must return true). It returns false
// with m.mu still held when this state cannot make progress now. stillWanted
// rechecks the caller's own reason for the server after every unlock.
func (m *LSPManager) startStateLocked(key string, state *lspClientState, stillWanted func() bool) bool {
	if state.status != LSPIdle && !state.waiting {
		return false
	}
	if m.liveCountLocked() >= m.settings.MaxServers {
		var idle *lspClientState
		for candidateKey, candidate := range m.states {
			if candidate.client != nil && candidate.teardownErr == nil && m.documentCountLocked(candidateKey) == 0 &&
				!m.projectWantedLocked(candidateKey) && (idle == nil || candidate.lastUsed.Before(idle.lastUsed)) {
				idle = candidate
			}
		}
		if idle != nil {
			m.retireLocked(idle)
			m.mu.Unlock()
			return true
		}
	}
	if state.ticket == nil {
		state.instance = rand.Text()
		ticket, err := m.analysisGovernor.Submit(governor.Request{
			Owner:          governor.Owner{Workspace: m.analysisWorkspace, Generation: m.analysisGeneration, Producer: state.instance},
			WorkspaceLimit: m.settings.MaxServers,
		})
		if err != nil {
			state.status, state.err = LSPFailed, "Analysis process admission failed; check core capacity and workspace limits: "+err.Error()
			m.recomputeActiveStatusLocked()
			return false
		}
		state.ticket = ticket
		m.analysisRetained = true
	}
	observation, _ := state.ticket.Observe()
	if observation.State == governor.Queued {
		if !state.waiting {
			state.status, state.waiting = LSPIdle, true
			state.err = "Waiting for analysis process capacity; close another language's files or increase the core/workspace server limit."
			m.recomputeActiveStatusLocked()
		}
		return false
	}
	if observation.State != governor.Granted {
		state.status, state.err, state.waiting = LSPFailed, "Analysis admission was cancelled; restart the language server after checking core capacity.", false
		m.recomputeActiveStatusLocked()
		return false
	}
	epoch := m.settingsGeneration
	// A grant may have waited behind another workspace for arbitrarily long.
	// Observe the bound root again for every new launch, off the owner lock.
	if !m.checkAnalysisRootLocked() {
		m.mu.Unlock()
		return true
	}
	if m.states[key] != state || m.settingsGeneration != epoch || state.closing || state.stopped || !stillWanted() {
		m.mu.Unlock()
		return true
	}
	lease, err := state.ticket.Begin()
	if err != nil {
		state.status, state.err, state.waiting = LSPFailed, "Analysis start admission failed: "+err.Error(), false
		m.recomputeActiveStatusLocked()
		return false
	}
	state.lease, state.epoch = lease, epoch
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	state.status, state.err, state.waiting, state.cancel = LSPStarting, "", false, cancel
	runtime := m.runtime
	store, workspace := m.toolStore, m.workspaceRoot
	spec, rootURI := state.spec, state.rootURI
	admission := m.startAdmission(key, state, epoch)
	m.recomputeActiveStatusLocked()
	m.mu.Unlock()
	var release func()
	var managed bool
	if store != nil {
		release, managed, err = store.AcquireExecutable(ctx, spec.Command)
	}
	if err == nil {
		err = admission()
	}
	if err != nil {
		if release != nil {
			release()
		}
		cancel()
		m.finishStart(key, state, nil, err)
		return true
	}
	client := runtime.NewClient(spec, rootURI, admission)
	if client == nil {
		if release != nil {
			release()
		}
		cancel()
		m.finishStart(key, state, nil, fmt.Errorf("LSP transport is unavailable"))
		return true
	}
	if release != nil {
		client = &leasedLSPClient{LSPClient: client, release: release}
	}
	m.mu.Lock()
	state.client = client
	m.mu.Unlock()
	client.OnDiagnostics(func(p ttypes.InsightDiagnosticPublication) { m.receiveDiagnostics(key, state, p) })
	if unopened, ok := client.(lspUnopenedDiagnostics); ok {
		unopened.OnUnopenedDiagnostics(func(path string, p ttypes.InsightDiagnosticPublication) {
			m.receiveUnopenedDiagnostics(key, state, path, p)
		})
	}
	err = client.Start(ctx)
	if err == nil && managed {
		err = store.MarkExecutableUsedContext(ctx, spec.Command, workspace, func(ctx context.Context) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			return admission()
		})
	}
	cancel()
	m.finishStart(key, state, client, err)
	return true
}

// A managed tool pin belongs to the exact transport, not its registry key.
// Stop always runs off m.mu. Failed/unverified teardown retains the pin even
// after configuration changes or manager shutdown.
type leasedLSPClient struct {
	LSPClient
	release func()
	once    sync.Once
}

func (c *leasedLSPClient) Stop() error {
	if err := c.LSPClient.Stop(); err != nil {
		return err
	}
	c.once.Do(c.release)
	return nil
}

func (c *leasedLSPClient) OnUnopenedDiagnostics(fn func(string, ttypes.InsightDiagnosticPublication)) {
	if unopened, ok := c.LSPClient.(lspUnopenedDiagnostics); ok {
		unopened.OnUnopenedDiagnostics(fn)
	}
}

func (m *LSPManager) finishStart(key string, state *lspClientState, client LSPClient, err error) {
	var assistance ttypes.LSPAssistanceCapabilities
	if err == nil && client != nil {
		assistance = client.AssistanceCapabilities()
	}
	m.mu.Lock()
	stale := m.states[key] != state || state.closing || m.closed || m.paused ||
		m.analysisErrorLocked() != nil || state.epoch != m.settingsGeneration || !m.keyWantedLocked(key)
	if stale || err != nil {
		m.mu.Unlock()
		var stopErr error
		if client != nil {
			stopErr = client.Stop()
		}
		m.mu.Lock()
		if m.retainUnverifiedStopLocked(key, state, stopErr) {
			m.mu.Unlock()
			return
		}
		state.client = nil
		if !stale {
			state.status, state.err = LSPFailed, lspStartError(state.cmd, err)
			m.clearServerDiagnosticsLocked(key)
		} else if m.states[key] == state && !state.closing {
			m.retireLocked(state)
		}
	} else {
		state.assistance = ttypes.CloneLSPAssistance(assistance)
		state.status, state.err, state.lastUsed = LSPConnected, "", time.Now()
	}
	state.cancel = nil
	m.recomputeActiveStatusLocked()
	m.mu.Unlock()
}

// lspStartError says what a failed start means for the user. A missing
// executable is the common case and has one remedy, so it is not reported as
// the transport's stack of wrapped errors.
func lspStartError(cmd string, err error) string {
	if errors.Is(err, exec.ErrNotFound) {
		return cmd + " is not installed or not on PATH. Install it, then choose Retry."
	}
	return cmd + " failed to start: " + err.Error()
}

func (m *LSPManager) liveCountLocked() int {
	n := 0
	for _, state := range m.states {
		if state.client != nil || state.status == LSPStarting {
			n++
		}
	}
	return n
}

func (m *LSPManager) documentCountLocked(key string) int {
	n := 0
	for _, doc := range m.documents {
		if doc.key == key {
			n++
		}
	}
	return n
}

func (m *LSPManager) retireLocked(state *lspClientState) {
	if state.teardownErr != nil {
		return
	}
	state.closing = true
	state.waiting = false
	if state.ticket != nil {
		state.ticket.Cancel() // No effect after Begin; the exact lease stays retained.
	}
	if state.cancel != nil {
		state.cancel()
	}
}

// Stop failure is not a freed process slot. Retain the exact client and publish
// uncertainty, including after disable/shutdown, without retry-spinning a dead
// PID or admitting replacements against an unverified resource budget.
func (m *LSPManager) retainUnverifiedStopLocked(key string, state *lspClientState, err error) bool {
	if err == nil {
		if state.lease != nil {
			state.lease.Release()
			state.lease = nil
		}
		if state.ticket != nil {
			state.ticket.Cancel()
			state.ticket = nil
		}
		return false
	}
	if state.lease != nil {
		state.lease.Retain("Language server teardown is unverified; inspect the retained producer before restarting.")
	}
	state.teardownErr = err
	state.closing, state.waiting, state.cancel = false, false, nil
	state.status = LSPFailed
	state.err = "Language server teardown is unverified; its process slot is retained. Inspect the failure before restarting Crude: " + err.Error()
	m.clearServerDiagnosticsLocked(key)
	m.recomputeActiveStatusLocked()
	if m.logger != nil {
		m.logger.Error("lsp", state.err, "server", state.cmd)
	}
	return true
}

func (m *LSPManager) cancelUpdatesLocked() {
	for path, timer := range m.updateTimers {
		timer.Stop()
		delete(m.updateTimers, path)
	}
}

// OpenFile registers editor intent synchronously; no root probing or pipe I/O
// runs here. Closing before initialization completes prevents a late didOpen.
func (m *LSPManager) OpenFile(_ context.Context, source ttypes.InsightSource, text string) {
	path := source.Path
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return
	}
	if doc := m.documents[path]; doc != nil {
		if doc.text != text || doc.source != source {
			doc.source = source
			m.advanceContextLocked("Canonical source changed; dependent units need reanalysis.")
			doc.text = text
			doc.revision++
		}
		doc.editor = true
	} else {
		m.documents[path] = &lspDocument{text: text, source: source, revision: 1, editor: true}
		// The open document now owns this file's facts. The project view of
		// the file on disk gives way until the document closes again.
		m.removeProjectFactsLocked(func(b ttypes.InsightBoundary) bool { return b.Source.Path == path })
		m.advanceContextLocked("Canonical source view opened; dependent units need reanalysis.")
	}
	m.activeFilePath = path
	m.recomputeActiveStatusLocked()
	m.signalLocked()
}

func (m *LSPManager) SetActiveFile(path string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if path != "" {
		path = filepath.Clean(path)
	}
	if m.activeFilePath == path {
		return
	}
	m.activeFilePath = path
	m.recomputeActiveStatusLocked()
	if m.workerRunning {
		m.signalLocked()
	}
}

func (m *LSPManager) syncDocument(path string, doc *lspDocument) {
	m.documentSend.Lock()
	defer m.documentSend.Unlock()
	m.mu.Lock()
	if m.documents[path] != doc {
		m.mu.Unlock()
		return
	}
	state := m.states[doc.key]
	if state == nil || state.closing || state.stopped || state.status != LSPConnected || m.paused || !m.wantedLocked(doc) || len(doc.text) > m.settings.MaxDocumentBytes {
		m.mu.Unlock()
		return
	}
	if doc.opened && doc.syncedRevision == doc.revision && doc.syncedContext == m.contextGeneration {
		m.mu.Unlock()
		return
	}
	client, text, language, opened := state.client, doc.text, doc.language, doc.opened
	key, revision := doc.key, doc.revision
	boundary := m.diagnosticBoundaryLocked(doc, state)
	doc.syncPending = true
	doc.pendingPublication = nil
	if m.sources == nil || m.sources.Insight == nil {
		doc.syncPending = false
		m.mu.Unlock()
		return
	}
	if err := m.sources.Insight.diagnostics.Observe(boundary); err != nil {
		doc.syncPending = false
		state.err = err.Error()
		state.status = LSPFailed
		m.bumpVersionLocked()
		m.mu.Unlock()
		return
	}
	state.lastUsed = time.Now()
	m.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var wireVersion int
	var sendErr error
	if opened {
		wireVersion, sendErr = client.UpdateFile(ctx, path, text, boundary)
	} else {
		wireVersion, sendErr = client.OpenFile(ctx, path, language, text, boundary)
	}
	m.mu.Lock()
	closeOpened := false
	// Readiness belongs to the completed transport send, not its intent. A
	// probe can otherwise observe an open/current document before OpenFile has
	// installed it in the client and return unsettled immediately. Retain the
	// sent revision so an edit made during the send still needs another sync.
	if m.documents[path] == doc && doc.key == key && m.states[key] == state && state.client == client &&
		!state.closing && !state.stopped && !m.closed && !m.paused && m.wantedLocked(doc) && len(doc.text) <= m.settings.MaxDocumentBytes {
		doc.syncPending = false
		if sendErr == nil {
			doc.opened, doc.syncedRevision, doc.syncedContext, doc.wireVersion, doc.boundary = true, revision, boundary.Context.Generation, wireVersion, boundary
		} else {
			state.err = sendErr.Error()
			state.status = LSPFailed
			m.invalidateDiagnosticsLocked("Document synchronization failed: " + sendErr.Error())
		}
	} else if !opened && m.states[key] == state && state.client == client && !state.closing {
		// The last holder may have left while didOpen was in flight. It could
		// not queue didClose before this send completed. Release the server
		// view under documentSend before a replacement document can be sent.
		closeOpened = true
	}
	pending := doc.pendingPublication
	doc.pendingPublication = nil
	doc.syncPending = false
	// The per-file synchronization snapshot is now observable by shells.
	// A wake without a new version would not publish this readiness change.
	m.bumpVersionLocked()
	m.mu.Unlock()
	if sendErr == nil && pending != nil {
		m.receiveDiagnostics(key, state, *pending)
	}
	if closeOpened {
		client.CloseFile(ctx, path)
	}
}

func (m *LSPManager) CloseFile(_ context.Context, path string) {
	path = filepath.Clean(path)
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.activeFilePath == path {
		m.activeFilePath = ""
	}
	doc := m.documents[path]
	if doc != nil {
		doc.editor = false
		if doc.probes > 0 {
			// An agent request still borrows the server view; it is released
			// with the last probe.
			m.recomputeActiveStatusLocked()
			return
		}
	}
	m.dropDocumentLocked(path, doc)
}

// dropDocumentLocked forgets a document no holder wants and queues its
// didClose; the worker sends it off the caller's goroutine.
func (m *LSPManager) dropDocumentLocked(path string, doc *lspDocument) {
	delete(m.documents, path)
	if timer := m.updateTimers[path]; timer != nil {
		timer.Stop()
		delete(m.updateTimers, path)
	}
	var client LSPClient
	if doc != nil {
		if state := m.states[doc.key]; state != nil {
			state.lastUsed = time.Now()
			if (state.status == LSPStarting || state.ticket != nil && state.lease == nil) && m.documentCountLocked(doc.key) == 0 {
				m.retireLocked(state)
			}
			if doc.opened && !state.closing {
				client = state.client
			}
		}
	}
	m.recomputeActiveStatusLocked()
	if client != nil {
		m.closedDocuments = append(m.closedDocuments, lspClosedDocument{path, client})
	}
	m.advanceContextLocked("Producer document view closed or exceeded its limit.")
	if m.workerRunning {
		m.signalLocked()
	}
}

func (m *LSPManager) QueueFileUpdate(source ttypes.InsightSource, text string) {
	path := source.Path
	m.mu.Lock()
	defer m.mu.Unlock()
	doc := m.documents[path]
	if doc == nil || m.closed {
		return
	}
	if doc.text == text && doc.source == source {
		return
	}
	doc.source = source
	m.advanceContextLocked("Canonical source changed; dependent units need reanalysis.")
	doc.text = text
	doc.revision++
	if len(m.docSymbols[path]) > 0 {
		m.symbolCount -= len(m.docSymbols[path])
		delete(m.docSymbols, path)
		m.bumpVersionLocked()
	}
	if len(text) > m.settings.MaxDocumentBytes && doc.opened {
		if state := m.states[doc.key]; state != nil && state.client != nil {
			m.closedDocuments = append(m.closedDocuments, lspClosedDocument{path, state.client})
		}
		doc.opened = false
		doc.syncedRevision = 0
		m.invalidateDiagnosticsLocked("Producer document view closed or exceeded its limit.")
		m.signalLocked()
	}
	if timer := m.updateTimers[path]; timer != nil {
		timer.Stop()
	}
	var timer *time.Timer
	timer = time.AfterFunc(time.Duration(m.settings.UpdateDebounceMS)*time.Millisecond, func() {
		m.mu.Lock()
		if m.updateTimers[path] != timer {
			m.mu.Unlock()
			return
		}
		delete(m.updateTimers, path)
		m.signalLocked()
		m.mu.Unlock()
	})
	m.updateTimers[path] = timer
}

func (m *LSPManager) NotifyFileSave(ctx context.Context, path, text string, reason DocumentSaveReason, before bool) {
	path = filepath.Clean(path)
	m.mu.Lock()
	doc := m.documents[path]
	if timer := m.updateTimers[path]; timer != nil {
		timer.Stop()
		delete(m.updateTimers, path)
	}
	m.mu.Unlock()
	if doc == nil {
		return
	}
	m.syncDocument(path, doc)
	m.documentSend.Lock()
	defer m.documentSend.Unlock()
	client := m.clientForFile(path)
	if client == nil {
		return
	}
	saveReason := 1
	switch reason {
	case DocumentSaveAfterDelay:
		saveReason = 2
	case DocumentSaveFocusOut:
		saveReason = 3
	}
	client.NotifySave(ctx, path, text, saveReason, before)
}

func (m *LSPManager) clientForFile(path string) LSPClient {
	m.mu.Lock()
	defer m.mu.Unlock()
	doc := m.documents[filepath.Clean(path)]
	if m.paused || doc == nil || !doc.opened || !m.wantedLocked(doc) {
		return nil
	}
	state := m.states[doc.key]
	if state == nil || state.closing || state.stopped || state.status != LSPConnected || len(doc.text) > m.settings.MaxDocumentBytes {
		return nil
	}
	return state.client
}

// Watchers report disk events, never read contents or take ownership of editor
// overlays. Include manifests/configuration, not just known source extensions.
func (m *LSPManager) NotifyWatchedFilesChanged(ctx context.Context, path string, changeType int) {
	m.notifyWatchedFilesChanged(ctx, path, changeType, true, false)
}

func (m *LSPManager) notifyWatchedFilesChanged(ctx context.Context, path string, changeType int, deferReview, invalidated bool) {
	// The edit writer registers exact temporary paths before creating them.
	// Wait for creation to resolve before classifying an event, so a failed
	// exclusive create cannot hide a pre-existing project file.
	if m.editStages.ownsEvent(ctx, path, changeType) || ctx.Err() != nil {
		return
	}
	if deferReview && m.reviewWatch.deferEvent(ctx, path, changeType, func() {
		m.invalidateAssemblyForWatch(ctx, path, changeType)
		m.invalidateWatchedContext(ctx, path)
	}) {
		return
	}
	// Exact reviewed atomic replacements defer their events until the owned
	// transaction settles. Do not scan its transient remove/create topology.
	m.invalidateAssemblyForWatch(ctx, path, changeType)
	m.mu.Lock()
	// A watch generation can be cancelled while this callback waits for the
	// lock. Check inside the same critical section that selects producers.
	if ctx.Err() != nil || m.closed {
		m.mu.Unlock()
		return
	}
	clients := m.watchedClientsLocked(path, !invalidated)
	m.mu.Unlock()
	for _, client := range clients {
		client.NotifyWatchedFilesChanged(ctx, path, changeType)
	}
}

// Invalidate immediately even when ordered server delivery waits for a
// reviewed transaction. This holds no document lock and performs no I/O.
func (m *LSPManager) invalidateWatchedContext(ctx context.Context, path string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ctx.Err() == nil && !m.closed {
		m.watchedClientsLocked(path, true)
	}
}

func (m *LSPManager) watchedClientsLocked(path string, invalidate bool) []LSPClient {
	if invalidate {
		m.advanceContextLocked("Observed project input changed; dependent context is unverified.")
	}
	var clients []LSPClient
	if !m.paused && !m.closed && m.analysisErrorLocked() == nil {
		for key, state := range m.states {
			root, err := fileuri.ToPath(state.rootURI)
			if err != nil || !pathWithin(root, path) {
				continue
			}
			if _, project := m.projects[key]; invalidate && project && state.status == LSPStarting && !state.closing {
				// An initializing producer cannot receive this event yet. Retire
				// it so the next instance indexes after the observed change.
				m.clearServerDiagnosticsLocked(key)
				m.retireLocked(state)
			}
			if state.status == LSPConnected && !state.closing {
				clients = append(clients, state.client)
			}
		}
	}
	if invalidate {
		m.signalLocked()
	}
	return clients
}

// Stop is safe on Update: admission stops immediately; the worker owns teardown.
func (m *LSPManager) Stop() {
	m.mu.Lock()
	m.stopLocked()
	m.mu.Unlock()
}

func (m *LSPManager) stopLocked() {
	m.paused, m.manualStart = true, false
	m.cancelUpdatesLocked()
	for _, state := range m.states {
		m.retireLocked(state)
	}
	for _, doc := range m.documents {
		doc.opened = false
	}
	m.activeConnected, m.activeStatus, m.activeError, m.syncing, m.inflight = false, LSPIdle, "", false, 0
	m.bumpVersionLocked()
	m.invalidateDiagnosticsLocked("Language service was stopped or reconfigured.")
	m.recomputeActiveStatusLocked()
	if len(m.states) > 0 {
		m.signalLocked()
	}
}

// Close joins all owned subprocess lifetimes at workspace teardown, off Update.
func (m *LSPManager) Close() {
	m.reviewWatch.close()
	m.mu.Lock()
	// Publish terminal admission and retirement together. A worker which sees
	// closed must already see every owned server marked for joined shutdown;
	// otherwise it can exit between this flag and a later Stop call.
	m.closed = true
	m.cancelAssemblyInvalidationLocked()
	m.stopLocked()
	if m.workerRunning {
		m.signalLocked()
	}
	if m.assemblyCancel != nil {
		m.assemblyCancel()
	}
	done := m.workerDone
	m.mu.Unlock()
	if done != nil {
		<-done
	}
	// A cancelled listing returns at its next directory; the bound only
	// protects shutdown from a blocked filesystem call.
	m.joinAssembly(5 * time.Second)
}

// Refresh is the user's explicit request to check the project again: every
// language server restarts, and the results the stopped servers reported for
// files no editor holds are removed. A new server reports only files that
// still have problems, so without the removal a file fixed since the old
// report would keep its out-of-date error forever. Automatic restarts (watch
// recovery, settings changes) keep those results as out of date instead.
func (m *LSPManager) Refresh(root string) {
	m.mu.Lock()
	m.stopLocked()
	m.removeProjectFactsLocked(func(ttypes.InsightBoundary) bool { return true })
	m.mu.Unlock()
	m.StartForWorkspace(root)
}

func (m *LSPManager) RestartServer(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.states[id]
	if state == nil || m.closed || m.analysisErrorLocked() != nil {
		return false
	}
	m.paused, m.manualStart = false, true
	state.stopped = false
	m.retireLocked(state)
	m.signalLocked()
	return true
}

func (m *LSPManager) StopServer(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.states[id]
	if state == nil {
		return false
	}
	state.stopped = true
	state.waiting = false
	m.retireLocked(state)
	m.clearServerDiagnosticsLocked(id)
	m.recomputeActiveStatusLocked()
	m.signalLocked()
	return true
}

func (m *LSPManager) requestClient(ctx context.Context, path string) (LSPClient, context.Context, func() bool, context.CancelFunc) {
	return m.requestClientWithin(ctx, path, 10*time.Second)
}

// requestClientWithin is requestClient with the caller's own answer limit. A
// search across a large project is slow while the server still reads it.
func (m *LSPManager) requestClientWithin(ctx context.Context, path string, limit time.Duration) (LSPClient, context.Context, func() bool, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(ctx, limit)
	path = filepath.Clean(path)
	m.mu.Lock()
	doc := m.documents[path]
	if doc == nil {
		m.mu.Unlock()
		return nil, ctx, func() bool { return false }, cancel
	}
	if timer := m.updateTimers[path]; timer != nil {
		timer.Stop()
		delete(m.updateTimers, path)
	}
	revision, key, generation := doc.revision, doc.key, m.settingsGeneration
	m.mu.Unlock()
	m.syncDocument(path, doc)
	client := m.clientForFile(path)
	valid := func() bool {
		m.mu.Lock()
		defer m.mu.Unlock()
		state := m.states[key]
		return !m.paused && !m.closed && m.wantedLocked(doc) && m.settingsGeneration == generation && m.documents[path] == doc && doc.revision == revision && state != nil && state.teardownErr == nil && !state.closing && !state.stopped && state.client == client
	}
	return client, ctx, valid, cancel
}

func (m *LSPManager) clearServerDiagnosticsLocked(key string) {
	if m.sources != nil && m.sources.Insight != nil {
		m.sources.Insight.diagnostics.InvalidateMatching(func(b ttypes.InsightBoundary) bool {
			return isProjectFact(b) && b.Context.ID == key
		}, "Language server stopped; retained results are out of date.")
	}
	m.advanceContextLocked("Language server state changed; retained facts are stale.")
}

func lspServerKey(root string, spec ttypes.LSPServerSpec) string {
	// A JS and a TS document share a process, but different args/env/settings do
	// not. LanguageID is document metadata, never connection identity. Root
	// markers are per language too: they only choose the root, which is
	// already part of the key, so one server for two languages (a module
	// file and its sources) is one process.
	spec.LanguageID = ""
	spec.RootMarkers, spec.WorkspaceRootMarkers = nil, nil
	b, _ := json.Marshal(spec)
	return fmt.Sprintf("%s|%x", root, sha256.Sum256(b))
}

// editorActiveLocked reports whether the editor's own documents may use
// language servers. Agent probes are admitted separately by their callers.
func (m *LSPManager) editorActiveLocked() bool {
	return m.analysisErrorLocked() == nil && m.settings.Enabled && (m.settings.StartOnOpen || m.manualStart)
}

func (m *LSPManager) wantedLocked(doc *lspDocument) bool {
	return m.analysisErrorLocked() == nil && doc != nil && (doc.probes > 0 || doc.editor && m.editorActiveLocked())
}

func (m *LSPManager) anyWantedLocked() bool {
	for _, doc := range m.documents {
		if m.wantedLocked(doc) {
			return true
		}
	}
	for key := range m.projects {
		if m.projectWantedLocked(key) {
			return true
		}
	}
	// Live or closing servers still need the worker for idle eviction and
	// teardown even when no document wants them any more.
	return len(m.states) > 0
}

func (m *LSPManager) keyWantedLocked(key string) bool {
	if m.projectWantedLocked(key) {
		return true
	}
	for _, doc := range m.documents {
		if doc.key == key && m.wantedLocked(doc) && len(doc.text) <= m.settings.MaxDocumentBytes {
			return true
		}
	}
	return false
}

// broadcastLocked wakes probes waiting for a server or document transition.
func (m *LSPManager) broadcastLocked() {
	if m.stateChanged != nil {
		close(m.stateChanged)
	}
	m.stateChanged = make(chan struct{})
}

// resolveServerLocked maps a path to its configured server. The editor's
// master switch is not consulted here: whether anyone may use the server is
// decided per document by wantedLocked.
//
// The assembly plan refines it: a server the plan found in the tool store
// launches from there, and a server the plan found missing does not launch,
// so the status can name the missing program.
func (m *LSPManager) resolveServerLocked(path string) (ttypes.LSPServerSpec, bool) {
	settings := m.settings
	settings.Enabled = true
	spec, ok := settings.ResolveServer(path)
	if !ok {
		return spec, false
	}
	key := assemblyToolKey{spec.ServerID, spec.Command}
	if _, missing := m.toolMissing[key]; missing {
		return ttypes.LSPServerSpec{}, false
	}
	if executable := m.toolPaths[key]; executable != "" {
		spec.Command = executable
	}
	return spec, true
}
