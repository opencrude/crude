package workspace

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	ttypes "crude/types"

	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol"
)

// LSPManager is the sole workspace owner of server and open-document lifetimes.
// Expensive root discovery, startup, synchronization and shutdown run on its
// serialized worker; merely enabling intelligence never scans or opens sources.
type LSPManager struct {
	mu                                                                    sync.Mutex
	documentSend                                                          sync.Mutex
	states                                                                map[string]*lspClientState
	documents                                                             map[string]*lspDocument
	updateTimers                                                          map[string]*time.Timer
	runtime                                                               LSPRuntime
	settings                                                              ttypes.LSPSettings
	logger                                                                Logger
	workspaceRoot                                                         string
	activeFilePath                                                        string
	activeRootURI                                                         string
	activeConnected                                                       bool
	activeError                                                           string
	activeStatus                                                          LSPStatus
	idleStatus, idleReason                                                string
	paused, manualStart, closed                                           bool
	workerRunning                                                         bool
	wake                                                                  chan struct{}
	workerDone                                                            chan struct{}
	diagnosticChanges                                                     map[string]bool
	closedDocuments                                                       []lspClosedDocument
	settingsGeneration                                                    uint64
	onDiagnostics                                                         func(string, []LSPDiagnostic)
	fileDiagnostics                                                       map[string][]LSPDiagnostic
	docSymbols                                                            map[string][]LSPSymbol
	fileLastSync                                                          map[string]int64
	fileDiagnosticServer                                                  map[string]string
	serverDiagnosticCounts                                                map[string]diagnosticAggregate
	issueCount                                                            map[string]int
	symbolsCached                                                         bool
	diagnosticErrors, diagnosticWarnings, diagnosticInfo, diagnosticHints int
	filesAffected, symbolCount                                            int
	projectLastSync                                                       int64
	topIssue                                                              string
	syncing                                                               bool
	inflight                                                              int
	version                                                               uint64
	stateChanged                                                          chan struct{}
}

type diagnosticAggregate struct{ files, errors, warnings, information, hints int }

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
	if m.paused || m.closed || !m.settings.Enabled {
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

// AllDiagnostics returns a snapshot of every cached diagnostic across
// all files the LSP server has reported on. The returned map is a
// copy; mutating it does not affect internal state. Used by the
// status bar's "Goto first error" action and similar cross-file
// navigation features.
func (m *LSPManager) AllDiagnostics() map[string][]LSPDiagnostic {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string][]LSPDiagnostic, len(m.fileDiagnostics))
	for k, v := range m.fileDiagnostics {
		out[k] = append([]LSPDiagnostic(nil), v...)
	}
	return out
}

// ClearDiagnostics removes all cached diagnostics, document symbols,
// per-file sync timestamps, and the top-issue tally. Call this when
// a server restart should produce a clean slate rather than showing
// stale results from the previous session.
func (m *LSPManager) ClearDiagnostics() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.clearDiagnosticsLocked()
}

func (m *LSPManager) clearDiagnosticsLocked() {
	for path := range m.fileDiagnostics {
		m.diagnosticChanges[path] = true
	}
	m.fileDiagnostics = make(map[string][]LSPDiagnostic)
	m.docSymbols = make(map[string][]LSPSymbol)
	m.fileLastSync = make(map[string]int64)
	m.fileDiagnosticServer = make(map[string]string)
	m.serverDiagnosticCounts = make(map[string]diagnosticAggregate)
	m.issueCount = make(map[string]int)
	m.symbolsCached = false
	m.diagnosticErrors, m.diagnosticWarnings = 0, 0
	m.diagnosticInfo, m.diagnosticHints = 0, 0
	m.filesAffected, m.symbolCount, m.projectLastSync, m.topIssue = 0, 0, 0, ""
	m.bumpVersionLocked()
}

