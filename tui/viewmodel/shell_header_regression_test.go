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
	app.paintShellFrame()

	header := shellBufferRow(app, app.frameTopRow())
	for _, want := range []string{"Crude"} {
		if !strings.Contains(header, want) {
			t.Fatalf("global header row missing %q: %q", want, header)
		}
	}
	for _, unwanted := range []string{"main-model", "Ready", "Help"} {
		if strings.Contains(header, unwanted) {
			t.Fatalf("launcher header repeats irrelevant %q: %q", unwanted, header)
		}
	}
	if got := app.shell.slots[shellActivitySlot].rect.Y; got != app.shellTopRow() {
		t.Fatalf("activity rail starts at row %d, want %d below global header", got, app.shellTopRow())
	}
	if got := app.shell.slots[shellSidebarSlot].rect.Y; got != app.shellTopRow() {
		t.Fatalf("sidebar starts at row %d, want %d below global header", got, app.shellTopRow())
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
