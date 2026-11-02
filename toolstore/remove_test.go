package toolstore

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func installVersion(t *testing.T, store *Store, server *artifactServer, version, body string) Tool {
	t.Helper()
	path := "/" + version
	server.put(path, gzBytes(t, tarBytes(t, []entry{exe("bin/tool", body), file("share/data.txt", "data")})))
	r := request(server.URL+path, ArchiveTarGz, "bin/tool")
	r.Version = version
	tool, err := store.Install(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	return tool
}

func TestRemoveDeletesExactlyThePreview(t *testing.T) {
	store := newStore(t)
	server := serve(t, map[string][]byte{})
	one := installVersion(t, store, server, "1.0.0", "one")
	two := installVersion(t, store, server, "2.0.0", "two")
	if err := store.MarkUsed(one.ID, "/work/a"); err != nil {
		t.Fatal(err)
	}

	preview, err := store.PreviewRemove([]string{one.ID, one.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Tools) != 1 || len(preview.Paths) != 1 || preview.Paths[0] != one.Dir || preview.Token == "" {
		t.Fatalf("preview = %+v", preview)
	}
	if preview.Tools[0].Workspaces[0] != "/work/a" {
		t.Fatalf("preview does not show usage: %+v", preview.Tools[0])
	}
	if preview.Bytes <= one.Size {
		t.Fatalf("preview bytes %d do not include the manifest (content %d)", preview.Bytes, one.Size)
	}
	if err := store.Remove(context.Background(), preview); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(one.Dir); !isNotExist(err) {
		t.Fatalf("removed tool still exists: %v", err)
	}
	if _, err := os.Stat(two.Executable); err != nil {
		t.Fatalf("the other version was touched: %v", err)
	}
	if _, ok := store.Lookup("demo-server", "1.0.0"); ok {
		t.Fatal("Lookup still finds the removed tool")
	}
	tools, total, err := store.List()
	if err != nil || len(tools) != 1 || tools[0].ID != two.ID || total != two.Size {
		t.Fatalf("List after removal = %+v, %d, %v", tools, total, err)
	}
	reopened, err := Open(store.root)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reopened.usage.Tools[one.ID]; ok {
		t.Fatal("usage of the removed tool was kept")
	}
	history := events(t, store)
	if history[0].Kind != EventRemove || history[0].ToolID != one.ID {
		t.Fatalf("latest event = %+v", history[0])
	}
	requireEmptyStaging(t, store)

	// Removing the last version also removes the empty package folder.
	preview, err = store.PreviewRemove([]string{two.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Remove(context.Background(), preview); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(store.root, "demo-server")); !isNotExist(err) {
		t.Fatalf("empty package folder still exists: %v", err)
	}
}

func TestRemoveFailsClosedWhenTreeChanged(t *testing.T) {
	store := newStore(t)
	server := serve(t, map[string][]byte{})
	tool := installVersion(t, store, server, "1.0.0", "one")
	preview, err := store.PreviewRemove([]string{tool.ID})
	if err != nil {
		t.Fatal(err)
	}
	share := filepath.Join(tool.Dir, contentDir, "share")
	if err := unsealDir(share); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(share, "added.txt"), []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := sealDir(share); err != nil {
		t.Fatal(err)
	}
	if err := store.Remove(context.Background(), preview); !errors.Is(err, ErrPreviewStale) {
		t.Fatalf("Remove after a tree change = %v, want ErrPreviewStale", err)
	}
	if _, err := os.Stat(tool.Executable); err != nil {
		t.Fatalf("a stale removal deleted files: %v", err)
	}
	requireEmptyStaging(t, store)
	// A fresh preview covers the new file and succeeds.
	fresh, err := store.PreviewRemove([]string{tool.ID})
	if err != nil {
		t.Fatal(err)
	}
	if fresh.Token == preview.Token || fresh.Bytes != preview.Bytes+3 {
		t.Fatalf("fresh preview = %d bytes, old %d", fresh.Bytes, preview.Bytes)
	}
	if err := store.Remove(context.Background(), fresh); err != nil {
		t.Fatal(err)
	}
}

func TestRemoveFailsClosedWhenUsedAfterPreview(t *testing.T) {
	store := newStore(t)
	server := serve(t, map[string][]byte{})
	tool := installVersion(t, store, server, "1.0.0", "one")
	preview, err := store.PreviewRemoveUnused()
	if err != nil || len(preview.Tools) != 1 {
		t.Fatalf("unused preview = %+v, %v", preview, err)
	}
	if err := store.MarkUsed(tool.ID, "/work/a"); err != nil {
		t.Fatal(err)
	}
	if err := store.Remove(context.Background(), preview); !errors.Is(err, ErrPreviewStale) {
		t.Fatalf("Remove after a use = %v, want ErrPreviewStale", err)
	}
	if _, ok := store.Lookup("demo-server", "1.0.0"); !ok {
		t.Fatal("a stale removal deleted the tool")
	}
}

func TestRemoveFailsClosedWhenToolReplaced(t *testing.T) {
	store := newStore(t)
	server := serve(t, map[string][]byte{})
	tool := installVersion(t, store, server, "1.0.0", "one")
	preview, err := store.PreviewRemove([]string{tool.ID})
	if err != nil {
		t.Fatal(err)
	}
	other, err := store.PreviewRemove([]string{tool.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Remove(context.Background(), other); err != nil {
		t.Fatal(err)
	}
	if err := store.Remove(context.Background(), preview); !errors.Is(err, ErrPreviewStale) {
		t.Fatalf("Remove of a removed tool = %v, want ErrPreviewStale", err)
	}
	forged := RemovalPreview{Tools: []Tool{tool}, Paths: []string{tool.Dir}, Token: "forged"}
	if err := store.Remove(context.Background(), forged); !errors.Is(err, ErrPreviewStale) {
		t.Fatalf("Remove with a forged token = %v, want ErrPreviewStale", err)
	}
	if err := store.Remove(context.Background(), RemovalPreview{}); err == nil {
		t.Fatal("Remove accepted an empty preview")
	}
}

func TestPreviewRemoveUnusedSelectsOnlyUnusedTools(t *testing.T) {
	store := newStore(t)
	server := serve(t, map[string][]byte{})
	used := installVersion(t, store, server, "1.0.0", "one")
	unused := installVersion(t, store, server, "2.0.0", "two")
	if err := store.MarkUsed(used.ID, "/work/a"); err != nil {
		t.Fatal(err)
	}
	preview, err := store.PreviewRemoveUnused()
	if err != nil || len(preview.Tools) != 1 || preview.Tools[0].ID != unused.ID {
		t.Fatalf("unused preview = %+v, %v", preview, err)
	}
	if err := store.Remove(context.Background(), preview); err != nil {
		t.Fatal(err)
	}
	if _, ok := store.Lookup("demo-server", "1.0.0"); !ok {
		t.Fatal("the used tool was removed")
	}
	preview, err = store.PreviewRemoveUnused()
	if err != nil || len(preview.Tools) != 0 || preview.Token != "" {
		t.Fatalf("second unused preview = %+v, %v", preview, err)
	}
	if _, err := store.PreviewRemove(nil); err == nil {
		t.Fatal("PreviewRemove accepted no tools")
	}
	if _, err := store.PreviewRemove([]string{"missing/1-000000000000"}); err == nil {
		t.Fatal("PreviewRemove accepted an unknown tool")
	}
}

func TestRemoveCancelledBeforeStartKeepsTools(t *testing.T) {
	store := newStore(t)
	server := serve(t, map[string][]byte{})
	tool := installVersion(t, store, server, "1.0.0", "one")
	preview, err := store.PreviewRemove([]string{tool.ID})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := store.Remove(ctx, preview); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled Remove = %v", err)
	}
	if _, err := os.Stat(tool.Executable); err != nil {
		t.Fatalf("a cancelled removal deleted files: %v", err)
	}
	if err := store.Remove(context.Background(), preview); err != nil {
		t.Fatalf("the preview is still valid after a cancelled removal: %v", err)
	}
}
