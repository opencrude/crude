package localhistory

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gofrs/flock"
)

func selectedSnapshot(t *testing.T, store *Store, reference string, files ...RetainedFile) Manifest {
	t.Helper()
	manifest, err := store.SnapshotRetainedFiles(context.Background(), reference, "Reviewed edit", files)
	if err != nil {
		t.Fatal(err)
	}
	return manifest
}

func assertSelectedScope(t *testing.T, store *Store, id string, paths ...string) Manifest {
	t.Helper()
	manifest, err := store.LoadManifest(id)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(manifest.ScopePaths, paths) || len(manifest.TrackedPaths) != 0 {
		t.Fatalf("wrong coverage: scope=%v tracked=%v want=%v", manifest.ScopePaths, manifest.TrackedPaths, paths)
	}
	for path := range manifest.Entries {
		found := false
		for _, expected := range paths {
			found = found || expected == path
		}
		if !found {
			t.Fatalf("retained unrelated entry %s", path)
		}
	}
	return manifest
}

func TestSelectedSnapshotStoresOnlyDeclaredPreviewBytesWithoutReadingFiles(t *testing.T) {
	store, workspace := newTestStore(t)
	writeTestFile(t, workspace, ".gitignore", []byte("private/\n"), 0o600)
	writeTestFile(t, workspace, "private/secret", []byte("never retain private data"), 0o600)
	writeTestFile(t, workspace, "unrelated", []byte("never retain unselected data"), 0o600)
	writeTestFile(t, workspace, "large", bytes.Repeat([]byte("x"), selectedMaxFile+1), 0o600)
	writeTestFile(t, workspace, "unreadable/secret", []byte("cannot read this"), 0o600)
	if err := os.Chmod(filepath.Join(workspace, "unreadable"), 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(workspace, "unreadable"), 0o700) })
	writeTestFile(t, workspace, "z.txt", []byte("different disk bytes"), 0o600)
	// a.txt is absent: storage must still retain the exact preview preimage.
	data := []byte("preview\x00bytes\r\n")
	files := []RetainedFile{{Path: "z.txt", Data: []byte("z preview"), Mode: uint32(storedMode(0o755))}, {Path: "a.txt", Data: data, Mode: uint32(storedMode(0o640))}}
	manifest := selectedSnapshot(t, store, "selected-1", files...)
	assertSelectedScope(t, store, manifest.ID, "a.txt", "z.txt")
	if len(manifest.Entries) != 2 || files[0].Path != "z.txt" {
		t.Fatalf("wrong entries or mutated caller order: %v", manifest.Entries)
	}
	for _, file := range files {
		entry := manifest.Entries[file.Path]
		if entry.Type != EntryFile || entry.Mode != file.Mode || entry.Hash != sha256Hex(file.Data) || entry.Size != int64(len(file.Data)) {
			t.Fatalf("wrong preimage for %s: %+v", file.Path, entry)
		}
		got, err := store.readFileBytes(entry.Hash)
		if err != nil || !bytes.Equal(got, file.Data) {
			t.Fatalf("wrong bytes: %q err=%v", got, err)
		}
	}
	data[0] = 'X'
	got, err := store.readFileBytes(manifest.Entries["a.txt"].Hash)
	if err != nil || string(got) != "preview\x00bytes\r\n" {
		t.Fatalf("caller changed immutable snapshot: %q err=%v", got, err)
	}
	stats, err := store.Stats()
	if err != nil || stats.Snapshots != 1 || stats.FileObjects != 2 || stats.ChunkObjects != 2 {
		t.Fatalf("retained unrelated objects: %+v err=%v", stats, err)
	}
	for _, content := range []string{"never retain private data", "never retain unselected data", "different disk bytes"} {
		if _, err := store.readFileObject(sha256Hex([]byte(content))); !os.IsNotExist(err) {
			t.Fatalf("unrelated content stored: %q err=%v", content, err)
		}
	}
}

