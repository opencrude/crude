//go:build windows

package fileio

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestWindowsAsyncTreeHelper(t *testing.T) {
	mode := os.Getenv("CRUDE_WINDOWS_ASYNC_HELPER")
	if mode == "" {
		return
	}
	if mode == "child" {
		time.Sleep(time.Second)
		if marker := os.Getenv("CRUDE_WINDOWS_ASYNC_MARKER"); marker != "" {
			if err := os.WriteFile(marker, []byte("child survived"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		time.Sleep(30 * time.Second)
		return
	}
	if mode == "neighbor" {
		time.Sleep(30 * time.Second)
		return
	}
	child := exec.Command(os.Args[0], "-test.run=^TestWindowsAsyncTreeHelper$")
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "CRUDE_WINDOWS_ASYNC_HELPER=") {
			child.Env = append(child.Env, entry)
		}
	}
	child.Env = append(child.Env, "CRUDE_WINDOWS_ASYNC_HELPER=child")
	child.Stdout, child.Stderr = os.Stdout, os.Stderr
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	fmt.Printf("CHILD_PID=%d\n", child.Process.Pid)
	if mode != "parent-exit" {
		_ = child.Wait()
	}
}

func windowsAsyncHelperCommand(mode, marker string) string {
	return fmt.Sprintf(`set "CRUDE_WINDOWS_ASYNC_HELPER=%s"&&set "CRUDE_WINDOWS_ASYNC_MARKER=%s"&&"%s" -test.run=^TestWindowsAsyncTreeHelper$`, mode, marker, os.Args[0])
}

func windowsOwnedProcessHandle(t *testing.T, pid int) windows.Handle {
	t.Helper()
	handle, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_TERMINATE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		t.Fatalf("TODO(Windows): cannot retain exact test process %d: %v", pid, err)
	}
	t.Cleanup(func() {
		_ = windows.TerminateProcess(handle, 1)
		_ = windows.CloseHandle(handle)
	})
	return handle
}

func TestWindowsKillRefStopsOwnedTreeAndPreservesUnrelatedProcess(t *testing.T) {
	manager := NewAsyncCommandManager()
	defer manager.CloseAndWait()
	selected, err := manager.Start("selected", windowsAsyncHelperCommand("parent-hold", ""), t.TempDir(), 0)
	if err != nil {
		t.Fatalf("TODO(Windows): cannot create suspended, job-owned command: %v", err)
	}
	neighbor := exec.Command(os.Args[0], "-test.run=^TestWindowsAsyncTreeHelper$")
	neighbor.Env = append(os.Environ(), "CRUDE_WINDOWS_ASYNC_HELPER=neighbor")
	if err := neighbor.Start(); err != nil {
		t.Fatalf("TODO(Windows): cannot create unrelated process fixture: %v", err)
	}
	defer func() { _ = neighbor.Process.Kill(); _ = neighbor.Wait() }()
	neighborHandle := windowsOwnedProcessHandle(t, neighbor.Process.Pid)
	selectedHandle := windowsOwnedProcessHandle(t, selected.PID)
	deadline := time.Now().Add(5 * time.Second)
	childPID := 0
	for childPID == 0 && time.Now().Before(deadline) {
		output, _, err := manager.ReadOutput(selected.Handle, 0)
		if err != nil {
			t.Fatalf("TODO(Windows): cannot read child identity: %v", err)
		}
		for _, line := range strings.Split(output, "\n") {
			if strings.HasPrefix(line, "CHILD_PID=") {
				childPID, _ = strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "CHILD_PID=")))
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if childPID == 0 {
		t.Fatal("TODO(Windows): suspended command did not resume or expose its child PID")
	}
	childHandle := windowsOwnedProcessHandle(t, childPID)
	if err := manager.KillRef(selected.Ref()); err != nil {
		t.Fatalf("TODO(Windows): exact command Job Object termination failed: %v", err)
	}
	for _, handle := range []windows.Handle{selectedHandle, childHandle} {
		status, err := windows.WaitForSingleObject(handle, 5000)
		if err != nil || status != windows.WAIT_OBJECT_0 {
			t.Fatalf("TODO(Windows): owned parent/descendant survived TerminateJobObject: status=%d error=%v", status, err)
		}
	}
	if status, err := windows.WaitForSingleObject(neighborHandle, 0); err != nil || status != uint32(windows.WAIT_TIMEOUT) {
		t.Fatalf("TODO(Windows): termination affected an unrelated same-name process: status=%d error=%v", status, err)
	}
	waitAsyncCommand(t, manager, selected.Handle)
}

func TestWindowsCompletedCommandClosesInheritedPipesAndDescendants(t *testing.T) {
	manager := NewAsyncCommandManager()
	defer manager.CloseAndWait()
	marker := filepath.Join(t.TempDir(), "escaped-child")
	started, err := manager.Start("detached", windowsAsyncHelperCommand("parent-exit", marker), t.TempDir(), 0)
	if err != nil {
		t.Fatalf("TODO(Windows): cannot start owned completion fixture: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		info, _ := manager.Check(started.Handle)
		if info.Status != AsyncRunning {
			if info.Status != AsyncCompleted {
				t.Fatalf("TODO(Windows): normal command completion failed: %+v", info)
			}
			time.Sleep(1200 * time.Millisecond)
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatalf("TODO(Windows): descendant survived the owning command's exit: %v", err)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("TODO(Windows): command completion hangs on a descendant's inherited stdout; terminate the Job Object before Cmd.Wait drains output")
}

func TestWindowsInteractiveCommandsUseExactJobOwnership(t *testing.T) {
	for _, tty := range []bool{false, true} {
		t.Run(fmt.Sprintf("tty=%t", tty), func(t *testing.T) {
			manager := NewAsyncCommandManager()
			defer manager.CloseAndWait()
			result, err := manager.StartInteractive(t.Context(), InteractiveOptions{
				Command: windowsAsyncHelperCommand("parent-hold", ""), Shell: "cmd.exe", TTY: tty,
			}, 100*time.Millisecond, 1000)
			if err != nil || result.SessionID == 0 {
				t.Fatalf("TODO(Windows): interactive pipe/ConPTY command failed: %+v error=%v", result, err)
			}
			info, ok := manager.Check(fmt.Sprintf("auto-%d", result.SessionID))
			if !ok {
				t.Fatal("TODO(Windows): native command session is missing from its owning manager")
			}
			if err := manager.KillRef(info.Ref()); err != nil {
				t.Fatalf("TODO(Windows): interactive Job Object termination failed: %v", err)
			}
			deadline := time.Now().Add(5 * time.Second)
			for time.Now().Before(deadline) {
				current, _ := manager.Check(info.Handle)
				if current.Status != AsyncRunning {
					if current.Status != AsyncKilled {
						t.Fatalf("TODO(Windows): interactive termination has incorrect status: %+v", current)
					}
					return
				}
				time.Sleep(10 * time.Millisecond)
			}
			t.Fatal("TODO(Windows): pipe/ConPTY command did not settle after exact Job Object termination")
		})
	}
}
