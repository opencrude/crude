package workspace

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"crude/insight/governor"
	ttypes "crude/types"

	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol"
)

// LSPManager is the sole workspace owner of server and open-document lifetimes.
// Expensive root discovery, startup, synchronization and shutdown run on its
// serialized worker; merely enabling intelligence never scans or opens sources.
type LSPManager struct {
	analysisSettings            ttypes.WorkspaceAnalysisSettings
	analysisRootIdentity        string
	analysisBlocked             string
	analysisGovernor            *governor.Governor
	analysisWorkspace           string
	analysisGeneration          string
	analysisGovernorError       string
	analysisRetained            bool
	analysisIdentity            func(string) (string, error)
	contextGeneration           uint64
	sources                     *DocumentService
	mu                          sync.Mutex
	documentSend                sync.Mutex
	states                      map[string]*lspClientState
	documents                   map[string]*lspDocument
	updateTimers                map[string]*time.Timer
	runtime                     LSPRuntime
	settings                    ttypes.LSPSettings
	logger                      Logger
	workspaceRoot               string
	activeFilePath              string
	activeRootURI               string
	activeConnected             bool
	activeError                 string
	activeStatus                LSPStatus
	idleStatus, idleReason      string
	paused, manualStart, closed bool
	workerRunning               bool
	wake                        chan struct{}
	workerDone                  chan struct{}
	closedDocuments             []lspClosedDocument
	settingsGeneration          uint64
	docSymbols                  map[string][]LSPSymbol
	symbolCount                 int
	symbolsCached               bool
	syncing                     bool
	inflight                    int
	version                     uint64
	stateChanged                chan struct{}
}

func (m *LSPManager) Status() (connected bool, status LSPStatus, errMsg string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.activeConnected, m.activeStatus, m.activeError
}

func (m *LSPManager) recomputeActiveStatusLocked() {
	defer m.recomputeIdleReasonLocked()
	m.bumpVersionLocked()
	// Teardown uncertainty remains actionable even while language features are
	// disabled. Paused is an admission decision, not evidence that work stopped.
	failedKey := ""
	for key, state := range m.states {
		if state.teardownErr != nil && (failedKey == "" || key < failedKey) {
			failedKey = key
		}
	}
	if failedKey != "" {
		m.applyAggregateStateLocked(*m.states[failedKey])
		return
	}
	if m.paused || m.closed || m.analysisErrorLocked() != nil || !m.settings.Enabled {
		m.applyAggregateStateLocked(lspClientState{status: LSPIdle})
		return
	}
	if state, ok := m.activeFileStateLocked(); ok {
		m.applyAggregateStateLocked(*state)
		return
	}

	keys := make([]string, 0, len(m.states))
	for key := range m.states {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var firstStarting, firstFailed *lspClientState
	for _, key := range keys {
		state := m.states[key]
		if state.closing || state.stopped {
			continue
		}
		switch state.status {
		case LSPConnected:
			m.applyAggregateStateLocked(*state)
			return
		case LSPStarting:
			if firstStarting == nil {
				firstStarting = state
			}
		case LSPFailed:
			if firstFailed == nil {
				firstFailed = state
			}
		}
	}
	if firstStarting != nil {
		m.applyAggregateStateLocked(*firstStarting)
		return
	}
	if firstFailed != nil {
		m.applyAggregateStateLocked(*firstFailed)
		return
	}
	m.applyAggregateStateLocked(lspClientState{status: LSPIdle})
}

func (m *LSPManager) recomputeIdleReasonLocked() {
	m.idleStatus, m.idleReason = "", ""
	if m.activeStatus != LSPIdle {
		return
	}
	switch {
	case m.analysisErrorLocked() != nil:
		m.idleStatus, m.idleReason = "Static only", m.analysisErrorLocked().Error()
	case !m.settings.Enabled:
		m.idleStatus, m.idleReason = "Language intelligence off", "Enable Language intelligence in Settings → Code Intelligence."
	case m.paused || m.closed:
		m.idleStatus, m.idleReason = "Language servers paused", "Use Start Language Servers to resume services for open source files."
	case !m.settings.StartOnOpen && !m.manualStart:
		m.idleStatus, m.idleReason = "Automatic start off", "Enable Start on open in Settings → Code Intelligence, or use Start Language Servers."
	case m.activeError != "":
		m.idleStatus, m.idleReason = "Language services unavailable", m.activeError
	case len(m.documents) == 0:
		m.idleStatus, m.idleReason = "Waiting for source file", "Open a supported source file (for example, .go) to start its server. Enabling does not scan the workspace."
	default:
		if doc := m.documents[m.activeFilePath]; doc != nil {
			if state := m.states[doc.key]; state != nil && state.stopped {
				m.idleStatus, m.idleReason = "Language server stopped", "Restart the stopped server to resume services for this file."
				return
			}
			if _, ok := m.resolveServerLocked(m.activeFilePath); !ok {
				m.idleStatus, m.idleReason = "No matching language server", "No enabled server matches the open file. Check Settings → Language Servers; server executables must be installed separately."
				return
			}
		}
		m.idleStatus, m.idleReason = "Language services idle", "Services start only for supported open source files, not every file in the workspace."
	}
}

func (m *LSPManager) activeFileStateLocked() (*lspClientState, bool) {
	doc := m.documents[m.activeFilePath]
	if doc == nil {
		return nil, false
	}
	if len(doc.text) > m.settings.MaxDocumentBytes {
		return &lspClientState{status: LSPIdle, err: "Document exceeds the configured LSP size limit."}, true
	}
	state, ok := m.states[doc.key]
	if ok && (state.closing || state.stopped) {
		return &lspClientState{rootURI: state.rootURI, cmd: state.cmd, status: LSPIdle}, true
	}
	return state, ok
}

func (m *LSPManager) applyAggregateStateLocked(state lspClientState) {
	m.activeStatus = state.status
	m.activeConnected = state.status == LSPConnected
	m.activeError = state.err
	if state.rootURI != "" {
		m.activeRootURI = state.rootURI
	}
}

// ServerReady returns true when at least one LSP client has reached
// the connected state. Used by the right-click context menu to
// enable LSP-backed items.
func (m *LSPManager) ServerReady() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.activeConnected && m.activeStatus == LSPConnected
}

