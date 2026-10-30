package lspclient

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"crude/types"

	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol"
)

type observedWaitContext struct {
	context.Context
	entered chan struct{}
	once    sync.Once
}

func (c *observedWaitContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.entered) })
	return c.Context.Done()
}

func TestAwaitDiagnosticsCannotFollowAnotherSource(t *testing.T) {
	for _, mutation := range []string{"revision", "reopen", "launch"} {
		t.Run(mutation, func(t *testing.T) {
			l, _, path := settleFixture(t)
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			waitCtx := &observedWaitContext{Context: ctx, entered: make(chan struct{})}
			result := make(chan types.InsightDiagnosticPublication, 1)
			go func() {
				p, ok := l.AwaitDiagnostics(waitCtx, path)
				if ok {
					p.Detail = "unexpected settled answer"
				}
				result <- p
			}()
			select {
			case <-waitCtx.entered:
			case <-ctx.Done():
				t.Fatal("Await did not enter its wait")
			}
			l.mu.Lock()
			switch mutation {
			case "revision":
				l.openFiles[path].boundary.Source.Revision++
				l.openFiles[path].resetVersion(2)
			case "reopen":
				l.openFiles[path] = &openFileState{version: 1, boundary: testBoundary(path)}
			case "launch":
				l.launch = &lspLaunch{}
			}
			l.signalLocked()
			l.mu.Unlock()
			select {
			case p := <-result:
				if !reflect.DeepEqual(p, types.InsightDiagnosticPublication{}) {
					t.Fatalf("old waiter followed new source: %+v", p)
				}
			case <-ctx.Done():
				t.Fatal("source replacement did not release old waiter")
			}
		})
	}
}

func TestDiagnosticPublicationsOverFramedTransport(t *testing.T) {
	for _, mode := range []string{"publication-versioned", "publication-unversioned"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "source.go")
			l := protocolIntegration(t, mode, root)
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			if err := l.Start(ctx); err != nil {
				t.Fatal(err)
			}
			callbacks := make(chan types.InsightDiagnosticPublication, 32)
			l.OnDiagnostics(func(p types.InsightDiagnosticPublication) { callbacks <- p })
			b := testBoundary(path)
			b.Source.Revision = 900 // source revision must not become wire version
			sequence := uint64(0)
			check := func(wantWire int) {
				t.Helper()
				var p types.InsightDiagnosticPublication
				select {
				case p = <-callbacks:
				case <-ctx.Done():
					t.Fatal("no source publication")
				}
				if !reflect.DeepEqual(p.Boundary, b) || p.WireVersion != wantWire ||
					p.Sequence <= sequence || len(p.Diagnostics) != 1 ||
					p.Diagnostics[0].Message != "current input" || p.Diagnostics[0].Col != 3 ||
					p.Diagnostics[0].EndCol != 4 || p.Diagnostics[0].Code != "42" {
					t.Fatalf("incorrect publication: %+v", p)
				}
				if mode == "publication-unversioned" {
					if p.ReportedVersion != nil {
						t.Fatal("absent reported version was fabricated")
					}
				} else if p.ReportedVersion == nil || *p.ReportedVersion != wantWire {
					t.Fatalf("incorrect reported version: %+v", p)
				}
				sequence = p.Sequence
				got, settled := l.AwaitDiagnostics(ctx, path)
				if !settled || !reflect.DeepEqual(got, p) {
					t.Fatalf("Await must retain literal callback publication: got=%+v callback=%+v settled=%v", got, p, settled)
				}
				// Callback and Await own independent containers.
				p.Boundary.Context.UnknownInputs[0] = "mutated callback"
				p.Diagnostics[0].Message = "mutated callback"
				if p.ReportedVersion != nil {
					*p.ReportedVersion = -1
				}
				got.Boundary.Context.UnknownInputs[0] = "mutated await"
				got.Diagnostics[0].Message = "mutated await"
				if got.ReportedVersion != nil {
					*got.ReportedVersion = -2
				}
				again, ok := l.AwaitDiagnostics(ctx, path)
				if !ok || again.Sequence != sequence || !reflect.DeepEqual(again.Boundary, b) ||
					again.Diagnostics[0].Message != "current input" ||
					(again.ReportedVersion != nil && *again.ReportedVersion != wantWire) {
					t.Fatalf("caller mutation reached transport storage: %+v", again)
				}
				select {
				case stale := <-callbacks:
					t.Fatalf("obsolete explicit version leaked: %+v", stale)
				default:
				}
			}
			wire, err := l.OpenFile(ctx, path, "go", "a😀b", b)
			if err != nil || wire != 1 {
				t.Fatalf("open wire=%d err=%v", wire, err)
			}
			check(1)
			// Every full-boundary field matters even with unchanged source bytes.
			changes := []func(){
				func() { b.Source.Revision++ },
				func() { b.Context.Generation++ },
				func() { b.Source.Lifetime = "open-2" },
				func() { b.Producer.InstanceID = "launch-2" },
				func() { b.Context.UnknownInputs[0] = "other missing input" },
			}
			for i, change := range changes {
				change()
				wire, err = l.UpdateFile(ctx, path, "a😀b", b)
				if err != nil || wire != i+2 {
					t.Fatalf("equal-text boundary change wire=%d err=%v", wire, err)
				}
				check(i + 2)
			}
			if duplicate, err := l.UpdateFile(ctx, path, "a😀b", b); err != nil || duplicate != wire {
				t.Fatalf("identical boundary changed wire: %d %v", duplicate, err)
			}
			l.CloseFile(ctx, path)
			b.Source.Lifetime = "reopened"
			wire, err = l.OpenFile(ctx, path, "go", "a😀b", b)
			if err != nil || wire != 7 {
				t.Fatalf("reopen wire=%d err=%v", wire, err)
			}
			check(7)
			// Retired launch events cannot enter the replacement, and sequence
			// ordering survives replacement rather than starting from one again.
			oldLaunch := l.launch
			if err := l.Stop(); err != nil {
				t.Fatal(err)
			}
			if err := l.Start(ctx); err != nil {
				t.Fatal(err)
			}
			b.Producer.InstanceID = "replacement-process"
			wire, err = l.OpenFile(ctx, path, "go", "a😀b", b)
			if err != nil || wire != 8 {
				t.Fatalf("replacement wire=%d err=%v", wire, err)
			}
			l.updateDiagnostics(oldLaunch, protocol.URIFromPath(path), nil, []protocol.Diagnostic{{Message: "retired process"}})
			check(8)
		})
	}
}

