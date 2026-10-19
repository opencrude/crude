package viewmodel

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"crude/tui/screens/diffview"
	"crude/tui/screens/gitdiff"
	"crude/workspace"
)

func calibratedProfile(t *testing.T, width int) {
	t.Helper()
	ansi.SetTerminalWidthProfile(ansi.TerminalWidthProfile{Exact: map[string]int{"☰": width}})
	t.Cleanup(func() { ansi.SetTerminalWidthProfile(ansi.TerminalWidthProfile{}) })
}

func TestComparisonGeometryAppCalibrationAndInFlightPreparation(t *testing.T) {
	for _, preparing := range []bool{false, true} {
		t.Run(map[bool]string{false: "retained", true: "in-flight"}[preparing], func(t *testing.T) {
			app, ws := newRealShellApp(t)
			defer ws.Close()
			paneJourneySize(app, 160, 40)
			openEditorTabs(t, app, "geometry.txt")
			ed := app.EditorState.Editor
			ed.ReplaceDocument(strings.Repeat("☰", 4096) + " tail-marker\r\n")
			prepared := app.openLocalDiff(ed, localDiffSaved)().(localDiffPreparedMsg)
			view := app.activeLocalDiff()
			if !preparing {
				app.applyLocalDiffPrepared(prepared)
			}
			calibratedProfile(t, 1)
			app.applyTerminalWidthProfile(tea.TerminalWidthProfileMsg{Measured: 1, Changed: true})
			if preparing {
				// Finish the calibration's initial pass before old-width preparation
				// arrives. Its width generation must schedule another owned reflow.
				app.applyComparisonGeometry(app.drainComparisonGeometry()().(comparisonGeometryMsg))
				app.applyLocalDiffPrepared(prepared)
			}
			command := app.drainComparisonGeometry()
			if command == nil {
				t.Fatal("calibration did not schedule source reflow")
			}
			app.paintShellFrame()
			if text := bufferPlain(app.mainSurf.Buffer()); !strings.Contains(text, "Calibrating comparison") || strings.Contains(text, "☰") {
				t.Fatalf("mixed-width source painted during reflow:\n%s", text)
			}
			app.Update(command())
			sources := view.screen.ComparisonReader().SourceGeometry()
			if sources[1].Width() != 4108 || sources[1].GeometryStatus() != "" {
				t.Fatalf("calibrated geometry width=%d status=%q", sources[1].Width(), sources[1].GeometryStatus())
			}
			view.screen.ComparisonReader().SetQuery("tail-marker")
			// Search uses the same real result ingress as normal app operation.
			app.Update(view.screen.ComparisonReader().SearchCommand()())
			app.paintShellFrame()
			if text := bufferPlain(app.mainSurf.Buffer()); !strings.Contains(text, "tail-marker") || strings.Contains(text, "Calibrating") {
				t.Fatalf("calibrated source Find did not paint:\n%s", text)
			}
		})
	}
}

func TestComparisonGeometryRejectsClosedReplacedAndWorkspaceResults(t *testing.T) {
	for _, boundary := range []string{"close", "close-queued", "replace", "workspace", "shutdown"} {
		t.Run(boundary, func(t *testing.T) {
			app, ws := newRealShellApp(t)
			defer ws.Close()
			paneJourneySize(app, 160, 40)
			openEditorTabs(t, app, "geometry.txt")
			ed := app.EditorState.Editor
			ed.ReplaceDocument(strings.Repeat("☰", 4096))
			app.applyLocalDiffPrepared(app.openLocalDiff(ed, localDiffSaved)().(localDiffPreparedMsg))
			view := app.activeLocalDiff()
			source := view.screen.ComparisonReader().SourceGeometry()[1]
			calibratedProfile(t, 1)
			app.applyTerminalWidthProfile(tea.TerminalWidthProfileMsg{Measured: 1})
			command := app.drainComparisonGeometry()
			var late comparisonGeometryMsg
			if boundary != "shutdown" && boundary != "close-queued" {
				late = command().(comparisonGeometryMsg)
			}
			switch boundary {
			case "close", "close-queued":
				app.closeLocalDiff(view.tab)
				if boundary == "close-queued" {
					late = command().(comparisonGeometryMsg)
					for _, result := range late.results {
						if applied, _ := result.Apply(); applied {
							t.Fatal("closed source did not cancel queued geometry")
						}
					}
				}
			case "replace":
				view.screen.SetPrepared(diffview.Prepare(diffview.Comparison{After: "replacement marker"}))
			case "workspace":
				app.beginWorkspaceSwitch()
			case "shutdown":
				app.StopAsyncIO() // Joins this still-queued command after cancelling it.
				late = command().(comparisonGeometryMsg)
			}
			app.applyComparisonGeometry(late)
			if source.Width() != 8192 {
				t.Fatalf("%s admitted orphaned/stale reflow width=%d", boundary, source.Width())
			}
			if boundary == "replace" {
				app.paintShellFrame()
				if text := bufferPlain(app.mainSurf.Buffer()); !strings.Contains(text, "replacement marker") || strings.Contains(text, "Calibrating") {
					t.Fatalf("replacement source lost:\n%s", text)
				}
			}
		})
	}
}