// IsConfigured returns true if StartForWorkspace has been called and
// produced a client (or attempted to). Distinguishes "never tried"
// (LSPIdle, no clients) from "configured but not connected" (e.g.
// LSPStarting, LSPFailed, or LSPConnected with activeConnected=true
// but no successful client). Used to enable "Restart LSP" only when
// there's actually something to restart.
func (m *LSPManager) IsConfigured() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.states) > 0 {
		return true
	}
	return m.activeStatus != LSPIdle
}

// Changed atomically captures the version and the next state broadcast. The
// same channel wakes document probes and retained-core publication.
func (m *LSPManager) Changed() (uint64, <-chan struct{}) {
	if m == nil {
		return 0, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stateChanged == nil {
		m.stateChanged = make(chan struct{})
	}
	return m.version, m.stateChanged
}

func (m *LSPManager) bumpVersionLocked() {
	m.version++
	m.broadcastLocked()
}

func (m *LSPManager) Version() uint64 {
	if m == nil {
		return 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.version
}

// ProjectInsights combines lifecycle health with qualified, observed facts.
func (m *LSPManager) ProjectInsights() ProjectInsights {
	m.mu.Lock()
	ins := ProjectInsights{ServerName: m.activeServerNameLocked(), ServerReady: m.activeStatus == LSPConnected && !m.isFailedLocked() && !m.syncing,
		ServerBusy: m.inflight > 0, ServerStarting: m.activeStatus == LSPStarting, ServerSyncing: m.syncing,
		ServerFailed: m.isFailedLocked(), ServerError: m.activeError, IdleStatus: m.idleStatus, IdleReason: m.idleReason, SymbolsCount: m.symbolCount}
	settings := ttypes.LSPSettings{Diagnostics: m.settings.Diagnostics, MinSeverity: m.settings.MinSeverity}
	sources := m.sources
	m.mu.Unlock()
	summary := ProjectInsights{}
	if sources != nil && sources.Insight != nil {
		summary = sources.Insight.diagnosticSummaryFor(settings)
	} else {
		summary.CoverageDetail = "No canonical source authority; project coverage is unverified."
	}
	summary.ServerName, summary.ServerReady, summary.ServerBusy = ins.ServerName, ins.ServerReady, ins.ServerBusy
	summary.ServerStarting, summary.ServerSyncing, summary.ServerFailed = ins.ServerStarting, ins.ServerSyncing, ins.ServerFailed
	summary.ServerError, summary.IdleStatus, summary.IdleReason, summary.SymbolsCount = ins.ServerError, ins.IdleStatus, ins.IdleReason, ins.SymbolsCount
	return summary
}
func (m *LSPManager) ProjectDiagnosticCounts() (int, int, int) {
	view := m.ProjectInsights()
	return view.Errors, view.Warnings, view.Info + view.Hints
}

// ServerHealthSnapshots exposes every configured/attempted server separately.
// Diagnostic counts remain project-scoped until the cache records server origin.
func (m *LSPManager) ServerHealthSnapshots() []ttypes.LSPServerHealth {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	out := make([]ttypes.LSPServerHealth, 0, len(m.states))
	for id, state := range m.states {
		health := ttypes.LSPServerHealth{
			ID: id, Name: filepath.Base(state.cmd), RootURI: state.rootURI,
			Status: lspStatusLabel(state.status), Error: state.err,
			Connected: state.status == LSPConnected,
			Starting:  state.status == LSPStarting,
			Syncing:   m.syncing, Busy: m.inflight > 0,
		}
		if state.waiting {
			health.Status = "Waiting for slot"
		}
		if state.closing {
			health.Status, health.Connected, health.Starting = "Stopping", false, false
		}
		if state.stopped && state.teardownErr == nil {
			health.Status, health.Connected, health.Starting = "Stopped", false, false
		}
		out = append(out, health)
	}
	m.mu.Unlock()
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].RootURI < out[j].RootURI
	})
	return out
}

