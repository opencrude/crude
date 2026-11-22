package workspace

import (
	"context"
	"encoding/json"

	"crude/types/insight"
	"crude/types/setting"
)

const (
	// LSPIdle means the language server has not started yet.
	LSPIdle = insight.LSPIdle
	// LSPStarting means the language server is starting.
	LSPStarting = insight.LSPStarting
	// LSPConnected means the language server is ready.
	LSPConnected = insight.LSPConnected
	// LSPFailed means the language server did not start.
	LSPFailed = insight.LSPFailed
	// DiagError is the error severity of a diagnostic.
	DiagError = insight.DiagError
	// DiagWarning is the warning severity of a diagnostic.
	DiagWarning = insight.DiagWarning
	// DiagInfo is the information severity of a diagnostic.
	DiagInfo = insight.DiagInfo
	// DiagHint is the hint severity of a diagnostic.
	DiagHint = insight.DiagHint
)

// LSPClient is the workspace-facing connection contract. The concrete
// subprocess transport is supplied by the workspace runtime.
type LSPClient interface {
	OnDiagnostics(func(insight.DiagnosticPublication))
	Start(context.Context) error
	AssistanceCapabilities() insight.LSPAssistanceCapabilities
	// Stop joins the owned process lifetime. A non-nil result means retirement
	// could not be verified; its admission slot must not be reused.
	Stop() error
	// Done is closed when the underlying server connection dies (server
	// exit, pipe error, or a wedged server that hit the write-queue cap).
	// The manager reaps the client on Done and restarts it on demand.
	Done() <-chan struct{}
	OpenFile(context.Context, string, string, string, insight.Boundary) (int, error)
	UpdateFile(context.Context, string, string, insight.Boundary) (int, error)
	CloseFile(context.Context, string)
	NotifySave(context.Context, string, string, int, bool)
	NotifyWatchedFilesChanged(context.Context, string, int)
	RequestNavigation(context.Context, string, string, int, int, bool) (insight.LSPNavigationResponse, error)
	RequestDocumentSymbols(context.Context, string) []insight.LSPSymbol
	RequestHover(context.Context, string, int, int) (string, error)
	RequestSignatureHelp(context.Context, string, int, int, insight.LSPSignatureContext) (insight.LSPSignatureHelp, error)
	RequestRename(context.Context, string, int, int, string) (insight.LSPRenameEdit, error)
	RequestCodeActions(context.Context, string, int, int, int, int) (insight.LSPCodeActionResponse, error)
	ResolveCodeAction(context.Context, string, int, []byte) (insight.LSPCodeActionResponse, error)
	RequestInlayHints(context.Context, string, insight.LSPInlayRange) (insight.LSPInlayResponse, error)
	ResolveInlayHint(context.Context, string, int, []byte) (insight.LSPInlayResponse, error)
	RequestCompletions(context.Context, string, int, int, insight.LSPCompletionContext) (insight.LSPCompletionResponse, error)
	ResolveCompletion(context.Context, string, int, []byte) (insight.LSPCompletionResponse, error)
	// AwaitDiagnostics returns the open document's diagnostics once the
	// server has settled, or the latest seen and false when ctx ends first.
	AwaitDiagnostics(context.Context, string) (insight.DiagnosticPublication, bool)
}

// LSPCommandClient is the optional command capability of a transport client.
// Read-only and test clients need not implement it.
type LSPCommandClient interface {
	ExecuteCommand(context.Context, string, int, string, json.RawMessage) (insight.LSPCommandResponse, error)
}

// LSPRuntime creates transport clients; language and project policy stay in the workspace.
type LSPRuntime interface {
	NewClient(spec setting.LSPServerSpec, rootURI string, startAdmission func() error) LSPClient
}

// LSPEvidenceClient exposes a bounded launch-owned tail. Data from a server is
// diagnostic evidence, not an instruction. Clients without a subprocess may
// omit this optional observation capability.
type LSPEvidenceClient interface {
	Evidence() []string
	ErrorMessage() string
}
