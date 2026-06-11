package editor

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/charmbracelet/x/powernap/pkg/lsp"
	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol"
)

type LSPDiagnostic struct {
	Line      int
	Col       int
	Message   string
	Severity  int
	EndLine   int
	EndCol    int
}

type LSPIntegration struct {
	client     *lsp.Client
	mu         sync.Mutex
	diagnostics []LSPDiagnostic
	fileURI    string
	serverCmd  string
	serverArgs []string
	rootURI    string
	version    int
	onDiagnostics func(diags []LSPDiagnostic)
}

func NewLSPIntegration(serverCmd string, serverArgs []string, rootURI string) *LSPIntegration {
	return &LSPIntegration{
		serverCmd:  serverCmd,
		serverArgs: serverArgs,
		rootURI:    rootURI,
	}
}

func (l *LSPIntegration) Start(ctx context.Context) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.client != nil {
		return nil
	}

	client, err := lsp.NewClient(lsp.ClientConfig{
		Command: l.serverCmd,
		Args:    l.serverArgs,
		RootURI: l.rootURI,
	})
	if err != nil {
		return err
	}

	client.RegisterNotificationHandler("textDocument/publishDiagnostics", func(_ context.Context, _ string, params json.RawMessage) {
		var diagParams protocol.PublishDiagnosticsParams
		if err := json.Unmarshal(params, &diagParams); err != nil {
			return
		}
		l.updateDiagnostics(diagParams.Diagnostics)
	})

	initCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	if err := client.Initialize(initCtx, false); err != nil {
		client.Kill()
		return err
	}

	l.client = client
	return nil
}

func (l *LSPIntegration) OpenFile(ctx context.Context, filePath, languageID, text string) {
	l.mu.Lock()
	l.fileURI = string(protocol.URIFromPath(filePath))
	l.version = 1
	l.mu.Unlock()

	if l.client == nil {
		return
	}

	_ = l.client.NotifyDidOpenTextDocument(ctx, l.fileURI, languageID, l.version, text)
}

func (l *LSPIntegration) UpdateFile(ctx context.Context, text string) {
	l.mu.Lock()
	l.version++
	uri := l.fileURI
	l.mu.Unlock()

	if l.client == nil || uri == "" {
		return
	}

	_ = l.client.NotifyDidChangeTextDocument(ctx, uri, l.version, []protocol.TextDocumentContentChangeEvent{
		{Value: protocol.TextDocumentContentChangeWholeDocument{Text: text}},
	})
}

func (l *LSPIntegration) CloseFile(ctx context.Context) {
	l.mu.Lock()
	uri := l.fileURI
	l.fileURI = ""
	l.mu.Unlock()

	if l.client == nil || uri == "" {
		return
	}

	_ = l.client.NotifyDidCloseTextDocument(ctx, uri)
}

func (l *LSPIntegration) RequestCompletions(ctx context.Context, line, col int) []protocol.CompletionItem {
	if l.client == nil {
		return nil
	}

	l.mu.Lock()
	uri := l.fileURI
	l.mu.Unlock()

	if uri == "" {
		return nil
	}

	result, err := l.client.RequestCompletion(ctx, uri, protocol.Position{
		Line:      uint32(line),
		Character: uint32(col),
	})
	if err != nil || result == nil {
		return nil
	}
	return result.Items
}

func (l *LSPIntegration) Diagnostics() []LSPDiagnostic {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]LSPDiagnostic, len(l.diagnostics))
	copy(out, l.diagnostics)
	return out
}

func (l *LSPIntegration) Stop() {
	l.mu.Lock()
	client := l.client
	l.client = nil
	l.mu.Unlock()

	if client != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = client.Shutdown(ctx)
		client.Exit()
		client.Kill()
	}
}

func (l *LSPIntegration) updateDiagnostics(diags []protocol.Diagnostic) {
	l.mu.Lock()
	l.diagnostics = l.diagnostics[:0]
	for _, d := range diags {
		l.diagnostics = append(l.diagnostics, LSPDiagnostic{
			Line:     int(d.Range.Start.Line),
			Col:      int(d.Range.Start.Character),
			EndLine:  int(d.Range.End.Line),
			EndCol:   int(d.Range.End.Character),
			Message:  d.Message,
			Severity: int(d.Severity),
		})
	}
	out := make([]LSPDiagnostic, len(l.diagnostics))
	copy(out, l.diagnostics)
	l.mu.Unlock()

	if l.onDiagnostics != nil {
		l.onDiagnostics(out)
	}
}