func lspStatusLabel(status LSPStatus) string {
	switch status {
	case LSPStarting:
		return "Starting"
	case LSPConnected:
		return "Connected"
	case LSPFailed:
		return "Failed"
	case LSPIdle:
		return "Stopped"
	default:
		return "Not configured"
	}
}

func (m *LSPManager) activeServerNameLocked() string {
	if state, ok := m.activeFileStateLocked(); ok {
		return state.cmd
	}
	keys := make([]string, 0, len(m.states))
	for key := range m.states {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if state := m.states[key]; state.status == LSPConnected {
			return state.cmd
		}
	}
	for _, key := range keys {
		return m.states[key].cmd
	}
	return ""
}

func (m *LSPManager) isFailedLocked() bool {
	return m.activeStatus == LSPFailed || (m.activeError != "" && m.activeStatus != LSPConnected)
}

func (m *LSPManager) HasActiveFile() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.activeFilePath != ""
}

// InflightRequests returns the number of LSP requests currently in flight.
// The status bar uses this to show the busy indicator.
func (m *LSPManager) InflightRequests() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.inflight
}

//  ─ Request routing ──
//
// The methods below route a request to the correct LSP client (chosen by file
// extension + project root) and track in-flight counts for the busy indicator.
// All routes share the same bookkeeping helpers to keep the busy state
// accurate even when a request fails or panics.

// trackInflight increments the in-flight counter for the duration of fn.
func (m *LSPManager) trackInflight(fn func() error) error {
	m.mu.Lock()
	m.inflight++
	m.bumpVersionLocked()
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		m.inflight--
		if m.inflight < 0 {
			m.inflight = 0
		}
		m.bumpVersionLocked()
		m.mu.Unlock()
	}()
	return fn()
}

func (m *LSPManager) featureOn(pred func(ttypes.LSPSettings) bool) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.analysisErrorLocked() == nil && m.settings.Enabled && pred(m.settings)
}

func (m *LSPManager) ShowDiagnosticsInEditor() bool {
	return m.featureOn(func(s ttypes.LSPSettings) bool { return s.Diagnostics && s.DiagInEditor })
}

func (m *LSPManager) ShowDiagnosticsInTree() bool {
	return m.featureOn(func(s ttypes.LSPSettings) bool { return s.Diagnostics && s.DiagInTree })
}

func (m *LSPManager) ShowDiagnosticsInStatus() bool {
	return m.featureOn(func(s ttypes.LSPSettings) bool { return s.Diagnostics && s.DiagInStatus })
}

// RequestDefinition routes a "go to definition" request to the right client.
func (m *LSPManager) RequestDefinition(ctx context.Context, filePath string, line, col int) []LSPLocation {
	if !m.featureOn(func(s ttypes.LSPSettings) bool { return s.Definition }) {
		return nil
	}
	var out []LSPLocation
	_ = m.trackInflight(func() error {
		client, requestCtx, valid, cancel := m.requestClient(ctx, filePath)
		defer cancel()
		if client == nil {
			return nil
		}
		out = client.RequestDefinition(requestCtx, filePath, line, col)
		if !valid() {
			out = nil
		}
		return nil
	})
	return out
}

// FindReferences routes a "find references" request to the right client.
func (m *LSPManager) FindReferences(ctx context.Context, filePath string, line, col int, includeDeclaration bool) []LSPLocation {
	if !m.featureOn(func(s ttypes.LSPSettings) bool { return s.References }) {
		return nil
	}
	var out []LSPLocation
	_ = m.trackInflight(func() error {
		client, requestCtx, valid, cancel := m.requestClient(ctx, filePath)
		defer cancel()
		if client == nil {
			return nil
		}
		out = client.FindReferences(requestCtx, filePath, line, col, includeDeclaration)
		if !valid() {
			out = nil
		}
		return nil
	})
	return out
}

