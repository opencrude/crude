package toolstore

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A failed history write after the real rename must not make the artifact
// eligible for automatic use, either now or after opening the store again.
func TestUnconfirmedPublicationIsNeverAutomaticallySelected(t *testing.T) {
	store := newStore(t)
	server := serve(t, map[string][]byte{"/tool": []byte("fixture executable")})
	req := request(server.URL+"/tool", ArchiveRaw, "tool")
	if err := os.Mkdir(filepath.Join(store.root, historyName), 0700); err != nil {
		t.Fatal(err)
	}
	tool, err := store.Install(t.Context(), req)
	var unconfirmed *PublicationUnconfirmedError
	if !errors.As(err, &unconfirmed) || tool.ID == "" || tool.ID != unconfirmed.ToolID {
		t.Fatalf("install did not reach an unconfirmed publication: %+v, %v", tool, err)
	}
	if _, err := os.Stat(tool.Executable); err != nil {
		t.Fatalf("the artifact was not published: %v", err)
	}
	if _, ok := store.Lookup(req.Package, req.Version); ok {
		t.Fatal("an unconfirmed artifact was offered for use")
	}
	if err := store.MarkUsed(tool.ID, "/project"); err == nil {
		t.Fatal("an unconfirmed artifact was recorded as used")
	}
	tools, _, err := store.List(t.Context())
	if err != nil || len(tools) != 0 {
		t.Fatalf("unconfirmed artifact appeared in installed tools: %+v, %v", tools, err)
	}
	unknown, err := store.Unrecognized(t.Context())
	if err != nil || !strings.Contains(strings.Join(unknown, "\n"), tool.ID) {
		t.Fatalf("unconfirmed artifact is not available for inspection: %v, %v", unknown, err)
	}

	// Restore the test's history path so reopening can inspect the store.
	if err := os.Remove(filepath.Join(store.root, historyName)); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(store.root)
	if err != nil {
		t.Fatal(err)
	}
	reopened.allowLoopbackHTTP = true
	if _, ok := reopened.Lookup(req.Package, req.Version); ok {
		t.Fatal("reopening selected an unconfirmed artifact")
	}
	tools, _, err = reopened.List(t.Context())
	if err != nil || len(tools) != 0 {
		t.Fatalf("reopened store exposed unconfirmed artifact: %+v, %v", tools, err)
	}
	if _, err := reopened.PreviewRemove(t.Context(), []string{tool.ID}); err == nil {
		t.Fatal("ordinary removal accepted an unconfirmed artifact")
	}
	if _, err := reopened.Install(t.Context(), req); err == nil || server.hits.Load() != 1 {
		t.Fatalf("unconfirmed install was replayed: %v; downloads %d", err, server.hits.Load())
	}
	if _, err := os.Stat(tool.Executable); err != nil {
		t.Fatalf("inspection path lost the artifact: %v", err)
	}
}

// An empty directory is still somebody else's data. Some operating systems
// allow os.Rename to replace it, so the actual publication must refuse it.
func TestPublicationDoesNotReplaceEmptyDirectory(t *testing.T) {
	store := newStore(t)
	server := serve(t, map[string][]byte{"/tool": []byte("fixture executable")})
	req := request(server.URL+"/tool", ArchiveRaw, "tool")
	final := filepath.Join(store.root, req.Package, versionDir(req.Version, digestOf([]byte("fixture executable"))))
	var before os.FileInfo
	req.AdmitPublication = func(context.Context) error {
		if err := os.Mkdir(final, 0700); err != nil {
			return err
		}
		var err error
		before, err = os.Stat(final)
		return err
	}
	_, err := store.Install(t.Context(), req)
	if before == nil {
		t.Fatalf("the test did not create the competing directory: %v", err)
	}
	if err == nil {
		t.Fatal("publication replaced an existing empty directory")
	}
	entries, err := os.ReadDir(final)
	if err != nil || len(entries) != 0 {
		t.Fatalf("existing directory changed: %v, %v", entries, err)
	}
	after, err := os.Stat(final)
	if err != nil || !os.SameFile(before, after) {
		t.Fatalf("the competing directory was replaced: %v", err)
	}
}

func TestPublicationDoesNotRemoveChangedPendingRecord(t *testing.T) {
	store := newStore(t)
	data := []byte("fixture executable")
	server := serve(t, map[string][]byte{"/tool": data})
	req := request(server.URL+"/tool", ArchiveRaw, "tool")
	id := req.Package + "/" + versionDir(req.Version, digestOf(data))
	marker := publicationMarker(filepath.Join(store.root, req.Package), filepath.Base(id))
	req.AdmitPublication = func(context.Context) error {
		if err := os.Remove(marker); err != nil {
			return err
		}
		return os.WriteFile(marker, []byte("another writer's record"), 0600)
	}
	tool, err := store.Install(t.Context(), req)
	var unconfirmed *PublicationUnconfirmedError
	if !errors.As(err, &unconfirmed) || unconfirmed.ToolID != id || tool.ID != id {
		t.Fatalf("changed pending record was accepted: %+v, %v", tool, err)
	}
	if content, err := os.ReadFile(marker); err != nil || string(content) != "another writer's record" {
		t.Fatalf("another writer's record was changed: %q, %v", content, err)
	}
	if _, ok := store.Lookup(req.Package, req.Version); ok {
		t.Fatal("a tool with changed publication evidence was offered for use")
	}
}

