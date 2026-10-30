package workspace

import (
	"context"
	"crude/types"
	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol"
)

// reapableClient is an LSPClient fake whose Done channel the test controls,
// letting the reaper test exercise the restart-on-death lifecycle.
type reapableClient struct {
	done chan struct{}
}

func (c *reapableClient) OnDiagnostics(func(types.InsightDiagnosticPublication)) {}
func (c *reapableClient) Start(context.Context) error                            { return nil }
func (c *reapableClient) Stop() error                                            { return nil }
func (c *reapableClient) Done() <-chan struct{}                                  { return c.done }
func (c *reapableClient) OpenFile(context.Context, string, string, string, types.InsightBoundary) (int, error) {
	return 1, nil
}
func (c *reapableClient) UpdateFile(context.Context, string, string, types.InsightBoundary) (int, error) {
	return 2, nil
}
func (c *reapableClient) CloseFile(context.Context, string)                     {}
func (c *reapableClient) NotifySave(context.Context, string, string, int, bool) {}
func (c *reapableClient) NotifyWatchedFilesChanged(context.Context, string, int) {
}
func (c *reapableClient) RequestDefinition(context.Context, string, int, int) []LSPLocation {
	return nil
}
func (c *reapableClient) FindReferences(context.Context, string, int, int, bool) []LSPLocation {
	return nil
}
func (c *reapableClient) RequestDocumentSymbols(context.Context, string) []LSPSymbol {
	return nil
}
func (c *reapableClient) RequestHover(context.Context, string, int, int) (string, error) {
	return "", nil
}
func (c *reapableClient) RequestRename(context.Context, string, int, int, string) (*protocol.WorkspaceEdit, error) {
	return nil, nil
}
func (c *reapableClient) RequestCompletions(context.Context, string, int, int) []CompletionItem {
	return nil
}

func (*reapableClient) AwaitDiagnostics(context.Context, string) (types.InsightDiagnosticPublication, bool) {
	return types.InsightDiagnosticPublication{}, true
}
