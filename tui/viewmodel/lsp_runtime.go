package viewmodel

import (
	"context"

	"crude/tui/editor"
	model "crude/types"
	"crude/workspace"
)

// editorLSPRuntime is the composition-boundary adapter between the workspace
// LSP service and the specialized editor integration. The workspace never
// imports this package or the editor implementation.
type editorLSPRuntime struct{}

type editorLSPClient struct {
	client *editor.LSPIntegration
}

func newEditorLSPRuntime() workspace.LSPRuntime {
	return editorLSPRuntime{}
}

func (editorLSPRuntime) NewClient(spec model.LSPServerSpec, rootURI string) workspace.LSPClient {
	return &editorLSPClient{client: editor.NewLSPIntegration(spec.Command, spec.Args, rootURI,
		editor.WithServerConfiguration(spec.Environment, spec.Settings, spec.InitializationOptions))}
}

func (c *editorLSPClient) OnDiagnostics(fn func(string, []workspace.LSPDiagnostic)) {
	c.client.OnDiagnostics(fn)
}

func (c *editorLSPClient) Start(ctx context.Context) error { return c.client.Start(ctx) }
func (c *editorLSPClient) Stop()                           { c.client.Stop() }
func (c *editorLSPClient) Done() <-chan struct{}           { return c.client.Done() }
func (c *editorLSPClient) OpenFile(ctx context.Context, path, language, text string) {
	c.client.OpenFile(ctx, path, language, text)
}
func (c *editorLSPClient) UpdateFile(ctx context.Context, path, text string) {
	c.client.UpdateFile(ctx, path, text)
}
func (c *editorLSPClient) CloseFile(ctx context.Context, path string) {
	c.client.CloseFile(ctx, path)
}

func (c *editorLSPClient) NotifySave(ctx context.Context, path, text string, reason int, before bool) {
	c.client.NotifySave(ctx, path, text, reason, before)
}
func (c *editorLSPClient) NotifyWatchedFilesChanged(ctx context.Context, path string, changeType int) {
	c.client.NotifyWatchedFilesChanged(ctx, path, changeType)
}
func (c *editorLSPClient) RequestDefinition(ctx context.Context, path string, line, col int) []workspace.LSPLocation {
	return c.client.RequestDefinition(ctx, path, line, col)
}
func (c *editorLSPClient) FindReferences(ctx context.Context, path string, line, col int, includeDeclaration bool) []workspace.LSPLocation {
	return c.client.FindReferences(ctx, path, line, col, includeDeclaration)
}
func (c *editorLSPClient) RequestDocumentSymbols(ctx context.Context, path string) []workspace.LSPSymbol {
	return c.client.RequestDocumentSymbols(ctx, path)
}
func (c *editorLSPClient) RequestHover(ctx context.Context, path string, line, col int) (string, error) {
	return c.client.RequestHover(ctx, path, line, col)
}
func (c *editorLSPClient) RequestRename(ctx context.Context, path string, line, col int, name string) (interface{}, error) {
	return c.client.RequestRename(ctx, path, line, col, name)
}
func (c *editorLSPClient) RequestCompletions(ctx context.Context, path string, line, col int) []workspace.CompletionItem {
	return c.client.RequestCompletions(ctx, path, line, col)
}
