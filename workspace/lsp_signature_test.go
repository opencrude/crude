package workspace

import (
	"context"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"crude/types"
)

type signatureGateClient struct {
	lifecycleClient
	entered chan struct{}
	release chan struct{}
	calls   atomic.Int64
}

func (c *signatureGateClient) RequestSignatureHelp(context.Context, string, int, int) (types.LSPSignatureHelp, error) {
	c.calls.Add(1)
	if c.entered != nil {
		close(c.entered)
		<-c.release
	}
	return types.LSPSignatureHelp{Signatures: []types.LSPSignature{{Label: "call(value int)", ActiveParameter: 0,
		Parameters: []types.LSPSignatureParameter{{Label: "value int"}}}}}, nil
}

func TestSignatureManagerRequiresQualifiedBoundaryAndLiveRequest(t *testing.T) {
	for _, condition := range []string{"unqualified", "cancelled"} {
		t.Run(condition, func(t *testing.T) {
			root := t.TempDir()
			client := &signatureGateClient{lifecycleClient: lifecycleClient{runtime: &lifecycleRuntime{}}}
			manager := NewLSPManager(nil, signatureGateRuntime{client})
			t.Cleanup(manager.Close)
			trustAnalysis(t, manager, root)
			source := editorSource(t, manager, filepath.Join(root, "main.go"), "package main\n")
			awaitLSP(t, func() bool { return manager.ServerReady() })
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if condition == "cancelled" {
				cancel()
			} else {
				manager.mu.Lock()
				manager.documents[source.Path].boundary = types.InsightBoundary{}
				manager.mu.Unlock()
			}
			help, err := manager.RequestSignatureHelp(ctx, source.Path, 0, 0)
			if err == nil || len(help.Signatures) != 0 || client.calls.Load() != 0 {
				t.Fatal("unqualified or cancelled request probed/accepted provider evidence", help, err, client.calls.Load())
			}
			if condition == "cancelled" && !errors.Is(err, context.Canceled) {
				t.Fatal("request cancellation lost its cause", err)
			}
		})
	}
}

type signatureGateRuntime struct{ client *signatureGateClient }

func (r signatureGateRuntime) NewClient(types.LSPServerSpec, string, func() error) LSPClient {
	return r.client
}

func TestSignatureManagerRetainsSourceAndRejectsContextChange(t *testing.T) {
	for _, changed := range []bool{false, true} {
		t.Run(map[bool]string{false: "exact", true: "changed-context"}[changed], func(t *testing.T) {
			root := t.TempDir()
			client := &signatureGateClient{lifecycleClient: lifecycleClient{runtime: &lifecycleRuntime{}},
				entered: make(chan struct{}), release: make(chan struct{})}
			manager := NewLSPManager(nil, signatureGateRuntime{client})
			t.Cleanup(manager.Close)
			trustAnalysis(t, manager, root)
			source := editorSource(t, manager, filepath.Join(root, "main.go"), "package main\nvar x = call(1)\n")
			awaitLSP(t, func() bool { return manager.ServerReady() })
			type result struct {
				help types.LSPSignatureHelp
				err  error
			}
			done := make(chan result, 1)
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			go func() {
				help, err := manager.RequestSignatureHelp(ctx, source.Path, 1, 13)
				done <- result{help, err}
			}()
			select {
			case <-client.entered:
			case <-ctx.Done():
				close(client.release)
				t.Fatal("parameter request did not reach the exact synchronized client")
			}
			if changed {
				manager.NotifyWatchedFilesChanged(ctx, filepath.Join(root, "build-config"), 2)
			}
			close(client.release)
			got := <-done
			if changed {
				if got.err == nil || len(got.help.Signatures) != 0 {
					t.Fatal("changed context retained the old semantic answer", got)
				}
				return
			}
			owner, generation := manager.ContextCursor()
			if got.err != nil || len(got.help.Signatures) != 1 ||
				got.help.Boundary.Source.DocumentID != source.ID || got.help.Boundary.Source.Revision != source.Revision ||
				got.help.Boundary.Source.Lifetime != source.AnalysisLifetime || got.help.Boundary.Source.WorkspaceGeneration != owner ||
				got.help.Boundary.Context.Generation != generation || got.help.Boundary.Producer.InstanceID == "" {
				t.Fatal("parameter answer lost exact source/provider/context qualification", got)
			}
		})
	}
}
