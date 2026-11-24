package lspclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"maps"
	"math"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/charmbracelet/x/powernap/pkg/lsp"
	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol"

	"crude/types/insight"
)

// Pull diagnostics (LSP 3.17). The design follows the specification's
// implementation considerations and VS Code's language client
// (vscode-languageserver-node client/src/common/diagnostic.ts): pull the
// document the user changes, pull the other open documents after it when the
// server declares inter-file dependencies, pull everything again on
// workspace/diagnostic/refresh, and keep a workspace request open when the
// server offers workspace diagnostics. Push (publishDiagnostics) keeps working
// beside it; each source is one part of a document's publication.
//
// A document is pulled only while the server holds it open: servers answer a
// pull for a file they have not opened with an empty full report, which would
// erase real problems.

const (
	pushPart = "push"
	pullPart = "pull\x00"
	// After this many unanswered pulls in a row a provider is treated as
	// absent for the rest of the launch, and the server's pushes remain.
	pullTimeoutLimit = 3
)

var (
	// A pull that has no answer in this time leaves the push part alone.
	pullTimeout = 20 * time.Second
	// VS Code's background scheduler waits this long between documents.
	interFileSpacing = 500 * time.Millisecond
)

// ErrWorkspacePullRefresh ends a workspace pull that the server asked to
// start again (workspace/diagnostic/refresh or ServerCancelled).
var ErrWorkspacePullRefresh = errors.New("the language server asked for its workspace diagnostics again")

// diagnosticPart is one source's report for the document's current version.
type diagnosticPart struct {
	diags []protocol.Diagnostic
	// raw holds the original protocol objects for code actions; nil when they
	// are unavailable or ambiguous. envelope is a push's own notification,
	// which code actions check as it arrived when it is the only part.
	raw      []json.RawMessage
	envelope json.RawMessage
	// unversioned marks a push without a document version.
	unversioned bool
}

// documentPull is one provider's pull state for one open document.
type documentPull struct {
	cancel   context.CancelFunc
	done     chan struct{}
	version  int // version of the request in flight
	inFlight bool
	again    bool // pull again for the current version when this one ends
	// resultID and kept are the last full report, which an "unchanged"
	// answer refers to.
	resultID string
	kept     diagnosticPart
}

// pullProvider is one provider of a launch: the static capability (key "")
// or one dynamic registration ID.
type pullProvider struct {
	lsp.DiagnosticSupport
	timeouts int
	disabled bool
}

// pullState belongs to one launch. Its maps are guarded by LSPIntegration.mu;
// work ends with the launch and wg joins its goroutines.
type pullState struct {
	work      context.Context
	stopWork  context.CancelFunc
	wg        sync.WaitGroup
	providers map[string]*pullProvider
	// background queues documents of inter-file providers, by identifier.
	background map[string][]string
	running    map[string]bool
	// workspaceResults are the result IDs of workspace reports, by identifier.
	workspaceResults map[string]map[protocol.DocumentURI]string
	workspaceCancel  context.CancelCauseFunc
	partial          map[string]func(json.RawMessage)
}

var partialTokens atomic.Uint64

func newPullState() *pullState {
	work, stop := context.WithCancel(context.Background())
	return &pullState{work: work, stopWork: stop, providers: map[string]*pullProvider{},
		background: map[string][]string{}, running: map[string]bool{},
		workspaceResults: map[string]map[protocol.DocumentURI]string{}, partial: map[string]func(json.RawMessage){}}
}

// registerPullHandlers installs the server requests of pull diagnostics.
func (l *LSPIntegration) registerPullHandlers(launch *lspLaunch, client *lsp.Client) {
	client.RegisterHandler("client/registerCapability", func(_ context.Context, _ string, params json.RawMessage) (any, error) {
		return nil, l.registerCapabilities(launch, params)
	})
	client.RegisterHandler("client/unregisterCapability", func(_ context.Context, _ string, params json.RawMessage) (any, error) {
		return nil, l.unregisterCapabilities(launch, params)
	})
	client.RegisterHandler("workspace/diagnostic/refresh", func(context.Context, string, json.RawMessage) (any, error) {
		l.refreshDiagnostics(launch)
		return nil, nil
	})
}

