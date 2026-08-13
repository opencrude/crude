package viewmodel

import (
	"strings"
	"testing"
	"time"
)

func TestGlobalHeaderRemainsAboveActivitySidebar(t *testing.T) {
	app := newFullTestApp(t)
	app.ModelName = "main-model"
	app.StartTime = time.Now().Add(-2 * time.Minute)
	app.StatusBar.UpdateUsage(1200, 345, 200, 0.42, 8192, 4096, false)

	app.paintShellFrame()

	header := shellBufferRow(app, 1)
	for _, want := range []string{"crude", "main-model", "↑1.2K", "↓345", "$0.4200"} {
		if !strings.Contains(header, want) {
			t.Fatalf("global header row missing %q: %q", want, header)
		}
	}
	if got := app.shell.slots[shellActivitySlot].rect.Y; got != 3 {
		t.Fatalf("activity rail starts at row %d, want 3 below global header", got)
	}
	if got := app.shell.slots[shellSidebarSlot].rect.Y; got != 3 {
		t.Fatalf("sidebar starts at row %d, want 3 below global header", got)
	}
}

func shellBufferRow(app *App, y int) string {
	if app == nil || app.mainSurf == nil || y < 0 || y >= app.mainSurf.Height() {
		return ""
	}
	buf := app.mainSurf.Buffer()
	var out strings.Builder
	for x := 0; x < buf.Width(); x++ {
		c := buf.Get(x, y)
		if c.Width > 0 && c.Char != 0 {
			out.WriteRune(c.Char)
		}
	}
	return out.String()
}
