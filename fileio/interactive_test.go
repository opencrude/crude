//go:build !windows

package fileio

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestInteractiveExecPipesAndPTY(t *testing.T) {
	for _, tty := range []bool{false, true} {
		t.Run(map[bool]string{false: "pipes", true: "pty"}[tty], func(t *testing.T) {
			mgr := NewAsyncCommandManager()
			defer mgr.CloseAndWait()
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			result, err := mgr.StartInteractive(ctx, InteractiveOptions{Command: "printf ready; read x; printf 'reply:%s' \"$x\"", Shell: "/bin/sh", TTY: tty}, 50*time.Millisecond, 1000)
			if err != nil || result.SessionID == 0 || !strings.Contains(result.Output, "ready") {
				t.Fatalf("start %+v %v", result, err)
			}
			result, err = mgr.WriteInteractive(ctx, result.SessionID, "hello\n", time.Second, 1000)
			if err != nil || result.ExitCode == nil || *result.ExitCode != 0 || !strings.Contains(result.Output, "reply:hello") {
				t.Fatalf("continue %+v %v", result, err)
			}
		})
	}
}

func TestInteractiveExecCancellationAndShutdown(t *testing.T) {
	mgr := NewAsyncCommandManager()
	result, err := mgr.StartInteractive(t.Context(), InteractiveOptions{Command: "sleep 60", Shell: "/bin/sh"}, time.Millisecond, 1000)
	if err != nil || result.SessionID == 0 {
		t.Fatalf("%+v %v", result, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := mgr.WriteInteractive(ctx, result.SessionID, "", time.Second, 1000); err == nil {
		t.Fatal("cancel not observed")
	}
	mgr.CloseAndWait()
	if mgr.Running() {
		t.Fatal("process survived close")
	}
	if _, err := mgr.StartInteractive(t.Context(), InteractiveOptions{Command: "echo no"}, time.Millisecond, 10); err == nil {
		t.Fatal("closed manager launched a process")
	}
}