// installStaticProviderLocked records the initialize capability when the
// launch becomes the integration's client.
func installStaticProviderLocked(launch *lspLaunch, client *lsp.Client) {
	if support, ok, err := client.DiagnosticProvider(); ok && err == nil {
		launch.pull.providers[""] = &pullProvider{DiagnosticSupport: support}
	}
}

// Registrations are keyed by registration ID, but requests, result IDs and
// displayed parts are keyed by diagnostic identifier. Several registrations
// (or the static provider) may select the same document and identifier.
func (l *LSPIntegration) registerCapabilities(launch *lspLaunch, params json.RawMessage) error {
	var request struct {
		Registrations []struct {
			ID, Method      string
			RegisterOptions json.RawMessage `json:"registerOptions"`
		} `json:"registrations"`
	}
	if err := json.Unmarshal(params, &request); err != nil {
		return err
	}
	diagnostics := map[string]*pullProvider{}
	completions := map[string]registeredCompletion{}
	for _, r := range request.Registrations {
		if r.ID == "" {
			return errors.New("missing language server registration ID")
		}
		switch r.Method {
		case lsp.MethodWorkspaceDidChangeConfiguration:
			// Configuration is immutable for this launch. The client sends its
			// initial settings after initialized and answers scoped pulls. Any
			// settings change replaces the launch, so there is no live change
			// listener to install (or remove on unregister).
			if len(r.RegisterOptions) != 0 {
				var options protocol.DidChangeConfigurationRegistrationOptions
				if err := json.Unmarshal(r.RegisterOptions, &options); err != nil {
					return err
				}
			}
		case "textDocument/diagnostic":
			support, err := lsp.ParseDiagnosticSupport(r.RegisterOptions)
			if err != nil {
				return err
			}
			diagnostics[r.ID] = &pullProvider{DiagnosticSupport: support}
		case "textDocument/completion":
			var options struct {
				protocol.CompletionOptions
				DocumentSelector []json.RawMessage `json:"documentSelector"`
			}
			if err := json.Unmarshal(r.RegisterOptions, &options); err != nil {
				return err
			}
			selector, err := lsp.ParseDocumentSelector(options.DocumentSelector)
			if err != nil {
				return err
			}
			completions[r.ID] = registeredCompletion{selector, options.CompletionOptions}
		default:
			return errors.New("unsupported dynamic registration: " + r.Method)
		}
	}
	l.mu.Lock()
	if l.launch != launch {
		l.mu.Unlock()
		return errors.New("language server launch ended")
	}
	maps.Copy(launch.pull.providers, diagnostics)
	if len(completions) > 0 {
		if launch.completions == nil {
			launch.completions = make(map[string]registeredCompletion)
		}
		maps.Copy(launch.completions, completions)
	}
	l.finishRegistrationLocked(launch, len(diagnostics) > 0, len(completions) > 0)
	return nil
}

func (l *LSPIntegration) unregisterCapabilities(launch *lspLaunch, params json.RawMessage) error {
	var request struct { // Historical spelling in LSP 3.17.
		Unregisterations []struct{ ID, Method string } `json:"unregisterations"`
	}
	if err := json.Unmarshal(params, &request); err != nil {
		return err
	}
	l.mu.Lock()
	if l.launch != launch {
		l.mu.Unlock()
		return nil
	}
	var diagnostics, completions bool
	for _, r := range request.Unregisterations {
		if r.ID == "" {
			continue
		}
		switch r.Method {
		case "textDocument/diagnostic":
			if _, ok := launch.pull.providers[r.ID]; ok {
				delete(launch.pull.providers, r.ID)
				diagnostics = true
			}
		case "textDocument/completion":
			if _, ok := launch.completions[r.ID]; ok {
				delete(launch.completions, r.ID)
				completions = true
			}
		}
	}
	l.finishRegistrationLocked(launch, diagnostics, completions)
	return nil
}

