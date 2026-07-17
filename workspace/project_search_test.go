package workspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"crude/fileio"
)

func TestProjectSearchServiceExecutesAndAcceptsCurrentRequest(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\nfunc Target() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	service := NewProjectSearchService()
	req := service.Begin(root, "Target", fileio.ProjectSearchOptions{CaseSensitive: true})
	result := service.Execute(req)
	if result.Err != nil {
		t.Fatal(result.Err)
	}
	if len(result.Matches) != 1 || result.Matches[0].Line != 2 {
		t.Fatalf("matches=%+v", result.Matches)
	}
	if !service.Accept(result) {
		t.Fatal("current result should be accepted")
	}
}

func TestProjectSearchServiceReplacementCancelsAndRejectsStaleResult(t *testing.T) {
	service := NewProjectSearchService()
	root := t.TempDir()
	first := service.Begin(root, "first", fileio.ProjectSearchOptions{})
	second := service.Begin(root, "second", fileio.ProjectSearchOptions{})

	stale := service.Execute(first)
	if !errors.Is(stale.Err, context.Canceled) {
		t.Fatalf("replaced request error=%v, want cancellation", stale.Err)
	}
	if service.Accept(stale) {
		t.Fatal("replaced request result must be rejected")
	}
	current := service.Execute(second)
	if current.Err != nil {
		t.Fatal(current.Err)
	}
	if !service.Accept(current) {
		t.Fatal("replacement request should remain current")
	}
}

func TestProjectSearchServiceCancelInvalidatesOutstandingResult(t *testing.T) {
	service := NewProjectSearchService()
	req := service.Begin(t.TempDir(), "query", fileio.ProjectSearchOptions{})
	service.Cancel()
	result := service.Execute(req)
	if !errors.Is(result.Err, context.Canceled) {
		t.Fatalf("canceled request error=%v", result.Err)
	}
	if service.Accept(result) {
		t.Fatal("canceled result must not be accepted")
	}
}

func TestWorkspaceProjectSearchPublishesCompletionOnEventBus(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "event.go"), []byte("package sample\nvar EventTarget = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	bus := NewEventBus()
	ws := &Workspace{
		RootPath:      root,
		ProjectSearch: NewProjectSearchService(),
		eventBus:      bus,
	}
	requestID := ws.StartProjectSearch("EventTarget", fileio.ProjectSearchOptions{CaseSensitive: true})
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, event := range bus.RecentEvents(10) {
			if strings.Contains(event, "ProjectSearchCompletedMsg") && strings.Contains(event, "request=") {
				if requestID == 0 {
					t.Fatal("workspace returned zero request ID")
				}
				return
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("project search completion was not published through EventBus")
}
