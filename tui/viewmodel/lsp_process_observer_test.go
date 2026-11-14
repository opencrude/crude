package viewmodel

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"testing"
	"time"
)

func TestLSPVisualProcessObserverChild(t *testing.T) {
	if os.Getenv("CRUDE_LSP_OBSERVER_CHILD") != "1" {
		t.Skip("owned process-observer helper")
	}
	fmt.Println("observer ready")
	if _, err := io.Copy(io.Discard, os.Stdin); err != nil {
		t.Fatal(err)
	}
}

func TestLSPVisualProcessObserverUsesExecutableAndDirectory(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	other := t.TempDir()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable, "-test.run=^TestLSPVisualProcessObserverChild$", "-test.count=1")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "CRUDE_LSP_OBSERVER_CHILD=1")
	input, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	joined := false
	defer func() {
		_ = input.Close()
		if !joined {
			cancel()
			_ = cmd.Wait()
		}
	}()
	ready, err := bufio.NewReader(output).ReadString('\n')
	if err != nil || ready != "observer ready\n" {
		t.Fatalf("observer child did not start: %q %v", ready, err)
	}
	pids, err := lspVisualProcesses(root, executable)
	if err != nil || !slices.Contains(pids, cmd.Process.Pid) {
		t.Fatalf("live owned process missing: %v %v", pids, err)
	}
	pids, err = lspVisualProcesses(other, executable)
	if err != nil || slices.Contains(pids, cmd.Process.Pid) {
		t.Fatalf("foreign working directory matched: %v %v", pids, err)
	}
	if err := input.Close(); err != nil {
		t.Fatal(err)
	}
	err = cmd.Wait()
	joined = true
	if err != nil {
		t.Fatal(err)
	}
	pids, err = lspVisualProcesses(root, executable)
	if err != nil || slices.Contains(pids, cmd.Process.Pid) {
		t.Fatalf("exited process remained: %v %v", pids, err)
	}
}
