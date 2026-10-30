package lspclient

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"crude/types"

	"github.com/charmbracelet/x/powernap/pkg/lsp"
	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol"
)

func settleFixture(t *testing.T) (*LSPIntegration, *lspLaunch, string) {
	t.Helper()
	l := NewLSPIntegration("unused", nil, "")
	launch := &lspLaunch{}
	l.launch, l.client = launch, &lsp.Client{}
	path := filepath.Join(t.TempDir(), "a.go")
	state := &openFileState{text: "package a", boundary: testBoundary(path)}
	state.resetVersion(1)
	l.openFiles[path] = state
	return l, launch, path
}

// Diagnostics are reported as settled only after a publication for the
// current version and with no server work in progress; otherwise an agent
// would be told "no problems" while the server was still loading.
func TestAwaitDiagnosticsWaitsForPublicationAndIdleServer(t *testing.T) {
	l, launch, path := settleFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	if _, settled := l.AwaitDiagnostics(ctx, path); settled {
		t.Fatal("settled before any publication")
	}
	cancel()

	l.updateProgress(launch, "load", "begin")
	version := int32(1)
	l.updateDiagnostics(launch, protocol.URIFromPath(path), &version, []protocol.Diagnostic{{Message: "undefined: x", Severity: 1, Source: "compiler", Code: "UndeclaredName"}})
	ctx, cancel = context.WithTimeout(context.Background(), 30*time.Millisecond)
	if _, settled := l.AwaitDiagnostics(ctx, path); settled {
		t.Fatal("settled while the server reported work in progress")
	}
	cancel()

	done := make(chan struct{})
	var got types.InsightDiagnosticPublication
	var settled bool
	go func() {
		defer close(done)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		got, settled = l.AwaitDiagnostics(ctx, path)
	}()
	l.updateProgress(launch, "load", "end")
	<-done
	if !settled || len(got.Diagnostics) != 1 || got.Diagnostics[0].Source != "compiler" || got.Diagnostics[0].Code != "UndeclaredName" {
		t.Fatalf("AwaitDiagnostics = %+v settled=%v", got, settled)
	}
}

func TestAwaitDiagnosticsIgnoresPublicationForAnOlderVersion(t *testing.T) {
	l, launch, path := settleFixture(t)
	version := int32(1)
	l.updateDiagnostics(launch, protocol.URIFromPath(path), &version, []protocol.Diagnostic{{Message: "stale"}})
	l.mu.Lock()
	l.openFiles[path].resetVersion(2)
	l.mu.Unlock()
	l.updateDiagnostics(launch, protocol.URIFromPath(path), &version, []protocol.Diagnostic{{Message: "late stale"}})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if got, settled := l.AwaitDiagnostics(ctx, path); settled || len(got.Diagnostics) != 0 {
		t.Fatalf("an older version's diagnostics were reported: %+v settled=%v", got, settled)
	}
}

// Real framed subprocess: the loading-time publication is superseded, and the
// result settles only after the server ends its progress.
func TestAwaitDiagnosticsOverTheWire(t *testing.T) {
	root := t.TempDir()
	l := protocolIntegration(t, "progress", root)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := l.Start(ctx); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "a.go")
	openTestFile(t, l, ctx, path, "go", "package a\nvar x = y\n")
	got, settled := l.AwaitDiagnostics(ctx, path)
	if !settled || len(got.Diagnostics) != 1 || got.Diagnostics[0].Message != "undefined: y" || got.Diagnostics[0].Code != "UndeclaredName" {
		t.Fatalf("AwaitDiagnostics = %+v settled=%v", got, settled)
	}
}
