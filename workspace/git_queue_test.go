package workspace

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestGitReadCancellationIncludesRepositoryQueue(t *testing.T) {
	service := NewGitService(t.TempDir())
	if err := service.lock(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer service.unlock()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := service.CompareChanges(ctx, nil); done <- err }()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("queued read error=%v", err)
		}
	case <-t.Context().Done():
		t.Fatal("cancelled repository read waited on unrelated Git work")
	}
}

func TestCompareChangesReportsEveryOmittedPath(t *testing.T) {
	_, _, service, root := reviewRepository(t, false)
	var changes []GitChange
	for i := 0; i < GitChangesLimit+3; i++ {
		name := fmt.Sprintf("%03d.txt", i)
		reviewWrite(t, root, name, "")
		changes = append(changes, GitChange{Path: name, Area: GitAreaUntracked})
	}
	items, err := service.CompareChanges(t.Context(), changes)
	if err != nil || len(items) != len(changes) {
		t.Fatalf("items=%d err=%v", len(items), err)
	}
	if items[0].Err != nil || items[len(items)-1].Err == nil || !strings.Contains(items[len(items)-1].Err.Error(), "limit") {
		t.Fatal("bounded review silently omitted files")
	}
}
