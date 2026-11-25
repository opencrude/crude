package viewmodel

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/screens/gitpanel"
)

func TestConnectedGitTopicFaultStaysInlineAndClears(t *testing.T) {
	root := newGitRepoFixture(t)
	runtime := remoteJourneyRuntime(t, "http://unused.invalid")
	shell := newRemoteJourneyShell(t, runtime, root)
	app := shell.app
	app.Update(tea.WindowSizeMsg{Width: 80, Height: 25})
	shells := []remoteJourneyShell{shell}
	remoteJourneyUntil(t, shells, func() bool {
		return app.gitPanel.Snapshot().Branch == "work" && len(app.gitPanel.Snapshot().Changes) == 1
	})
	runAppCommand(t, app, app.openGitPanel(gitpanel.ModeChanges))
	app.gitPanel.SetMessage("Keep my draft")
	old := app.gitPanel.Snapshot()
	index := filepath.Join(root, ".git", "index")
	savedIndex, err := os.ReadFile(index)
	if err != nil {
		t.Fatal(err)
	}
	write := func(path string, content []byte) {
		t.Helper()
		if err := os.WriteFile(path, content, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// A corrupt index makes native status fail without any user action or
	// synthetic shell message. Touch a tracked file to drive the core watch.
	tracked := filepath.Join(root, "tracked.txt")
	write(index, []byte("invalid index"))
	write(tracked, []byte("three\n"))
	const warning = "Git status is not up to date."
	paint := func() string {
		app.paintShellFrame()
		return bufferPlain(app.mainSurf.Buffer())
	}
	remoteJourneyUntil(t, shells, func() bool {
		return app.operationIssue != nil || strings.Contains(paint(), warning)
	})
	if app.operationIssue != nil || len(app.pendingOperationIssues) != 0 {
		t.Fatal("a background Git topic failure opened an operation dialog")
	}
	_, after, _ := strings.Cut(paint(), warning)
	tail, _, _ := strings.Cut(after, "\n")
	if next := strings.TrimSpace(tail); next != "" && next != "Press Ctrl+R to refresh." {
		t.Fatal("the constrained Git panel cut the warning in the middle of the next step")
	}
	if got := app.gitPanel.Snapshot(); got.Head != old.Head || got.Branch != old.Branch || len(got.Changes) != len(old.Changes) ||
		app.gitPanel.Message() != "Keep my draft" || !strings.Contains(paint(), "tracked.txt") {
		t.Fatal("a background Git topic failure replaced the last status or commit draft")
	}
	write(index, savedIndex)
	write(tracked, []byte("one\n"))
	remoteJourneyUntil(t, shells, func() bool {
		return len(app.gitPanel.Snapshot().Changes) == 0 && !strings.Contains(paint(), warning)
	})
	if app.operationIssue != nil || app.gitPanel.Message() != "Keep my draft" {
		t.Fatal("a successful Git topic refresh changed the dialog or draft")
	}
}