// finishRegistrationLocked reconciles one batch and unlocks before delivery.
func (l *LSPIntegration) finishRegistrationLocked(launch *lspLaunch, diagnostics, completions bool) {
	var deliveries []func()
	if diagnostics && l.client != nil {
		for path, state := range l.openFiles {
			ids := launch.pull.documentIdentifiers(path, state.languageID)
			for _, id := range ids {
				if state.pulls[id] == nil {
					l.pullIdentifierLocked(launch, path, state, id)
				}
			}
			removed := false
			for id, pull := range state.pulls {
				if slices.Contains(ids, id) {
					continue
				}
				if pull.inFlight {
					pull.cancel()
				}
				delete(state.pulls, id)
				if _, ok := state.parts[pullPart+id]; ok {
					delete(state.parts, pullPart+id)
					removed = true
				}
			}
			if removed {
				deliveries = append(deliveries, l.publishPartsLocked(launch, path, state))
			}
		}
	}
	notify := l.onAssistanceChanged
	l.signalLocked()
	l.mu.Unlock()
	for _, deliver := range deliveries {
		deliver()
	}
	if completions && notify != nil {
		notify()
	}
}

// A missing identifier is itself a valid wire identifier, not a wildcard.
func (p *pullState) documentIdentifiers(path, language string) []string {
	var ids []string
	for _, provider := range p.providers {
		if providerMatches(provider, path, language) && !slices.Contains(ids, provider.Identifier) {
			ids = append(ids, provider.Identifier)
		}
	}
	return ids
}

// refreshDiagnostics answers workspace/diagnostic/refresh: pull every open
// document again and restart the workspace request.
func (l *LSPIntegration) refreshDiagnostics(launch *lspLaunch) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.launch != launch || l.client == nil {
		return
	}
	for path, state := range l.openFiles {
		l.pullDocumentLocked(launch, path, state)
	}
	if launch.pull.workspaceCancel != nil {
		launch.pull.workspaceCancel(ErrWorkspacePullRefresh)
	}
}

// providerMatches reports whether a provider covers an open document. A
// filter matches by language, file scheme and LSP glob pattern.
func providerMatches(provider *pullProvider, path, languageID string) bool {
	if provider.disabled {
		return false
	}
	return selectorMatches(provider.Selector, path, languageID)
}
func selectorMatches(selector []lsp.DiagnosticFilter, path, languageID string) bool {
	if selector == nil {
		return true
	}
	for _, filter := range selector {
		if filter.Notebook || filter.Language != "" && filter.Language != languageID || filter.Scheme != "" && filter.Scheme != "file" {
			continue
		}
		if filter.Pattern != "" {
			matched, err := doublestar.Match(filepath.ToSlash(filter.Pattern), filepath.ToSlash(path))
			if err != nil || !matched {
				continue
			}
		}
		return true
	}
	return false
}

// pullDocumentLocked asks once per wire identity, regardless of how many
// selectors match. Only an edit, save or explicit refresh supersedes a pull.
func (l *LSPIntegration) pullDocumentLocked(launch *lspLaunch, path string, state *openFileState) {
	if launch == nil || launch.pull == nil || l.client == nil {
		return
	}
	for _, id := range launch.pull.documentIdentifiers(path, state.languageID) {
		l.pullIdentifierLocked(launch, path, state, id)
	}
}

func (l *LSPIntegration) pullIdentifierLocked(launch *lspLaunch, path string, state *openFileState, identifier string) chan struct{} {
	if state.pulls == nil {
		state.pulls = map[string]*documentPull{}
	}
	pull := state.pulls[identifier]
	if pull == nil {
		pull = &documentPull{}
		state.pulls[identifier] = pull
	}
	if pull.inFlight {
		// As in VS Code, a new reason to pull cancels the request in
		// flight and pulls again when it ends: its answer may predate
		// the edit, save or refresh that asked.
		pull.again = true
		pull.cancel()
		return pull.done
	}
	ctx, cancel := context.WithTimeout(launch.pull.work, pullTimeout)
	pull.inFlight, pull.again, pull.version, pull.cancel, pull.done = true, false, state.version, cancel, make(chan struct{})
	launch.pull.wg.Add(1)
	go l.runDocumentPull(ctx, documentPullRequest{launch: launch, client: l.client, path: path, state: state,
		identifier: identifier, pull: pull, done: pull.done, cancel: cancel, uri: state.uri, previous: pull.resultID})
	return pull.done
}

