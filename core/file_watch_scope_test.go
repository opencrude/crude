package core

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"crude/core/coreprotocol"
	"crude/insight/textindex"
	"crude/types/setting"
	"crude/types/statuscenter"
	"crude/workspace"
)

func TestVolumeRootWatchHasNoMissingParent(t *testing.T) {
	root := filepath.VolumeName(t.TempDir()) + string(filepath.Separator)
	w, err := newCoreWatcher(testWatchRoot(t, root))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close() }()
	if !addCoreWatchParent(w, root) {
		t.Fatal("volume-root watcher will retry a parent which cannot exist")
	}
}

func attachTopLevelWatch(t *testing.T, root string) (*resourceWire, string, *workspace.Workspace) {
	t.Helper()
	setCoreTestHome(t, root)
	t.Setenv("CRUDE_HOME", t.TempDir())
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
	return wire, attached.WorkspaceID, r.Workspaces()[0]
}

func TestTopLevelWatchUpdatesBrowsedFoldersAndBuildsIndexOnQuery(t *testing.T) {
	root := t.TempDir()
	shown := filepath.Join(root, "browse")
	hidden := filepath.Join(root, "other")
	for _, dir := range []string{shown, hidden} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	wire, id, owner := attachTopLevelWatch(t, root)
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

// Hold the recursive worker before its first walk. Directory reads and changes
// in loaded folders must still work while it prepares to take over.
func TestScopeObservesWhileRecursiveAdmissionPending(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "browsed")
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	analysis := make(chan struct{})
	entered, release := make(chan struct{}), make(chan struct{})
	events := make(chan string, 64)
	var first atomic.Bool
	idx := textindex.New(root, 0)
	defer idx.Close()
	w, err := startDirectoryScope(root, func(v coreprotocol.FileChange) {
		if v.Resync && first.CompareAndSwap(false, true) {
			close(entered)
			<-release
		}
		if !v.Resync {
			events <- v.Path
		}
	}, nil, func(bool) {}, analysis, idx)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	defer close(release)
	owner := coreprotocol.Owner{ClientID: "browser", Epoch: 1}
	w.scope.attach(owner)
	if err := w.scope.observe(t.Context(), owner, path); err != nil {
		t.Fatal(err)
	}
	close(analysis)
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("recursive admission did not start")
	}
	file := filepath.Join(path, "visible")
	if err := os.WriteFile(file, nil, 0600); err != nil {
		t.Fatal(err)
	}
	timeout := time.After(time.Second)
	for {
		select {
		case p := <-events:
			if p == file {
				return
			}
		case <-timeout:
			t.Fatal("loaded listing lost observation during recursive admission")
		}
	}
}

func TestScopeRejectsReplacementDuringRecursiveAdmission(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "root")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	started, release := make(chan struct{}), make(chan struct{})
	var first, granted atomic.Bool
	analysis := make(chan struct{})
	idx := textindex.New(root, 0)
	defer idx.Close()
	w, err := startDirectoryScope(root, func(v coreprotocol.FileChange) {
		if v.Resync && first.CompareAndSwap(false, true) {
			close(started)
			<-release
		}
	}, nil, func(ok bool) {
		if ok {
			granted.Store(true)
		}
	}, analysis, idx)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	close(analysis)
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("admission did not start")
	}
	err = os.Rename(root, filepath.Join(parent, "old"))
	if err == nil {
		err = os.Mkdir(root, 0700)
	}
	close(release)
	if err != nil {
		t.Fatal(err)
	}
	idx.Related("replacement", 1, nil)
	// Browse must not install a watch in the replacement. No producer may start.
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	owner := coreprotocol.Owner{ClientID: "browser", Epoch: 1}
	w.scope.attach(owner)
	if err := w.scope.observe(ctx, owner, root); err == nil {
		t.Error("admitted a listing in the replacement root")
	}
	w.Close()
	if granted.Load() || idx.Ready() {
		t.Fatal("replacement root acquired coverage")
	}
}

