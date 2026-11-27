// Command crude starts the Crude IDE: it parses the command line, prepares the
// console and hands control to the app package.
package main

import (
	"io"
	"log/slog"
	"net/http"
	_ "net/http/pprof" // registers handlers on http.DefaultServeMux
	"os"

	"crude/app"
	"crude/internal/clipboard"
	"crude/internal/console"
)

func main() {
	if code, ok := clipboard.RunHelper(os.Args[1:]); ok {
		os.Exit(code)
	}
	// Suppress slog globally: powernap uses slog.Default() which writes to
	// os.Stderr and would corrupt the Bubble Tea TUI.
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))

	// CRUDE_PPROF=":6060" serves runtime/pprof on localhost for live profiling.
	if addr := os.Getenv("CRUDE_PPROF"); addr != "" {
		go func() {
			_ = http.ListenAndServe(addr, nil)
		}()
	}

	// Protocol/core routes may share a Windows console with their caller. Do
	// not change that console's code pages for a noninteractive subprocess.
	if app.NeedsInteractiveConsole(os.Args[1:]) {
		console.Init()
	}
	os.Exit(app.Main())
}