func TestSelectedSnapshotRejectsUnboundedAndInvalidShapesBeforeStoreAccess(t *testing.T) {
	valid := RetainedFile{Path: "file", Data: []byte("data"), Mode: uint32(storedMode(0o644))}
	aggregate := make([]RetainedFile, 9)
	for i := range aggregate {
		aggregate[i] = RetainedFile{Path: fmt.Sprintf("%d", i), Data: make([]byte, selectedMaxFile), Mode: valid.Mode}
	}
	cases := []struct {
		name, reference, label string
		files                  []RetainedFile
	}{
		{"empty", "ref", "", nil},
		{"count", "ref", "", make([]RetainedFile, 201)},
		{"aggregate", "ref", "", aggregate},
		{"duplicate", "ref", "", []RetainedFile{valid, valid}},
		{"reference", "../bad", "", []RetainedFile{valid}},
		{"long-reference", strings.Repeat("a", 129), "", []RetainedFile{valid}},
		{"label", "ref", strings.Repeat("x", 257), []RetainedFile{valid}},
		{"label-control", "ref", "bad\nlabel", []RetainedFile{valid}},
		{"label-utf8", "ref", "\xff", []RetainedFile{valid}},
	}
	for _, path := range []string{"", ".", "..", "../outside", "/absolute", "a/../b", "a//b", "a/", "./a", `a\b`, "C:file", ".git", ".GIT/config", "nested/.git/config", "a\x00b", "\xff", strings.Repeat("a", 4097)} {
		file := valid
		file.Path = path
		cases = append(cases, struct {
			name, reference, label string
			files                  []RetainedFile
		}{"path-" + path, "ref", "", []RetainedFile{file}})
	}
	for _, mode := range []os.FileMode{os.ModeDir | 0o755, os.ModeSymlink | 0o644, os.ModeNamedPipe | 0o600, os.ModeSocket | 0o600, os.ModeDevice | 0o600, os.ModeAppend | 0o600, os.ModeExclusive | 0o600, 0o100644} {
		file := valid
		file.Mode = uint32(mode)
		cases = append(cases, struct {
			name, reference, label string
			files                  []RetainedFile
		}{fmt.Sprintf("mode-%x", mode), "ref", "", []RetainedFile{file}})
	}
	file := valid
	file.Data = make([]byte, selectedMaxFile+1)
	cases = append(cases, struct {
		name, reference, label string
		files                  []RetainedFile
	}{"file-size", "ref", "", []RetainedFile{file}})
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var store *Store // Invalid input must fail before any receiver access.
			if manifest, err := store.SnapshotRetainedFiles(context.Background(), tc.reference, tc.label, tc.files); err == nil || manifest.ID != "" {
				t.Fatalf("accepted invalid input: %+v err=%v", manifest, err)
			}
		})
	}
}

func TestSelectedSnapshotAcceptsInclusiveBoundsAndUnsortedKeys(t *testing.T) {
	store, _ := newTestStore(t)
	files := make([]RetainedFile, 200)
	for i := range files {
		files[i] = RetainedFile{Path: fmt.Sprintf("%03d", 199-i), Mode: uint32(storedMode(0o600))}
		if i < 8 {
			files[i].Data = bytes.Repeat([]byte{byte(i)}, selectedMaxFile)
		}
	}
	manifest, err := store.SnapshotRetainedFiles(context.Background(), strings.Repeat("a", 128), strings.Repeat("x", 256), files)
	if err != nil || len(manifest.Entries) != 200 || manifest.ScopePaths[0] != "000" || manifest.ScopePaths[199] != "199" {
		t.Fatalf("inclusive bounds rejected: entries=%d err=%v", len(manifest.Entries), err)
	}
	// A 4096-byte logical key need not exist on disk and must not be opened.
	manifest = selectedSnapshot(t, store, "max-path", RetainedFile{Path: strings.Repeat("p", 4096), Mode: uint32(storedMode(0o600))})
	if len(manifest.ScopePaths[0]) != 4096 {
		t.Fatal("maximum logical path was not retained")
	}
}