// DrainDiagnosticChanges coalesces server notifications into one UI projection
// per tick. It never performs I/O or blocks the server's protocol reader.
func (m *LSPManager) DrainDiagnosticChanges() map[string][]LSPDiagnostic {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.diagnosticChanges) == 0 {
		return nil
	}
	out := make(map[string][]LSPDiagnostic, len(m.diagnosticChanges))
	for path := range m.diagnosticChanges {
		out[path] = append([]LSPDiagnostic(nil), m.fileDiagnostics[path]...)
	}
	clear(m.diagnosticChanges)
	return out
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

// FileLastSync returns the UnixNano timestamp of the most recent
// diagnostic publish for the given file, or 0 if the server has
// never reported on it. Lets navigation features skip stale entries.
func (m *LSPManager) FileLastSync(filePath string) int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.fileLastSync[filepath.Clean(filePath)]
}

// ProjectDiagnosticCounts aggregates diagnostics across all tracked files.
func (m *LSPManager) ProjectDiagnosticCounts() (errors, warnings, hints int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.diagnosticErrors, m.diagnosticWarnings, m.diagnosticInfo + m.diagnosticHints
}

// ProjectInsights returns a whole-project snapshot suitable for the bottom-left
// status bar. Always reflects the state of every file the LSP server knows
// about, regardless of which file (if any) is open in the editor.
func (m *LSPManager) ProjectInsights() ProjectInsights {
	m.mu.Lock()
	defer m.mu.Unlock()

	ins := ProjectInsights{
		ServerName:     m.activeServerNameLocked(),
		ServerReady:    m.activeStatus == LSPConnected && !m.isFailedLocked() && !m.syncing,
		ServerBusy:     m.inflight > 0,
		ServerStarting: m.activeStatus == LSPStarting,
		ServerSyncing:  m.syncing,
		ServerFailed:   m.isFailedLocked(),
		ServerError:    m.activeError,
		IdleStatus:     m.idleStatus,
		IdleReason:     m.idleReason,
		FilesTracked:   len(m.fileDiagnostics),
		FilesAffected:  m.filesAffected,
		Errors:         m.diagnosticErrors,
		Warnings:       m.diagnosticWarnings,
		Info:           m.diagnosticInfo,
		Hints:          m.diagnosticHints,
		SymbolsCount:   m.symbolCount,
		LastSync:       m.projectLastSync,
		TopIssue:       m.topIssue,
	}
	return ins
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
		diagnostics := m.serverDiagnosticCounts[id]
		health := ttypes.LSPServerHealth{
			ID: id, Name: filepath.Base(state.cmd), RootURI: state.rootURI,
			Status: lspStatusLabel(state.status), Error: state.err,
			Connected: state.status == LSPConnected,
			Starting:  state.status == LSPStarting,
			Syncing:   m.syncing, Busy: m.inflight > 0,
			FilesAffected: diagnostics.files,
			Errors:        diagnostics.errors, Warnings: diagnostics.warnings,
			Information: diagnostics.information, Hints: diagnostics.hints,
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

func (m *LSPManager) lastSyncLocked() int64 {
	var latest int64
	for _, t := range m.fileLastSync {
		if t > latest {
			latest = t
		}
	}
	return latest
}

func (m *LSPManager) HasActiveFile() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.activeFilePath != ""
}

func (m *LSPManager) setFileDiagnostics(filePath string, diags []LSPDiagnostic) {
	m.setFileDiagnosticsForServer("", filePath, diags)
}

func (m *LSPManager) setFileDiagnosticsForServer(serverID, filePath string, diags []LSPDiagnostic) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.setFileDiagnosticsLocked(serverID, filePath, diags)
}

