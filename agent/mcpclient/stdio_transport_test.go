package mcpclient

import (
	"context"
	"fmt"
	"io"
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
	if mode == "graceful" {
		_, _ = io.Copy(io.Discard, os.Stdin)
		os.Exit(0)
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
	for _, mode := range []string{"oversized", "silent", "graceful"} {
		t.Run(mode, func(t *testing.T) {
			lifetime, cancel := context.WithCancel(t.Context())
			defer cancel()
			command := exec.Command(executable, "-test.run=^TestBoundedStdioHelper$")
			// The race runtime otherwise sleeps one second after main returns,
			// exactly the production grace period. Test process behavior, not that
			// instrumentation-only exit delay.
			command.Env = append(stdioBaseEnvironment(), "CRUDE_MCP_BOUND_HELPER="+mode, "GORACE=atexit_sleep_ms=0")
			transport := &boundedCommandTransport{command: command, lifetime: lifetime}
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
				go func() {
					defer closers.Done()
					err := connection.Close()
					if mode == "graceful" && err != nil {
						t.Errorf("graceful cancellation raced process exit: %v", err)
					}
				}()
			}
			closed := make(chan struct{})
			go func() { closers.Wait(); close(closed) }()
			select {
			case <-closed:
			case <-time.After(3 * time.Second):
				t.Fatal("shutdown blocked")
			}
			if mode == "silent" || mode == "graceful" {
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
