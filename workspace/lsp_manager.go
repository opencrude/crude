package workspace

import (
	"context"
	"crude/insight/facts"
	"crude/types/insight"
	"crude/types/setting"
	"crude/types/statuscenter"
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"sync"
	"time"

	"crude/agent/toolstore"
	"crude/insight/governor"
	"crude/internal/sysload"
)

// LSPManager is the sole workspace owner of server and open-document lifetimes.
// Expensive root discovery, startup, synchronization and shutdown run on its
// serialized worker; merely enabling intelligence never opens sources. The
// assembly service (lsp_assembly.go) lists file names to plan the project.
type LSPManager struct {
	analysisSettings      setting.WorkspaceAnalysis
	analysisRootIdentity  string
	analysisBlocked       string
	analysisGovernor      *governor.Governor
	analysisWorkspace     string
	analysisGeneration    string
	analysisGovernorError string
	analysisRetained      bool
	analysisIdentity      func(string) (string, error)
	contextGeneration     uint64
	// factSequence orders the project results this manager publishes.
	factSequence uint64
	// Project check (lsp_project_check.go): checkPending asks for another
	// pass; lastEdit is the user's last edit or file open.
	checkPending  bool
	checkRunning  bool
	checkCancel   context.CancelFunc
	checkDone     chan struct{}
	checkPaths    map[string]bool // active background disk views, never editor or agent ownership
	checkLaunch   bool            // the foreground/load gate admitted a background start
	checkTarget   string          // the one root whose server the pass is negotiating or using
	checkWait     string
	forceFallback bool // diagnostic test override, sampled once at manager creation
	inventory     *analysisInventory
	checkResults  map[string]projectCheckResult
	checkCounts   map[string]checkCounts // compact background receipts by launch

	// workspacePulls joins the workspace diagnostic request loops.
	workspacePulls sync.WaitGroup
	// checkRetryAt delays a pass that stopped because a busy computer or
	// server left files without an answer; any explicit request clears it.
	checkRetryAt time.Time
	lastEdit     time.Time
	// load observes the computer; loadProbe and contention replace it in tests.
	load                   sysload.Probe
	loadProbe              func() sysload.Sample
	contention             func() bool
	sources                *DocumentService
	editStages             editStageRegistry
	reviewWatch            reviewedWatchRegistry
	mu                     sync.Mutex
	documentSend           sync.Mutex
	states                 map[string]*lspClientState
	documents              map[string]*lspDocument
	projects               map[string]insight.LSPProjectProducer // by server key
	projectWatchUnreliable bool
	// Assembly (lsp_assembly.go): the published plan, the cached project
	// listing, and the tool locations the plan resolved.
	toolStore                   *toolstore.Store
	assemblyActive              bool
	assembly                    insight.AssemblyPlan
	assemblyRequested           uint64
	assemblyCancel              context.CancelFunc
	assemblyWG                  sync.WaitGroup
	assemblyInvalidationTimer   *time.Timer
	assemblyInvalidationEpoch   uint64
	assemblyInvalidationStarted time.Time
	discovery                   *assemblyDiscovery
	toolPaths                   map[assemblyToolKey]string // server and command -> tool store executable
	toolMissing                 map[assemblyToolKey]string // server and command -> user reason
	updateTimers                map[string]*time.Timer
	runtime                     LSPRuntime
	settings                    setting.LSP
	logger                      Logger
	workspaceRoot               string
	activeFilePath              string
	activeRootURI               string
	activeConnected             bool
	activeError                 string
	activeStatus                insight.LSPStatus
	language                    insight.LanguageServiceStatus
	paused, manualStart, closed bool
	workerRunning               bool
	wake                        chan struct{}
	workerDone                  chan struct{}
	closedDocuments             []lspClosedDocument
	settingsGeneration          uint64
	inlayGeneration             uint64
	completionGeneration        uint64
	docSymbols                  map[string][]insight.LSPSymbol
	symbolCount                 int
	symbolsCached               bool
	syncing                     bool
	inflight                    int
	version                     uint64
	stateChanged                chan struct{}
	// crashes holds recent unexpected exits per server key; it bounds
	// automatic restarts. Explicit Restart and Start clear it.
	crashes map[string][]time.Time
}

// Status returns the connection state, status and last error of the active language server.
func (m *LSPManager) Status() (connected bool, status insight.LSPStatus, errMsg string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.activeConnected, m.activeStatus, m.activeError
}

