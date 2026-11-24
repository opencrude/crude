package lspclient

import (
	"path/filepath"
	"strings"
	"time"

	"crude/internal/metrics"
)

// Language server series, keyed by "<request> <server>" so a slow server is
// named.
var (
	lspRequests = metrics.NewTable(metrics.Meta{Name: "lsp.requests", Group: metrics.GroupLSP, Title: "Requests by server",
		Help: "Language server request round trips (completion, definition, hover, ...).", Warn: 300_000, Crit: 2_000_000})
	lspStarts = metrics.NewTable(metrics.Meta{Name: "lsp.start", Group: metrics.GroupLSP, Title: "Server start",
		Help: "Launching and initializing a language server.", Warn: 3_000_000, Crit: 10_000_000})
	// Keep request and cancellation counts in the ordinary archive. A storm
	// of quick cancellations is invisible in a slowest-request latency table.
	documentPulls  = metrics.NewCounter(metrics.Meta{Name: "lsp.document_pulls", Group: metrics.GroupLSP, Title: "Document diagnostic requests", Unit: "requests", Help: "Attempts to get diagnostics for one open document."})
	cancelledPulls = metrics.NewCounter(metrics.Meta{Name: "lsp.document_pulls_cancelled", Group: metrics.GroupLSP, Title: "Cancelled diagnostic requests", Unit: "requests", Help: "Document diagnostic requests cancelled by the client or server. Compare with total requests during the same period."})
	lspDiagnostics = metrics.NewCounter(metrics.Meta{Name: "lsp.diagnostics", Group: metrics.GroupLSP, Title: "Diagnostic publications",
		Help: "Diagnostics batches published by language servers."})
)

func (l *LSPIntegration) serverName() string {
	name := filepath.Base(l.serverCmd)
	return strings.TrimSuffix(name, filepath.Ext(name))
}

func (l *LSPIntegration) observeRequest(kind string, start time.Time) {
	lspRequests.Since(kind+" · "+l.serverName(), start)
}
