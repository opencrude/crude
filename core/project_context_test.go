package core

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"

	"crude/coreprotocol"
	"crude/types"
	"crude/workspace"
)

func TestRetainedCoreWatcherInvalidatesSelectedArtifactsWithoutAShell(t *testing.T) {
	setCoreTestHome(t, t.TempDir())
	root := t.TempDir()
	artifact := filepath.Join(root, "compile_commands.json")
	if err := os.WriteFile(artifact, []byte("[]"), 0600); err != nil {
		t.Fatal(err)
	}
	runtime, err := New(func(root string) (*workspace.Workspace, error) {
		w := workspace.NewWorkspace(root, "test", "tester", types.Settings{}, coreTestLogger{})
		w.SetAppDefaults(types.AppSettings{LocalHistory: types.LocalHistorySettings{Enabled: "off"}})
		return w, nil
	}, filepath.Join(t.TempDir(), "receipts.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	a, err := runtime.workspace(t.Context(), "artifact-workspace", root)
	if err != nil {
		t.Fatal(err)
	}
	if a.fileWatch == nil {
		t.Fatal("real retained-core watcher unavailable (not a pass)")
	}
	<-a.fileWatch.ready
	owner := a.ws.ProjectContext
	if owner == nil {
		t.Fatal("factory did not retain its context owner")
	}
	if err := owner.Select([]workspace.ProjectArtifactSet{{ID: "build", CompilationDatabase: "compile_commands.json"}}); err != nil {
		t.Fatal(err)
	}
	wait := func(greaterThan uint64, digest string) workspace.ProjectContextSnapshot {
		t.Helper()
		timer := time.NewTimer(5 * time.Second)
		defer timer.Stop()
		for {
			_, changed := owner.Changed()
			view := owner.Snapshot()
			if view.State == "ready" && view.Generation > greaterThan && len(view.Artifacts) == 1 && view.Artifacts[0].SHA256 == digest {
				return view
			}
			select {
			case <-changed:
			case <-timer.C:
				t.Fatal("retained watcher did not refresh exact selected-artifact evidence", view)
			}
		}
	}
	firstHash := sha256.Sum256([]byte("[]"))
	before := wait(0, hex.EncodeToString(firstHash[:]))
	replacement := `[{"directory":"/producer","file":"unit.cc","arguments":["compiler","unit.cc"]}]`
	if err := os.WriteFile(artifact, []byte(replacement), 0600); err != nil {
		t.Fatal(err)
	}
	want := sha256.Sum256([]byte(replacement))
	after := wait(before.Generation, hex.EncodeToString(want[:]))
	if after.Owner != before.Owner || after.ObservedGeneration != after.Generation || len(after.UnknownInputs) == 0 {
		t.Fatal("file event replaced the retained owner or fabricated complete context", after)
	}
	if err := a.inspect(t.Context(), func() {
		if len(a.subscribers) != 0 {
			t.Error("fixture accidentally relied on an attached shell")
		}
		// A delivered spelling is not enough to prove irrelevance on all
		// native filesystems. Exercise coarse ingress even on this Linux
		// fixture; this is not a claimed case-insensitive-volume experiment.
		a.serviceEvent(coreprotocol.FileChange{Path: filepath.Join(root, "COMPILE_COMMANDS.JSON")})
	}); err != nil {
		t.Fatal(err)
	}
	wait(after.Generation, hex.EncodeToString(want[:]))
	runtime.Close()
	if owner.Snapshot().State != "closed" {
		t.Fatal("runtime shutdown failed to join retained artifact observation")
	}
}