// RequestDocumentSymbols routes a "document symbols" request to the right
// client and caches the result for the document outline panel.
func (m *LSPManager) RequestDocumentSymbols(ctx context.Context, filePath string) []LSPSymbol {
	if !m.featureOn(func(s ttypes.LSPSettings) bool { return s.Outline }) {
		return nil
	}
	path := filepath.Clean(filePath)
	m.mu.Lock()
	epoch, owner := m.settingsGeneration, m.documents[path]
	var revision uint64
	if owner != nil {
		revision = owner.revision
	}
	m.mu.Unlock()
	var out []LSPSymbol
	_ = m.trackInflight(func() error {
		client, requestCtx, valid, cancel := m.requestClient(ctx, filePath)
		defer cancel()
		if client == nil {
			return nil
		}
		out = client.RequestDocumentSymbols(requestCtx, filePath)
		if !valid() {
			out = nil
		}
		if out != nil {
			m.mu.Lock()
			if m.analysisErrorLocked() != nil || m.closed || m.paused || !m.settings.Enabled || !m.settings.Outline ||
				m.settingsGeneration != epoch || owner == nil || m.documents[path] != owner || owner.revision != revision {
				out = nil
				m.mu.Unlock()
				return nil
			}
			m.symbolCount -= len(m.docSymbols[path])
			m.docSymbols[path] = out
			m.symbolCount += len(out)
			m.symbolsCached = true
			m.bumpVersionLocked()
			m.mu.Unlock()
		}
		return nil
	})
	return out
}

// DocumentSymbols returns the cached document outline for a file, if any.
// Callers should not assume the cache is fresh; refresh via
// RequestDocumentSymbols when the file's content has changed materially.
func (m *LSPManager) DocumentSymbols(filePath string) []LSPSymbol {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.docSymbols[filepath.Clean(filePath)]
}

// AllDocumentSymbols returns the cached outlines for every file. Used by the
// workspace symbols fuzzy search so we can search across the whole project
// without hitting the LSP server again.
func (m *LSPManager) AllDocumentSymbols() map[string][]LSPSymbol {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string][]LSPSymbol, len(m.docSymbols))
	for k, v := range m.docSymbols {
		out[k] = v
	}
	return out
}

// RequestHover routes a hover request to the right client.
func (m *LSPManager) RequestHover(ctx context.Context, filePath string, line, col int) (string, error) {
	if !m.featureOn(func(s ttypes.LSPSettings) bool { return s.Hover }) {
		return "", nil
	}
	var (
		out string
		err error
	)
	err = m.trackInflight(func() error {
		client, requestCtx, valid, cancel := m.requestClient(ctx, filePath)
		defer cancel()
		if client == nil {
			return fmt.Errorf("no LSP client for %s", filePath)
		}
		var requestErr error
		out, requestErr = client.RequestHover(requestCtx, filePath, line, col)
		if !valid() {
			out = ""
			return fmt.Errorf("LSP document changed during hover")
		}
		return requestErr
	})
	return out, err
}

// RequestRename routes a rename request to the right client.
func (m *LSPManager) RequestRename(ctx context.Context, filePath string, line, col int, newName string) (*protocol.WorkspaceEdit, error) {
	if !m.featureOn(func(s ttypes.LSPSettings) bool { return s.Rename }) {
		return nil, nil
	}
	var (
		out *protocol.WorkspaceEdit
		err error
	)
	err = m.trackInflight(func() error {
		client, requestCtx, valid, cancel := m.requestClient(ctx, filePath)
		defer cancel()
		if client == nil {
			return fmt.Errorf("no LSP client for %s", filePath)
		}
		var requestErr error
		out, requestErr = client.RequestRename(requestCtx, filePath, line, col, newName)
		if !valid() {
			out = nil
			return fmt.Errorf("LSP document changed during rename")
		}
		return requestErr
	})
	return out, err
}

// RequestCompletions routes a completion request to the right client.
func (m *LSPManager) RequestCompletions(ctx context.Context, filePath string, line, col int) []CompletionItem {
	if !m.featureOn(func(s ttypes.LSPSettings) bool { return s.Completions }) {
		return nil
	}
	var out []CompletionItem
	_ = m.trackInflight(func() error {
		client, requestCtx, valid, cancel := m.requestClient(ctx, filePath)
		defer cancel()
		if client == nil {
			return nil
		}
		out = client.RequestCompletions(requestCtx, filePath, line, col)
		if !valid() {
			out = nil
		}
		return nil
	})
	return out
}

// InvalidateFile removes all cached state for a file. Call this when a file
// is deleted, renamed, or its content has been replaced wholesale.
func (m *LSPManager) InvalidateFile(filePath string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	fp := filepath.Clean(filePath)
	m.invalidateDiagnosticsLocked("Source path invalidated.")
	m.symbolCount -= len(m.docSymbols[fp])
	delete(m.docSymbols, fp)
}
