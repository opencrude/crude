package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const projectContextDatabaseFixture = ` [
{"directory":"/producer/debug","file":"../src/unit.cc","arguments":["compiler","-DNAME=space value","@unread.rsp","../src/unit.cc"],"output":"debug.o"},
{"directory":"/producer/release","file":"../src/unit.cc","command":"must-not-execute && touch a-marker","output":"release.o"}
] `

func writeProjectContextFixture(t *testing.T, root, name, text string) {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
}

func awaitProjectContext(t *testing.T, p *ProjectContextService, state string) ProjectContextSnapshot {
	t.Helper()
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	for {
		_, changed := p.Changed()
		snapshot := p.Snapshot()
		if snapshot.State == state {
			return snapshot
		}
		select {
		case <-changed:
		case <-timer.C:
			t.Fatalf("context did not reach %q; observed %+v", state, snapshot)
		}
	}
}

func TestProjectContextObservesExactArtifactWithoutExecutingOrNormalizing(t *testing.T) {
	root := t.TempDir()
	writeProjectContextFixture(t, root, "build/commands.json", projectContextDatabaseFixture)
	p := NewProjectContextService(root, "workspace-1")
	t.Cleanup(p.Close)
	if err := p.Select([]ProjectArtifactSet{{ID: "native-build", CompilationDatabase: filepath.Join("build", "commands.json")}}); err != nil {
		t.Fatal(err)
	}
	snapshot := awaitProjectContext(t, p, "ready")
	hash := sha256.Sum256([]byte(projectContextDatabaseFixture))
	if snapshot.Owner != "workspace-1" || snapshot.Generation != 1 || snapshot.RootIdentity == "" ||
		len(snapshot.Artifacts) != 1 || snapshot.Artifacts[0].SHA256 != hex.EncodeToString(hash[:]) ||
		snapshot.Artifacts[0].Bytes != int64(len(projectContextDatabaseFixture)) || len(snapshot.UnknownInputs) == 0 {
		t.Fatal("artifact publication lost exact identity or overstated build coverage", snapshot)
	}
	database, ok := p.CompilationDatabase(snapshot.Generation, "native-build")
	if !ok || database.Len() != 2 {
		t.Fatal("selected immutable database not available to the exact context generation")
	}
	rows := database.Entries()
	if rows[0].Directory != "/producer/debug" || rows[0].File != "../src/unit.cc" || rows[0].Arguments[2] != "@unread.rsp" ||
		rows[1].Command != "must-not-execute && touch a-marker" || rows[1].Output != "release.o" {
		t.Fatal("observation resolved/normalized untrusted producer spellings", rows)
	}
	rows[0].Arguments[0] = "caller mutation"
	snapshot.Artifacts[0].SHA256, snapshot.UnknownInputs[0] = "caller mutation", "complete"
	again, _ := database.Entry(0)
	if again.Arguments[0] != "compiler" || p.Snapshot().Artifacts[0].SHA256 != hex.EncodeToString(hash[:]) ||
		p.Snapshot().UnknownInputs[0] == "complete" {
		t.Fatal("caller mutated retained context or immutable parser output")
	}
	if _, err := os.Stat(filepath.Join(root, "a-marker")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("metadata observation executed a producer command", err)
	}
}

func TestProjectContextInvalidationIsImmediateAndRetainsStaleEvidence(t *testing.T) {
	root := t.TempDir()
	writeProjectContextFixture(t, root, "commands.json", projectContextDatabaseFixture)
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	p := newProjectContextService(root, "workspace-1", func(ctx context.Context, root string, sets []ProjectArtifactSet) (projectContextData, string, error) {
		if calls.Add(1) == 2 {
			close(entered)
			<-release
		}
		return readProjectContext(ctx, root, sets)
	})
	t.Cleanup(p.Close)
	selected := []ProjectArtifactSet{{ID: "db", CompilationDatabase: "commands.json"}}
	if err := p.Select(selected); err != nil {
		t.Fatal(err)
	}
	before := awaitProjectContext(t, p, "ready")
	if err := p.Select(selected); err != nil || calls.Load() != 1 {
		t.Fatal("equal selection caused another read or failed", err)
	}
	writeProjectContextFixture(t, root, "commands.json", "not JSON private fixture")
	p.Invalidate()
	<-entered
	pending := p.Snapshot()
	if pending.State != "pending" || pending.Generation != before.Generation+1 ||
		!reflect.DeepEqual(pending.Artifacts, before.Artifacts) {
		close(release)
		t.Fatal("passive invalidation cleared evidence or failed to mark it stale", pending)
	}
	if _, ok := p.CompilationDatabase(before.Generation, "db"); ok {
		close(release)
		t.Fatal("stale database escaped as current")
	}
	close(release)
	failed := awaitProjectContext(t, p, "failed")
	if !reflect.DeepEqual(failed.Artifacts, before.Artifacts) || strings.Contains(failed.Detail, "private fixture") ||
		len(failed.UnknownInputs) == 0 {
		t.Fatal("invalid metadata cleared former observations or leaked its text", failed)
	}
	if err := p.Select(nil); err != nil {
		t.Fatal(err)
	}
	empty := awaitProjectContext(t, p, "unavailable")
	if !reflect.DeepEqual(empty.Artifacts, before.Artifacts) {
		t.Fatal("clearing selection fabricated clean evidence")
	}
}