func (m *LSPManager) recomputeActiveStatusLocked() {
	defer m.recomputeLanguageStatusLocked()
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
	if m.documents[m.activeFilePath] != nil {
		m.applyAggregateStateLocked(lspClientState{status: LSPIdle})
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

// ServerReady reports the selected status, usually for the active file. Without
// an active document it can fall back to another connected server. FileProducer
// observes the connected producer for a particular path.
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
func (m *LSPManager) ProjectInsights() insight.ProjectInsights {
	m.mu.Lock()
	watchUnreliable := m.projectWatchUnreliable
	ins := insight.ProjectInsights{ServerName: m.activeServerNameLocked(), ServerReady: m.activeStatus == LSPConnected && !m.isFailedLocked() && !m.syncing,
		ServerBusy: m.inflight > 0, ServerStarting: m.activeStatus == LSPStarting || m.activeFileWaitingLocked(), ServerSyncing: m.syncing,
		ServerFailed: m.isFailedLocked(), ServerError: m.activeError, Language: m.language, SymbolsCount: m.symbolCount}
	if !ins.ServerFailed {
		ins.ServerError = ""
	}
	settings := setting.LSP{Diagnostics: m.settings.Diagnostics, MinSeverity: m.settings.MinSeverity}
	sources := m.sources
	analysis := m.analysisProgressLocked()
	wait := m.checkWait
	clean, unchecked := 0, 0
	for _, counts := range m.checkCounts {
		clean += counts.clean
		unchecked += counts.unchecked
	}
	m.mu.Unlock()
	summary := insight.ProjectInsights{}
	if sources != nil && sources.Insight != nil {
		summary = sources.Insight.diagnosticSummaryFor(settings)
	} else {
		summary.CoverageDetail = "No canonical source authority; project coverage is unverified."
	}
	summary.ServerName, summary.ServerReady, summary.ServerBusy = ins.ServerName, ins.ServerReady, ins.ServerBusy
	summary.ServerStarting, summary.ServerSyncing, summary.ServerFailed = ins.ServerStarting, ins.ServerSyncing, ins.ServerFailed
	summary.ServerError, summary.Language, summary.SymbolsCount = ins.ServerError, ins.Language, ins.SymbolsCount
	summary.Analysis, summary.ProjectCheckWait = analysis, wait
	summary.ProjectCheckUnchecked = unchecked
	summary.DiagnosticsObserved += clean
	summary.FilesTracked += clean
	if unchecked > 0 {
		summary.DiagnosticsLimited += unchecked
		summary.CoverageDetail += fmt.Sprintf(" %d closed files could not be checked. Use Refresh to try again.", unchecked)
	}
	if watchUnreliable {
		summary.DiagnosticsLimited++
		summary.CoverageDetail += " Project file observation is incomplete; results may be out of date."
	}
	return summary
}
func (m *LSPManager) ProjectDiagnosticCounts() (int, int, int) {
	view := m.ProjectInsights()
	return view.Errors, view.Warnings, view.Info + view.Hints
}

// ServerHealthSnapshots exposes every configured/attempted server separately.
// Counts come from current published facts for each server context, not transport status.
// retainedResultHealth describes the results that Crude keeps for a server
// that does not run now, for example after an idle stop or an eviction. The
// results describe files, not the process: Problems shows them, so the issues
// that they raise must stay too. Such results are Crude's checks of closed
// files, so their scope is open files.
func (m *LSPManager) retainedResultHealth() []statuscenter.LSPServerHealth {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.sources == nil || m.sources.Insight == nil {
		return nil
	}
	var out []statuscenter.LSPServerHealth
	for id, count := range m.sources.Insight.diagnostics.ContextSummaries() {
		if _, running := m.states[id]; running || count.Provider == "" {
			continue
		}
		out = append(out, statuscenter.LSPServerHealth{
			ID: id, Name: filepath.Base(count.Provider), Command: count.Provider, Status: "Stopped",
			FilesTracked: count.FilesTracked + m.checkCounts[id].clean, FilesAffected: count.FilesAffected,
			Errors: count.Errors, Warnings: count.Warnings, Information: count.Information, Hints: count.Hints,
			LastDiagnostics: count.LastDiagnostics, Scope: insight.CoverageOpenFiles,
		})
	}
	return out
}

// ServerHealthSnapshots returns the health of every language server.
func (m *LSPManager) ServerHealthSnapshots() []statuscenter.LSPServerHealth {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	out := make([]statuscenter.LSPServerHealth, 0, len(m.states))
	var counts map[string]facts.ContextSummary
	if m.sources != nil && m.sources.Insight != nil {
		counts = m.sources.Insight.diagnostics.ContextSummaries()
	}
	now := time.Now()
	for id, state := range m.states {
		health := statuscenter.LSPServerHealth{
			ID: id, Name: filepath.Base(state.cmd), Command: state.cmd, RootURI: state.rootURI,
			Status: lspStatusLabel(state.status), Error: state.err,
			Connected: state.status == LSPConnected,
			Starting:  state.status == LSPStarting,
			Syncing:   m.syncing, Busy: m.inflight > 0,
		}
		count := counts[id]
		health.FilesTracked, health.FilesAffected = count.FilesTracked+m.checkCounts[id].clean, count.FilesAffected
		health.Errors, health.Warnings, health.Information, health.Hints = count.Errors, count.Warnings, count.Information, count.Hints
		health.LastDiagnostics = count.LastDiagnostics
		for _, at := range m.crashes[id] {
			if now.Sub(at) < lspAutomaticRestartWindow {
				health.Restarts++
			}
		}
		health.Restarting = state.restarting && !state.stopped && !m.paused && !m.closed && state.teardownErr == nil && m.keyWantedLocked(id)
		health.Evidence = slices.Clone(state.evidence)
		if client, ok := state.client.(LSPEvidenceClient); ok {
			health.Evidence = client.Evidence()
			if !state.closing && !state.stopped && state.status == LSPConnected {
				health.ErrorMessage = client.ErrorMessage()
			}
		}
		if !state.closing && !state.stopped && state.teardownErr == nil &&
			(state.status == LSPConnected || state.status == LSPStarting) {
			health.InstanceID = state.instance
		}
		if state.waiting {
			health.Status, health.Error = "Waiting for slot", ""
		}
		// Coverage: what this producer checks. A project producer reports
		// files that are not open; any other server checks open files only.
		health.Scope, health.Coverage = insight.CoverageOpenFiles, openFilesCoverage(m.settings)
		if m.reportsProjectLocked(id, state) {
			health.Scope, health.Coverage = insight.CoverageProject, "Checks the whole project, including files that are not open."
			if m.projectWatchUnreliable {
				health.Coverage = "Project file observation is incomplete; results may be out of date."
			}
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

func lspStatusLabel(status insight.LSPStatus) string {
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
	return !m.activeFileWaitingLocked() &&
		(m.activeStatus == LSPFailed || m.activeError != "" && m.activeStatus != LSPConnected)
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

func (m *LSPManager) featureOn(pred func(setting.LSP) bool) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.analysisErrorLocked() == nil && m.settings.Enabled && pred(m.settings)
}

func (m *LSPManager) ShowDiagnosticsInEditor() bool {
	return m.featureOn(func(s setting.LSP) bool { return s.Diagnostics && s.DiagInEditor })
}

func (m *LSPManager) ShowDiagnosticsInTree() bool {
	return m.featureOn(func(s setting.LSP) bool { return s.Diagnostics && s.DiagInTree })
}

func (m *LSPManager) ShowDiagnosticsInStatus() bool {
	return m.featureOn(func(s setting.LSP) bool { return s.Diagnostics && s.DiagInStatus })
}

// RequestDocumentSymbols routes a "document symbols" request to the right
// client and caches the result for the document outline panel.
func (m *LSPManager) RequestDocumentSymbols(ctx context.Context, filePath string) []insight.LSPSymbol {
	if !m.featureOn(func(s setting.LSP) bool { return s.Outline }) {
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
	var out []insight.LSPSymbol
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
func (m *LSPManager) DocumentSymbols(filePath string) []insight.LSPSymbol {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.docSymbols[filepath.Clean(filePath)]
}

// AllDocumentSymbols returns the cached outlines for every file. Used by the
// workspace symbols fuzzy search so we can search across the whole project
// without hitting the LSP server again.
func (m *LSPManager) AllDocumentSymbols() map[string][]insight.LSPSymbol {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string][]insight.LSPSymbol, len(m.docSymbols))
	for k, v := range m.docSymbols {
		out[k] = v
	}
	return out
}

// RequestHover routes a hover request to the right client.
func (m *LSPManager) RequestHover(ctx context.Context, filePath string, line, col int) (string, error) {
	if !m.featureOn(func(s setting.LSP) bool { return s.Hover }) {
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

// RequestSignatureHelp uses the same exact synchronized source and provider
// lifetime as other explicit editor requests. It never starts an extra server.
func (m *LSPManager) RequestSignatureHelp(ctx context.Context, path string, line, col int, trigger insight.LSPSignatureContext) (insight.LSPSignatureHelp, error) {
	if !m.featureOn(func(s setting.LSP) bool { return s.Enabled }) {
		return insight.LSPSignatureHelp{}, fmt.Errorf("parameter information unavailable: language intelligence is off or blocked")
	}
	var out insight.LSPSignatureHelp
	err := m.trackInflight(func() error {
		client, requestCtx, valid, cancel := m.requestClient(ctx, path)
		defer cancel()
		if client == nil {
			return fmt.Errorf("parameter information unavailable: no synchronized language server")
		}
		m.mu.Lock()
		doc := m.documents[filepath.Clean(path)]
		var boundary insight.Boundary
		if doc != nil && doc.opened && !doc.syncPending {
			boundary = doc.boundary
			boundary.Context.UnknownInputs = append([]string(nil), boundary.Context.UnknownInputs...)
		}
		generation := m.producerContextLocked(boundary.Context.ID)
		m.mu.Unlock()
		if boundary.Source.DocumentID == "" || boundary.Context.Generation != generation {
			return fmt.Errorf("parameter information unavailable: source context is not synchronized")
		}
		if err := requestCtx.Err(); err != nil {
			return err
		}
		if trigger.Producer.InstanceID != "" && trigger.Producer != boundary.Producer {
			return fmt.Errorf("language server changed; request parameter information again")
		}
		var requestErr error
		out, requestErr = client.RequestSignatureHelp(requestCtx, path, line, col, trigger)
		if err := requestCtx.Err(); err != nil {
			out = insight.LSPSignatureHelp{}
			return err
		}
		m.mu.Lock()
		sameContext := generation == m.producerContextLocked(boundary.Context.ID)
		m.mu.Unlock()
		if !valid() || !sameContext {
			out = insight.LSPSignatureHelp{}
			return fmt.Errorf("LSP source or context changed during parameter information")
		}
		out.Boundary = boundary
		return requestErr
	})
	return out, err
}

// ContextCursor is the retained semantic-context clock, separate from status,
// progress and diagnostic publication versions. Reads perform no I/O.
func (m *LSPManager) ContextCursor(path string) (string, uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.sources == nil || m.sources.Insight == nil {
		return "", m.contextGeneration
	}
	if doc := m.documents[filepath.Clean(path)]; doc != nil {
		return m.sources.Insight.generation, m.producerContextLocked(doc.key)
	}
	return m.sources.Insight.generation, m.contextGeneration
}

// RequestRename routes a rename request to the right client.
func (m *LSPManager) RequestRename(ctx context.Context, filePath string, line, col int, newName string) (insight.LSPRenameEdit, error) {
	if !m.featureOn(func(s setting.LSP) bool { return s.Rename }) {
		return insight.LSPRenameEdit{}, fmt.Errorf("rename unavailable: language intelligence is off or blocked")
	}
	var out insight.LSPRenameEdit
	err := m.trackInflight(func() error {
		client, requestCtx, valid, cancel := m.requestClient(ctx, filePath)
		defer cancel()
		if client == nil {
			return fmt.Errorf("no LSP client for %s", filePath)
		}
		m.mu.Lock()
		doc := m.documents[filepath.Clean(filePath)]
		var boundary insight.Boundary
		wireVersion := 0
		if doc != nil && doc.opened && !doc.syncPending {
			boundary, wireVersion = doc.boundary, doc.wireVersion
			boundary.Context.UnknownInputs = append([]string(nil), boundary.Context.UnknownInputs...)
		}
		generation := m.producerContextLocked(boundary.Context.ID)
		m.mu.Unlock()
		if boundary.Source.DocumentID == "" || boundary.Context.Generation != generation || wireVersion <= 0 {
			return fmt.Errorf("rename unavailable: canonical source context is not synchronized")
		}
		var requestErr error
		out, requestErr = client.RequestRename(requestCtx, filePath, line, col, newName)
		m.mu.Lock()
		sameContext := generation == m.producerContextLocked(boundary.Context.ID)
		m.mu.Unlock()
		if !valid() || !sameContext || requestCtx.Err() != nil {
			out = insight.LSPRenameEdit{}
			return fmt.Errorf("LSP source or context changed during rename")
		}
		if requestErr == nil && out.WireVersion != wireVersion {
			out = insight.LSPRenameEdit{}
			return fmt.Errorf("rename response does not identify the synchronized wire source")
		}
		out.Boundary = boundary
		return requestErr
	})
	return out, err
}

// InvalidateFile removes all cached state for a file. Call this when a file
// is deleted, renamed, or its content has been replaced wholesale.
func (m *LSPManager) InvalidateFile(filePath string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	fp := filepath.Clean(filePath)
	if m.sources != nil && m.sources.Insight != nil {
		m.sources.Insight.diagnostics.InvalidateMatching(func(b insight.Boundary) bool { return b.Source.Path == fp }, "Source path invalidated.")
	}
	m.bumpVersionLocked()
	m.symbolCount -= len(m.docSymbols[fp])
	delete(m.docSymbols, fp)
}
