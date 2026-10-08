//go:build linux

package lsp

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

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
