package lspclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/x/powernap/pkg/lsp"
	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol"

	"crude/metrics"
	model "crude/types"
)

type LSPDiagnostic = model.LSPDiagnostic

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
	scopes      []ConfigurationScope
	config      lsp.Configuration
	configErr   error
	environment lsp.ProcessEnvironment
	envErr      error
	// startAdmission is the runtime owner's launch-epoch check. It must be
	// memory-only; construction and joined cancellation still belong here.
	startAdmission func() error
	launch         *lspLaunch
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

	onDiagnostics func(model.InsightDiagnosticPublication)
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

// ConfigurationScope is a complete effective configuration for a directory URI.
// Selection is launch-bound and lexical, not execution authority or project truth.
type ConfigurationScope = model.LSPConfigurationScope

// WithConfigurationScopes supplies module-specific settings. They are detached
// when the integration is constructed, not re-read from caller maps on Start.
func WithConfigurationScopes(scopes []ConfigurationScope) LSPIntegrationOption {
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
	boundary   model.InsightBoundary
	// syncedAt is when this version was admitted; publishedAt and publication
	// record the latest publication for exactly this version.
	syncedAt    time.Time
	publishedAt time.Time
	publication model.InsightDiagnosticPublication
}

// OnDiagnostics sets the callback for receiving diagnostics for open files.
func (l *LSPIntegration) OnDiagnostics(fn func(model.InsightDiagnosticPublication)) {
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
	environment, err := analyzerEnvironment(os.Environ(), l.serverEnv, rootURI)
	if err == nil {
		l.environment, err = lsp.NewProcessEnvironment(environment)
	}
	l.envErr = err
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
	launch := &lspLaunch{cancel: cancel, done: make(chan struct{})}
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
		client, err = lsp.NewClient(lsp.ClientConfig{
			Command: l.serverCmd, Args: l.serverArgs, RootURI: l.rootURI,
			Environment: l.environment, Configuration: l.config, InitOptions: l.initOptions,
			TransportWrap: func(reader io.Reader, writer io.Writer) (io.Reader, io.Writer) {
				nr, nw, mon := wrapLSPPipe(reader, writer)
				pipe = mon
				return nr, nw
			},
		})
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
				var diagnostics struct {
					protocol.PublishDiagnosticsParams
					Version *int32 `json:"version"`
				}
				if json.Unmarshal(params, &diagnostics) == nil {
					lspDiagnostics.Inc()
					l.updateDiagnostics(launch, diagnostics.URI, diagnostics.Version, diagnostics.Diagnostics)
				}
			})
			client.RegisterHandler("window/workDoneProgress/create", func(context.Context, string, json.RawMessage) (any, error) {
				return nil, nil
			})
			client.RegisterNotificationHandler("$/progress", func(_ context.Context, _ string, params json.RawMessage) {
				var progress struct {
					Token any `json:"token"`
					Value struct {
						Kind string `json:"kind"`
					} `json:"value"`
				}
				if json.Unmarshal(params, &progress) == nil {
					l.updateProgress(launch, fmt.Sprint(progress.Token), progress.Value.Kind)
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
func (l *LSPIntegration) OpenFile(ctx context.Context, filePath, languageID, text string, boundary model.InsightBoundary) (int, error) {
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
		return l.updateFileLocked(ctx, state, text, boundary)
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
	return version, nil
}

func (l *LSPIntegration) UpdateFile(ctx context.Context, filePath, text string, boundary model.InsightBoundary) (int, error) {
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
	return l.updateFileLocked(ctx, state, text, boundary)
}

func (l *LSPIntegration) updateFileLocked(ctx context.Context, state *openFileState, text string, boundary model.InsightBoundary) (int, error) {
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
	l.versionCounter = version
	l.signalLocked()
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
}

// RequestCompletions returns a flat list of completion items at (line, col).
// Returns nil if the request fails or the file is not open in the LSP server.
func (l *LSPIntegration) RequestCompletions(ctx context.Context, filePath string, line, col int) []CompletionItem {
	defer l.observeRequest("completion", time.Now())
	l.mu.Lock()
	fp := filepath.Clean(filePath)
	state, ok := l.openFiles[fp]
	if l.client == nil || !ok {
		l.mu.Unlock()
		return nil
	}
	uri := state.uri
	client := l.client
	version := stateVersion(state)
	l.mu.Unlock()

	result, err := client.RequestCompletion(ctx, uri, protocol.Position{
		Line:      uint32(line),
		Character: uint32(col),
	})
	if !l.currentDocument(fp, client, state, version) {
		return nil
	}
	if err != nil || result == nil {
		return nil
	}
	out := make([]CompletionItem, 0, len(result.Items))
	for _, it := range result.Items {
		if it.Command != nil {
			continue
		}
		item := CompletionItem{
			Label:         it.Label,
			Detail:        it.Detail,
			Documentation: documentationOf(it),
			Kind:          int(it.Kind),
			InsertText:    insertTextOf(it),
		}
		if it.TextEdit != nil {
			var edit protocol.TextEdit
			switch v := it.TextEdit.Value.(type) {
			case protocol.TextEdit:
				edit = v
			case *protocol.TextEdit:
				edit = *v
			case protocol.InsertReplaceEdit:
				edit = protocol.TextEdit{Range: v.Replace, NewText: v.NewText}
			case *protocol.InsertReplaceEdit:
				edit = protocol.TextEdit{Range: v.Replace, NewText: v.NewText}
			default:
				continue
			}
			converted := neutralTextEdit(edit)
			item.TextEdit = &converted
			item.InsertText = edit.NewText
		}
		if it.InsertTextFormat != nil && *it.InsertTextFormat == 2 {
			plain, ok := expandSnippet(item.InsertText)
			if !ok {
				continue
			}
			item.InsertText = plain
			if item.TextEdit != nil {
				item.TextEdit.NewText = plain
			}
		}
		for _, edit := range it.AdditionalTextEdits {
			item.AdditionalTextEdits = append(item.AdditionalTextEdits, neutralTextEdit(edit))
		}
		out = append(out, item)
	}
	return out
}

// CompletionItem is a flat, viewmodel-friendly representation of a single
// completion result returned by the LSP server.
type CompletionItem = model.CompletionItem

func insertTextOf(it protocol.CompletionItem) string {
	// it.TextEdit is a tagged union; if present, it carries the actual
	// replacement text. Fall back to the simpler InsertText when the server
	// doesn't supply a text edit.
	if it.TextEdit != nil {
		switch t := it.TextEdit.Value.(type) {
		case protocol.TextEdit:
			return t.NewText
		case *protocol.TextEdit:
			return t.NewText
		}
	}
	if it.InsertText == "" {
		return it.Label
	}
	return it.InsertText
}

func documentationOf(it protocol.CompletionItem) string {
	if it.Documentation == nil {
		return ""
	}
	switch v := it.Documentation.Value.(type) {
	case string:
		return v
	case protocol.MarkupContent:
		return v.Value
	}
	return ""
}

// RequestDefinition returns the definition location(s) for the symbol at
// (line, col) in filePath. Returns nil if the request fails or the client is
// not connected. Multiple locations can be returned for symbols defined in
// multiple places (interfaces, generics).
func (l *LSPIntegration) RequestDefinition(ctx context.Context, filePath string, line, col int) []LSPLocation {
	defer l.observeRequest("definition", time.Now())
	l.mu.Lock()
	client := l.client
	fp := filepath.Clean(filePath)
	state, ok := l.openFiles[fp]
	version := stateVersion(state)
	l.mu.Unlock()

	if client == nil || !ok {
		return nil
	}
	results, err := client.RequestDefinition(ctx, fp, line, col)
	if !l.currentDocument(fp, client, state, version) {
		return nil
	}
	if err != nil {
		return nil
	}
	return l.toLSPLocations(results)
}

// FindReferences returns all references to the symbol at (line, col) in
// filePath, optionally including the declaration site. includeDeclaration=true
// is the typical UX (VSCode/JetBrains include it by default).
func (l *LSPIntegration) FindReferences(ctx context.Context, filePath string, line, col int, includeDeclaration bool) []LSPLocation {
	defer l.observeRequest("references", time.Now())
	l.mu.Lock()
	client := l.client
	fp := filepath.Clean(filePath)
	state, ok := l.openFiles[fp]
	version := stateVersion(state)
	l.mu.Unlock()

	if client == nil || !ok {
		return nil
	}
	results, err := client.FindReferences(ctx, fp, line, col, includeDeclaration)
	if !l.currentDocument(fp, client, state, version) {
		return nil
	}
	if err != nil {
		return nil
	}
	return l.toLSPLocations(results)
}

// RequestDocumentSymbols returns a hierarchical outline (functions, types,
// variables) for the given file. Children of nested symbols are preserved.
func (l *LSPIntegration) RequestDocumentSymbols(ctx context.Context, filePath string) []LSPSymbol {
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
	out := make([]LSPSymbol, 0, len(results))
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
func (l *LSPIntegration) RequestRename(ctx context.Context, filePath string, line, col int, newName string) (*protocol.WorkspaceEdit, error) {
	defer l.observeRequest("rename", time.Now())
	l.mu.Lock()
	client := l.client
	fp := filepath.Clean(filePath)
	state, ok := l.openFiles[fp]
	version := stateVersion(state)
	l.mu.Unlock()

	if client == nil || !ok {
		return nil, fmt.Errorf("file not open in LSP")
	}
	result, err := client.RequestRename(ctx, fp, line, col, newName)
	if !l.currentDocument(fp, client, state, version) {
		return nil, context.Canceled
	}
	return result, err
}

// ServerCmd returns the command used to launch the LSP server, e.g. "gopls".
func (l *LSPIntegration) ServerCmd() string {
	return l.serverCmd
}

func (l *LSPIntegration) toLSPLocations(locs []protocol.Location) []LSPLocation {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]LSPLocation, 0, len(locs))
	for _, p := range locs {
		uri := string(p.URI)
		col, endCol := int(p.Range.Start.Character), int(p.Range.End.Character)

		out = append(out, LSPLocation{
			URI:     uri,
			Path:    l.uriToPath(p.URI),
			Line:    int(p.Range.Start.Line),
			Col:     col,
			EndLine: int(p.Range.End.Line),
			EndCol:  endCol,
		})
	}
	return out
}

// convertDocSymbol flattens a DocumentSymbolResult (which can be either
// DocumentSymbol or SymbolInformation) into our LSPSymbol type. The depth
// parameter caps recursion to avoid pathological trees in the UI.
func (l *LSPIntegration) convertDocSymbol(r protocol.DocumentSymbolResult, depth int) []LSPSymbol {
	if depth > 6 {
		return nil
	}
	switch v := r.(type) {
	case *protocol.DocumentSymbol:
		sym := LSPSymbol{
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
			sym.Children = make([]LSPSymbol, 0, len(v.Children))
			for i := range v.Children {
				kids := l.convertDocSymbol(&v.Children[i], depth+1)
				sym.Children = append(sym.Children, kids...)
			}
		}
		return []LSPSymbol{sym}
	case *protocol.SymbolInformation:
		return []LSPSymbol{{
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

func (l *LSPIntegration) Diagnostics() []LSPDiagnostic {
	return nil // per-file diagnostics are not stored centrally; routed via callback
}

func (l *LSPIntegration) uriToPath(uri protocol.DocumentURI) string {
	path, err := uri.Path()
	if err != nil {
		return string(uri)
	}
	return filepath.Clean(path)
}

func (l *LSPIntegration) updateDiagnostics(launch *lspLaunch, uri protocol.DocumentURI, version *int32, diags []protocol.Diagnostic) {
	filePath := l.uriToPath(uri)
	l.mu.Lock()
	if launch == nil || l.launch != launch || l.client == nil {
		l.mu.Unlock()
		return
	}
	client := l.client
	state := l.openFiles[filePath]
	if state == nil || (version != nil && int(*version) != state.version) || l.sequence == math.MaxUint64 {
		l.mu.Unlock()
		return
	}
	publication := model.InsightDiagnosticPublication{
		Boundary: cloneBoundary(state.boundary), WireVersion: state.version,
		Sequence: l.sequence + 1,
	}
	if version != nil {
		reported := int(*version)
		publication.ReportedVersion = &reported
	}
	// Only one bounded transient publication per synchronized document is kept
	// for protocol waiters. Product fact identity and validity live upstream.
	bytes := 0
	for _, d := range diags {
		code := diagnosticCode(d.Code)
		size := len(d.Message) + len(d.Source) + len(code)
		if len(publication.Diagnostics) == maxTransportDiagnostics || size > maxTransportDiagnosticBytes-bytes {
			publication.Limited = true
			publication.Detail = "LSP transport publication exceeded 4096 diagnostics or 1 MiB of diagnostic text"
			break
		}
		bytes += size
		publication.Diagnostics = append(publication.Diagnostics, LSPDiagnostic{Line: int(d.Range.Start.Line), Col: int(d.Range.Start.Character), EndLine: int(d.Range.End.Line), EndCol: int(d.Range.End.Character), Message: d.Message, Severity: int(d.Severity), Source: d.Source, Code: code})
	}
	l.sequence = publication.Sequence
	state.publishedAt, state.publication = time.Now(), publication
	l.signalLocked()
	cb := l.onDiagnostics
	// The callback gets its own mutable containers, not the waiter's storage.
	publication = clonePublication(publication)
	l.mu.Unlock()
	if cb != nil {
		l.mu.Lock()
		current := l.launch == launch && l.client == client && l.openFiles[filePath] == state &&
			state.version == publication.WireVersion && equalBoundary(state.boundary, publication.Boundary) &&
			state.publication.Sequence == publication.Sequence
		l.mu.Unlock()
		if current {
			cb(publication)
		}
	}
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
	s.publishedAt, s.publication = time.Time{}, model.InsightDiagnosticPublication{}
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
func (l *LSPIntegration) AwaitDiagnostics(ctx context.Context, filePath string) (model.InsightDiagnosticPublication, bool) {
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
			return model.InsightDiagnosticPublication{}, false
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

func neutralTextEdit(edit protocol.TextEdit) model.LSPTextEdit {
	return model.LSPTextEdit{Line: int(edit.Range.Start.Line), Col: int(edit.Range.Start.Character), EndLine: int(edit.Range.End.Line), EndCol: int(edit.Range.End.Character), NewText: edit.NewText}
}

// expandSnippet supports numeric tab stops, defaults and choices as plain text.
// Variables/transforms are rejected rather than inserting executable-looking
// snippet syntax or guessing environment values. Repeated stops mirror defaults.
func expandSnippet(source string) (string, bool) {
	values := make(map[string]string)
	var parse func(string, int) (string, bool)
	parse = func(s string, depth int) (string, bool) {
		if depth > 16 {
			return "", false
		}
		var out strings.Builder
		for i := 0; i < len(s); {
			if s[i] == '\\' {
				if i+1 >= len(s) || !strings.ContainsRune(`\$},|`, rune(s[i+1])) {
					return "", false
				}
				out.WriteByte(s[i+1])
				i += 2
				continue
			}
			if s[i] != '$' {
				out.WriteByte(s[i])
				i++
				continue
			}
			i++
			braced := i < len(s) && s[i] == '{'
			if braced {
				i++
			}
			start := i
			for i < len(s) && s[i] >= '0' && s[i] <= '9' {
				i++
			}
			if start == i {
				return "", false
			}
			id := s[start:i]
			if _, err := strconv.Atoi(id); err != nil {
				return "", false
			}
			value := values[id]
			if braced {
				if i >= len(s) {
					return "", false
				}
				switch s[i] {
				case '}':
					i++
				case ':':
					i++
					start = i
					level := 1
					for i < len(s) && level > 0 {
						if s[i] == '\\' {
							i += 2
							continue
						}
						if s[i] == '{' {
							level++
						}
						if s[i] == '}' {
							level--
						}
						if level > 0 {
							i++
						}
					}
					if i >= len(s) {
						return "", false
					}
					var ok bool
					value, ok = parse(s[start:i], depth+1)
					if !ok {
						return "", false
					}
					i++
				case '|':
					i++
					var first strings.Builder
					chosen := false
					for i < len(s) && (s[i] != '|' || i+1 >= len(s) || s[i+1] != '}') {
						if s[i] == '\\' {
							if i+1 >= len(s) {
								return "", false
							}
							i++
							if !chosen {
								first.WriteByte(s[i])
							}
							i++
							continue
						}
						if s[i] == ',' {
							chosen = true
						} else if !chosen {
							first.WriteByte(s[i])
						}
						i++
					}
					if i+1 >= len(s) {
						return "", false
					}
					value = first.String()
					i += 2
				default:
					return "", false
				}
			}
			values[id] = value
			out.WriteString(value)
		}
		return out.String(), true
	}
	return parse(source, 0)
}

func (l *LSPIntegration) currentDocument(path string, client *lsp.Client, state *openFileState, version int) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.client == client && state != nil && l.openFiles[path] == state && state.version == version
}