// If the publication record disappears after admission, refusing the result
// in memory is not enough: a reopened store must not select the artifact.
func TestMissingPublicationRecordRemainsUnconfirmedAfterReopen(t *testing.T) {
	store := newStore(t)
	data := []byte("fixture executable")
	server := serve(t, map[string][]byte{"/tool": data})
	req := request(server.URL+"/tool", ArchiveRaw, "tool")
	id := req.Package + "/" + versionDir(req.Version, digestOf(data))
	marker := publicationMarker(filepath.Join(store.root, req.Package), filepath.Base(id))
	req.AdmitPublication = func(context.Context) error { return os.Remove(marker) }

	tool, err := store.Install(t.Context(), req)
	var unconfirmed *PublicationUnconfirmedError
	if !errors.As(err, &unconfirmed) || unconfirmed.ToolID != id || tool.ID != id {
		t.Fatalf("missing publication record was accepted: %+v, %v", tool, err)
	}
	if _, err := os.Stat(tool.Executable); err != nil {
		t.Fatalf("test did not reach a real publication: %v", err)
	}
	if _, ok := store.Lookup(req.Package, req.Version); ok {
		t.Fatal("same process offered an unconfirmed tool")
	}

	reopened, err := Open(store.root)
	if err != nil {
		t.Fatal(err)
	}
	reopened.allowLoopbackHTTP = true
	if _, ok := reopened.Lookup(req.Package, req.Version); ok {
		t.Fatal("reopened store offered an unconfirmed tool")
	}
	_, err = reopened.Install(t.Context(), req)
	if !errors.As(err, &unconfirmed) || server.hits.Load() != 1 {
		t.Fatalf("reopened store retried an unconfirmed tool: %v; downloads %d", err, server.hits.Load())
	}
}

// A crash can leave a real installed tool in a removal staging folder. Opening
// the store must not treat that folder as disposable installation scratch.
func TestOpenPreservesInterruptedRemoval(t *testing.T) {
	store := newStore(t)
	server := serve(t, map[string][]byte{})
	tool := installVersion(t, store, server, "1.0", "installed before removal")
	if err := unsealDir(tool.Dir); err != nil {
		t.Fatal(err)
	}
	trash, err := os.MkdirTemp(filepath.Join(store.root, stagingName), "remove-")
	if err != nil {
		t.Fatal(err)
	}
	retained := filepath.Join(trash, "0")
	if err := os.Rename(tool.Dir, retained); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(store.root)
	if err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(retained, contentDir, "bin", "tool")
	if got, err := os.ReadFile(executable); err != nil || string(got) != "installed before removal" {
		t.Fatalf("interrupted removal lost its tool: %q, %v", got, err)
	}
	unknown, err := reopened.Unrecognized(t.Context())
	want := filepath.ToSlash(filepath.Join(stagingName, filepath.Base(trash)))
	if err != nil || !strings.Contains(strings.Join(unknown, "\n"), want) {
		t.Fatalf("interrupted removal was not listed for inspection: %v, %v", unknown, err)
	}
	if _, err := Open(store.root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(executable); err != nil {
		t.Fatalf("a second open removed the retained tool: %v", err)
	}
}

func TestHistoryDirectorySyncFailureKeepsPublicationUnconfirmed(t *testing.T) {
	store := newStore(t)
	server := serve(t, map[string][]byte{"/tool": []byte("fixture executable")})
	req := request(server.URL+"/tool", ArchiveRaw, "tool")
	failure := errors.New("history directory sync failed")
	store.syncHistoryDir = func(string) error { return failure }
	tool, err := store.Install(t.Context(), req)
	var unconfirmed *PublicationUnconfirmedError
	if !errors.As(err, &unconfirmed) || !errors.Is(err, failure) || tool.ID == "" {
		t.Fatalf("history durability failure was not retained: %+v, %v", tool, err)
	}
	if _, ok := store.Lookup(req.Package, req.Version); ok {
		t.Fatal("tool with unconfirmed history was offered for use")
	}
	reopened, err := Open(store.root)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reopened.Lookup(req.Package, req.Version); ok {
		t.Fatal("reopened store offered a tool with unconfirmed history")
	}
}

func TestFailureAfterPublicationRestoresMissingRecord(t *testing.T) {
	store := newStore(t)
	data := []byte("fixture executable")
	server := serve(t, map[string][]byte{"/tool": data})
	req := request(server.URL+"/tool", ArchiveRaw, "tool")
	id := req.Package + "/" + versionDir(req.Version, digestOf(data))
	marker := publicationMarker(filepath.Join(store.root, req.Package), filepath.Base(id))
	failure := errors.New("history directory sync failed")
	store.syncHistoryDir = func(string) error {
		if err := os.Remove(marker); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return failure
	}
	tool, err := store.Install(t.Context(), req)
	var unconfirmed *PublicationUnconfirmedError
	if !errors.As(err, &unconfirmed) || !errors.Is(err, failure) || tool.ID != id {
		t.Fatalf("publication failure was not retained: %+v, %v", tool, err)
	}
	if _, ok := store.Lookup(req.Package, req.Version); ok {
		t.Fatal("same process offered an unconfirmed tool")
	}
	reopened, err := Open(store.root)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reopened.Lookup(req.Package, req.Version); ok {
		t.Fatal("reopened store offered an unconfirmed tool after a failed history write")
	}
}
