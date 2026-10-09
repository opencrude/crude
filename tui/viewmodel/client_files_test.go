package viewmodel

import (
	"context"
	"path/filepath"
	"testing"

	"crude/shellport"
	filepickerscreen "crude/tui/screens/filepicker"
	"crude/workspace"
	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol"
)

type fakeClientFiles struct {
	shellport.Workspace
	root    string
	entries []shellport.ClientDirectoryEntry
}

func (f *fakeClientFiles) RootPath() string { return f.root }
func (f *fakeClientFiles) BrowseDirectory(context.Context, string) ([]shellport.ClientDirectoryEntry, error) {
	return f.entries, nil
}
func (f *fakeClientFiles) BrowseExternalDirectory(ctx context.Context, path string) ([]shellport.ClientDirectoryEntry, error) {
	return f.BrowseDirectory(ctx, path)
}
func (f *fakeClientFiles) CatalogFiles(context.Context, bool) (workspace.BrowserCatalog, error) {
	return workspace.BrowserCatalog{}, nil
}
func (f *fakeClientFiles) ReadClientFile(context.Context, string) ([]byte, error) {
	return []byte("old\n"), nil
}
func (f *fakeClientFiles) ReadExternalClientFile(ctx context.Context, path string) ([]byte, error) {
	return f.ReadClientFile(ctx, path)
}
func (f *fakeClientFiles) ListSavedWorkspaces(context.Context) ([]workspace.WorkspaceInfo, error) {
	return nil, nil
}
func (f *fakeClientFiles) CanonicalClientPath(path string) string { return path }

func TestClientFileTreeNonlocalAndStale(t *testing.T) {
	m := newTestApp(t)
	root := filepath.Join(t.TempDir(), "does-not-exist")
	port := &fakeClientFiles{Workspace: m.ws, root: root, entries: []shellport.ClientDirectoryEntry{{Name: "remote.go", Path: filepath.Join(root, "remote.go")}}}
	m.ws = port
	model := newClientFileTree(m)
	m.Tree = NewTreeState(model, 30)
	old := m.beginClientFileTreeLoad()().(clientTreeDirectoryMsg)
	newer := m.beginClientFileTreeLoad()().(clientTreeDirectoryMsg)
	m.applyClientTreeDirectory(old)
	if len(model.VisibleNodes()) != 0 {
		t.Fatal("superseded result installed")
	}
	m.applyClientTreeDirectory(newer)
	if len(model.VisibleNodes()) != 1 {
		t.Fatalf("remote file missing: %v", model.VisibleRows())
	}
	port.entries = []shellport.ClientDirectoryEntry{{Name: "wrong.go", Path: filepath.Join(root, "wrong.go")}}
	stale := m.beginClientFileTreeLoad()().(clientTreeDirectoryMsg)
	m.workspaceSwitchGen.Add(1)
	m.applyClientTreeDirectory(stale)
	if model.VisibleNodes()[0].Name != "remote.go" {
		t.Fatal("previous workspace completion installed")
	}
}

func TestClientRenameReadsNonlocalFile(t *testing.T) {
	m := newTestApp(t)
	root := filepath.Join(t.TempDir(), "remote-only")
	path := filepath.Join(root, "code.go")
	m.ws = &fakeClientFiles{Workspace: m.ws, root: root}
	edit := &protocol.WorkspaceEdit{Changes: map[protocol.DocumentURI][]protocol.TextEdit{protocol.URIFromPath(path): {{Range: protocol.Range{End: protocol.Position{Character: 3}}, NewText: "new"}}}}
	result := m.prepareClientLSPRename(lspRenameResultMsg{newName: "new"}, edit)().(lspRenamePreparedMsg)
	if result.err != nil {
		t.Fatal(result.err)
	}
	if len(result.edits) != 1 || result.edits[0].NewContent != "new\n" {
		t.Fatalf("incorrect remote rename: %#v", result.edits)
	}
}

func TestClientPickerBrowsesNonlocalDirectory(t *testing.T) {
	m := newTestApp(t)
	root := filepath.Join(t.TempDir(), "remote-picker-only")
	m.ws = &fakeClientFiles{Workspace: m.ws, root: root, entries: []shellport.ClientDirectoryEntry{{Name: "code.go", Path: filepath.Join(root, "code.go")}}}
	picker := filepickerscreen.New(m.Theme)
	picker.Open(root, filepickerscreen.ModeOpenFile, nil)
	m.FilePicker = picker
	result := m.beginDirectoryAsyncLoad(picker, root)().(directoryAsyncResultMsg)
	if result.err != nil {
		t.Fatal(result.err)
	}
	if result.listing.Path != root || len(result.listing.Entries) != 1 || result.listing.Entries[0].Name != "code.go" {
		t.Fatalf("wrong remote directory: %#v", result.listing)
	}
	m.applyDirectoryAsyncResult(result)
}