type documentPullRequest struct {
	launch        *lspLaunch
	client        *lsp.Client
	path          string
	state         *openFileState
	identifier    string
	pull          *documentPull
	done          chan struct{}
	cancel        context.CancelFunc
	uri, previous string
}

func (l *LSPIntegration) runDocumentPull(ctx context.Context, r documentPullRequest) {
	defer r.launch.pull.wg.Done()
	defer close(r.done)
	defer r.cancel()
	documentPulls.Inc()
	raw, err := r.client.RequestDocumentDiagnostics(ctx, protocol.DocumentURI(r.uri), r.identifier, r.previous)
	cancelled, retrigger := lsp.DiagnosticRetrigger(err)
	if cancelled || errors.Is(ctx.Err(), context.Canceled) {
		cancelledPulls.Inc()
	}
	timedOut := errors.Is(ctx.Err(), context.DeadlineExceeded)
	l.mu.Lock()
	r.pull.inFlight = false
	state := r.state
	if l.launch != r.launch || l.client != r.client || l.openFiles[r.path] != state || state.pulls[r.identifier] != r.pull {
		l.mu.Unlock()
		return
	}
	for _, provider := range r.launch.pull.providers {
		if provider.Identifier != r.identifier || !providerMatches(provider, r.path, state.languageID) {
			continue
		}
		if timedOut {
			provider.timeouts++
			provider.disabled = provider.timeouts >= pullTimeoutLimit
		} else if err == nil {
			provider.timeouts = 0
		}
	}
	current := r.pull.version == state.version
	var deliver func()
	answered := false
	switch {
	case err == nil:
		if part, resultID, ok := documentReportPart(raw, r.pull); ok {
			answered = current
			r.pull.resultID, r.pull.kept = resultID, part
			if current {
				if state.parts == nil {
					state.parts = map[string]diagnosticPart{}
				}
				state.parts[pullPart+r.identifier] = part
				deliver = l.publishPartsLocked(r.launch, r.path, state)
			}
		}
	case cancelled && retrigger:
		r.pull.again = true
	}
	if r.pull.again && !timedOut && slices.Contains(r.launch.pull.documentIdentifiers(r.path, state.languageID), r.identifier) {
		l.pullIdentifierLocked(r.launch, r.path, state, r.identifier)
	} else if answered && state.changed {
		state.changed = false
		l.queueInterFileLocked(r.launch, r.path, r.identifier)
	}
	l.mu.Unlock()
	if deliver != nil {
		deliver()
	}
}

// documentReportPart reads a full or unchanged document report. An unchanged
// report repeats the kept full report for its result ID.
func documentReportPart(raw json.RawMessage, pull *documentPull) (diagnosticPart, string, bool) {
	var report struct {
		Kind     string            `json:"kind"`
		ResultID string            `json:"resultId"`
		Items    []json.RawMessage `json:"items"`
	}
	if json.Unmarshal(raw, &report) != nil {
		return diagnosticPart{}, "", false
	}
	switch report.Kind {
	case "unchanged":
		if report.ResultID == "" || report.ResultID != pull.resultID {
			return diagnosticPart{}, "", false
		}
		return pull.kept, report.ResultID, true
	case "full":
		part, ok := reportItemsPart(report.Items, validOriginalDiagnosticJSON(raw))
		return part, report.ResultID, ok
	}
	return diagnosticPart{}, "", false
}

