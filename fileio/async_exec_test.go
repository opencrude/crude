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

func TestNextAutoHandleSkipsLiveHandles(t *testing.T) {
	manager := NewAsyncCommandManager()
	if _, err := manager.Start("auto-1", asyncHelperCommand(), t.TempDir(), 0); err != nil {
		t.Fatal(err)
	}
	if handle := manager.NextAutoHandle(); handle != "auto-2" {
		t.Fatalf("handle=%q, want auto-2 because auto-1 is already in use", handle)
	}
	manager.CloseAndWait()
}

func TestPromoteRegistersRunningCommandUnderNewHandle(t *testing.T) {
	manager := NewAsyncCommandManager()
	uc, err := manager.StartUntracked(asyncHelperCommand(), t.TempDir(), 0)
	if err != nil {
		t.Fatal(err)
	}
	handle := manager.NextAutoHandle()
	info, err := manager.Promote(uc, handle)
	if err != nil {
		t.Fatalf("promote: %v", err)
	}
	if info.Handle != handle || info.Status != AsyncRunning || info.PID != uc.PID() {
		t.Fatalf("promoted info = %+v, want handle %q running with pid %d", info, handle, uc.PID())
	}
	if _, ok := manager.Check(handle); !ok {
		t.Fatal("promoted handle is not addressable through the manager")
	}
	if !manager.Running() {
		t.Fatal("promoted command is not covered by the activity gate")
	}
	if _, err := manager.Promote(uc, handle); err == nil {
		t.Fatal("re-promoting an in-use handle must fail")
	}
	if err := manager.Kill(handle); err != nil {
		t.Fatalf("kill: %v", err)
	}
	<-uc.Done()
	if got := uc.Status(); got != AsyncKilled {
		t.Fatalf("status=%s, want %s", got, AsyncKilled)
	}
	manager.CloseAndWait()
}

func TestAgentCommandInheritsConfiguredCrossPlatformTempDirectory(t *testing.T) {
	manager := NewAsyncCommandManager()
	tempDir := filepath.Join(t.TempDir(), "agent-temp")
	if err := manager.SetTempDir(tempDir); err != nil {
		t.Fatal(err)
	}
	command := `printf "%s" "$CRUDE_TEMP_DIR"`
	if runtime.GOOS == "windows" {
		command = `echo %CRUDE_TEMP_DIR%`
	}
	if _, err := manager.Start("temp-env", command, t.TempDir(), 10); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		info, ok := manager.Check("temp-env")
		if !ok {
			t.Fatal("command disappeared")
		}
		if info.Status != AsyncRunning {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("command did not finish")
		}
		time.Sleep(10 * time.Millisecond)
	}
	output, _, _, err := manager.ReadOutputLimit("temp-env", 0, DefaultOutputChunkBytes)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(output) != tempDir {
		t.Fatalf("CRUDE_TEMP_DIR=%q want %q", strings.TrimSpace(output), tempDir)
	}
	manager.CloseAndWait()
}