func TestProjectContextCoalescesAndJoinsAnActuallyBlockedWorker(t *testing.T) {
	entered, release, second, secondRelease := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
	var releaseOnce, secondReleaseOnce sync.Once
	releaseFirst := func() { releaseOnce.Do(func() { close(release) }) }
	releaseSecond := func() { secondReleaseOnce.Do(func() { close(secondRelease) }) }
	var calls atomic.Int32
	p := newProjectContextService(t.TempDir(), "workspace-1", func(_ context.Context, _ string, selected []ProjectArtifactSet) (projectContextData, string, error) {
		switch calls.Add(1) {
		case 1:
			close(entered)
			<-release // intentionally ignores cancellation, testing actual join
		case 2:
			if len(selected) != 1 || selected[0].ID != "latest" || selected[0].CMakeReplies[0] != "model.json" {
				t.Error("intermediate/caller-mutated selection reached worker", selected)
			}
			close(second)
			<-secondRelease
		default:
			t.Error("unbounded extra worker/read")
		}
		return projectContextData{}, "native-root-fixture", nil
	})
	t.Cleanup(func() { releaseFirst(); releaseSecond(); p.Close() })
	if err := p.Select([]ProjectArtifactSet{{ID: "old", CompilationDatabase: "old.json"}}); err != nil {
		t.Fatal(err)
	}
	<-entered
	for _, id := range []string{"discarded", "latest"} {
		selected := []ProjectArtifactSet{{ID: id, CMakeIndex: "index.json", CMakeReplies: []string{"model.json"}}}
		if err := p.Select(selected); err != nil {
			t.Fatal(err)
		}
		selected[0].CMakeReplies[0] = "caller-mutation.json"
	}
	releaseFirst()
	<-second
	if snapshot := p.Snapshot(); snapshot.State != "pending" || snapshot.Generation != 3 || len(snapshot.Artifacts) != 0 {
		releaseSecond()
		t.Fatal("late old read published into latest context", snapshot)
	}
	closed := make(chan struct{})
	go func() { p.Close(); close(closed) }()
	awaitProjectContext(t, p, "closed")
	select {
	case <-closed:
		releaseSecond()
		t.Fatal("Close returned while its exact worker remained live")
	default:
	}
	releaseSecond()
	<-closed
	p.Close()
	if err := p.Select(nil); !errors.Is(err, ErrProjectContextClosed) {
		t.Fatal("closed owner admitted replacement work", err)
	}
	if calls.Load() != 2 || p.Snapshot().State != "closed" {
		t.Fatal("closed owner revived or replayed a superseded request")
	}
}

