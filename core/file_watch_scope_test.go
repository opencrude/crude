package core

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"crude/core/coreprotocol"
	"crude/types/setting"
	"crude/workspace"
)

func TestVolumeRootWatchHasNoMissingParent(t *testing.T) {
	root := filepath.VolumeName(t.TempDir()) + string(filepath.Separator)
	w, err := newCoreWatcher(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close() }()
	if !addCoreWatchParent(w, root) {
		t.Fatal("volume-root watcher will retry a parent which cannot exist")
	}
}

func TestTopLevelWatchUpdatesBrowsedFoldersAndBuildsIndexOnQuery(t *testing.T) {
	root := t.TempDir()
	setCoreTestHome(t, root)
	t.Setenv("CRUDE_HOME", t.TempDir())
	shown := filepath.Join(root, "browse")
	hidden := filepath.Join(root, "other")
	for _, dir := range []string{shown, hidden} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	r, err := New(func(path string) (*workspace.Workspace, error) {
		w := workspace.NewWorkspace(path, "", "", setting.Settings{}, coreTestLogger{})
		w.SetAppDefaults(setting.App{})
		return w, nil
	}, filepath.Join(t.TempDir(), "receipts.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := r.Close(); err != nil {
			t.Error(err)
		}
	})
	wire := newResourceWire(t, r, "browser")
	var attached coreprotocol.AttachResult
	wire.call(t, "", coreprotocol.Attach, &coreprotocol.AttachRequest{Root: root, Entry: coreprotocol.EntryIntent{Explicit: true}}, &attached)
	id := attached.WorkspaceID
	owner := r.Workspaces()[0]
	updates := resourceTopic[coreprotocol.WorkspaceUpdate](t, wire.c, id, coreprotocol.TopicWorkspace)
	nextResourcePublication(t, updates, func(u coreprotocol.WorkspaceUpdate) bool { return u.Snapshot != nil })
	wire.call(t, id, coreprotocol.ClientFilesBrowse, &coreprotocol.FilePathRequest{Path: shown}, new(coreprotocol.ClientDirectoryEntries))
	outside := filepath.Join(hidden, "unopened.txt")
	visible := filepath.Join(shown, "appeared.txt")
	for _, p := range []string{outside, visible} {
		if err := os.WriteFile(p, []byte("uniqueSearchWord"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	nextResourcePublication(t, updates, func(u coreprotocol.WorkspaceUpdate) bool {
		for _, f := range u.Files {
			if f.Path == outside {
				t.Fatal("observed a file in an unread subtree")
			}
		}
		return slices.ContainsFunc(u.Files, func(f coreprotocol.FileChange) bool { return f.Path == visible })
	})
	var cat workspace.BrowserCatalog
	wire.call(t, id, coreprotocol.FilesCatalog, &coreprotocol.FileCatalogRequest{}, &cat)
	found := false
	for _, entry := range cat.Entries {
		found = found || entry.Path == outside
	}
	if !found {
		t.Fatal("Go to File missed unopened subtree")
	}
	var hits coreprotocol.FileSearchResult
	wire.call(t, id, coreprotocol.FilesSearch, &coreprotocol.FileSearchRequest{Query: "uniqueSearchWord"}, &hits)
	if len(hits.Matches) != 2 {
		t.Fatalf("Find in Files returned %d matches", len(hits.Matches))
	}
	if owner.TextIndex.Ready() || owner.LSPMgr.Assembly().FilesSeen != 0 {
		t.Fatal("browsing started an unsolicited index/assembly")
	}
	owner.TextIndex.Related("uniqueSearchWord", 1, nil)
	deadline := time.Now().Add(4 * time.Second)
	for !owner.TextIndex.Ready() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !owner.TextIndex.Ready() || owner.TextIndex.Stats().Files != 2 {
		t.Fatal("content query did not build the full index", owner.TextIndex.Stats())
	}
}
