package lspclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/x/powernap/pkg/lsp"
	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol"

	"crude/insight/catalog"
	"crude/internal/metrics"
	"crude/lsp/lspedit"
	"crude/types/insight"
	"crude/types/setting"
)

// LSPIntegration manages a single LSP server connection for one project root.
// Multiple files within the same root can be opened simultaneously.
type LSPIntegration struct {
	mu          sync.Mutex
	client      *lsp.Client
	serverCmd   string
	serverArgs  []string
	serverEnv   map[string]string
	settings    map[string]any
	initOptions map[string]any
	scopes      []setting.LSPConfigurationScope
	config      lsp.Configuration
	configErr   error
	environment lsp.ProcessEnvironment
	envErr      error
	program     catalog.PackageManagerRecipe
	// startAdmission is the runtime owner's launch-epoch check. It must be
	// memory-only; construction and joined cancellation still belong here.
	startAdmission func() error
	launch         *lspLaunch
	evidence       *serverEvidence // latest launch, retained after Stop or failed initialization
	stop           *lspStop
	teardownErr    error
	rootURI        string

	// pipe monitors the subprocess stream. All writes are decoupled from
	// caller goroutines through the async writer (see lsp_pipe.go); Done
	// signals connection death (server exit, pipe error, write-queue cap).
	pipe *lspPipeMonitor

	openFiles      map[string]*openFileState // filePath -> state
	versionCounter int
	sequence       uint64 // never reset across launches in this integration

	// progress holds the server's active work-done tokens; changed is closed
	// and replaced whenever progress or a document's diagnostics change.
	progress map[string]bool
	changed  chan struct{}

	onDiagnostics func(insight.DiagnosticPublication)
	// onUnopened receives unversioned publications for files this client has
	// not opened, for example from a server that checks a whole project.
	onUnopened func(path string, p insight.DiagnosticPublication)

	// command captures only the server effects of the one explicit
	// workspace/executeCommand in flight. It is never a mutation path.
	command *commandCapture
}

type commandCapture struct {
	edits    [][]byte
	messages []string
	show     *insight.LSPShowDocument
}

// LSPIntegrationOption configures a new LSPIntegration.
type LSPIntegrationOption func(*LSPIntegration)

// WithServerEnv supplies explicit analyzer launch choices over a positive
// OS/toolchain inheritance policy, never the whole ambient core environment.
// Choices are detached and validated at construction; they grant no Trust.
func WithServerEnv(env map[string]string) LSPIntegrationOption {
	return func(l *LSPIntegration) {
		l.serverEnv = env
	}
}

type lspLaunch struct {
	evidence *serverEvidence
	// pull is this launch's pull diagnostic state; its work ends with Stop.
	pull       *pullState
	cancel     context.CancelFunc
	done       chan struct{}
	client     *lsp.Client
	err        error
	cleanupErr error
}

type lspStop struct {
	done   chan struct{}
	err    error
	client *lsp.Client
	launch *lspLaunch
}

// WithServerConfiguration configures the subprocess and server-specific protocol settings.
func WithServerConfiguration(environment map[string]string, settings, initOptions map[string]any) LSPIntegrationOption {
	return func(l *LSPIntegration) {
		l.serverEnv = environment
		l.settings = settings
		l.initOptions = initOptions
	}
}

// WithConfigurationScopes supplies module-specific settings. They are detached
// when the integration is constructed, not re-read from caller maps on Start.
func WithConfigurationScopes(scopes []setting.LSPConfigurationScope) LSPIntegrationOption {
	return func(l *LSPIntegration) { l.scopes = scopes }
}

// WithStartAdmission installs the producer owner's final admission check at the
// subprocess-construction boundary. It grants no permission itself. A launch
// admitted before concurrent revocation remains owned until verified teardown.
func WithStartAdmission(admit func() error) LSPIntegrationOption {
	return func(l *LSPIntegration) { l.startAdmission = admit }
}

