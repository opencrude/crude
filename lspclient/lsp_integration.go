package lspclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	launch      *lspLaunch
	rootURI     string

	// pipe monitors the subprocess stream. All writes are decoupled from
	// caller goroutines through the async writer (see lsp_pipe.go); Done
	// signals connection death (server exit, pipe error, write-queue cap).
	pipe *lspPipeMonitor

	openFiles      map[string]*openFileState // filePath -> state
	versionCounter int

	// progress holds the server's active work-done tokens; changed is closed
	// and replaced whenever progress or a document's diagnostics change.
	progress map[string]bool
	changed  chan struct{}

	onDiagnostics func(filePath string, diags []LSPDiagnostic)
}

// LSPIntegrationOption configures a new LSPIntegration.
type LSPIntegrationOption func(*LSPIntegration)

// WithServerEnv adds environment variables to the language server
// subprocess (merged over the inherited environment).
func WithServerEnv(env map[string]string) LSPIntegrationOption {
	return func(l *LSPIntegration) {
		l.serverEnv = env
	}
}

type lspLaunch struct {
	cancel context.CancelFunc
	done   chan struct{}
	client *lsp.Client
	err    error
}

// WithServerConfiguration configures the subprocess and server-specific protocol settings.
func WithServerConfiguration(environment map[string]string, settings, initOptions map[string]any) LSPIntegrationOption {
	return func(l *LSPIntegration) {
		l.serverEnv = environment
		l.settings = settings
		l.initOptions = initOptions
	}
}

type openFileState struct {
	version    int
	uri        string
	languageID string
	text       string
	// syncedAt is when this version was sent; publishedAt and diagnostics
	// record the latest publication for exactly this version.
	syncedAt    time.Time
	publishedAt time.Time
	diagnostics []LSPDiagnostic
}

// OnDiagnostics sets the callback for receiving diagnostics for open files.
func (l *LSPIntegration) OnDiagnostics(fn func(filePath string, diags []LSPDiagnostic)) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.onDiagnostics = fn
}

func NewLSPIntegration(serverCmd string, serverArgs []string, rootURI string, opts ...LSPIntegrationOption) *LSPIntegration {
	l := &LSPIntegration{
		serverCmd:  serverCmd,
		serverArgs: serverArgs,
		rootURI:    rootURI,
		openFiles:  make(map[string]*openFileState),
		progress:   make(map[string]bool),
		changed:    make(chan struct{}),
	}
	for _, opt := range opts {
		opt(l)
	}
	return l
}

func (l *LSPIntegration) RootURI() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.rootURI
}

