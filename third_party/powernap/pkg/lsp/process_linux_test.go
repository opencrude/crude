//go:build linux

package lsp

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func TestOwnedProcessObservationFailureNeverSignalsGroup(t *testing.T) {
	for _, observeErr := range []error{syscall.ENOSYS, syscall.ECHILD} {
		t.Run(observeErr.Error(), func(t *testing.T) {
			cmd := startCloseTestProcess(t)
			// For ECHILD the observer cannot safely kill even the direct child.
			if observeErr == syscall.ECHILD {
				if err := cmd.Process.Kill(); err != nil {
					t.Fatal(err)
				}
				_ = cmd.Wait()
			}
			var signals atomic.Int32
			tree := ownProcessWith(cmd, func(int) error { return observeErr }, func(int, syscall.Signal) error {
				signals.Add(1)
				return nil
			})
			if err := tree.wait(); !errors.Is(err, observeErr) {
				t.Fatalf("observation failure not returned: %v", err)
			}
			for range 3 {
				tree.stop()
			}
			if got := signals.Load(); got != 0 {
				t.Fatalf("signaled group %d times after lost identity", got)
			}
		})
	}
}

func TestOwnedProcessReportsGroupKillAndReapFailure(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^TestProcessCloseHelper$")
	cmd.Env = append(os.Environ(), "POWERNAP_CLOSE_HELPER=exit-7")
	prepareProcess(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	tree := ownProcessWith(cmd, waitProcessExit, func(int, syscall.Signal) error { return syscall.EPERM })
	err := tree.wait()
	var exit *exec.ExitError
	if !errors.Is(err, syscall.EPERM) || !errors.As(err, &exit) || exit.ExitCode() != 7 {
		t.Fatalf("group kill or unexpected reap failure lost: %v", err)
	}
}

func TestOwnedProcessESRCHIsExpected(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^TestProcessCloseHelper$")
	cmd.Env = append(os.Environ(), "POWERNAP_CLOSE_HELPER=exit-0")
	prepareProcess(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	tree := ownProcessWith(cmd, waitProcessExit, func(int, syscall.Signal) error { return syscall.ESRCH })
	if err := tree.wait(); err != nil {
		t.Fatalf("already absent process group reported as failure: %v", err)
	}
}

func TestOwnedProcessReportsLostWaitIdentity(t *testing.T) {
	cmd := startCloseTestProcess(t)
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err == nil {
		t.Fatal("killed helper unexpectedly exited successfully")
	}
	tree, err := ownProcess(cmd)
	if err != nil {
		t.Fatal(err)
	}
	<-tree.done
	pipe := &failingPipe{}
	closer := &processCloser{stdin: pipe, stdout: pipe, stderr: pipe, tree: tree}
	if err := closer.Close(); !errors.Is(err, syscall.ECHILD) {
		t.Fatalf("lost wait identity was falsely reported as complete: %v", err)
	}
}

func TestOwnedProcessAlreadyReapedNeverSignalsGroup(t *testing.T) {
	cmd := startCloseTestProcess(t)
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	observe := make(chan struct{})
	var signals atomic.Int32
	tree := ownProcessWith(cmd, func(int) error {
		<-observe
		return syscall.ECHILD
	}, func(int, syscall.Signal) error {
		signals.Add(1)
		return nil
	})
	tree.stop() // observation has not finished yet, but the PID is already reaped
	close(observe)
	if err := tree.wait(); !errors.Is(err, syscall.ECHILD) {
		t.Fatalf("lost identity failure missing: %v", err)
	}
	if signals.Load() != 0 {
		t.Fatal("shutdown signaled a group whose leader had already been reaped")
	}
}

func TestOwnedServerTree(t *testing.T) {
	for _, natural := range []bool{false, true} {
		t.Run(strconv.FormatBool(natural), func(t *testing.T) {
			root := t.TempDir()
			pidFile := filepath.Join(root, "descendant")
			cmd := exec.Command("sh", "-c", `sleep 60 & echo $! > "$1"; while [ ! -f "$2" ]; do sleep .01; done`, "helper", pidFile, filepath.Join(root, "exit"))
			prepareProcess(cmd)
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			tree, err := ownProcess(cmd)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(tree.stop)
			deadline := time.Now().Add(2 * time.Second)
			var pid int
			for time.Now().Before(deadline) {
				data, _ := os.ReadFile(pidFile)
				pid, _ = strconv.Atoi(strings.TrimSpace(string(data)))
				if pid != 0 {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			if pid == 0 {
				t.Fatal("descendant did not start")
			}
			if natural {
				if err := os.WriteFile(filepath.Join(root, "exit"), nil, 0600); err != nil {
					t.Fatal(err)
				}
			} else {
				tree.stop()
			}
			select {
			case <-tree.done:
			case <-time.After(2 * time.Second):
				t.Fatal("tree not reaped")
			}
			if err := tree.wait(); err != nil {
				t.Fatal(err)
			}
			// Linux may leave an orphan zombie to init, but it must not run.
			deadline = time.Now().Add(time.Second)
			for {
				data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
				if err != nil || strings.Contains(string(data), ") Z ") || syscall.Kill(pid, 0) != nil {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("descendant still running: %s", data)
				}
				time.Sleep(time.Millisecond)
			}

			tree.stop() // settled stop must never signal a recycled group.
		})
	}
}
