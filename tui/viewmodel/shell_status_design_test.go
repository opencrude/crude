package viewmodel

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"crude/tui/editor"
	tuitypes "crude/tui/types"
	"crude/tui/ui"
	types "crude/types"
	"crude/workspace"
)

func TestShellStatusUsesSharedRailsAndOmitsZeroNoise(t *testing.T) {
	app := newFullTestApp(t)
	app.width, app.height = 120, 28
	app.AddOrchestrator("root")
	session := workspace.NewSession("session", "root", nil, nil)
	app.ActiveSession = session
	app.SessionsByAgent = make(map[string]*workspace.Session)
	app.SessionsByAgent["root"] = session
	app.AgentMgr.Get("root").Model = "glm-5.3"
	app.Status.SetAgent(types.AgentStatusSnapshot{Ref: types.ResourceRef{Kind: types.ResourceAgent, ID: "root"}, Label: "Main Agent", Phase: types.AgentIdle, Detail: "Idle", ModelID: "glm-5.3"})
	app.RecordAgentUsage("root", map[string]any{
		"request_id": "root/1/1", "prompt": 71500, "completion": 177,
		"prompt_cached": 14300, "cache_read_known": true,
		"context_prompt": 71500, "context_window": 1_050_000,
		"cost": 0.004, "cost_known": true, "estimated": true,
	})
	app.paintShellFrame()

	header := rowText(app, 0)
	footer := rowText(app, app.height-1)
	left, right := ui.ShellContentRail(ui.NewTokenSet(app.Theme), app.width)
	if firstTextColumn(header) != left || firstTextColumn(footer) != left {
		t.Fatalf("shell rails differ: header=%d footer=%d want=%d\nheader=%q\nfooter=%q", firstTextColumn(header), firstTextColumn(footer), left, header, footer)
	}
	if lastTextColumn(header) != right-1 || lastTextColumn(footer) != right-1 {
		t.Fatalf("shell right rails differ: header=%d footer=%d want=%d\nheader=%q\nfooter=%q", lastTextColumn(header), lastTextColumn(footer), right-1, header, footer)
	}
	for _, noise := range []string{"0 agent", "1 idle", "Main Agent", "glm-5.3"} {
		if strings.Contains(footer, noise) {
			t.Fatalf("footer repeats routine active-resource state %q: %q", noise, footer)
		}
	}
	if !strings.Contains(header, "glm-5.3") || !strings.Contains(header, "71.5k/1.05M 7%") || !strings.Contains(header, "↓177") || !strings.Contains(header, "cache 20.0%") || !strings.Contains(header, "<$0.01") {
		t.Fatalf("agent context missing from header: %q", header)
	}
	if strings.Contains(header, "Reasoning") || strings.Contains(header, "Responding") {
		t.Fatalf("header exposes low-level agent phase: %q", header)
	}
	app.Status.SetAgent(types.AgentStatusSnapshot{Ref: types.ResourceRef{Kind: types.ResourceAgent, ID: "root"}, Phase: types.AgentThinking, TotalBytes: 2 * 1024 * 1024})
	app.paintShellFrame()
	header = rowText(app, 0)
	if !strings.Contains(header, "↑~71.5k/1.05M 7%") || strings.Contains(header, " MB") || strings.Contains(header, "last ctx") {
		t.Fatalf("active request changed telemetry units or added lifecycle detail: %q", header)
	}
	if strings.Contains(footer, "Enter send") || strings.Contains(footer, "cancel") {
		t.Fatalf("footer retained obvious composer inventory: %q", footer)
	}
	for _, hint := range []string{"Alt+Enter", "Ctrl+O", "history"} {
		if !strings.Contains(footer, hint) {
			t.Fatalf("footer missing useful composer hint %q: %q", hint, footer)
		}
	}
	if !strings.Contains(footer, "Help") {
		t.Fatalf("footer lost Help entry: %q", footer)
	}
}

func TestAgentHeaderRetainsUploadSizeAfterProviderWaitTransition(t *testing.T) {
	app := newFullTestApp(t)
	app.width, app.height = 120, 28
	app.AddOrchestrator("root")
	app.syncAgentStatusEvent(tuitypes.AgentEventMsg{AgentID: "root", Type: "phase", Data: map[string]any{"name": "preparing", "progress": 100, "uploaded_bytes": float64(2 * 1024 * 1024), "total_bytes": float64(2 * 1024 * 1024)}})
	app.syncAgentStatusEvent(tuitypes.AgentEventMsg{AgentID: "root", Type: "phase", Data: map[string]any{"name": "waiting_provider"}})
	app.paintShellFrame()
	header := rowText(app, 0)
	status, _ := app.Status.Agent("root")
	if status.TotalBytes != 2*1024*1024 || strings.Contains(header, " MB") || strings.Contains(header, "Waiting") {
		t.Fatalf("upload data lost or leaked out of details: status=%+v header=%q", status, header)
	}
}