func TestEqualTextBoundaryClearsTransientPublication(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "source.go")
	l := protocolIntegration(t, "normal", root) // does not publish on didOpen
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := l.Start(ctx); err != nil {
		t.Fatal(err)
	}
	b := testBoundary(path)
	openTestFile(t, l, ctx, path, "go", "unchanged")
	l.updateDiagnostics(l.launch, protocol.URIFromPath(path), nil, nil)
	b.Source.Revision++
	// The transport lock spans admission and installation. Inspect from the
	// send owner before a real server response could supersede the reset.
	l.mu.Lock()
	wire, err := l.updateFileLocked(ctx, l.openFiles[path], "unchanged", b)
	state := l.openFiles[path]
	if err != nil || wire != 2 || !state.publishedAt.IsZero() || state.publication.Sequence != 0 {
		t.Errorf("old settled publication survived equal-text revision: %+v, %v", state, err)
	}
	l.mu.Unlock()
}

func TestSynchronizationRejectsInvalidInputsAndMissingDocument(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "source.go")
	l := protocolIntegration(t, "normal", root)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := l.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if wire, err := l.UpdateFile(ctx, path, "text", testBoundary(path)); err == nil || wire != 0 {
		t.Fatalf("missing document update = %d, %v", wire, err)
	}
	for _, change := range []func(*types.InsightBoundary){
		func(b *types.InsightBoundary) { b.Source.Path = filepath.Join(root, "wrong.go") },
		func(b *types.InsightBoundary) { b.Source.Revision = 0 },
		func(b *types.InsightBoundary) { b.Source.Lifetime = "" },
		func(b *types.InsightBoundary) { b.Source.Kind = "unknown" },
		func(b *types.InsightBoundary) { b.Context.Generation = 0 },
		func(b *types.InsightBoundary) { b.Producer.InstanceID = "" },
		func(b *types.InsightBoundary) { b.Context.UnknownInputs = []string{"\xff"} },
	} {
		b := testBoundary(path)
		change(&b)
		if wire, err := l.OpenFile(ctx, path, "go", "text", b); err == nil || wire != 0 {
			t.Fatalf("invalid boundary admitted: %+v wire=%d err=%v", b, wire, err)
		}
		if wire, err := l.UpdateFile(ctx, path, "text", b); err == nil || wire != 0 {
			t.Fatalf("invalid update admitted: %+v wire=%d err=%v", b, wire, err)
		}
	}
	for _, invalid := range []string{"", "relative.go", "bad\x00path"} {
		if wire, err := l.OpenFile(ctx, invalid, "go", "text", testBoundary(path)); err == nil || wire != 0 {
			t.Fatalf("invalid path admitted: %q wire=%d err=%v", invalid, wire, err)
		}
	}
	if err := l.Stop(); err != nil {
		t.Fatal(err)
	}
	if wire, err := l.OpenFile(ctx, path, "go", "text", testBoundary(path)); err == nil || wire != 0 {
		t.Fatalf("stopped client admitted open: %d, %v", wire, err)
	}
}

func TestDiagnosticTransportLimitsAreExplicit(t *testing.T) {
	for _, ds := range [][]protocol.Diagnostic{
		make([]protocol.Diagnostic, maxTransportDiagnostics+1),
		{{Message: strings.Repeat("x", maxTransportDiagnosticBytes+1)}},
	} {
		l, launch, path := settleFixture(t)
		var callback types.InsightDiagnosticPublication
		l.OnDiagnostics(func(p types.InsightDiagnosticPublication) { callback = p })
		l.updateDiagnostics(launch, protocol.URIFromPath(path), nil, ds)
		got, ok := l.AwaitDiagnostics(t.Context(), path)
		if !ok || !got.Limited || got.Detail == "" || got.Sequence == 0 ||
			!reflect.DeepEqual(got, callback) || len(got.Diagnostics) > maxTransportDiagnostics {
			t.Fatalf("unreported transport limit: %+v", got)
		}
	}
}