// reportItemsPart decodes report items. Original objects are kept for code
// actions only when the whole report passed the strict original-JSON check.
func reportItemsPart(items []json.RawMessage, strict bool) (diagnosticPart, bool) {
	part := diagnosticPart{diags: make([]protocol.Diagnostic, 0, len(items))}
	for _, item := range items {
		var diagnostic protocol.Diagnostic
		if json.Unmarshal(item, &diagnostic) != nil {
			return diagnosticPart{}, false
		}
		part.diags = append(part.diags, diagnostic)
	}
	if strict && len(items) <= lsp.MaxCodeActionDiagnostics {
		part.raw = make([]json.RawMessage, len(items))
		for index, item := range items {
			part.raw[index] = bytes.Clone(item)
		}
	}
	return part, true
}

// pushDiagnosticPart turns one publishDiagnostics notification into the push
// part. The original items are kept only from a strictly valid envelope.
func pushDiagnosticPart(diags []protocol.Diagnostic, version *int32, originals []json.RawMessage) diagnosticPart {
	part := diagnosticPart{diags: diags, unversioned: version == nil}
	if len(originals) != 1 {
		return part
	}
	part.envelope = originals[0]
	if len(originals[0]) > lsp.MaxCodeActionDiagnosticBytes+(16<<10) || !validOriginalDiagnosticJSON(originals[0]) {
		return part
	}
	var envelope struct {
		Diagnostics []json.RawMessage `json:"diagnostics"`
	}
	if json.Unmarshal(originals[0], &envelope) != nil || len(envelope.Diagnostics) != len(diags) {
		return part
	}
	part.raw = envelope.Diagnostics
	return part
}

// publishPartsLocked publishes the union of the current version's parts:
// push first, then pull providers by identifier. It returns the callback
// delivery, which the caller runs after unlocking.
func (l *LSPIntegration) publishPartsLocked(launch *lspLaunch, path string, state *openFileState) func() {
	client := l.client
	keys := make([]string, 0, len(state.parts))
	for key := range state.parts {
		keys = append(keys, key)
	}
	slices.SortFunc(keys, func(a, b string) int {
		switch {
		case a == b:
			return 0
		case a == pushPart:
			return -1
		case b == pushPart:
			return 1
		case a < b:
			return -1
		}
		return 1
	})
	var diags []protocol.Diagnostic
	var raw []json.RawMessage
	exact, originals := true, true
	for _, key := range keys {
		part := state.parts[key]
		diags = append(diags, part.diags...)
		raw = append(raw, part.raw...)
		exact = exact && !part.unversioned
		originals = originals && (part.raw != nil || len(part.diags) == 0)
	}
	publication := insight.DiagnosticPublication{
		Boundary: cloneBoundary(state.boundary), WireVersion: state.version, Sequence: l.sequence + 1,
	}
	if exact {
		reported := state.version
		publication.ReportedVersion = &reported
	}
	// Only one bounded transient publication per synchronized document is kept
	// for protocol waiters. Product fact identity and validity live upstream.
	publication.Diagnostics, publication.Limited, publication.Detail = transportDiagnostics(diags)
	l.sequence = publication.Sequence
	state.publishedAt, state.publication = time.Now(), publication
	var envelope []json.RawMessage
	if push, ok := state.parts[pushPart]; ok && len(keys) == 1 {
		// The common push-only case keeps the server's own notification.
		if push.envelope != nil {
			envelope = []json.RawMessage{push.envelope}
		}
	} else if originals && exact {
		if items := raw; items != nil || len(diags) == 0 {
			if items == nil {
				items = []json.RawMessage{}
			}
			if data, err := json.Marshal(map[string]any{"uri": state.uri, "version": state.version, "diagnostics": items}); err == nil {
				envelope = []json.RawMessage{data}
			}
		}
	}
	state.actionDiagnostics = qualifyActionDiagnostics(state.text, publication, envelope)
	l.signalLocked()
	cb := l.onDiagnostics
	// The callback gets its own mutable containers, not the waiter's storage.
	publication = clonePublication(publication)
	return func() {
		if cb == nil {
			return
		}
		l.mu.Lock()
		current := l.launch == launch && l.client == client && l.openFiles[path] == state &&
			state.version == publication.WireVersion && equalBoundary(state.boundary, publication.Boundary) &&
			state.publication.Sequence == publication.Sequence
		l.mu.Unlock()
		if current {
			cb(publication)
		}
	}
}

