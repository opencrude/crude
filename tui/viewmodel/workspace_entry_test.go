package viewmodel

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/core/coreprotocol"
	"crude/internal/clientstate"
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
	if n, ok := app.ws.Status().LatestTransient(time.Now(), time.Minute); !ok || !strings.Contains(n.Summary, "note.png") || !strings.Contains(n.Summary, "Language servers are off") {
		t.Fatal("file launch notice missing", n.Summary)
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
}
