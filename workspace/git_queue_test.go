package workspace

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestGitReadDeadlineIncludesRepositoryQueue(t *testing.T) {
	service := NewGitService(t.TempDir())
	if err := service.lock(t.Context()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := service.CompareChanges(ctx, nil); done <- err }()
	select {
	case err := <-done:
		service.unlock()
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("queued read error=%v", err)
		}
	case <-time.After(250 * time.Millisecond):
		service.unlock()
		<-done
		t.Fatal("cancelled repository read waited on unrelated Git work instead of its deadline")
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
