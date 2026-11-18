package lspclient

import (
	"path/filepath"
	"strings"
	"time"

	"crude/metrics"
)

// Language server series, keyed by "<request> <server>" so a slow server is
// named.
var (
	lspRequests = metrics.NewTable(metrics.Meta{Name: "lsp.requests", Group: metrics.GroupLSP, Title: "Requests by server",
		Help: "Language server request round trips (completion, definition, hover, ...).", Warn: 300_000, Crit: 2_000_000})
	lspStarts = metrics.NewTable(metrics.Meta{Name: "lsp.start", Group: metrics.GroupLSP, Title: "Server start",
		Help: "Launching and initializing a language server.", Warn: 3_000_000, Crit: 10_000_000})
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