type openFileState struct {
	version    int
	uri        string
	languageID string
	text       string
	boundary   insight.Boundary
	// syncedAt is when this version was admitted; publishedAt and publication
	// record the latest publication for exactly this version.
	syncedAt    time.Time
	publishedAt time.Time
	publication insight.DiagnosticPublication
	// parts are this version's reports by source (push, each pull provider);
	// publication is their union. pulls keep each provider's pull state
	// across versions. changed marks a version from an edit, whose pull is
	// followed by the other open documents of inter-file providers.
	parts   map[string]diagnosticPart
	pulls   map[string]*documentPull
	changed bool
	// This immutable adjunct is original protocol evidence at this publication,
	// not a second diagnostic projection or shell-visible provider-data cache.
	actionDiagnostics codeActionDiagnosticAdjunct
}

// OnUnopenedDiagnostics sets the callback for unversioned diagnostics about
// files that this client has not opened. The publication has no boundary:
// the caller owns the source identity of a file that no editor holds.
// Versioned publications for unopened files are dropped: a version names a
// document text that this client never sent.
func (l *LSPIntegration) OnUnopenedDiagnostics(fn func(path string, p insight.DiagnosticPublication)) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.onUnopened = fn
}

// OnDiagnostics sets the callback for receiving diagnostics for open files.
func (l *LSPIntegration) OnDiagnostics(fn func(insight.DiagnosticPublication)) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.onDiagnostics = fn
}

func NewLSPIntegration(serverCmd string, serverArgs []string, rootURI string, opts ...LSPIntegrationOption) *LSPIntegration {
	l := &LSPIntegration{
		serverCmd:  serverCmd,
		serverArgs: append([]string(nil), serverArgs...),
		rootURI:    rootURI,
		openFiles:  make(map[string]*openFileState),
		progress:   make(map[string]bool),
		changed:    make(chan struct{}),
	}
	for _, opt := range opts {
		opt(l)
	}
	if len(l.scopes) > lsp.MaxConfigurationScopes {
		l.configErr = lsp.ErrConfiguration
	} else {
		scopes := make([]lsp.ConfigurationScope, len(l.scopes))
		for i, scope := range l.scopes {
			scopes[i] = lsp.ConfigurationScope{URI: scope.URI, Settings: scope.Settings}
		}
		l.config, l.configErr = lsp.NewConfiguration(rootURI, l.settings, scopes)
	}
	l.settings, l.scopes = nil, nil // only the immutable launch table remains
	l.environment, l.envErr = prepareAnalyzerEnvironment(l.serverEnv, rootURI)
	l.serverEnv = nil // no caller-owned maps survive preparation
	return l
}

func (l *LSPIntegration) RootURI() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.rootURI
}

