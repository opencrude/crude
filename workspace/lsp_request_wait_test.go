package workspace

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"crude/types"
)

func TestManualLSPRequestDoesNotWaitForMissingOrStoppedServer(t *testing.T) {
	for _, state := range []string{"unhandled-file", "stopped", "failed-start"} {
		t.Run(state, func(t *testing.T) {
			m, runtime, root := lifecycleManager(t)
			path := filepath.Join(root, "main.go")
			if state == "unhandled-file" {
				path = filepath.Join(root, "file.crude-unknown-language")
			}
			if state == "failed-start" {
				runtime.startErrs = []error{errors.New("fixture server could not start")}
			}
			editorSource(t, m, path, "package probe\n")
			switch state {
			case "stopped":
				m.Stop()
			case "failed-start":
				awaitLSP(t, func() bool {
					_, status, _ := m.Status()
					return status == LSPFailed
				})
			}
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			result, err := m.RequestNavigation(ctx, path, types.LSPNavigationReferences, 0, 1, true)
			if err == nil || errors.Is(err, context.DeadlineExceeded) || len(result.Targets) != 0 {
				t.Fatalf("an unavailable server consumed the wait budget or returned targets: %+v, %v", result, err)
			}
		})
	}
}

func TestManualLSPRequestStartupWaitHonorsCancellation(t *testing.T) {
	m, runtime, root := lifecycleManager(t)
	start := make(chan struct{})
	runtime.startGate = start
	path := filepath.Join(root, "main.go")
	editorSource(t, m, path, "package probe\n")
	awaitLSP(t, func() bool { return len(runtime.snapshot()) != 0 })
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	result, err := m.RequestNavigation(ctx, path, types.LSPNavigationReferences, 0, 1, true)
	if !errors.Is(err, context.Canceled) || len(result.Targets) != 0 {
		t.Fatalf("canceled startup request was not retired: %+v, %v", result, err)
	}
	close(start)
}