// queueInterFileLocked pulls the server's other open documents after a
// change, one at a time, when the provider declares inter-file dependencies.
func (l *LSPIntegration) queueInterFileLocked(launch *lspLaunch, changed, identifier string) {
	for _, provider := range launch.pull.providers {
		if provider.Identifier != identifier || !provider.InterFileDependencies || provider.disabled {
			continue
		}
		queue := launch.pull.background[identifier]
		for path := range l.openFiles {
			if path != changed && !slices.Contains(queue, path) {
				queue = append(queue, path)
			}
		}
		slices.Sort(queue)
		launch.pull.background[identifier] = queue
		if !launch.pull.running[identifier] && len(queue) > 0 {
			launch.pull.running[identifier] = true
			launch.pull.wg.Add(1)
			go l.runInterFile(launch, identifier)
		}
		return
	}
}

func (l *LSPIntegration) runInterFile(launch *lspLaunch, identifier string) {
	defer launch.pull.wg.Done()
	for {
		timer := time.NewTimer(interFileSpacing)
		select {
		case <-launch.pull.work.Done():
			timer.Stop()
			l.mu.Lock()
			launch.pull.running[identifier] = false
			l.mu.Unlock()
			return
		case <-timer.C:
		}
		l.mu.Lock()
		queue := launch.pull.background[identifier]
		if l.launch != launch || len(queue) == 0 {
			launch.pull.running[identifier] = false
			l.mu.Unlock()
			return
		}
		path := queue[0]
		launch.pull.background[identifier] = queue[1:]
		var done chan struct{}
		if state := l.openFiles[path]; state != nil && slices.Contains(launch.pull.documentIdentifiers(path, state.languageID), identifier) {
			done = l.pullIdentifierLocked(launch, path, state, identifier)
		}
		l.mu.Unlock()
		if done != nil {
			select {
			case <-done:
			case <-launch.pull.work.Done():
			}
		}
	}
}

// WorkspaceDiagnostics reports whether the server offers workspace
// diagnostics, so that it reports files that are not open when asked.
func (l *LSPIntegration) WorkspaceDiagnostics() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.launch == nil || l.launch.pull == nil || l.client == nil {
		return false
	}
	for _, provider := range l.launch.pull.providers {
		if provider.WorkspaceDiagnostics && !provider.disabled {
			return true
		}
	}
	return false
}

// PullWorkspaceDiagnostics runs one workspace diagnostic request for each
// provider that offers it and returns when the server ends them. Reports for
// files that no document holds go to the unopened callback; an open document
// keeps its own pulls, which win over workspace reports. The caller decides
// when to ask again.
func (l *LSPIntegration) PullWorkspaceDiagnostics(ctx context.Context) error {
	l.mu.Lock()
	launch, client := l.launch, l.client
	if launch == nil || launch.pull == nil || client == nil {
		l.mu.Unlock()
		return errors.New("LSP client is unavailable")
	}
	var identifiers []string
	for _, provider := range launch.pull.providers {
		if provider.WorkspaceDiagnostics && !provider.disabled && !slices.Contains(identifiers, provider.Identifier) {
			identifiers = append(identifiers, provider.Identifier)
		}
	}
	l.mu.Unlock()
	slices.Sort(identifiers)
	for _, identifier := range identifiers {
		if err := l.pullWorkspace(ctx, launch, client, identifier); err != nil {
			return err
		}
	}
	return nil
}