func TestScopeIndexesReadableSiblingsAfterDeniedFolder(t *testing.T) {
	for _, order := range []string{"query-first", "analysis-first"} {
		t.Run(order, func(t *testing.T) {
			root := t.TempDir()
			blocked := filepath.Join(root, "a-denied")
			if err := os.Mkdir(blocked, 0700); err != nil {
				t.Fatal(err)
			}
			hidden := filepath.Join(blocked, "file.txt")
			if err := os.WriteFile(hidden, []byte("hiddenIdentifier"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(blocked, 0); err != nil {
				t.Fatal(err)
			}
			defer func() { _ = os.Chmod(blocked, 0700) }()
			if _, err := os.ReadDir(blocked); !os.IsPermission(err) {
				t.Skip("fixture needs an enforced directory permission denial")
			}
			visible := filepath.Join(root, "z-visible.txt")
			if err := os.WriteFile(visible, []byte("visibleIdentifier"), 0600); err != nil {
				t.Fatal(err)
			}
			idx := textindex.New(root, 0)
			defer idx.Close()
			analysis := make(chan struct{})
			var reliable atomic.Bool
			var resync atomic.Int64
			w, err := startDirectoryScope(root, func(v coreprotocol.FileChange) {
				if v.Resync {
					resync.Add(1)
				}
			}, func(_ context.Context, p string, _ int) { idx.Changed(p) }, func(ok bool) {
				reliable.Store(ok)
				if ok && idx.Ready() {
					idx.Rescan()
				}
			}, analysis, idx)
			if err != nil {
				t.Fatal(err)
			}
			defer w.Close()
			if order == "analysis-first" {
				close(analysis)
				select {
				case <-w.scope.promoted:
				case <-time.After(2 * time.Second):
					t.Fatal("readable folders never gained observation")
				}
				if idx.Ready() {
					t.Fatal("analysis built an unsolicited text index")
				}
			}
			idx.Related("visibleIdentifier", 1, nil)
			watchAwait(t, "readable content ranking", func() bool { return len(idx.Related("visibleIdentifier", 1, nil)) > 0 })
			if reliable.Load() || idx.Complete() {
				t.Fatal("unreadable tree claimed complete coverage")
			}
			before := resync.Load()
			time.Sleep(800 * time.Millisecond)
			if resync.Load() != before || idx.Stats().Builds != 1 {
				t.Fatalf("unchanged denial rescanned the workspace: resync %d -> %d, builds %d", before, resync.Load(), idx.Stats().Builds)
			}
			if err := os.Chmod(blocked, 0700); err != nil {
				t.Fatal(err)
			}
			watchAwait(t, "restored access and new ranking", func() bool {
				return reliable.Load() && idx.Complete() && len(idx.Related("hiddenIdentifier", 1, nil)) > 0
			})
		})
	}
}

// Reach the real retained actor and status owner. A permission warning must
// name an affected directory, clear after recovery, and leave ranking usable.
func TestTopLevelWatchReportsAndResolvesDeniedCoverage(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "denied")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	for name, token := range map[string]string{"visible.txt": "availableContent", filepath.Join("denied", "hidden.txt"): "restoredContent"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(token), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(dir, 0); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(dir, 0700) }()
	if _, err := os.ReadDir(dir); !os.IsPermission(err) {
		t.Skip("fixture needs an enforced directory permission denial")
	}
	_, _, ws := attachTopLevelWatch(t, root)
	idx := ws.TextIndex
	idx.Related("availableContent", 1, nil)
	issueAt := func(resolved bool) bool {
		return slices.ContainsFunc(ws.Status.Issues(), func(i statuscenter.IssueSnapshot) bool {
			return !i.ResolvedAt.IsZero() == resolved && strings.Contains(i.Detail, dir)
		})
	}
	watchAwait(t, "unreadable folder and readable ranking", func() bool { return issueAt(false) && len(idx.Related("availableContent", 1, nil)) == 1 })
	if idx.Complete() {
		t.Fatal("permission error reported complete content coverage")
	}
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	watchAwait(t, "resolved warning and previously unreadable content", func() bool {
		return issueAt(true) && idx.Complete() && len(idx.Related("restoredContent", 1, nil)) == 1
	})
}
