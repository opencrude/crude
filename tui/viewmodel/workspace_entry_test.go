package viewmodel

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/core/coreprotocol"
	"crude/internal/clientstate"
	"crude/tui/screens/statusoverview"
	"crude/tui/ui"
	"crude/types/statuscenter"
)

func TestFileEntryCreatesNamedFileOnlyOnSave(t *testing.T) {
	store, err := clientstate.Open(filepath.Join(t.TempDir(), "clients.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	root := t.TempDir()
	shell := newRemoteJourneyShellWithEntry(t, remoteJourneyRuntime(t, "http://unused.invalid"), root, coreprotocol.EntryIntent{Explicit: true, FileOnly: true})
	app := shell.app
	path := filepath.Join(root, "note.png")
	app.SetLaunchFile(path, true)
	attachViewMemoryProfile(t, store, shell, "file-entry")
	runAppCommand(t, app, app.drainLiveEditorCommands())
	remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool {
		return app.EditorState.ActiveView == path && app.liveEditors[app.EditorState.Editor].topicReady
	})
	if app.Tree.Visible || app.captureViewMemory().SidebarHidden {
		t.Fatal("launch did not close just this shell's folder tree")
	}
	notice := entryNotice(t, app)
	if !strings.Contains(notice.Summary, "note.png") || !strings.Contains(notice.Detail, "Language servers are off") || !strings.Contains(notice.Detail, "Ctrl+B") {
		t.Fatalf("file launch did not explain the editor and folder tree: %+v", notice)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("opening created a file before Save", err)
	}
	_, cmd := app.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	runAppCommand(t, app, cmd)
	_, cmd = app.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	runAppCommand(t, app, cmd)
	remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool { data, e := os.ReadFile(path); return e == nil && string(data) == "x" })
	app.ToggleSidebar()
	if !app.Tree.Visible {
		t.Fatal("user cannot reopen the sidebar")
	}
	app.closeEditorTab()
	writeImageFixture(t, root, "note.png")
	runAppCommand(t, app, app.openPaneFiles([]string{path}, app.Win.ActiveGroup()))
	if app.Win.TabByTitle(imageTabTitle(filepath.ToSlash(path))) == nil {
		t.Fatal("reopening a saved image used the missing-file launch mode")
	}
}

func entryNotice(t *testing.T, app *App) statuscenter.NotificationRecord {
	t.Helper()
	notices := app.ws.Status().Notifications(false)
	var matches []statuscenter.NotificationRecord
	for _, n := range notices {
		if n.Source == "workspace-entry" {
			matches = append(matches, n)
		}
	}
	if len(matches) != 1 || matches[0].Transient || matches[0].Read || matches[0].Owner.ID != app.ws.RootPath() || app.ws.Status().UnreadCount() == 0 {
		t.Fatalf("want one unread workspace entry in Notices, got %+v", matches)
	}
	return matches[0]
}

func TestHomeEntryNoticeSurvivesReadingAndReopen(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	app := newRemoteJourneyShellWithEntry(t, remoteJourneyRuntime(t, "http://unused.invalid"), home, coreprotocol.EntryIntent{Explicit: true}).app
	app.showEntryFolderNotice()
	n := entryNotice(t, app)
	if !strings.Contains(n.Summary, "Local History and language servers are off") || !strings.Contains(n.Summary+n.Detail, "your home folder") ||
		!slices.ContainsFunc(n.Actions, func(a statuscenter.Action) bool { return a.Enabled && a.ID == "settings:workspace" }) {
		t.Fatalf("missing reason or Settings action: %+v", n)
	}
	app.ws.Status().MarkRead(n.ID, true)
	app.showEntryFolderNotice()
	if again := entryNotice(t, app); again.ID != n.ID || again.Occurrences != 2 {
		t.Fatal("a second entry did not renew the same notice", again)
	}
}

func TestFileEntryOpenFolderPreservesLiveWork(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "note.go")
	if err := os.WriteFile(path, []byte("package original\n"), 0600); err != nil {
		t.Fatal(err)
	}
	shell := newRemoteJourneyShellWithEntry(t, remoteJourneyRuntime(t, "http://unused.invalid"), root, coreprotocol.EntryIntent{Explicit: true, FileOnly: true})
	app := shell.app
	app.SetLaunchFile(path, false)
	app.installViewMemory(viewMemory{})
	runAppCommand(t, app, app.drainLiveEditorCommands())
	remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool {
		return app.EditorState.ActiveView == path && app.liveEditors[app.EditorState.Editor].topicReady
	})
	ed := app.EditorState.Editor
	_, cmd := app.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	runAppCommand(t, app, cmd)
	app.Input.SetInput("keep this draft")
	work := ed.LineText(0)
	if !ed.Modified() || app.ws.LoadAppSettings().LSP.Enabled {
		t.Fatal("file mode precondition failed")
	}
	n := entryNotice(t, app)
	i := slices.IndexFunc(n.Actions, func(a statuscenter.Action) bool { return a.Enabled && a.Label == "Open folder as workspace" })
	if i < 0 {
		t.Fatal("the file notice has no action to open its folder")
	}
	runAppCommand(t, app, app.handleStatusOverviewNavigate(statusoverview.NavigateMsg{Item: statusoverview.Item{Owner: n.Owner}, Action: n.Actions[i].ID}))
	remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool { return app.ws.LoadAppSettings().LSP.Enabled })
	if app.EditorState.Editor != ed || !ed.Modified() || ed.LineText(0) != work || app.Input.Value() != "keep this draft" {
		t.Fatal("opening the same folder lost the editor or composer")
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != "package original\n" {
		t.Fatal("promoting saved the user's draft", err)
	}
	app.ToggleSidebar()
	if !app.Tree.Visible {
		t.Fatal("folder tree cannot be shown")
	}
}

func TestQuitClosesStartupPromptsWithoutChoosing(t *testing.T) {
	for _, setup := range []bool{false, true} {
		app := onboardingTestApp(t)
		id := ui.OverlayWorkspaceTrust
		if setup {
			loadOnboarding(t, app)
			id = aiSetupReminderOverlay
		}
		app.Update(TickMsg{})
		if !app.overlayHost.IsOpen(id) {
			t.Fatal("expected startup prompt", id)
		}
		app.Update(tea.KeyPressMsg{Code: 'q', Mod: tea.ModCtrl})
		if app.overlayHost.IsOpen(id) || !app.ws.LoadAppSettings().Analysis.DecisionNeeded() || app.ws.LoadAppSettings().Onboarding.AISetupReminderHidden {
			t.Fatal("Quit did not dismiss the prompt without saving a decision", id)
		}
	}
}
