package workspace

import (
	"context"
	"net/url"
	"path/filepath"
	"strings"

	model "crude/types"
)

type LSPStatus = model.LSPStatus

const (
	LSPIdle      = model.LSPIdle
	LSPStarting  = model.LSPStarting
	LSPConnected = model.LSPConnected
	LSPFailed    = model.LSPFailed
	DiagError    = model.DiagError
	DiagWarning  = model.DiagWarning
	DiagInfo     = model.DiagInfo
	DiagHint     = model.DiagHint
)

type LSPDiagnostic = model.LSPDiagnostic
type LSPSymbol = model.LSPSymbol
type CompletionItem = model.CompletionItem
type ProjectInsights = model.ProjectInsights

// LSPClient is the workspace-facing connection contract. The concrete
// subprocess transport is supplied by the workspace runtime.
type LSPClient interface {
	OnDiagnostics(func(model.InsightDiagnosticPublication))
	Start(context.Context) error
	// Stop joins the owned process lifetime. A non-nil result means retirement
	// could not be verified; its admission slot must not be reused.
	Stop() error
	// Done is closed when the underlying server connection dies (server
	// exit, pipe error, or a wedged server that hit the write-queue cap).
	// The manager reaps the client on Done and restarts it on demand.
	Done() <-chan struct{}
	OpenFile(context.Context, string, string, string, model.InsightBoundary) (int, error)
	UpdateFile(context.Context, string, string, model.InsightBoundary) (int, error)
	CloseFile(context.Context, string)
	NotifySave(context.Context, string, string, int, bool)
	NotifyWatchedFilesChanged(context.Context, string, int)
	RequestNavigation(context.Context, string, string, int, int, bool) (model.LSPNavigationResponse, error)
	RequestDocumentSymbols(context.Context, string) []LSPSymbol
	RequestHover(context.Context, string, int, int) (string, error)
	RequestSignatureHelp(context.Context, string, int, int) (model.LSPSignatureHelp, error)
	RequestRename(context.Context, string, int, int, string) (model.LSPRenameEdit, error)
	RequestCodeActions(context.Context, string, int, int, int, int) (model.LSPCodeActionResponse, error)
	ResolveCodeAction(context.Context, string, int, []byte) (model.LSPCodeActionResponse, error)
	RequestCompletions(context.Context, string, int, int) []CompletionItem
	// AwaitDiagnostics returns the open document's diagnostics once the
	// server has settled, or the latest seen and false when ctx ends first.
	AwaitDiagnostics(context.Context, string) (model.InsightDiagnosticPublication, bool)
}

// LSPRuntime creates transport clients; language and project policy stay in the workspace.
type LSPRuntime interface {
	NewClient(spec model.LSPServerSpec, rootURI string, startAdmission func() error) LSPClient
}

func fileURI(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	slashPath := filepath.ToSlash(abs)
	if filepath.VolumeName(abs) != "" {
		slashPath = "/" + slashPath
	}
	return (&url.URL{Scheme: "file", Path: slashPath}).String()
}

func filePathFromURI(uri string) string {
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "file" {
		return filepath.Clean(strings.TrimPrefix(uri, "file://"))
	}
	path := u.Path
	if u.Host != "" && !strings.EqualFold(u.Host, "localhost") {
		path = "//" + u.Host + path
	}
	if trimmed := strings.TrimPrefix(path, "/"); filepath.VolumeName(trimmed) != "" {
		path = trimmed
	}
	return filepath.Clean(filepath.FromSlash(path))
}
