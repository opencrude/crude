package main

import (
	"io"
	"log/slog"
	"os"

	"crude/app"
	"crude/tui/platform"
)

func main() {
	// Suppress slog globally: powernap uses slog.Default() which writes to
	// os.Stderr and would corrupt the Bubble Tea TUI.
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))

	platform.InitConsole()
	os.Exit(app.Main())
}
