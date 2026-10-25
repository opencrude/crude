package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"time"

	ttypes "crude/types"
)

type lspClientState struct {
	rootURI  string
	cmd      string
	args     []string
	spec     ttypes.LSPServerSpec
	status   LSPStatus
	err      string
	client   LSPClient
	cancel   context.CancelFunc
	closing  bool
	waiting  bool
	stopped  bool
	lastUsed time.Time
}

type lspDocument struct {
	text           string
	key            string
	language       string
	opened         bool
	revision       uint64
	syncedRevision uint64
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
		states: make(map[string]*lspClientState), documents: make(map[string]*lspDocument),
		updateTimers: make(map[string]*time.Timer), wake: make(chan struct{}, 1),
		fileDiagnostics: make(map[string][]LSPDiagnostic), docSymbols: make(map[string][]LSPSymbol),
		fileLastSync: make(map[string]int64), fileDiagnosticServer: make(map[string]string),
		serverDiagnosticCounts: make(map[string]diagnosticAggregate), issueCount: make(map[string]int),
		diagnosticChanges: make(map[string]bool), stateChanged: make(chan struct{}),
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
	if len(m.states) > 0 || len(m.documents) > 0 {
		m.signalLocked()
	}
	m.mu.Unlock()
}

// SetWorkspaceRoot is passive and performs no scan, process launch or disk I/O.
func (m *LSPManager) SetWorkspaceRoot(root string) {
	m.mu.Lock()
	m.workspaceRoot = filepath.Clean(root)
	m.mu.Unlock()
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
	m.settings = settings
	m.settingsGeneration++
	// Changed launch policy invalidates the old process identity, never mutates
	// a live connection's environment/settings beneath its outstanding requests.
	for _, state := range m.states {
		m.retireLocked(state)
	}
	for _, doc := range m.documents {
		doc.key, doc.opened = "", false
	}
	m.cancelUpdatesLocked()
	m.recomputeActiveStatusLocked()
	m.clearDiagnosticsLocked()
	if len(m.states) > 0 || len(m.documents) > 0 {
		m.signalLocked()
	}
	m.mu.Unlock()
}

// StartForWorkspace explicitly resumes intelligence for retained editor buffers.
// It never fabricates didOpen messages for the contents of the workspace.
func (m *LSPManager) StartForWorkspace(root string) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.workspaceRoot = filepath.Clean(root)
	m.paused, m.manualStart = false, true
	for _, state := range m.states {
		state.stopped = false
		if state.status == LSPFailed {
			m.retireLocked(state)
		}
	}
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
		select {
		case <-m.wake:
		case <-ticker.C:
		}
	}
}