func TestSelectedRestoreDefaultOnlyObservesSelectedFilesAndKeepsSnapshotsBounded(t *testing.T) {
	store, workspace := newTestStore(t)
	before := selectedSnapshot(t, store, "before", RetainedFile{Path: "nested/selected", Data: []byte("preview"), Mode: uint32(storedMode(0o751))})
	writeTestFile(t, workspace, "nested/selected", []byte("current"), 0o600)
	writeTestFile(t, workspace, "unrelated", []byte("preserve me"), 0o600)
	// Recursive full scanning would fail on this malformed ignore file.
	writeTestFile(t, workspace, "private/.gitignore", []byte("x"), 0o600)
	if err := os.Mkdir(filepath.Join(workspace, ".crudehistoryignore"), 0o700); err != nil {
		t.Fatal(err)
	}
	plan, err := store.PlanRestore(context.Background(), before.ID, SnapshotOptions{})
	if err != nil || len(plan.Changes) != 1 || plan.Changes[0].Path != "nested/selected" || plan.Changes[0].Action != ChangeRestore {
		t.Fatalf("selected default restore leaked scope: %+v err=%v", plan, err)
	}
	if !strings.Contains(plan.Changes[0].TextDiff, "preview") {
		t.Fatal("selected preview missing target content")
	}
	result, err := store.ApplyRestore(context.Background(), plan.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	safety := assertSelectedScope(t, store, result.SafetySnapshotID, "nested/selected")
	after := assertSelectedScope(t, store, result.AfterSnapshotID, "nested/selected")
	if string(readTestFile(t, workspace, "nested/selected")) != "preview" || string(readTestFile(t, workspace, "unrelated")) != "preserve me" {
		t.Fatal("selected restore changed unrelated files or lost target")
	}
	if safety.Entries["nested/selected"].Hash != sha256Hex([]byte("current")) || after.Entries["nested/selected"] != before.Entries["nested/selected"] {
		t.Fatalf("wrong selected safety/after entries: %+v %+v", safety, after)
	}
	// The safety inverse is itself scoped and restores just the prior state.
	inverse, err := store.PlanRestore(context.Background(), safety.ID, SnapshotOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ApplyRestore(context.Background(), inverse.ID, nil); err != nil {
		t.Fatal(err)
	}
	if string(readTestFile(t, workspace, "nested/selected")) != "current" || string(readTestFile(t, workspace, "unrelated")) != "preserve me" {
		t.Fatal("inverse escaped selected scope")
	}
}

func TestSelectedRestoreMissingPathHasExplicitAbsentSafetyAndInverseDelete(t *testing.T) {
	store, workspace := newTestStore(t)
	before := selectedSnapshot(t, store, "before", RetainedFile{Path: "missing", Data: []byte("preview"), Mode: uint32(storedMode(0o644))})
	plan, err := store.PlanRestore(context.Background(), before.ID, SnapshotOptions{})
	if err != nil || len(plan.Changes) != 1 || plan.Changes[0].Action != ChangeCreate || plan.Changes[0].Current != nil {
		t.Fatalf("missing path not observed absent: %+v err=%v", plan, err)
	}
	result, err := store.ApplyRestore(context.Background(), plan.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	safety := assertSelectedScope(t, store, result.SafetySnapshotID, "missing")
	if len(safety.Entries) != 0 {
		t.Fatalf("missing state not explicit selected absence: %+v", safety)
	}
	writeTestFile(t, workspace, "unrelated", []byte("leave"), 0o600)
	inverse, err := store.PlanRestore(context.Background(), safety.ID, SnapshotOptions{})
	if err != nil || len(inverse.Changes) != 1 || inverse.Changes[0].Action != ChangeDelete {
		t.Fatalf("absent inverse not selective delete: %+v err=%v", inverse, err)
	}
	if _, err := store.ApplyRestore(context.Background(), inverse.ID, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(workspace, "missing")); !os.IsNotExist(err) {
		t.Fatalf("inverse did not delete selected key: %v", err)
	}
	if string(readTestFile(t, workspace, "unrelated")) != "leave" {
		t.Fatal("inverse deleted unknown unselected state")
	}
}

func TestSelectedRestoreRefusesNonregularOversizedAndSymlinkParents(t *testing.T) {
	for _, kind := range []string{"oversized", "directory", "symlink", "symlink-parent"} {
		t.Run(kind, func(t *testing.T) {
			store, workspace := newTestStore(t)
			before := selectedSnapshot(t, store, "before", RetainedFile{Path: "nested/file", Data: []byte("base"), Mode: uint32(storedMode(0o644))})
			switch kind {
			case "oversized":
				writeTestFile(t, workspace, "nested/file", make([]byte, selectedMaxFile+1), 0o600)
			case "directory":
				if err := os.MkdirAll(filepath.Join(workspace, "nested/file"), 0o700); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				writeTestFile(t, workspace, "nested/other", []byte("other"), 0o600)
				if err := os.Symlink("other", filepath.Join(workspace, "nested/file")); err != nil {
					t.Skip(err)
				}
			case "symlink-parent":
				if err := os.Symlink(t.TempDir(), filepath.Join(workspace, "nested")); err != nil {
					t.Skip(err)
				}
			}
			if plan, err := store.PlanRestore(context.Background(), before.ID, SnapshotOptions{}); err == nil || plan.ID != "" {
				t.Fatalf("unsafe selected path accepted: %+v err=%v", plan, err)
			}
		})
	}
}

func TestSelectedRestorePathSubsetDoesNotReadOtherScopedKeys(t *testing.T) {
	store, workspace := newTestStore(t)
	before := selectedSnapshot(t, store, "before",
		RetainedFile{Path: "a", Data: []byte("base a"), Mode: uint32(storedMode(0o600))},
		RetainedFile{Path: "b", Data: []byte("base b"), Mode: uint32(storedMode(0o600))})
	writeTestFile(t, workspace, "a", []byte("current a"), 0o600)
	writeTestFile(t, workspace, "b", make([]byte, selectedMaxFile+1), 0o600)
	plan, err := store.PlanRestorePaths(context.Background(), before.ID, SnapshotOptions{}, []string{"unknown", "a", "a"})
	if err != nil || len(plan.Changes) != 1 || plan.Changes[0].Path != "a" {
		t.Fatalf("subset read unrelated scoped key: %+v err=%v", plan, err)
	}
	result, err := store.ApplyRestore(context.Background(), plan.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertSelectedScope(t, store, result.SafetySnapshotID, "a")
	assertSelectedScope(t, store, result.AfterSnapshotID, "a")
	if info, err := os.Stat(filepath.Join(workspace, "b")); err != nil || info.Size() != selectedMaxFile+1 {
		t.Fatalf("subset changed other scoped key: %v err=%v", info, err)
	}
}

func TestSelectedRestoreUsesCanonicalRollbackAtEveryApplyBoundary(t *testing.T) {
	for failAt := 0; failAt < 2; failAt++ {
		t.Run(fmt.Sprint(failAt), func(t *testing.T) {
			store, workspace := newTestStore(t)
			before := selectedSnapshot(t, store, "before",
				RetainedFile{Path: "a", Data: []byte("base a"), Mode: uint32(storedMode(0o600))},
				RetainedFile{Path: "b", Data: []byte("base b"), Mode: uint32(storedMode(0o600))})
			writeTestFile(t, workspace, "a", []byte("current a"), 0o600)
			writeTestFile(t, workspace, "b", []byte("current b"), 0o600)
			writeTestFile(t, workspace, "unrelated", []byte("leave"), 0o600)
			plan, err := store.PlanRestore(context.Background(), before.ID, SnapshotOptions{})
			if err != nil {
				t.Fatal(err)
			}
			store.beforeApply = func(index int, _ Change) error {
				if index == failAt {
					return errors.New("injected canonical apply failure")
				}
				return nil
			}
			if _, err := store.ApplyRestore(context.Background(), plan.ID, nil); err == nil {
				t.Fatal("failure ignored")
			}
			for _, path := range []string{"a", "b"} {
				if string(readTestFile(t, workspace, path)) != "current "+path {
					t.Fatalf("canonical rollback lost %s", path)
				}
			}
			if string(readTestFile(t, workspace, "unrelated")) != "leave" {
				t.Fatal("rollback escaped scope")
			}
		})
	}
}

func TestSelectedDiffDoesNotTreatUnknownKeysAsAbsence(t *testing.T) {
	store, workspace := newTestStore(t)
	writeTestFile(t, workspace, "a", []byte("a"), 0o600)
	writeTestFile(t, workspace, "b", []byte("b"), 0o600)
	full, err := store.Snapshot(context.Background(), SnapshotOptions{})
	if err != nil {
		t.Fatal(err)
	}
	a := selectedSnapshot(t, store, "a", RetainedFile{Path: "a", Data: []byte("changed"), Mode: uint32(storedMode(0o600))})
	b := selectedSnapshot(t, store, "b", RetainedFile{Path: "b", Data: []byte("changed"), Mode: uint32(storedMode(0o600))})
	for _, ids := range [][2]string{{a.ID, b.ID}, {b.ID, a.ID}} {
		if paths, err := store.DiffSnapshotPaths(ids[0], ids[1]); err != nil || len(paths) != 0 {
			t.Fatalf("diff claimed unknown state: %v err=%v", paths, err)
		}
	}
	for _, ids := range [][2]string{{full.ID, a.ID}, {a.ID, full.ID}} {
		if paths, err := store.DiffSnapshotPaths(ids[0], ids[1]); err != nil || !reflect.DeepEqual(paths, []string{"a"}) {
			t.Fatalf("diff omitted observed change or claimed unknown key: %v err=%v", paths, err)
		}
	}
}

func TestSelectedRollbackRefusesUnobservedSafetyKeys(t *testing.T) {
	store, workspace := newTestStore(t)
	before := selectedSnapshot(t, store, "before", RetainedFile{Path: "selected", Data: []byte("base"), Mode: uint32(storedMode(0o600))})
	writeTestFile(t, workspace, "unrelated", []byte("leave"), 0o600)
	current := Entry{Type: EntryFile, Mode: uint32(storedMode(0o600)), Hash: sha256Hex([]byte("leave")), Size: 5}
	if changes, _, err := store.safeRollbackChanges(before, []Change{{Path: "unrelated", Target: &current}}); err == nil || changes != nil {
		t.Fatalf("rollback interpreted unknown as absent: %+v err=%v", changes, err)
	}
	if string(readTestFile(t, workspace, "unrelated")) != "leave" {
		t.Fatal("rollback changed unobserved key")
	}
}

func TestSelectedManifestValidationOnSaveAndLoad(t *testing.T) {
	store, _ := newTestStore(t)
	base := selectedSnapshot(t, store, "base", RetainedFile{Path: "a", Data: []byte("a"), Mode: uint32(storedMode(0o600))})
	mutations := []func(*Manifest){
		func(m *Manifest) { m.ScopePaths = []string{} },
		func(m *Manifest) { m.ScopePaths = []string{"b", "a"} },
		func(m *Manifest) { m.ScopePaths = []string{"a", "a"} },
		func(m *Manifest) { m.ScopePaths = []string{"../a"} },
		func(m *Manifest) { m.ScopePaths = []string{".git/config"} },
		func(m *Manifest) { m.ScopePaths = make([]string, 201) },
		func(m *Manifest) { m.TrackedPaths = []string{"/external"} },
		func(m *Manifest) { m.Entries["unknown"] = m.Entries["a"] },
		func(m *Manifest) { e := m.Entries["a"]; e.Type = EntryDirectory; m.Entries["a"] = e },
		func(m *Manifest) { e := m.Entries["a"]; e.Mode = uint32(os.ModeSymlink); m.Entries["a"] = e },
		func(m *Manifest) { e := m.Entries["a"]; e.Size = selectedMaxFile + 1; m.Entries["a"] = e },
		func(m *Manifest) { e := m.Entries["a"]; e.Size = -1; m.Entries["a"] = e },
		func(m *Manifest) { e := m.Entries["a"]; e.Hash = "bad"; m.Entries["a"] = e },
		func(m *Manifest) { e := m.Entries["a"]; e.HardlinkTarget = "other"; m.Entries["a"] = e },
		func(m *Manifest) { m.Label = "bad\nlabel" },
		func(m *Manifest) {
			m.ScopePaths = nil
			m.Entries = make(map[string]Entry)
			for i := range 9 {
				path := fmt.Sprint(i)
				m.ScopePaths = append(m.ScopePaths, path)
				entry := base.Entries["a"]
				entry.Size = selectedMaxFile
				m.Entries[path] = entry
			}
		},
	}
	for i, mutate := range mutations {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			m := base
			m.Entries = map[string]Entry{"a": base.Entries["a"]}
			mutate(&m)
			if err := store.saveManifest(&m); err == nil {
				t.Fatal("saved invalid scoped manifest")
			}
			// Publish a correctly hashed but invalid manifest to prove Load also
			// checks shape, not merely its content-addressed identity.
			if len(m.ScopePaths) == 0 {
				return // Empty omitempty slices have no serialized identity.
			}
			m.ID = ""
			payload, err := json.Marshal(m)
			if err != nil {
				t.Fatal(err)
			}
			m.ID = sha256Hex(payload)
			payload, err = json.Marshal(m)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(store.root, "manifests", m.ID+".json"), payload, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := store.LoadManifest(m.ID); err == nil {
				t.Fatal("loaded invalid selected manifest")
			}
		})
	}
	// Full snapshot identity is unchanged: the new nil coverage is omitted.
	base.ScopePaths = nil
	data, err := json.Marshal(base)
	if err != nil || bytes.Contains(data, []byte("scope_paths")) {
		t.Fatalf("nil scope changed full manifest bytes: %s err=%v", data, err)
	}
}

func TestSelectedRetainedEvidenceAndPublicationFailuresFailClosed(t *testing.T) {
	for _, failure := range []string{"index publication", "missing indexed entry", "missing manifest", "corrupt object", "corrupt reused chunk"} {
		t.Run(failure, func(t *testing.T) {
			store, _ := newTestStore(t)
			file := RetainedFile{Path: "file", Data: []byte("original"), Mode: uint32(storedMode(0o600))}
			manifest := selectedSnapshot(t, store, "review", file)
			switch failure {
			case "index publication":
				if _, err := store.db.Exec(`CREATE TRIGGER reject_selected_index BEFORE INSERT ON history_snapshots BEGIN SELECT RAISE(ABORT,'index failed'); END`); err != nil {
					t.Fatal(err)
				}
			case "missing indexed entry":
				if _, err := store.db.Exec(`DELETE FROM history_snapshot_entries WHERE snapshot_id=?`, manifest.ID); err != nil {
					t.Fatal(err)
				}
			case "missing manifest":
				if err := os.Remove(filepath.Join(store.root, "manifests", manifest.ID+".json")); err != nil {
					t.Fatal(err)
				}
			case "corrupt object":
				path, err := objectPath(store.root, "files", manifest.Entries["file"].Hash, ".json")
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("corrupt"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "corrupt reused chunk":
				path, err := objectPath(store.root, "chunks", sha256Hex(file.Data), "")
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("corrupt"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if failure == "index publication" || failure == "corrupt reused chunk" {
				got, err := store.SnapshotRetainedFiles(context.Background(), "new", "", []RetainedFile{file})
				if err == nil || got.ID != "" {
					t.Fatalf("failed publication claimed success: %+v err=%v", got, err)
				}
				if _, err := store.LoadRetainedSnapshot(context.Background(), "new"); err == nil {
					t.Fatal("failed publication acquired reference")
				}
			}
			if failure != "index publication" {
				if _, err := store.LoadRetainedSnapshot(context.Background(), "review"); err == nil {
					t.Fatal("loaded damaged selected evidence")
				}
				if _, err := store.Prune(context.Background(), RetentionPolicy{BudgetBytes: 1}); err == nil {
					t.Fatal("prune accepted damaged selected reference")
				}
			}
		})
	}
}

func TestSelectedSnapshotUsesProcessGateAndSurvivesConcurrentPrune(t *testing.T) {
	store, _ := newTestStore(t)
	file := RetainedFile{Path: "file", Data: []byte("preimage"), Mode: uint32(storedMode(0o600))}
	lock := flock.New(filepath.Join(store.root, "mutation.lock"))
	if err := lock.Lock(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	_, err := store.SnapshotRetainedFiles(ctx, "blocked", "", []RetainedFile{file})
	cancel()
	if unlockErr := lock.Unlock(); unlockErr != nil {
		t.Fatal(unlockErr)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("selected capture bypassed process gate: %v", err)
	}
	ctx = context.Background()
	const captures = 12
	var wg sync.WaitGroup
	start := make(chan struct{})
	results := make(chan error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		for i := range captures {
			ref := fmt.Sprintf("review-%d", i)
			manifest, err := store.SnapshotRetainedFiles(ctx, ref, "", []RetainedFile{file})
			if err != nil {
				results <- err
				return
			}
			if loaded, err := store.LoadRetainedSnapshot(ctx, ref); err != nil || loaded.ID != manifest.ID {
				results <- fmt.Errorf("lost selected capture %s: %v", ref, err)
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		<-start
		for range captures {
			if _, err := store.Snapshot(ctx, SnapshotOptions{}); err != nil {
				results <- err
				return
			}
			if _, err := store.Prune(ctx, RetentionPolicy{MaxSnapshots: 1, BudgetBytes: 1}); err != nil {
				results <- err
				return
			}
		}
	}()
	close(start)
	wg.Wait()
	close(results)
	for err := range results {
		t.Error(err)
	}
	for i := range captures {
		if _, err := store.LoadRetainedSnapshot(ctx, fmt.Sprintf("review-%d", i)); err != nil {
			t.Fatalf("selected reference lost after prune: %v", err)
		}
	}
}

func TestSelectedRetainedPinDuplicatePruneReopenAndFailure(t *testing.T) {
	store, workspace := newTestStore(t)
	original := selectedSnapshot(t, store, "review", RetainedFile{Path: "missing", Data: []byte("immutable"), Mode: uint32(storedMode(0o751))})
	before, err := store.Stats()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SnapshotRetainedFiles(context.Background(), "review", "", []RetainedFile{{Path: "different", Data: []byte("later"), Mode: uint32(storedMode(0o600))}}); err == nil {
		t.Fatal("duplicate replaced original")
	}
	after, err := store.Stats()
	if err != nil || after != before {
		t.Fatalf("duplicate captured new evidence: before=%+v after=%+v err=%v", before, after, err)
	}
	selectedSnapshot(t, store, "other", RetainedFile{Path: "other", Data: []byte("other"), Mode: uint32(storedMode(0o600))})
	if _, err := store.Prune(context.Background(), RetentionPolicy{MaxSnapshots: 1, BudgetBytes: 1}); err != nil {
		t.Fatal(err)
	}
	root := store.root
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(root, workspace)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	loaded, err := store.LoadRetainedSnapshot(context.Background(), "review")
	if err != nil || !reflect.DeepEqual(loaded, original) {
		t.Fatalf("retained selected pin changed: %+v err=%v", loaded, err)
	}
	if _, err := store.db.Exec(`CREATE TRIGGER reject_selected_pin BEFORE INSERT ON history_retained_snapshots BEGIN SELECT RAISE(ABORT,'pin failed'); END`); err != nil {
		t.Fatal(err)
	}
	if manifest, err := store.SnapshotRetainedFiles(context.Background(), "fails", "", []RetainedFile{{Path: "file", Data: []byte("not success"), Mode: uint32(storedMode(0o600))}}); err == nil || manifest.ID != "" {
		t.Fatalf("pin failure claimed success: %+v err=%v", manifest, err)
	}
}

func TestSelectedSnapshotRejectsRelocatedGitAdministrationAndChangedRoot(t *testing.T) {
	t.Run("git", func(t *testing.T) {
		store, workspace := newTestStore(t)
		writeTestFile(t, workspace, ".git", []byte("gitdir: admin/worktree\n"), 0o600)
		writeTestFile(t, workspace, "admin/worktree/commondir", []byte("../common\n"), 0o600)
		writeTestFile(t, workspace, "admin/common/config", []byte("private admin bytes"), 0o600)
		for _, path := range []string{"admin/worktree/HEAD", "admin/common/config"} {
			if manifest, err := store.SnapshotRetainedFiles(context.Background(), "ref", "", []RetainedFile{{Path: path, Data: []byte("supplied"), Mode: uint32(storedMode(0o600))}}); err == nil || manifest.ID != "" {
				t.Fatalf("retained Git administration %s: %+v err=%v", path, manifest, err)
			}
		}
	})
	t.Run("root", func(t *testing.T) {
		store, workspace := newTestStore(t)
		if err := os.Rename(workspace, workspace+"-original"); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.RemoveAll(workspace + "-original") })
		if err := os.Mkdir(workspace, 0o700); err != nil {
			t.Fatal(err)
		}
		if _, err := store.SnapshotRetainedFiles(context.Background(), "ref", "", []RetainedFile{{Path: "file", Mode: uint32(storedMode(0o600))}}); err == nil {
			t.Fatal("selected snapshot accepted changed root")
		}
	})
}