func TestComparisonGeometryGitSharesSourcesAndRejectsOlderCalibration(t *testing.T) {
	app := newFullTestApp(t)
	app.width, app.height = 160, 40
	change := workspace.GitChange{Path: "geometry.txt", Area: workspace.GitAreaWorking}
	files := gitdiff.PrepareChanges([]workspace.GitChangeComparison{{Change: change, Comparison: workspace.GitComparison{Before: "old", After: strings.Repeat("☰", 4096), Fingerprint: "fixture"}}})
	app.gitPanel.SetPrepared(files)
	app.openGitFileReview(change)
	source := files[0].SourceGeometry()[1]
	calibratedProfile(t, 1)
	app.applyTerminalWidthProfile(tea.TerminalWidthProfileMsg{Measured: 1})
	old := app.drainComparisonGeometry()().(comparisonGeometryMsg)
	if len(old.results) != 2 {
		t.Fatalf("shared Git sources were reflowed %d times instead of twice", len(old.results))
	}
	ansi.SetTerminalWidthProfile(ansi.TerminalWidthProfile{Exact: map[string]int{"☰": 3}})
	app.applyTerminalWidthProfile(tea.TerminalWidthProfileMsg{Measured: 1})
	current := app.drainComparisonGeometry()().(comparisonGeometryMsg)
	app.applyComparisonGeometry(old)
	if source.Width() != 8192 {
		t.Fatal("older calibration applied")
	}
	app.applyComparisonGeometry(current)
	if source.Width() != 12288 {
		t.Fatalf("current calibration width=%d", source.Width())
	}
	app.paintShellFrame()
	if text := bufferPlain(app.mainSurf.Buffer()); !strings.Contains(text, "☰") || strings.Contains(text, "Calibrating") {
		t.Fatalf("Git review did not publish calibrated geometry:\n%s", text)
	}
}

func TestComparisonGeometryFailedWorkspaceSwitchRestartsCancelledPass(t *testing.T) {
	app := newFullTestApp(t)
	app.width, app.height = 160, 40
	change := workspace.GitChange{Path: "geometry.txt", Area: workspace.GitAreaWorking}
	files := gitdiff.PrepareChanges([]workspace.GitChangeComparison{{Change: change, Comparison: workspace.GitComparison{Before: "old", After: strings.Repeat("☰", 4096), Fingerprint: "fixture"}}})
	app.gitPanel.SetPrepared(files)
	app.openGitFileReview(change)
	source := files[0].SourceGeometry()[1]
	calibratedProfile(t, 1)
	app.applyTerminalWidthProfile(tea.TerminalWidthProfileMsg{Measured: 1})
	abandoned := app.drainComparisonGeometry()
	state := app.beginWorkspaceSwitch()
	state.restore(app)
	app.applyComparisonGeometry(abandoned().(comparisonGeometryMsg))
	if source.Width() != 8192 {
		t.Fatal("abandoned geometry applied after failed switch")
	}
	command := app.drainComparisonGeometry()
	if command == nil {
		t.Fatal("restored workspace left calibration permanently pending")
	}
	app.applyComparisonGeometry(command().(comparisonGeometryMsg))
	app.paintShellFrame()
	if source.Width() != 4096 || strings.Contains(bufferPlain(app.mainSurf.Buffer()), "Calibrating") {
		t.Fatal("restored workspace did not publish fresh geometry")
	}
}
