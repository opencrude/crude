package workspace

import (
	"context"
	"crude/types/insight"
)

// reapableClient is an LSPClient fake whose Done channel the test controls,
// letting the reaper test exercise the restart-on-death lifecycle.
type reapableClient struct {
	done chan struct{}
}

func (c *reapableClient) OnDiagnostics(func(insight.DiagnosticPublication)) {}
func (c *reapableClient) Start(context.Context) error                       { return nil }
func (c *reapableClient) Stop() error                                       { return nil }
func (c *reapableClient) Done() <-chan struct{}                             { return c.done }
func (c *reapableClient) OpenFile(context.Context, string, string, string, insight.Boundary) (int, error) {
	return 1, nil
}
func (c *reapableClient) UpdateFile(context.Context, string, string, insight.Boundary) (int, error) {
	return 2, nil
}
func (c *reapableClient) CloseFile(context.Context, string)                     {}
func (c *reapableClient) NotifySave(context.Context, string, string, int, bool) {}
func (c *reapableClient) NotifyWatchedFilesChanged(context.Context, string, int) {
}
func (c *reapableClient) RequestNavigation(context.Context, string, string, int, int, bool) (insight.LSPNavigationResponse, error) {
	return insight.LSPNavigationResponse{}, context.Canceled
}
func (c *reapableClient) RequestDocumentSymbols(context.Context, string) []insight.LSPSymbol {
	return nil
}
func (c *reapableClient) RequestHover(context.Context, string, int, int) (string, error) {
	return "", nil
}
func (c *reapableClient) RequestSignatureHelp(context.Context, string, int, int, insight.LSPSignatureContext) (insight.LSPSignatureHelp, error) {
	return insight.LSPSignatureHelp{}, nil
}
func (c *reapableClient) RequestRename(context.Context, string, int, int, string) (insight.LSPRenameEdit, error) {
	return insight.LSPRenameEdit{}, nil
}
func (c *reapableClient) RequestCodeActions(context.Context, string, int, int, int, int) (insight.LSPCodeActionResponse, error) {
	return insight.LSPCodeActionResponse{}, context.Canceled
}
func (c *reapableClient) ResolveCodeAction(context.Context, string, int, []byte) (insight.LSPCodeActionResponse, error) {
	return insight.LSPCodeActionResponse{}, context.Canceled
}
func (c *reapableClient) RequestInlayHints(context.Context, string, insight.LSPInlayRange) (insight.LSPInlayResponse, error) {
	return insight.LSPInlayResponse{}, context.Canceled
}
func (c *reapableClient) ResolveInlayHint(context.Context, string, int, []byte) (insight.LSPInlayResponse, error) {
	return insight.LSPInlayResponse{}, context.Canceled
}
func (c *reapableClient) RequestCompletions(context.Context, string, int, int, insight.LSPCompletionContext) (insight.LSPCompletionResponse, error) {
	return insight.LSPCompletionResponse{}, context.Canceled
}
func (c *reapableClient) ResolveCompletion(context.Context, string, int, []byte) (insight.LSPCompletionResponse, error) {
	return insight.LSPCompletionResponse{}, context.Canceled
}

func (*reapableClient) AwaitDiagnostics(context.Context, string) (insight.DiagnosticPublication, bool) {
	return insight.DiagnosticPublication{}, true
}

func (*reapableClient) AssistanceCapabilities(string) insight.LSPAssistanceCapabilities {
	return insight.LSPAssistanceCapabilities{Completion: true, CompletionTriggers: []string{"."}, SignatureHelp: true, SignatureTriggers: []string{"(", ","}}
}
