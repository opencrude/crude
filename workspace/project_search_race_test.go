package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"crude/fileio"
)

// TestProjectSearchServiceConcurrentBeginExecuteAccept races replacement,
// execution, and acceptance without corrupting request identity.
func TestProjectSearchServiceConcurrentBeginExecuteAccept(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 8; i++ {
		name := fmt.Sprintf("file_%d.go", i)
		body := fmt.Sprintf("package sample\nvar Target%d = %d\n", i, i)
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	service := NewProjectSearchService()
	const workers = 16
	var wg sync.WaitGroup
	wg.Add(workers)
	for w := 0; w < workers; w++ {
		go func(id int) {
			defer wg.Done()
			query := fmt.Sprintf("Target%d", id%8)
			req := service.Begin(root, query, fileio.ProjectSearchOptions{CaseSensitive: true})
			result := service.Execute(req)
			_ = service.Accept(result)
		}(w)
	}
	wg.Wait()
}

// TestWorkspaceProjectSearchConcurrentPublish races async completion publishing
// with replacement and cancellation, then verifies the bus still accepts results.
func TestWorkspaceProjectSearchConcurrentPublish(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "needle.go"), []byte("package sample\nvar Needle = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	bus := NewEventBus()
	ws := &Workspace{
		RootPath:      root,
		ProjectSearch: NewProjectSearchService(),
		eventBus:      bus,
	}

	const rounds = 24
	var wg sync.WaitGroup
	wg.Add(rounds)
	for i := 0; i < rounds; i++ {
		go func(n int) {
			defer wg.Done()
			query := "Needle"
			if n%3 == 0 {
				query = "missing"
			}
			_ = ws.StartProjectSearch(query, fileio.ProjectSearchOptions{})
			if n%5 == 0 {
				ws.CancelProjectSearch()
			}
			_ = bus.RecentEvents(8)
		}(i)
	}
	wg.Wait()

	_ = ws.StartProjectSearch("Needle", fileio.ProjectSearchOptions{})
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, event := range bus.RecentEvents(32) {
			if strings.Contains(event, "ProjectSearchCompletedMsg") {
				return
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("expected project search completion on EventBus after concurrent publish")
}