func (m *LSPManager) setFileDiagnosticsLocked(serverID, filePath string, diags []LSPDiagnostic) {
	fp := filepath.Clean(filePath)
	// Protocol output is untrusted. Bound retained diagnostics independently of
	// the server heap hint, and avoid walking a growing cache on idle UI frames.
	if len(diags) > 1000 {
		diags = diags[:1000]
	}
	if _, exists := m.fileDiagnostics[fp]; !exists && len(diags) > 0 && len(m.fileDiagnostics) >= 10000 {
		return
	}
	m.diagnosticChanges[fp] = true

	if !m.settings.Enabled || !m.settings.Diagnostics {
		diags = nil
	} else if len(diags) > 0 {
		filtered := make([]LSPDiagnostic, 0, len(diags))
		for _, d := range diags {
			if m.settings.SeverityAtLeast(d.Severity) {
				filtered = append(filtered, d)
			}
		}
		diags = filtered
		if len(diags) == 0 {
			diags = nil
		}
	}

	// Subtract the old diagnostic messages from the global issue counter so
	// the "top issue" only reflects the current snapshot. Skipping this
	// would let stale messages keep "winning" the top slot forever.
	if prev, ok := m.fileDiagnostics[fp]; ok {
		oldServer := m.fileDiagnosticServer[fp]
		aggregate := m.serverDiagnosticCounts[oldServer]
		if len(prev) > 0 {
			m.filesAffected--
			aggregate.files--
		}
		for _, d := range prev {
			m.adjustDiagnosticCountLocked(d.Severity, -1)
			adjustDiagnosticAggregate(&aggregate, d.Severity, -1)
			if msg := strings.TrimSpace(d.Message); msg != "" {
				if c := m.issueCount[msg]; c <= 1 {
					delete(m.issueCount, msg)
				} else {
					m.issueCount[msg] = c - 1
				}
			}
		}
		m.serverDiagnosticCounts[oldServer] = aggregate
	}

	if diags == nil {
		delete(m.fileDiagnostics, fp)
		delete(m.fileLastSync, fp)
		delete(m.fileDiagnosticServer, fp)
	} else {
		m.fileDiagnostics[fp] = append([]LSPDiagnostic(nil), diags...)
		m.fileDiagnosticServer[fp] = serverID
		aggregate := m.serverDiagnosticCounts[serverID]
		if len(diags) > 0 {
			m.filesAffected++
			aggregate.files++
		}
		now := time.Now().UnixNano()
		m.fileLastSync[fp], m.projectLastSync = now, max(m.projectLastSync, now)
		for _, d := range diags {
			m.adjustDiagnosticCountLocked(d.Severity, 1)
			adjustDiagnosticAggregate(&aggregate, d.Severity, 1)
			if msg := strings.TrimSpace(d.Message); msg != "" {
				m.issueCount[msg]++
			}
		}
		m.serverDiagnosticCounts[serverID] = aggregate
	}
	if diags == nil {
		m.projectLastSync = m.lastSyncLocked()
	}
	m.recomputeTopIssueLocked()
	m.bumpVersionLocked()
}

func adjustDiagnosticAggregate(aggregate *diagnosticAggregate, severity, delta int) {
	switch severity {
	case DiagError:
		aggregate.errors += delta
	case DiagWarning:
		aggregate.warnings += delta
	case DiagInfo:
		aggregate.information += delta
	default:
		aggregate.hints += delta
	}
}

func (m *LSPManager) adjustDiagnosticCountLocked(severity int, delta int) {
	switch severity {
	case DiagError:
		m.diagnosticErrors += delta
	case DiagWarning:
		m.diagnosticWarnings += delta
	case DiagInfo:
		m.diagnosticInfo += delta
	default:
		m.diagnosticHints += delta
	}
}

func (m *LSPManager) recomputeTopIssueLocked() {
	m.topIssue = ""
	count := 0
	for issue, occurrences := range m.issueCount {
		if occurrences > count || occurrences == count && issue < m.topIssue {
			m.topIssue, count = issue, occurrences
		}
	}
}

func (m *LSPManager) FileDiagnostics(filePath string) []LSPDiagnostic {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]LSPDiagnostic(nil), m.fileDiagnostics[filepath.Clean(filePath)]...)
}

func (m *LSPManager) HasFileDiagnostics(filePath string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.fileDiagnostics[filepath.Clean(filePath)]) > 0
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
	return m.settings.Enabled && pred(m.settings)
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
			path := filepath.Clean(filePath)
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
	m.setFileDiagnosticsLocked(m.fileDiagnosticServer[fp], fp, nil)
	m.symbolCount -= len(m.docSymbols[fp])
	delete(m.docSymbols, fp)
}