func TestAgentHeaderKeepsLifecycleAndPayloadInDetails(t *testing.T) {
	app := newFullTestApp(t)
	app.width, app.height = 120, 28
	app.AddOrchestrator("root")
	app.Status.SetAgent(types.AgentStatusSnapshot{Ref: types.ResourceRef{Kind: types.ResourceAgent, ID: "root"}, Phase: types.AgentThinking, TotalBytes: 2 * 1024 * 1024, StartedAt: time.Now().Add(-5 * time.Second), PhaseStartedAt: time.Now().Add(-2 * time.Second)})
	app.paintShellFrame()
	header := rowText(app, 0)
	if strings.Contains(header, "Working") || strings.Contains(header, "Waiting") || strings.Contains(header, "Reasoning") || strings.Contains(header, " MB") {
		t.Fatalf("lifecycle/payload leaked into telemetry header: %q", header)
	}
	for _, segment := range app.statusHeaderRightSegments() {
		if segment.Role == ui.ColorFg {
			t.Fatalf("normal agent header segment uses bright foreground: %+v", segment)
		}
	}
	for _, phase := range []types.AgentPhase{types.AgentApproval, types.AgentQuestion, types.AgentRetrying, types.AgentFailed, types.AgentCancelled} {
		app.Status.SetAgent(types.AgentStatusSnapshot{Ref: types.ResourceRef{Kind: types.ResourceAgent, ID: "root"}, Phase: phase})
		app.paintShellFrame()
		header = rowText(app, 0)
		for _, label := range []string{"Permission", "Answer", "Retrying", "Failed", "Terminated"} {
			if strings.Contains(header, label) {
				t.Fatalf("%s belongs in details: %q", label, header)
			}
		}
		status, _ := app.Status.Agent("root")
		if status.Phase != phase {
			t.Fatalf("hiding header text destroyed state: %+v", status)
		}
	}
}

func TestEditorAndGitFactsLiveInHeaderNotFooterHints(t *testing.T) {
	app := newFullTestApp(t)
	app.width, app.height = 120, 28
	app.launcherScreen = nil
	path := filepath.Join(t.TempDir(), "main.go")
	if err := os.WriteFile(path, []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ed := editor.New(app.Theme)
	if err := ed.Open(path); err != nil {
		t.Fatal(err)
	}
	defer ed.Close()
	app.EditorState.Editor, app.EditorState.ActiveView = ed, path
	ed.SetGitAnnotation(ed.Revision(), nil, []editor.GitBlameLine{{Line: 1, Hash: "abcdef123456", ShortHash: "abcdef1", Author: "Ada", When: time.Now().Add(-2 * time.Hour), Subject: "test"}}, false)
	app.sourceControl.SetSnapshot(workspace.GitSnapshot{Branch: "main", Ahead: 1, Behind: 2, Changes: []workspace.GitChange{{Path: "one"}, {Path: "two"}}})
	app.paintShellFrame()
	header, footer := rowText(app, 0), rowText(app, app.height-1)
	for _, fact := range []string{"main", "1:1", "Ada", "abcdef1"} {
		if !strings.Contains(header, fact) {
			t.Fatalf("header missing %q: %q", fact, header)
		}
	}
	for _, hint := range []string{"Ctrl+F", "Ctrl+]", "Ctrl+1", "Help"} {
		if !strings.Contains(footer, hint) {
			t.Fatalf("footer missing useful editor hint %q: %q", hint, footer)
		}
	}
	// The branch identity owns the repo state: changed-file count and
	// ahead/behind tracking paint next to the branch in the header, not in
	// the footer hint row.
	for _, fact := range []string{"2", "↑1 ↓2"} {
		if !strings.Contains(header, fact) {
			t.Fatalf("header missing Git workspace fact %q: %q", fact, header)
		}
		if strings.Contains(footer, fact) {
			t.Fatalf("footer retained Git workspace fact %q (lives in the header branch segment): %q", fact, footer)
		}
	}
	if strings.Contains(header, "Git main") {
		t.Fatalf("header spells out Git instead of using the shared branch symbol: %q", header)
	}
	for _, clutter := range []string{"main.go", "1:1", "Ctrl+S", "undo", "redo"} {
		if strings.Contains(footer, clutter) {
			t.Fatalf("footer retained editor clutter %q: %q", clutter, footer)
		}
	}
}

func firstTextColumn(row string) int {
	for index, r := range []rune(row) {
		if r != ' ' {
			return index
		}
	}
	return -1
}

func lastTextColumn(row string) int {
	runes := []rune(row)
	for index := len(runes) - 1; index >= 0; index-- {
		if runes[index] != ' ' {
			return index
		}
	}
	return -1
}