func (l *LSPIntegration) Start(ctx context.Context) error {
	for {
		l.mu.Lock()
		if err := ctx.Err(); err != nil {
			l.mu.Unlock()
			return err
		}
		if l.configErr != nil {
			l.mu.Unlock()
			return l.configErr
		}
		if l.envErr != nil {
			l.mu.Unlock()
			return l.envErr
		}
		stopping := l.stop
		if stopping == nil {
			break
		}
		l.mu.Unlock()
		select {
		case <-stopping.done:
			if stopping.err != nil {
				return fmt.Errorf("previous language server teardown is unverified: %w", stopping.err)
			}
		case <-ctx.Done():
			return ctx.Err()
		}
		l.mu.Lock()
		if l.stop == stopping {
			l.stop = nil
		}
		l.mu.Unlock()
	}
	if l.teardownErr != nil {
		err := l.teardownErr
		l.mu.Unlock()
		return fmt.Errorf("previous language server teardown is unverified: %w", err)
	}
	if l.client != nil {
		l.mu.Unlock()
		return nil
	}
	if launch := l.launch; launch != nil {
		l.mu.Unlock()
		select {
		case <-launch.done:
			return launch.err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	started := time.Now()
	initCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	launch := &lspLaunch{cancel: cancel, done: make(chan struct{}), evidence: &serverEvidence{}, pull: newPullState()}
	l.evidence = launch.evidence
	l.launch = launch
	l.mu.Unlock()
	defer cancel()
	var pipe *lspPipeMonitor
	var client *lsp.Client
	// Stop can revoke this launch after it is registered but before construction.
	// A constructor already admitted past this point remains owned and must be
	// joined below; cancellation is never evidence that no process was created.
	err := initCtx.Err()
	if err == nil && l.startAdmission != nil {
		err = l.startAdmission()
	}
	if err == nil {
		var command string
		var args []string
		command, args, err = resolveServerProgram(initCtx, l.serverCmd, l.serverArgs, l.environment, l.program)
		if err == nil {
			client, err = lsp.NewClient(lsp.ClientConfig{
				Command: command, Args: args, RootURI: l.rootURI,
				Environment: l.environment, Configuration: l.config, InitOptions: l.initOptions,
				StderrSink: launch.evidence.stderr,
				TransportWrap: func(reader io.Reader, writer io.Writer) (io.Reader, io.Writer) {
					nr, nw, mon := wrapLSPPipe(reader, writer)
					pipe = mon
					return nr, nw
				},
			})
		}
	}
	if err == nil {
		l.mu.Lock()
		launch.client = client
		stopped := l.launch != launch
		l.mu.Unlock()
		if stopped {
			err = context.Canceled
		} else {
			client.RegisterNotificationHandler("textDocument/publishDiagnostics", func(_ context.Context, _ string, params json.RawMessage) {
				l.receiveDiagnostics(launch, params)
			})
			l.registerPullHandlers(launch, client)
			client.RegisterHandler("window/workDoneProgress/create", func(context.Context, string, json.RawMessage) (any, error) {
				return nil, nil
			})
			client.RegisterHandler("workspace/applyEdit", func(_ context.Context, _ string, params json.RawMessage) (any, error) {
				return l.captureApplyEdit(client, params)
			})
			client.RegisterHandler("window/showDocument", func(_ context.Context, _ string, params json.RawMessage) (any, error) {
				return l.captureShowDocument(client, params)
			})
			client.RegisterNotificationHandler("window/showMessage", func(_ context.Context, _ string, params json.RawMessage) {
				l.captureServerMessage(launch, "window/showMessage", params)
				l.captureShowMessage(client, params)
			})
			client.RegisterNotificationHandler("window/logMessage", func(_ context.Context, _ string, params json.RawMessage) {
				l.captureServerMessage(launch, "window/logMessage", params)
			})
			client.RegisterNotificationHandler("$/progress", func(_ context.Context, _ string, params json.RawMessage) {
				var progress struct {
					Token any             `json:"token"`
					Value json.RawMessage `json:"value"`
				}
				if json.Unmarshal(params, &progress) != nil {
					return
				}
				token := fmt.Sprint(progress.Token)
				// Partial results of a workspace diagnostic pull share
				// $/progress with work-done progress; the token tells them apart.
				if l.receivePartialResult(launch, token, progress.Value) {
					return
				}
				var value struct {
					Kind string `json:"kind"`
				}
				if json.Unmarshal(progress.Value, &value) == nil {
					l.updateProgress(launch, token, value.Kind)
				}
			})
			err = client.Initialize(initCtx, true)
		}
	}
	l.mu.Lock()
	if l.launch != launch || initCtx.Err() != nil {
		if err == nil {
			err = context.Canceled
		}
	} else if err == nil {
		l.client = client
		l.pipe = pipe
		if launch.pull != nil {
			installStaticProviderLocked(launch, client)
		}
	}
	l.mu.Unlock()
	if err != nil && client != nil {
		launch.cleanupErr = client.Kill()
		err = errors.Join(err, launch.cleanupErr)
	} else if errors.Is(err, lsp.ErrProcessCleanupUnverified) {
		// A failed constructor can still own a process whose setup rollback
		// was not verified. Nil is not evidence that its slot is reusable.
		launch.cleanupErr = err
	}
	l.mu.Lock()
	launch.err = err
	if err != nil && launch.pull != nil {
		launch.pull.stopWork()
	}
	l.teardownErr = errors.Join(l.teardownErr, launch.cleanupErr)
	if err != nil && launch.cleanupErr == nil && l.launch == launch {
		l.launch = nil
	}
	l.mu.Unlock()
	lspStarts.Since(l.serverName(), started)
	if err != nil && !errors.Is(err, context.Canceled) {
		metrics.Note(metrics.GroupLSP, "Language server failed to start", l.serverName(), err.Error(), "crit")
	}
	close(launch.done)
	return err
}

// Done is closed when the underlying server connection dies (server exit,
// pipe error, or write-queue cap). A closed Done means the client must be
// reaped and restarted before further LSP work. It is nil-safe before Start.
func (l *LSPIntegration) Done() <-chan struct{} {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.pipe == nil {
		// Never started: report a never-closing channel rather than a
		// closed one so callers cannot reap an unstarted client.
		return nil
	}
	return l.pipe.Done()
}

// OpenFile admits source synchronization and returns its actual LSP wire version.
// Admission to the asynchronous write queue is not an analysis acknowledgement.
func (l *LSPIntegration) OpenFile(ctx context.Context, filePath, languageID, text string, boundary insight.Boundary) (int, error) {
	fp, err := diagnosticPath(filePath, boundary)
	if err != nil {
		return 0, err
	}
	if languageID == "" {
		return 0, errors.New("language ID is empty")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.availableLocked(ctx); err != nil {
		return 0, err
	}
	if state := l.openFiles[fp]; state != nil {
		return l.updateFileLocked(ctx, fp, state, text, boundary)
	}
	if l.versionCounter == math.MaxInt32 {
		return 0, errors.New("LSP wire version exhausted")
	}
	uri := string(protocol.URIFromPath(fp))
	version := l.versionCounter + 1
	if err := l.client.NotifyDidOpenTextDocument(ctx, uri, languageID, version, text); err != nil {
		return 0, fmt.Errorf("send didOpen: %w", err)
	}
	// The notification reader uses mu too: it cannot observe this admission
	// until the immutable input and version have been installed together.
	state := &openFileState{uri: uri, languageID: languageID, text: text, boundary: cloneBoundary(boundary)}
	state.resetVersion(version)
	l.openFiles[fp] = state
	l.versionCounter = version
	l.signalLocked()
	l.pullDocumentLocked(l.launch, fp, state, "")
	return version, nil
}

// UpdateFile sends the full text of an open file and returns the version that the server confirms.
func (l *LSPIntegration) UpdateFile(ctx context.Context, filePath, text string, boundary insight.Boundary) (int, error) {
	fp, err := diagnosticPath(filePath, boundary)
	if err != nil {
		return 0, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.availableLocked(ctx); err != nil {
		return 0, err
	}
	state := l.openFiles[fp]
	if state == nil {
		return 0, errors.New("LSP document is not open")
	}
	return l.updateFileLocked(ctx, fp, state, text, boundary)
}

func (l *LSPIntegration) updateFileLocked(ctx context.Context, path string, state *openFileState, text string, boundary insight.Boundary) (int, error) {
	if state.text == text && equalBoundary(state.boundary, boundary) {
		return state.version, nil
	}
	if l.versionCounter == math.MaxInt32 {
		return 0, errors.New("LSP wire version exhausted")
	}
	version := l.versionCounter + 1
	if err := l.client.NotifyDidChangeTextDocument(ctx, state.uri, version, []protocol.TextDocumentContentChangeEvent{
		{Value: protocol.TextDocumentContentChangeWholeDocument{Text: text}},
	}); err != nil {
		return 0, fmt.Errorf("send didChange: %w", err)
	}
	state.text, state.boundary = text, cloneBoundary(boundary)
	state.resetVersion(version)
	state.changed = true
	l.versionCounter = version
	l.signalLocked()
	l.pullDocumentLocked(l.launch, path, state, "")
	return version, nil
}

func (l *LSPIntegration) availableLocked(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if l.client == nil {
		return errors.New("LSP client is unavailable")
	}
	if l.pipe != nil {
		select {
		case <-l.pipe.Done():
			return errors.New("LSP connection is closed")
		default:
		}
	}
	return nil
}

func (l *LSPIntegration) CloseFile(ctx context.Context, filePath string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	fp := filepath.Clean(filePath)
	state, ok := l.openFiles[fp]
	if !ok {
		return
	}
	delete(l.openFiles, fp)
	for _, pull := range state.pulls {
		if pull.inFlight {
			pull.cancel()
		}
	}
	l.signalLocked()
	uri := state.uri
	client := l.client

	if client == nil {
		return
	}
	_ = client.NotifyDidCloseTextDocument(ctx, uri)
}

func (l *LSPIntegration) NotifySave(ctx context.Context, path, text string, reason int, before bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	state := l.openFiles[filepath.Clean(path)]
	if l.client == nil || state == nil {
		return
	}
	_ = l.client.NotifyDocumentSave(ctx, state.uri, text, reason, before)
	if !before {
		l.pullDocumentLocked(l.launch, filepath.Clean(path), state, "")
	}
}

// RequestDocumentSymbols returns a hierarchical outline (functions, types,
// variables) for the given file. Children of nested symbols are preserved.
func (l *LSPIntegration) RequestDocumentSymbols(ctx context.Context, filePath string) []insight.LSPSymbol {
	defer l.observeRequest("symbols", time.Now())
	l.mu.Lock()
	client := l.client
	fp := filepath.Clean(filePath)
	state, ok := l.openFiles[fp]
	version := stateVersion(state)
	l.mu.Unlock()

	if client == nil || !ok {
		return nil
	}
	results, err := client.RequestDocumentSymbols(ctx, fp)
	if !l.currentDocument(fp, client, state, version) {
		return nil
	}
	if err != nil {
		return nil
	}
	out := make([]insight.LSPSymbol, 0, len(results))
	for _, r := range results {
		out = append(out, l.convertDocSymbol(r, 0)...)
	}
	return out
}

// RequestHover returns the hover information (markup content + range) for the
// symbol at (line, col). Returns nil if no hover info is available.
func (l *LSPIntegration) RequestHover(ctx context.Context, filePath string, line, col int) (string, error) {
	defer l.observeRequest("hover", time.Now())
	l.mu.Lock()
	fp := filepath.Clean(filePath)
	state, ok := l.openFiles[fp]
	if l.client == nil || !ok {
		l.mu.Unlock()
		return "", fmt.Errorf("file not open in LSP")
	}
	uri := state.uri
	client := l.client
	version := stateVersion(state)
	l.mu.Unlock()

	hover, err := client.RequestHover(ctx, uri, protocol.Position{
		Line:      uint32(line),
		Character: uint32(col),
	})
	if !l.currentDocument(fp, client, state, version) {
		return "", context.Canceled
	}
	if err != nil {
		return "", err
	}
	if hover == nil || hover.Contents.Value == "" {
		return "", nil
	}
	// Hover.Contents can be a string, MarkupContent, or MarkedString.
	// protocol normalizes it to a single string in our usage; strip basic
	// markdown noise for terminal display.
	return stripMarkdown(hover.Contents.Value), nil
}

// RequestRename returns the workspace edit that would rename the symbol at
// (line, col) to newName. The caller is responsible for applying the edit.
func (l *LSPIntegration) RequestRename(ctx context.Context, filePath string, line, col int, newName string) (insight.LSPRenameEdit, error) {
	defer l.observeRequest("rename", time.Now())
	l.mu.Lock()
	client := l.client
	fp := filepath.Clean(filePath)
	state, ok := l.openFiles[fp]
	version := stateVersion(state)
	text := ""
	if state != nil {
		text = state.text
	}
	l.mu.Unlock()

	if client == nil || !ok {
		return insight.LSPRenameEdit{}, fmt.Errorf("file not open in LSP")
	}
	if _, err := lspedit.Offset(text, line, col); err != nil {
		return insight.LSPRenameEdit{}, err
	}
	result, err := client.RequestRename(ctx, fp, line, col, newName)
	if !l.currentDocument(fp, client, state, version) || ctx.Err() != nil {
		return insight.LSPRenameEdit{}, context.Canceled
	}
	if err != nil {
		return insight.LSPRenameEdit{}, err
	}
	if len(result) > lspedit.MaxRawBytes || !utf8.Valid(result) {
		return insight.LSPRenameEdit{}, errors.New("invalid or oversized language-server rename edit")
	}
	return insight.LSPRenameEdit{JSON: append([]byte(nil), result...), WireVersion: version}, nil
}

// ServerCmd returns the command used to launch the LSP server, e.g. "gopls".
func (l *LSPIntegration) ServerCmd() string {
	return l.serverCmd
}

// convertDocSymbol flattens a DocumentSymbolResult (which can be either
// DocumentSymbol or SymbolInformation) into our LSPSymbol type. The depth
// parameter caps recursion to avoid pathological trees in the UI.
func (l *LSPIntegration) convertDocSymbol(r protocol.DocumentSymbolResult, depth int) []insight.LSPSymbol {
	if depth > 6 {
		return nil
	}
	switch v := r.(type) {
	case *protocol.DocumentSymbol:
		sym := insight.LSPSymbol{
			Name:     v.Name,
			Detail:   v.Detail,
			Kind:     int(v.Kind),
			KindName: SymbolKindName(int(v.Kind)),
			Line:     int(v.Range.Start.Line),
			Col:      int(v.Range.Start.Character),
			EndLine:  int(v.Range.End.Line),
			EndCol:   int(v.Range.End.Character),
		}
		if len(v.Children) > 0 {
			sym.Children = make([]insight.LSPSymbol, 0, len(v.Children))
			for i := range v.Children {
				kids := l.convertDocSymbol(&v.Children[i], depth+1)
				sym.Children = append(sym.Children, kids...)
			}
		}
		return []insight.LSPSymbol{sym}
	case *protocol.SymbolInformation:
		return []insight.LSPSymbol{{
			Name:     v.Name,
			Detail:   "", // SymbolInformation has no detail field
			Kind:     int(v.Kind),
			KindName: SymbolKindName(int(v.Kind)),
			Line:     int(v.Location.Range.Start.Line),
			Col:      int(v.Location.Range.Start.Character),
			EndLine:  int(v.Location.Range.End.Line),
			EndCol:   int(v.Location.Range.End.Character),
		}}
	}
	return nil
}

// stripMarkdown removes common markdown noise that doesn't render well in a
// terminal popup: fenced code blocks' ``` fences, heading #'s, and bold/italic
// markers. Code spans are preserved as-is.
func stripMarkdown(s string) string {
	s = strings.ReplaceAll(s, "```", "")
	s = strings.ReplaceAll(s, "`", "")
	// Drop leading "## ", "### ", etc. from start of lines.
	lines := strings.Split(s, "\n")
	for i, ln := range lines {
		ln = strings.TrimLeft(ln, " \t")
		for strings.HasPrefix(ln, "#") {
			ln = strings.TrimPrefix(ln, "#")
			ln = strings.TrimLeft(ln, " \t")
		}
		lines[i] = ln
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// Stop fences admission, cancels startup and joins the exact owned teardown.
// Repeated and concurrent calls observe the same outcome. Failure retains that
// lifetime and prevents this integration from launching a replacement.
func (l *LSPIntegration) Stop() error {
	l.mu.Lock()
	if stopping := l.stop; stopping != nil {
		l.mu.Unlock()
		<-stopping.done
		return stopping.err
	}
	stopping := &lspStop{done: make(chan struct{}), client: l.client, launch: l.launch}
	client, launch := stopping.client, stopping.launch
	l.launch = nil
	l.stop = stopping
	l.client = nil
	l.openFiles = make(map[string]*openFileState)
	l.progress = make(map[string]bool)
	l.signalLocked()
	var starting *lsp.Client
	if launch != nil {
		launch.cancel()
		if launch.pull != nil {
			launch.pull.stopWork()
		}
		starting = launch.client
	}
	l.mu.Unlock()
	var teardownErr error
	if client != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
		if client.Shutdown(ctx) == nil {
			_ = client.Exit()
		}
		cancel()
		teardownErr = client.Kill()
	} else if starting != nil {
		teardownErr = starting.Kill()
	}
	if launch != nil {
		<-launch.done
		teardownErr = errors.Join(teardownErr, launch.cleanupErr)
		// Pull goroutines end with the work context and the closed connection.
		if launch.pull != nil {
			launch.pull.wg.Wait()
		}
	}
	l.mu.Lock()
	l.teardownErr = errors.Join(l.teardownErr, teardownErr)
	if reapedProcessExitOnly(l.teardownErr) {
		// A server's own exit status is an operational failure, not a live
		// resource. Kill still exposes it; connection loss remains failed in
		// manager health. Do not poison admission after verified group/Job
		// cleanup and a completed Wait merely because the server crashed.
		metrics.Note(metrics.GroupLSP, "Language server exited", l.serverName(), l.teardownErr.Error(), "warn")
		l.teardownErr = nil
	}
	stopping.err = l.teardownErr
	close(stopping.done)
	l.mu.Unlock()
	return stopping.err
}

func reapedProcessExitOnly(err error) bool {
	if err == nil {
		return false
	}
	if _, ok := err.(*exec.ExitError); ok {
		return true
	}
	switch wrapped := err.(type) {
	case interface{ Unwrap() []error }:
		causes := wrapped.Unwrap()
		if len(causes) == 0 {
			return false
		}
		for _, cause := range causes {
			if !reapedProcessExitOnly(cause) {
				return false
			}
		}
		return true
	case interface{ Unwrap() error }:
		return reapedProcessExitOnly(wrapped.Unwrap())
	default:
		return false
	}
}

// NotifyWatchedFilesChanged sends workspace/didChangeWatchedFiles to the LSP
// server for files that changed on disk (outside the editor).
func (l *LSPIntegration) NotifyWatchedFilesChanged(ctx context.Context, filePath string, changeType int) {
	l.mu.Lock()
	client := l.client
	l.mu.Unlock()

	if client == nil {
		return
	}

	fp := filepath.Clean(filePath)
	uri := string(protocol.URIFromPath(fp))

	_ = client.NotifyDidChangeWatchedFiles(ctx, []protocol.FileEvent{
		{
			URI:  protocol.DocumentURI(uri),
			Type: protocol.FileChangeType(changeType),
		},
	})
}

// Diagnostics returns nil because per-file diagnostics arrive through a callback.
func (l *LSPIntegration) Diagnostics() []insight.LSPDiagnostic {
	return nil // per-file diagnostics are not stored centrally; routed via callback
}

func (l *LSPIntegration) uriToPath(uri protocol.DocumentURI) string {
	path, err := uri.Path()
	if err != nil {
		return string(uri)
	}
	return filepath.Clean(path)
}

func (l *LSPIntegration) updateDiagnostics(launch *lspLaunch, uri protocol.DocumentURI, version *int32, diags []protocol.Diagnostic, originals ...json.RawMessage) {
	filePath := l.uriToPath(uri)
	l.mu.Lock()
	if launch == nil || l.launch != launch || l.client == nil {
		l.mu.Unlock()
		return
	}
	state := l.openFiles[filePath]
	if state == nil && version == nil && l.onUnopened != nil && l.sequence != math.MaxUint64 {
		l.sequence++
		publication := insight.DiagnosticPublication{Sequence: l.sequence, WireVersion: 1}
		publication.Diagnostics, publication.Limited, publication.Detail = transportDiagnostics(diags)
		cb := l.onUnopened
		l.mu.Unlock()
		cb(filePath, publication)
		return
	}
	if state == nil || (version != nil && int(*version) != state.version) || l.sequence == math.MaxUint64 {
		if state != nil {
			state.actionDiagnostics = codeActionDiagnosticAdjunct{detail: "diagnostics omitted: stale or invalid publication"}
		}
		l.mu.Unlock()
		return
	}
	if state.parts == nil {
		state.parts = map[string]diagnosticPart{}
	}
	state.parts[pushPart] = pushDiagnosticPart(diags, version, originals)
	deliver := l.publishPartsLocked(launch, filePath, state)
	l.mu.Unlock()
	deliver()
}

// transportDiagnostics converts one publication within the transport bounds.
func transportDiagnostics(diags []protocol.Diagnostic) ([]insight.LSPDiagnostic, bool, string) {
	var out []insight.LSPDiagnostic
	bytes := 0
	for _, d := range diags {
		code := diagnosticCode(d.Code)
		size := len(d.Message) + len(d.Source) + len(code)
		if len(out) == maxTransportDiagnostics || size > maxTransportDiagnosticBytes-bytes {
			return out, true, "LSP transport publication exceeded 4096 diagnostics or 1 MiB of diagnostic text"
		}
		bytes += size
		out = append(out, insight.LSPDiagnostic{Line: int(d.Range.Start.Line), Col: int(d.Range.Start.Character), EndLine: int(d.Range.End.Line), EndCol: int(d.Range.End.Character), Message: d.Message, Severity: int(d.Severity), Source: d.Source, Code: code})
	}
	return out, false, ""
}

// diagnosticCode renders the protocol's integer-or-string code.
func diagnosticCode(code any) string {
	switch v := code.(type) {
	case nil:
		return ""
	case string:
		return v
	case float64:
		return strconv.FormatInt(int64(v), 10)
	default:
		return fmt.Sprint(v)
	}
}

func (s *openFileState) resetVersion(version int) {
	s.version, s.syncedAt = version, time.Now()
	s.publishedAt, s.publication, s.parts = time.Time{}, insight.DiagnosticPublication{}, nil
	s.actionDiagnostics = codeActionDiagnosticAdjunct{}
}

func (l *LSPIntegration) signalLocked() {
	close(l.changed)
	l.changed = make(chan struct{})
}

func (l *LSPIntegration) updateProgress(launch *lspLaunch, token, kind string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.launch != launch {
		return
	}
	switch kind {
	case "begin":
		l.progress[token] = true
	case "end":
		delete(l.progress, token)
	default:
		return
	}
	l.signalLocked()
}

// Busy reports active work-done progress, such as an initial project load.
// The manager uses it to tell a settled project report from one in flight.
func (l *LSPIntegration) Busy() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.progress) > 0
}

// Settled diagnostics bound how long a quiet server is trusted: the wait after
// the last publication matches the server's own response time, within limits
// that keep a slow first analysis from stalling the caller indefinitely.
const (
	minDiagnosticsQuiet = 50 * time.Millisecond
	maxDiagnosticsQuiet = time.Second
)

// AwaitDiagnostics returns the literal latest publication for the source pinned
// at entry. Settled means observed quiescence only, not exact source attribution
// (ReportedVersion can be nil) or complete project context.
func (l *LSPIntegration) AwaitDiagnostics(ctx context.Context, filePath string) (insight.DiagnosticPublication, bool) {
	fp := filepath.Clean(filePath)
	l.mu.Lock()
	state, client, launch := l.openFiles[fp], l.client, l.launch
	version := stateVersion(state)
	l.mu.Unlock()
	for {
		l.mu.Lock()
		if state == nil || client == nil || l.client != client || l.launch != launch ||
			l.openFiles[fp] != state || state.version != version {
			l.mu.Unlock()
			return insight.DiagnosticPublication{}, false
		}
		published, synced, busy, changed := state.publishedAt, state.syncedAt, len(l.progress) > 0, l.changed
		publication := clonePublication(state.publication)
		if ctx.Err() != nil {
			l.mu.Unlock()
			return publication, false
		}
		var timer *time.Timer
		var fired <-chan time.Time
		if !published.IsZero() && !busy {
			quiet := min(max(published.Sub(synced), minDiagnosticsQuiet), maxDiagnosticsQuiet)
			remaining := quiet - time.Since(published)
			if remaining <= 0 {
				l.mu.Unlock()
				return publication, true
			}
			timer = time.NewTimer(remaining)
			fired = timer.C
		}
		l.mu.Unlock()
		select {
		case <-ctx.Done():
		case <-changed:
		case <-fired:
		}
		if timer != nil {
			timer.Stop()
		}
	}
}

func stateVersion(state *openFileState) int {
	if state == nil {
		return 0
	}
	return state.version
}

func (l *LSPIntegration) currentDocument(path string, client *lsp.Client, state *openFileState, version int) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.client == client && state != nil && l.openFiles[path] == state && state.version == version
}