func TestProjectContextIdleFencePreservesInvalidationAndReleasesAtomically(t *testing.T) {
	root := t.TempDir()
	writeProjectContextFixture(t, root, "commands.json", "[]")
	p := NewProjectContextService(root, "workspace-1")
	t.Cleanup(p.Close)
	selection := []ProjectArtifactSet{{ID: "db", CompilationDatabase: "commands.json"}}
	if err := p.Select(selection); err != nil {
		t.Fatal(err)
	}
	before := awaitProjectContext(t, p, "ready")
	w := &Workspace{ProjectContext: p}
	release, err := w.BeginIdleLease(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if err := p.Select([]ProjectArtifactSet{{ID: "replacement", CompilationDatabase: "other.json"}}); !errors.Is(err, ErrProjectContextReserved) {
		t.Fatal("idle-reserved owner admitted new artifact observation", err)
	}
	p.Invalidate()
	snapshot := p.Snapshot()
	if snapshot.State != "pending" || snapshot.Generation != before.Generation+1 || snapshot.ObservedGeneration != before.Generation {
		t.Fatal("idle fence discarded passive invalidation or relabelled former evidence current", snapshot)
	}
	if u, err := w.BeginIdleLease(t.Context()); err == nil {
		u()
		t.Fatal("second idle lease bypassed the existing context reservation")
	}
	release()
	release()
	after := awaitProjectContext(t, p, "ready")
	if after.Generation != snapshot.Generation || after.ObservedGeneration != after.Generation ||
		!reflect.DeepEqual(after.Artifacts, before.Artifacts) {
		t.Fatal("rollback failed to recompute retained metadata intent exactly once", after)
	}
}

func TestWorkspaceProjectContextLifecycleReplacesOwnerAndJoinsOldWorker(t *testing.T) {
	w, _ := newSwitchPreflightWorkspace(t)
	old := w.ProjectContext
	if old == nil || old.Snapshot().Owner != w.LiveDocuments.Insight.generation {
		t.Fatal("workspace did not own one context for its source generation")
	}
	writeProjectContextFixture(t, w.RootPath, "commands.json", "[]")
	if err := old.Select([]ProjectArtifactSet{{ID: "db", CompilationDatabase: "commands.json"}}); err != nil {
		t.Fatal(err)
	}
	awaitProjectContext(t, old, "ready")
	if err := w.SwitchProject(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if old.Snapshot().State != "closed" || w.ProjectContext == old || w.ProjectContext.Snapshot().Owner == old.Snapshot().Owner ||
		w.ProjectContext.Snapshot().Owner != w.LiveDocuments.Insight.generation || len(w.ProjectContext.Snapshot().Artifacts) != 0 {
		t.Fatal("workspace replacement retained an old root/worker/observation authority")
	}
	w.Close()
	if w.ProjectContext.Snapshot().State != "closed" {
		t.Fatal("workspace teardown did not join its new context owner")
	}
}

func TestProjectContextRootReplacementRefusesOldSelection(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "root")
	writeProjectContextFixture(t, root, "commands.json", projectContextDatabaseFixture)
	p := NewProjectContextService(root, "workspace-1")
	t.Cleanup(p.Close)
	if err := p.Select([]ProjectArtifactSet{{ID: "db", CompilationDatabase: "commands.json"}}); err != nil {
		t.Fatal(err)
	}
	before := awaitProjectContext(t, p, "ready")
	if err := os.Rename(root, filepath.Join(parent, "old-root")); err != nil {
		t.Fatal(err)
	}
	writeProjectContextFixture(t, root, "commands.json", "[]")
	p.Invalidate()
	after := awaitProjectContext(t, p, "failed")
	if after.RootIdentity != before.RootIdentity || !reflect.DeepEqual(after.Artifacts, before.Artifacts) ||
		!strings.Contains(after.Detail, "identity changed") {
		t.Fatal("same-path replacement silently rebound the old context owner", after)
	}
}

func TestProjectContextRechecksEveryFileAfterWholeSetDerivation(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"a.json", "b.json"} {
		writeProjectContextFixture(t, root, name, "[]")
	}
	read := func(ctx context.Context, root, path string, budget int64) ([]byte, projectArtifactStamp, error) {
		if path == "b.json" {
			if err := os.Remove(filepath.Join(root, "a.json")); err != nil {
				t.Fatal(err)
			}
			writeProjectContextFixture(t, root, "a.json", "[]")
		}
		return readProjectArtifact(ctx, root, path, budget)
	}
	selection := []ProjectArtifactSet{{ID: "a", CompilationDatabase: "a.json"}, {ID: "b", CompilationDatabase: "b.json"}}
	data, _, err := readProjectContextWith(t.Context(), root, selection, read)
	if !errors.Is(err, ErrProjectContextChanged) || len(data.identities) != 0 || len(data.compilation) != 0 {
		t.Fatal("mixed-time artifact set was published after an earlier identity changed", err)
	}
}

func TestProjectContextCMakeReferencesAreNotReadPermissions(t *testing.T) {
	root := t.TempDir()
	writeProjectContextFixture(t, root, "index.json", `{"objects":[{"kind":"codemodel","version":{"major":2,"minor":0},"jsonFile":"model.json"}]}`)
	writeProjectContextFixture(t, root, "model.json", `{"kind":"codemodel","version":{"major":2,"minor":0},
"paths":{"source":"/producer","build":"/build"},
"configurations":[{"name":"Debug","directories":[{"source":".","build":".","projectIndex":0}],
"projects":[{"name":"Fixture","directoryIndexes":[0]}],"targets":[]}]}`)
	p := NewProjectContextService(root, "workspace-1")
	t.Cleanup(p.Close)
	if err := p.Select([]ProjectArtifactSet{{ID: "cmake", CMakeIndex: "index.json"}}); err != nil {
		t.Fatal(err)
	}
	awaitProjectContext(t, p, "failed")
	if _, ok := p.CMake(p.Snapshot().Generation, "cmake"); ok {
		t.Fatal("JSON reference widened the explicit artifact selection")
	}
	selected := []ProjectArtifactSet{{ID: "cmake", CMakeIndex: "index.json", CMakeReplies: []string{"model.json"}}}
	if err := p.Select(selected); err != nil {
		t.Fatal(err)
	}
	snapshot := awaitProjectContext(t, p, "ready")
	model, ok := p.CMake(snapshot.Generation, "cmake")
	if !ok || model.Snapshot().Paths.Source != "/producer" || len(snapshot.Artifacts) != 2 || len(snapshot.UnknownInputs) == 0 {
		t.Fatal("explicit artifact set lost provenance or claimed complete project context", snapshot)
	}
}