func (l *LSPIntegration) Start(ctx context.Context) error {
	l.mu.Lock()
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
	client, err := lsp.NewClient(lsp.ClientConfig{
		Command: l.serverCmd, Args: l.serverArgs, RootURI: l.rootURI,
		Environment: l.serverEnv, Settings: l.settings, InitOptions: l.initOptions,
		TransportWrap: func(reader io.Reader, writer io.Writer) (io.Reader, io.Writer) {
			nr, nw, mon := wrapLSPPipe(reader, writer)
			pipe = mon
			return nr, nw
		},
	})
	if err == nil {
		l.mu.Lock()
		launch.client = client
		stopped := l.launch != launch
		l.mu.Unlock()
		if stopped {
			client.Kill()
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
			if err != nil {
				client.Kill()
			}
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
	launch.err = err
	if err != nil && l.launch == launch {
		l.launch = nil
	}
	l.mu.Unlock()
	if err != nil && client != nil {
		client.Kill()
	}
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

func (l *LSPIntegration) OpenFile(ctx context.Context, filePath, languageID, text string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.client == nil {
		return
	}
	fp := filepath.Clean(filePath)
	uri := string(protocol.URIFromPath(fp))
	version := l.versionCounter + 1
	if state := l.openFiles[fp]; state != nil {
		if state.text == text {
			return
		}
		if l.client.NotifyDidChangeTextDocument(ctx, state.uri, version, []protocol.TextDocumentContentChangeEvent{{Value: protocol.TextDocumentContentChangeWholeDocument{Text: text}}}) == nil {
			state.text = text
			state.resetVersion(version)
			l.versionCounter = version
			l.signalLocked()
		}
		return
	}
	if l.client.NotifyDidOpenTextDocument(ctx, uri, languageID, version, text) == nil {
		l.versionCounter = version
		state := &openFileState{uri: uri, languageID: languageID, text: text}
		state.resetVersion(version)
		l.openFiles[fp] = state
		l.signalLocked()
	}
}

func (l *LSPIntegration) UpdateFile(ctx context.Context, filePath, text string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	fp := filepath.Clean(filePath)
	state, ok := l.openFiles[fp]
	if l.client == nil || !ok || state.text == text {
		return
	}
	version := l.versionCounter + 1
	uri := state.uri

	if err := l.client.NotifyDidChangeTextDocument(ctx, uri, version, []protocol.TextDocumentContentChangeEvent{
		{Value: protocol.TextDocumentContentChangeWholeDocument{Text: text}},
	}); err == nil {
		state.text = text
		state.resetVersion(version)
		l.versionCounter = version
		l.signalLocked()
	}
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

func (l *LSPIntegration) Stop() {
	l.mu.Lock()
	launch := l.launch
	l.launch = nil
	client := l.client
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
	if client != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
		if client.Shutdown(ctx) == nil {
			_ = client.Exit()
		}
		cancel()
		client.Kill()
	} else if starting != nil {
		starting.Kill()
	}
	if launch != nil {
		<-launch.done
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
	if l.launch != launch {
		l.mu.Unlock()
		return
	}
	state := l.openFiles[filePath]
	if state == nil || (version != nil && int(*version) != state.version) {
		l.mu.Unlock()
		return
	}
	out := make([]LSPDiagnostic, 0, len(diags))
	for _, d := range diags {
		out = append(out, LSPDiagnostic{Line: int(d.Range.Start.Line), Col: int(d.Range.Start.Character), EndLine: int(d.Range.End.Line), EndCol: int(d.Range.End.Character), Message: d.Message, Severity: int(d.Severity), Source: d.Source, Code: diagnosticCode(d.Code)})
	}
	state.publishedAt, state.diagnostics = time.Now(), out
	l.signalLocked()
	cb := l.onDiagnostics
	l.mu.Unlock()
	if cb != nil {
		l.mu.Lock()
		current := l.launch == launch && l.openFiles[filePath] == state && (version == nil || state.version == int(*version))
		l.mu.Unlock()
		if current {
			cb(filePath, out)
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
	s.publishedAt, s.diagnostics = time.Time{}, nil
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

// AwaitDiagnostics waits until the server has published diagnostics for the
// document's current version, reports no work in progress, and has stayed
// quiet for as long as it took to answer. It returns the published
// diagnostics and whether they settled before ctx ended; an unsettled result
// may be partial and must be presented as such.
func (l *LSPIntegration) AwaitDiagnostics(ctx context.Context, filePath string) ([]LSPDiagnostic, bool) {
	fp := filepath.Clean(filePath)
	for {
		l.mu.Lock()
		state := l.openFiles[fp]
		if state == nil || l.client == nil {
			l.mu.Unlock()
			return nil, false
		}
		published, synced, busy, changed := state.publishedAt, state.syncedAt, len(l.progress) > 0, l.changed
		diagnostics := append([]LSPDiagnostic(nil), state.diagnostics...)
		l.mu.Unlock()
		var timer *time.Timer
		var fired <-chan time.Time
		if !published.IsZero() && !busy {
			quiet := min(max(published.Sub(synced), minDiagnosticsQuiet), maxDiagnosticsQuiet)
			remaining := quiet - time.Since(published)
			if remaining <= 0 {
				return diagnostics, true
			}
			timer = time.NewTimer(remaining)
			fired = timer.C
		}
		select {
		case <-ctx.Done():
		case <-changed:
		case <-fired:
		}
		if timer != nil {
			timer.Stop()
		}
		if ctx.Err() != nil {
			return diagnostics, false
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