func (l *LSPIntegration) pullWorkspace(ctx context.Context, launch *lspLaunch, client *lsp.Client, identifier string) error {
	token := "crude-workspace-diagnostics-" + strconv.FormatUint(partialTokens.Add(1), 10)
	request, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	stop := context.AfterFunc(launch.pull.work, func() { cancel(context.Canceled) })
	defer stop()
	l.mu.Lock()
	if l.launch != launch {
		l.mu.Unlock()
		return errors.New("LSP client is unavailable")
	}
	results := launch.pull.workspaceResults[identifier]
	if results == nil {
		results = map[protocol.DocumentURI]string{}
		launch.pull.workspaceResults[identifier] = results
	}
	previous := make([]lsp.PreviousResultID, 0, len(results))
	for uri, value := range results {
		previous = append(previous, lsp.PreviousResultID{URI: uri, Value: value})
	}
	slices.SortFunc(previous, func(a, b lsp.PreviousResultID) int {
		switch {
		case a.URI < b.URI:
			return -1
		case a.URI > b.URI:
			return 1
		}
		return 0
	})
	launch.pull.workspaceCancel = cancel
	launch.pull.partial[token] = func(value json.RawMessage) { l.receiveWorkspaceReport(launch, identifier, value) }
	l.mu.Unlock()
	defer func() {
		l.mu.Lock()
		delete(launch.pull.partial, token)
		launch.pull.workspaceCancel = nil
		l.mu.Unlock()
	}()
	raw, err := client.RequestWorkspaceDiagnostics(request, identifier, previous, token)
	if errors.Is(context.Cause(request), ErrWorkspacePullRefresh) {
		return ErrWorkspacePullRefresh
	}
	if cancelled, retrigger := lsp.DiagnosticRetrigger(err); cancelled {
		if retrigger {
			return ErrWorkspacePullRefresh
		}
		return nil
	}
	if err != nil {
		return err
	}
	l.receiveWorkspaceReport(launch, identifier, raw)
	return nil
}

// receivePartialResult routes a $/progress value to the workspace request
// that owns its token. It reports false for ordinary work-done progress.
func (l *LSPIntegration) receivePartialResult(launch *lspLaunch, token string, value json.RawMessage) bool {
	if launch.pull == nil {
		return false
	}
	l.mu.Lock()
	handler := launch.pull.partial[token]
	l.mu.Unlock()
	if handler == nil {
		return false
	}
	handler(value)
	return true
}

// receiveWorkspaceReport applies a workspace report or partial result; the
// last report for a document wins, as the specification says.
func (l *LSPIntegration) receiveWorkspaceReport(launch *lspLaunch, identifier string, raw json.RawMessage) {
	if len(raw) > lsp.MaxWorkspaceDiagnosticBytes {
		return
	}
	var report struct {
		Items []struct {
			URI      protocol.DocumentURI `json:"uri"`
			Kind     string               `json:"kind"`
			ResultID string               `json:"resultId"`
			Items    []json.RawMessage    `json:"items"`
		} `json:"items"`
	}
	if json.Unmarshal(raw, &report) != nil {
		return
	}
	for _, item := range report.Items {
		path := l.uriToPath(item.URI)
		if !filepath.IsAbs(path) {
			continue
		}
		l.mu.Lock()
		if l.launch != launch {
			l.mu.Unlock()
			return
		}
		// A document the client holds is pulled for its own version.
		_, open := l.openFiles[path]
		cb := l.onUnopened
		var publication insight.DiagnosticPublication
		if item.Kind == "full" && !open && cb != nil && l.sequence != math.MaxUint64 {
			if part, ok := reportItemsPart(item.Items, false); ok {
				l.sequence++
				publication = insight.DiagnosticPublication{Sequence: l.sequence, WireVersion: 1}
				publication.Diagnostics, publication.Limited, publication.Detail = transportDiagnostics(part.diags)
			}
		}
		// Only a full report actually consumed by the closed-file collection
		// can be the base of a later unchanged response. Releasing an open file
		// must not leave a token for a result the workspace never received.
		if results := launch.pull.workspaceResults[identifier]; results != nil && item.Kind == "full" {
			if publication.Sequence != 0 && item.ResultID != "" {
				results[item.URI] = item.ResultID
			} else {
				delete(results, item.URI)
			}
		}
		l.mu.Unlock()
		if publication.Sequence != 0 {
			cb(path, publication)
		}
	}
}
