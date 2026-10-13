package mcpclient

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestBoundedStdioHelper(t *testing.T) {
	mode := os.Getenv("CRUDE_MCP_BOUND_HELPER")
	if mode == "" {
		return
	}
	if mode == "oversized" {
		_, _ = fmt.Fprint(os.Stdout, `{"jsonrpc":"2.0","method":"notifications/message","params":{"data":"`+strings.Repeat("z", maxResultBytes)+`"}}`+"\n")
	}
	// Intentionally ignore stdin EOF: transport shutdown must release the pipe,
	// terminate the exact process and reap it rather than await helper cooperation.
	for {
		time.Sleep(time.Hour)
	}
}

func TestStdioIngressBoundAndOwnedShutdown(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"oversized", "silent"} {
		t.Run(mode, func(t *testing.T) {
			lifetime, cancel := context.WithCancel(t.Context())
			defer cancel()
			command := exec.CommandContext(lifetime, executable, "-test.run=^TestBoundedStdioHelper$")
			command.Env = []string{"CRUDE_MCP_BOUND_HELPER=" + mode}
			transport := &boundedCommandTransport{command: command}
			connection, err := transport.Connect(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			read := make(chan error, 1)
			go func() { _, err := connection.Read(t.Context()); read <- err }()
			if mode == "oversized" {
				select {
				case err := <-read:
					if err == nil || !strings.Contains(err.Error(), "maximum line length") {
						t.Fatalf("ingress was not bounded: %v", err)
					}
				case <-time.After(3 * time.Second):
					t.Fatal("oversized decode did not stop")
				}
			}
			cancel()
			var closers sync.WaitGroup
			for i := 0; i < 3; i++ {
				closers.Add(1)
				go func() { defer closers.Done(); _ = connection.Close() }()
			}
			closed := make(chan struct{})
			go func() { closers.Wait(); close(closed) }()
			select {
			case <-closed:
			case <-time.After(3 * time.Second):
				t.Fatal("shutdown blocked")
			}
			if mode == "silent" {
				select {
				case err := <-read:
					if err == nil {
						t.Fatal("closed read succeeded")
					}
				case <-time.After(time.Second):
					t.Fatal("read leaked")
				}
			}
			if command.ProcessState == nil || !command.ProcessState.Exited() && command.ProcessState.ExitCode() != -1 {
				t.Fatalf("process not reaped: %v", command.ProcessState)
			}
		})
	}
}
