//go:build windows

package lsp

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"
)

func TestWindowsProcessTreeHelper(t *testing.T) {
	if os.Getenv("POWERNAP_JOB_TREE_HELPER") != "1" {
		return
	}
	child := exec.Command(os.Args[0], "-test.run=^TestProcessCloseHelper$")
	child.Env = append(os.Environ(), "POWERNAP_CLOSE_HELPER=1")
	if err := child.Start(); err != nil {
		os.Exit(2)
	}
	if err := os.WriteFile(os.Getenv("POWERNAP_JOB_CHILD_PID"), []byte(strconv.Itoa(child.Process.Pid)), 0600); err != nil {
		os.Exit(3)
	}
	for {
		if _, err := os.Stat(os.Getenv("POWERNAP_JOB_EXIT_FILE")); err == nil {
			os.Exit(0)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestWindowsOwnedJobJoinsDescendants(t *testing.T) {
	for _, natural := range []bool{false, true} {
		t.Run(strconv.FormatBool(natural), func(t *testing.T) {
			root := t.TempDir()
			pidFile, exitFile := filepath.Join(root, "child"), filepath.Join(root, "exit")
			cmd := exec.Command(os.Args[0], "-test.run=^TestWindowsProcessTreeHelper$")
			cmd.Env = append(os.Environ(), "POWERNAP_JOB_TREE_HELPER=1", "POWERNAP_JOB_CHILD_PID="+pidFile, "POWERNAP_JOB_EXIT_FILE="+exitFile)
			prepareProcess(cmd)
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			tree, err := ownProcess(cmd)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				tree.stop()
				if err := tree.wait(); err != nil {
					t.Error(err)
				}
			})
			var pid int
			deadline := time.Now().Add(3 * time.Second)
			for time.Now().Before(deadline) {
				data, _ := os.ReadFile(pidFile)
				pid, _ = strconv.Atoi(string(data))
				if pid != 0 {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			if pid == 0 {
				t.Fatal("contained descendant did not start")
			}
			// Retain the exact descendant identity before asking for shutdown.
			h, err := syscall.OpenProcess(0x00100000, false, uint32(pid))
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := syscall.CloseHandle(h); err != nil {
					t.Error(err)
				}
			}()
			if natural {
				if err := os.WriteFile(exitFile, nil, 0600); err != nil {
					t.Fatal(err)
				}
			} else {
				tree.stop()
			}
			if err := tree.wait(); err != nil {
				t.Fatal(err)
			}
			if status, err := syscall.WaitForSingleObject(h, 0); err != nil || status != syscall.WAIT_OBJECT_0 {
				t.Fatalf("successful shutdown left the exact descendant running: status %d, error %v", status, err)
			}
			tree.stop()
			if tree.job != 0 || tree.process != 0 {
				t.Fatal("joined shutdown did not release retained handles")
			}
		})
	}
}

func TestWindowsJobFailuresAreObservable(t *testing.T) {
	tree := &processTree{assigned: true} // invalid exact handle
	tree.stop()
	first := tree.err
	tree.stop()
	if !errors.Is(first, syscall.Errno(6)) || tree.err != first {
		t.Fatalf("termination failure lost or repeated: %v", tree.err)
	}
	if err := waitJobEmpty(0, time.Now()); !errors.Is(err, syscall.Errno(6)) {
		t.Fatalf("invalid job query reported success: %v", err)
	}
}

func TestWindowsFailedLaunchMarksOnlyUnverifiedCleanup(t *testing.T) {
	for _, badHandle := range []bool{false, true} {
		t.Run(strconv.FormatBool(badHandle), func(t *testing.T) {
			cmd := startCloseTestProcess(t) // still suspended
			tree := &processTree{cmd: cmd, done: make(chan struct{})}
			if badHandle {
				tree.job = syscall.Handle(0x12345678)
			}
			setupErr := errors.New("containment setup rejected")
			gotTree, err := tree.failedStart(setupErr)
			if gotTree != nil || !errors.Is(err, setupErr) {
				t.Fatalf("setup error lost: %v", err)
			}
			if errors.Is(err, ErrProcessCleanupUnverified) != badHandle {
				t.Fatalf("incorrect cleanup uncertainty marker: %v", err)
			}
			if badHandle && !errors.Is(err, syscall.Errno(6)) {
				t.Fatalf("handle-close failure lost: %v", err)
			}
			if badHandle {
				var retained *failedProcessCleanup
				if !errors.As(err, &retained) || retained.owner != tree || tree.job == 0 {
					t.Fatal("unverified cleanup discarded its exact retained owner")
				}
			}
		})
	}
}

func TestWindowsJobEmptyWaitIsBounded(t *testing.T) {
	cmd := startCloseTestProcess(t)
	tree, err := ownProcess(cmd)
	if err != nil {
		t.Fatal(err)
	}
	if err := waitJobEmpty(tree.job, time.Now()); err == nil {
		t.Error("active job reported empty at expired deadline")
	}
	tree.stop()
	if err := tree.wait(); err != nil {
		t.Fatal(err)
	}
}
