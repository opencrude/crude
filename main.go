package main

import (
	"io"
	"log/slog"
	"net/http"
	_ "net/http/pprof" // registers handlers on http.DefaultServeMux
	"os"

	"crude/app"
	"crude/tui/platform"
)

func main() {
	// Suppress slog globally: powernap uses slog.Default() which writes to
	// os.Stderr and would corrupt the Bubble Tea TUI.
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))

	// CRUDE_PPROF=":6060" serves runtime/pprof on localhost for live profiling.
	if addr := os.Getenv("CRUDE_PPROF"); addr != "" {
		go func() {
			_ = http.ListenAndServe(addr, nil)
		}()
	}

	platform.InitConsole()
	os.Exit(app.Main())
}
