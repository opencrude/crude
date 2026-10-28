package startup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/coreprotocol"
	"crude/projectpath"
	"crude/tui/screens/filepicker"
)

func TestStartupCreateFolderThenExplicitlyOpen(t *testing.T) {
	parent := t.TempDir()
	f := &fakeStartup{
		create: projectpath.CreateFolder,
		browse: func(ctx context.Context, req coreprotocol.StartupBrowseRequest) (coreprotocol.StartupBrowseResult, error) {
			page, err := projectpath.Browse(ctx, req.Path, req.Offset)
			out := coreprotocol.StartupBrowseResult{Path: page.Path, NextOffset: page.NextOffset, Truncated: page.Truncated}
			for _, entry := range page.Entries {
				out.Entries = append(out.Entries, coreprotocol.ClientDirectoryEntry{Name: entry.Name, Path: entry.Path, IsDir: entry.IsDir})
			}
			return out, err
		},
	}
	m := NewModel(context.Background(), f, coreprotocol.StartupSnapshot{Root: parent, FolderKind: "home"})
	t.Cleanup(m.Close)
	send(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	send(t, m, key(tea.KeyEnter))
	m.host.Focus(filepicker.ActionsID)
	send(t, m, key(tea.KeyTab))
	send(t, m, key(tea.KeyEnter))
	assertStartupFocus(t, m, filepicker.FolderNameID)
	if !strings.Contains(paintText(m), "NEW FOLDER") {
		t.Fatal("create form was not painted")
	}
	send(t, m, tea.PasteMsg{Content: "My project"})
	send(t, m, key(tea.KeyEnter))
	want := filepath.Join(parent, "My project")
	if info, err := os.Stat(want); err != nil || !info.IsDir() {
		t.Fatalf("folder was not created: %v %v", info, err)
	}
	if m.Result() != "" || m.closed || m.picker.Current() != want {
		t.Fatalf("creation selected or failed to browse new folder: result=%q current=%q", m.Result(), m.picker.Current())
	}
	if !strings.Contains(paintText(m), "Open Workspace") {
		t.Fatal("new folder cannot be opened")
	}
	send(t, m, ctrl('o'))
	if m.Result() != want || !m.closed {
		t.Fatalf("explicit open did not select created folder: %q", m.Result())
	}
}

func TestStartupCreateFolderErrorKeepsDraftAndDoesNotOpen(t *testing.T) {
	parent := t.TempDir()
	var called int
	f := &fakeStartup{create: func(context.Context, string, string) (string, error) {
		called++
		return "", errors.New("permission denied")
	}}
	m := NewModel(context.Background(), f, coreprotocol.StartupSnapshot{Root: parent, FolderKind: "home"})
	t.Cleanup(m.Close)
	send(t, m, key(tea.KeyEnter))
	m.host.Focus(filepicker.ActionsID)
	send(t, m, key(tea.KeyTab))
	send(t, m, key(tea.KeyEnter))
	send(t, m, tea.PasteMsg{Content: "my project"})
	send(t, m, key(tea.KeyEnter))
	text := paintText(m)
	if called != 1 || !m.picker.CreatingFolder() || m.Result() != "" ||
		!strings.Contains(text, "permission denied") || !strings.Contains(text, "my project") {
		t.Fatalf("failure discarded name/error or opened workspace: calls=%d\n%s", called, text)
	}
	send(t, m, key(tea.KeyEsc))
	if m.picker.CreatingFolder() || !m.picker.IsOpen() {
		t.Fatal("cancel did not return to picker")
	}
}

func TestStartupCreateFolderFailedBrowseDoesNotClaimNewFolderIsSelected(t *testing.T) {
	parent := t.TempDir()
	f := &fakeStartup{
		create: projectpath.CreateFolder,
		browse: func(_ context.Context, req coreprotocol.StartupBrowseRequest) (coreprotocol.StartupBrowseResult, error) {
			if req.Path != parent {
				return coreprotocol.StartupBrowseResult{}, errors.New("cannot read created folder")
			}
			return coreprotocol.StartupBrowseResult{Path: parent, NextOffset: -1}, nil
		},
	}
	m := NewModel(context.Background(), f, coreprotocol.StartupSnapshot{Root: parent, FolderKind: "home"})
	t.Cleanup(m.Close)
	send(t, m, key(tea.KeyEnter))
	m.host.Focus(filepicker.ActionsID)
	send(t, m, key(tea.KeyTab))
	send(t, m, key(tea.KeyEnter))
	send(t, m, tea.PasteMsg{Content: "project"})
	send(t, m, key(tea.KeyEnter))
	if _, err := os.Stat(filepath.Join(parent, "project")); err != nil {
		t.Fatal("creation failed", err)
	}
	text := paintText(m)
	if m.Result() != "" || m.picker.Current() != parent || !strings.Contains(text, "Created folder: project") ||
		!strings.Contains(text, "cannot read created folder") || strings.Contains(text, "Choose Open Workspace to open it") {
		t.Fatalf("failed navigation was misleading: current=%q\n%s", m.picker.Current(), text)
	}
}