func (m *LSPManager) reconcile() bool {
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
		if !state.closing {
			continue
		}
		client := state.client
		m.mu.Unlock()
		if client != nil {
			client.Stop()
		}
		m.mu.Lock()
		if m.states[key] == state {
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
	if m.closed || m.paused || m.runtime == nil || !m.anyWantedLocked() {
		m.mu.Unlock()
		return false
	}
	// A disconnected server is not silently restarted every tick or keypress.
	for key, state := range m.states {
		if state.client == nil {
			continue
		}
		select {
		case <-state.client.Done():
			client := state.client
			state.client = nil
			state.status, state.err = LSPFailed, "Language server connection lost; use Restart Language Servers."
			m.clearServerDiagnosticsLocked(key)
			m.recomputeActiveStatusLocked()
			m.mu.Unlock()
			client.Stop()
			return true
		default:
		}
		if m.documentCountLocked(key) == 0 && time.Since(state.lastUsed) >= time.Duration(m.settings.IdleTimeoutSeconds)*time.Second {
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
		m.mu.Unlock()
		rootURI := resolveLSPRoot(path, root, spec.RootMarkers)
		key := lspServerKey(rootURI, spec)
		m.mu.Lock()
		if m.documents[path] != doc || generation != m.settingsGeneration || m.paused || m.closed {
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
		if doc.opened && (doc.syncedRevision == doc.revision || m.updateTimers[path] != nil) {
			continue
		}
		if state.status == LSPConnected && state.client != nil {
			m.mu.Unlock()
			m.syncDocument(path, doc)
			return true
		}
		if state.status != LSPIdle && !state.waiting {
			continue
		}
		if m.liveCountLocked() >= m.settings.MaxServers {
			var idle *lspClientState
			for key, candidate := range m.states {
				if candidate.client != nil && m.documentCountLocked(key) == 0 && (idle == nil || candidate.lastUsed.Before(idle.lastUsed)) {
					idle = candidate
				}
			}
			if idle != nil {
				m.retireLocked(idle)
				m.mu.Unlock()
				return true
			}
			if !state.waiting {
				state.status, state.waiting = LSPIdle, true
				state.err = "Server limit reached; close another language's files or increase Maximum servers."
				m.recomputeActiveStatusLocked()
			}
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		state.status, state.err, state.waiting, state.cancel = LSPStarting, "", false, cancel
		key, runtime := doc.key, m.runtime
		m.recomputeActiveStatusLocked()
		m.mu.Unlock()
		client := runtime.NewClient(state.spec, state.rootURI)
		if client == nil {
			cancel()
			m.finishStart(key, state, nil, fmt.Errorf("LSP transport is unavailable"))
			return true
		}
		m.mu.Lock()
		state.client = client
		m.mu.Unlock()
		client.OnDiagnostics(func(fp string, diags []LSPDiagnostic) { m.receiveDiagnostics(key, state, fp, diags) })
		err := client.Start(ctx)
		cancel()
		m.finishStart(key, state, client, err)
		return true
	}
	m.mu.Unlock()
	return false
}

func (m *LSPManager) finishStart(key string, state *lspClientState, client LSPClient, err error) {
	m.mu.Lock()
	stale := m.states[key] != state || state.closing || m.closed || m.paused || !m.keyWantedLocked(key)
	if stale || err != nil {
		m.mu.Unlock()
		if client != nil {
			client.Stop()
		}
		m.mu.Lock()
		state.client = nil
		if !stale {
			state.status, state.err = LSPFailed, lspStartError(state.cmd, err)
			m.clearServerDiagnosticsLocked(key)
		}
	} else {
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
	state.closing = true
	if state.cancel != nil {
		state.cancel()
	}
}

func (m *LSPManager) cancelUpdatesLocked() {
	for path, timer := range m.updateTimers {
		timer.Stop()
		delete(m.updateTimers, path)
	}
}

// OpenFile registers editor intent synchronously; no root probing or pipe I/O
// runs here. Closing before initialization completes prevents a late didOpen.
func (m *LSPManager) OpenFile(_ context.Context, path, text string) {
	path = filepath.Clean(path)
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return
	}
	if doc := m.documents[path]; doc != nil {
		if doc.text != text {
			doc.text = text
			doc.revision++
		}
		doc.editor = true
	} else {
		m.documents[path] = &lspDocument{text: text, revision: 1, editor: true}
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
	if doc.opened && doc.syncedRevision == doc.revision {
		m.mu.Unlock()
		return
	}
	client, text, language, opened := state.client, doc.text, doc.language, doc.opened
	key, revision := doc.key, doc.revision
	state.lastUsed = time.Now()
	m.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if opened {
		client.UpdateFile(ctx, path, text)
	} else {
		client.OpenFile(ctx, path, language, text)
	}
	m.mu.Lock()
	closeOpened := false
	// Readiness belongs to the completed transport send, not its intent. A
	// probe can otherwise observe an open/current document before OpenFile has
	// installed it in the client and return unsettled immediately. Retain the
	// sent revision so an edit made during the send still needs another sync.
	if m.documents[path] == doc && doc.key == key && m.states[key] == state && state.client == client &&
		!state.closing && !state.stopped && !m.closed && !m.paused && m.wantedLocked(doc) && len(doc.text) <= m.settings.MaxDocumentBytes {
		doc.opened, doc.syncedRevision = true, revision
	} else if !opened && m.states[key] == state && state.client == client && !state.closing {
		// The last holder may have left while didOpen was in flight. It could
		// not queue didClose before this send completed. Release the server
		// view under documentSend before a replacement document can be sent.
		closeOpened = true
	}
	m.broadcastLocked()
	m.mu.Unlock()
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
			if state.status == LSPStarting && m.documentCountLocked(doc.key) == 0 {
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
	m.setFileDiagnosticsLocked(m.fileDiagnosticServer[path], path, nil)
	if m.workerRunning {
		m.signalLocked()
	}
}

func (m *LSPManager) UpdateFile(_ context.Context, path, text string) {
	path = filepath.Clean(path)
	m.mu.Lock()
	doc := m.documents[path]
	if doc != nil && doc.text != text {
		doc.text = text
		doc.revision++
		if len(m.docSymbols[path]) > 0 {
			m.symbolCount -= len(m.docSymbols[path])
			delete(m.docSymbols, path)
			m.bumpVersionLocked()
		}
	}
	if timer := m.updateTimers[path]; timer != nil {
		timer.Stop()
		delete(m.updateTimers, path)
	}
	m.mu.Unlock()
	if doc != nil {
		m.syncDocument(path, doc)
	}
}

func (m *LSPManager) QueueFileUpdate(path, text string) {
	path = filepath.Clean(path)
	m.mu.Lock()
	defer m.mu.Unlock()
	doc := m.documents[path]
	if doc == nil || m.closed {
		return
	}
	if doc.text == text {
		return
	}
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
		m.setFileDiagnosticsLocked(m.fileDiagnosticServer[path], path, nil)
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
	m.mu.Lock()
	var clients []LSPClient
	if !m.paused {
		for _, state := range m.states {
			if state.status == LSPConnected && !state.closing && pathWithin(filePathFromURI(state.rootURI), path) {
				clients = append(clients, state.client)
			}
		}
	}
	m.mu.Unlock()
	for _, client := range clients {
		client.NotifyWatchedFilesChanged(ctx, path, changeType)
	}
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
	m.clearDiagnosticsLocked()
	m.recomputeIdleReasonLocked()
	if len(m.states) > 0 {
		m.signalLocked()
	}
}

// Close joins all owned subprocess lifetimes at workspace teardown, off Update.
func (m *LSPManager) Close() {
	m.mu.Lock()
	// Publish terminal admission and retirement together. A worker which sees
	// closed must already see every owned server marked for joined shutdown;
	// otherwise it can exit between this flag and a later Stop call.
	m.closed = true
	m.stopLocked()
	if m.workerRunning {
		m.signalLocked()
	}
	done := m.workerDone
	m.mu.Unlock()
	if done != nil {
		<-done
	}
}

func (m *LSPManager) RestartServer(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.states[id]
	if state == nil || m.closed {
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

func (m *LSPManager) captureOnDiagnostics() func(string, []LSPDiagnostic) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.onDiagnostics
}

func (m *LSPManager) SetOnDiagnostics(fn func(string, []LSPDiagnostic)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onDiagnostics = fn
}

func (m *LSPManager) receiveDiagnostics(key string, owner *lspClientState, path string, diags []LSPDiagnostic) {
	m.mu.Lock()
	if m.states[key] != owner || owner.closing || owner.stopped || m.paused || m.closed {
		m.mu.Unlock()
		return
	}
	if doc := m.documents[filepath.Clean(path)]; doc != nil && len(doc.text) > m.settings.MaxDocumentBytes {
		m.mu.Unlock()
		return
	}
	m.setFileDiagnosticsLocked(key, path, diags)
	filtered := append([]LSPDiagnostic(nil), m.fileDiagnostics[filepath.Clean(path)]...)
	cb := m.onDiagnostics
	m.mu.Unlock()
	if cb != nil {
		cb(filepath.Clean(path), filtered)
	}
}

func (m *LSPManager) requestClient(ctx context.Context, path string) (LSPClient, context.Context, func() bool, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
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
		return !m.paused && !m.closed && m.wantedLocked(doc) && m.settingsGeneration == generation && m.documents[path] == doc && doc.revision == revision && state != nil && !state.closing && !state.stopped && state.client == client
	}
	return client, ctx, valid, cancel
}

func (m *LSPManager) clearServerDiagnosticsLocked(key string) {
	for path, source := range m.fileDiagnosticServer {
		if source == key {
			m.setFileDiagnosticsLocked(key, path, nil)
		}
	}
}

func lspServerKey(root string, spec ttypes.LSPServerSpec) string {
	// A JS and a TS document share a process, but different args/env/settings do
	// not. LanguageID is document metadata, never connection identity.
	spec.LanguageID = ""
	b, _ := json.Marshal(spec)
	return fmt.Sprintf("%s|%x", root, sha256.Sum256(b))
}

// editorActiveLocked reports whether the editor's own documents may use
// language servers. Agent probes are admitted separately by their callers.
func (m *LSPManager) editorActiveLocked() bool {
	return m.settings.Enabled && (m.settings.StartOnOpen || m.manualStart)
}

func (m *LSPManager) wantedLocked(doc *lspDocument) bool {
	return doc != nil && (doc.probes > 0 || doc.editor && m.editorActiveLocked())
}

func (m *LSPManager) anyWantedLocked() bool {
	for _, doc := range m.documents {
		if m.wantedLocked(doc) {
			return true
		}
	}
	// Live or closing servers still need the worker for idle eviction and
	// teardown even when no document wants them any more.
	return len(m.states) > 0
}

func (m *LSPManager) keyWantedLocked(key string) bool {
	for _, doc := range m.documents {
		if doc.key == key && m.wantedLocked(doc) {
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
func (m *LSPManager) resolveServerLocked(path string) (ttypes.LSPServerSpec, bool) {
	settings := m.settings
	settings.Enabled = true
	return settings.ResolveServer(path)
}
