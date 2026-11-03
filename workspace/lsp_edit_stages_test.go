package workspace

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestEditStagesClassifyExactOrderedPaths(t *testing.T) {
	var stages editStageRegistry
	root := t.TempDir()
	path := filepath.Join(root, ".crude-workspace-edit-owned")
	lookalike := filepath.Join(root, ".crude-workspace-edit-user")
	if stages.ownsEvent(t.Context(), lookalike, 1) {
		t.Fatal("an unregistered project file was treated as staging")
	}
	stage := stages.reserve(path)
	if stage == nil {
		t.Fatal("could not reserve exact staging path")
	}
	stages.resolve(path, stage, true)
	for _, kind := range []int{1, 2, 2, 3} {
		if !stages.ownsEvent(t.Context(), path, kind) {
			t.Fatalf("owned staging event %d was not recognized", kind)
		}
	}
	if stages.ownsEvent(t.Context(), path, 1) {
		t.Fatal("a new project file at the retired path was ignored")
	}
	if stages.ownsEvent(t.Context(), lookalike, 2) {
		t.Fatal("a matching prefix hid a project change")
	}
}

func TestEditStageCreationCollisionAndCancellation(t *testing.T) {
	var stages editStageRegistry
	path := filepath.Join(t.TempDir(), "existing")
	stage := stages.reserve(path)
	result := make(chan bool, 1)
	go func() { result <- stages.ownsEvent(t.Context(), path, 1) }()
	// Failed exclusive creation means the path belongs to somebody else.
	stages.resolve(path, stage, false)
	if <-result || stages.ownsEvent(t.Context(), path, 2) {
		t.Fatal("a failed exclusive create hid a project event")
	}
	stage = stages.reserve(path)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if stages.ownsEvent(ctx, path, 1) {
		t.Fatal("cancelled observation recognized an unresolved path")
	}
	stages.resolve(path, stage, true)
}

func TestEditStagePendingObservationWaitsForCreation(t *testing.T) {
	var stages editStageRegistry
	path := filepath.Join(t.TempDir(), "pending")
	stage := stages.reserve(path)
	started, result := make(chan struct{}), make(chan bool, 1)
	go func() {
		close(started)
		result <- stages.ownsEvent(t.Context(), path, 1)
	}()
	<-started
	select {
	case <-result:
		t.Fatal("event was classified before exclusive creation finished")
	case <-time.After(20 * time.Millisecond):
	}
	stages.resolve(path, stage, true)
	select {
	case owned := <-result:
		if !owned {
			t.Fatal("successfully created stage was not recognized")
		}
	case <-time.After(time.Second):
		t.Fatal("observation did not resume after creation")
	}
}

func TestEditStageGapRetiresUnobservedRemovals(t *testing.T) {
	var stages editStageRegistry
	path := filepath.Join(t.TempDir(), "stage")
	stage := stages.reserve(path)
	stages.resolve(path, stage, true)
	stages.setReliable(false)
	if stages.ownsEvent(t.Context(), path, 2) || stages.reserve(path) != nil {
		t.Fatal("a gap retained or admitted a staging reservation")
	}
	stages.setReliable(true)
	if stages.ownsEvent(t.Context(), path, 1) {
		t.Fatal("a later file was hidden after observation returned")
	}
	next := stages.reserve(path)
	if next == nil {
		t.Fatal("observation recovery did not allow a new reservation")
	}
	stages.resolve(path, next, true)
}
