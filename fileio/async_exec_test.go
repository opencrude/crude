package fileio

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func asyncHelperCommand() string {
	command := fmt.Sprintf(`CRUDE_ASYNC_HELPER=1 %q -test.run=^TestAsyncCommandHelperProcess$`, os.Args[0])
	if runtime.GOOS == "windows" {
		command = fmt.Sprintf(`set CRUDE_ASYNC_HELPER=1&&"%s" -test.run=^TestAsyncCommandHelperProcess$`, strings.ReplaceAll(os.Args[0], `"`, `""`))
	}
	return command
}

func TestAsyncCommandHelperProcess(t *testing.T) {
	if os.Getenv("CRUDE_ASYNC_HELPER") != "1" {
		return
	}
	time.Sleep(30 * time.Second)
}

func TestAsyncCommandCloseKillsAndJoinsProcess(t *testing.T) {
	manager := NewAsyncCommandManager()
	if _, err := manager.Start("liveness", asyncHelperCommand(), t.TempDir(), 0); err != nil {
		t.Fatal(err)
	}
	if !manager.Running() {
		t.Fatal("started command was not included in the activity gate")
	}
	started := time.Now()
	manager.CloseAndWait()
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("command tree did not terminate promptly: %s", elapsed)
	}
	if manager.Running() {
		t.Fatal("command remained active after CloseAndWait")
	}
}

func TestConcurrentDuplicateHandleStartsOnlyOneProcess(t *testing.T) {
	manager := NewAsyncCommandManager()
	var wg sync.WaitGroup
	results := make(chan error, 2)
	start := make(chan struct{})
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := manager.Start("same", asyncHelperCommand(), t.TempDir(), 0)
			results <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	succeeded := 0
	for err := range results {
		if err == nil {
			succeeded++
		}
	}
	if succeeded != 1 {
		t.Fatalf("successful starts=%d, want 1", succeeded)
	}
	manager.CloseAndWait()
}

func TestCompletedShellCannotLeaveDetachedWriter(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows process-tree ownership is compile-checked separately")
	}
	manager := NewAsyncCommandManager()
	target := filepath.Join(t.TempDir(), "late")
	command := fmt.Sprintf(`(sleep 1; printf late > %q) >/dev/null 2>&1 &`, target)
	if _, err := manager.Start("detached", command, t.TempDir(), 0); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for manager.Running() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(1200 * time.Millisecond)
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		t.Fatalf("detached child survived command ownership: %v", err)
	}
}
