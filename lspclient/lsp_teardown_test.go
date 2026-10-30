package lspclient

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/charmbracelet/x/powernap/pkg/lsp"
)

func TestLSPConcurrentStopJoinsTheSameLaunchingLifetime(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(map[bool]string{false: "verified", true: "unverified"}[failed], func(t *testing.T) {
			l := NewLSPIntegration("nonexistent-lsp-server-12345", nil, "file:///unused")
			cancelled := make(chan struct{})
			launch := &lspLaunch{cancel: func() { close(cancelled) }, done: make(chan struct{})}
			l.launch = launch
			first, second := make(chan error, 1), make(chan error, 1)
			go func() { first <- l.Stop() }()
			<-cancelled
			go func() { second <- l.Stop() }()
			// Even another Start cannot bypass an in-progress owned shutdown.
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			if err := l.Start(ctx); !errors.Is(err, context.Canceled) {
				t.Fatalf("Start bypassed prior teardown admission: %v", err)
			}
			select {
			case err := <-second:
				t.Fatalf("second Stop returned before startup cleanup: %v", err)
			default:
			}
			want := errors.New("process termination could not be observed")
			if failed {
				launch.cleanupErr = want
			}
			close(launch.done)
			for _, result := range []<-chan error{first, second} {
				select {
				case err := <-result:
					if errors.Is(err, want) != failed || err != nil && !failed {
						t.Fatalf("Stop lost the exact cleanup outcome: %v", err)
					}
				case <-time.After(time.Second):
					t.Fatal("Stop did not join settled startup")
				}
			}
			if failed {
				if err := l.Stop(); !errors.Is(err, want) {
					t.Fatal("repeat Stop manufactured successful teardown", err)
				}
				if err := l.Start(t.Context()); !errors.Is(err, want) {
					t.Fatal("unverified lifetime admitted a replacement process", err)
				}
			}
		})
	}
}

func TestLSPStopExitStatusHelper(t *testing.T) {
	if os.Getenv("CRUDE_LSP_REAPED_EXIT_FIXTURE") == "1" {
		os.Exit(7)
	}
}

func TestLSPVerifiedReapDoesNotTurnServerCrashIntoPoisonedAdmission(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^TestLSPStopExitStatusHelper$")
	cmd.Env = append(os.Environ(), "CRUDE_LSP_REAPED_EXIT_FIXTURE=1")
	err := cmd.Run()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 7 {
		t.Fatalf("fixture did not produce independently reaped exit 7: %v", err)
	}
	unknown := errors.New("group termination unverified")
	if !reapedProcessExitOnly(fmt.Errorf("reap: %w", exit)) ||
		reapedProcessExitOnly(errors.Join(exit, unknown)) ||
		reapedProcessExitOnly(errors.Join(exit, lsp.ErrProcessCleanupUnverified)) {
		t.Fatal("provider exit was conflated with failed resource cleanup")
	}
	integration := NewLSPIntegration("nonexistent-lsp-server-12345", nil, "file:///unused")
	integration.teardownErr = fmt.Errorf("reap: %w", exit)
	if err := integration.Stop(); err != nil {
		t.Fatalf("a verified reap retained a non-existent process reservation: %v", err)
	}
	if err := integration.Start(t.Context()); !errors.Is(err, exec.ErrNotFound) {
		t.Fatalf("verified retirement prevented an explicit new launch attempt: %v", err)
	}
}
