package viewmodel

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"crude/tui/rendering"
	"crude/tui/screens/activitybar"
	"crude/workspace"
)

// A remote layout load that fails returns no layout. Installing that zero value
// erased every rail destination on Windows when a stored layout was rejected.
func TestFailedToolLayoutLoadKeepsRailAndReportsError(t *testing.T) {
	app, ws := newRealShellApp(t)
	railPainted := func() bool {
		app.forceFrame()
		lines := strings.Split(rendering.StripAnsi(app.View().RenderContent()), "\n")
		for _, view := range activitybar.Views() {
			found := false
			for _, line := range lines {
				runes := []rune(line)
				rail := string(runes[:min(len(runes), 8)])
				if strings.Contains(rail, view.Icon(true)) || strings.Contains(rail, view.Icon(false)) {
					found = true
					break
				}
			}
			if !found {
				return false
			}
		}
		return true
	}
	if !railPainted() {
		t.Fatal("fixture rail did not paint every destination")
	}
	before := app.toolLayout
	app.Update(toolLayoutMsg{
		generation: app.workspaceSwitchGen.Load(), version: app.toolLayoutVersion,
		layout: workspace.ToolLayout{}, err: errors.New("invalid workspace navigation identity or order"),
	})
	if !railPainted() {
		t.Fatal("failed layout load erased the painted navigation rail")
	}
	if !reflect.DeepEqual(app.toolLayout, before) {
		t.Fatal("failed layout load replaced the current layout")
	}
	reported := false
	for _, record := range ws.Status.Notifications(false) {
		reported = reported || strings.Contains(record.Summary, "Workspace layout could not be loaded")
	}
	if !reported {
		t.Fatal("failed layout load was not reported")
	}
}
