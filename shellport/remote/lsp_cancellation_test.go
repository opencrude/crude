package remote

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"crude/core"
	"crude/coreclient"
	"crude/coreprotocol"
	"crude/types"
	"crude/workspace"
)

// Only the semantic provider is fake. The entry point under test is the actual
// remote LSPService, not a direct call to the transport helper it should use.
type cancelledPortProvider struct {
	workspace.LSPClient
	admit   func() error
	mu      sync.Mutex
	publish func(types.InsightDiagnosticPublication)
	latest  types.InsightDiagnosticPublication
	version int
	entered chan chan struct{}
	stops   atomic.Int32
}

func (p *cancelledPortProvider) Start(context.Context) error { return p.admit() }
func (p *cancelledPortProvider) Stop() error                 { p.stops.Add(1); return nil }
func (p *cancelledPortProvider) Done() <-chan struct{}       { return nil }
func (p *cancelledPortProvider) OnDiagnostics(publish func(types.InsightDiagnosticPublication)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.publish = publish
}
func (p *cancelledPortProvider) sync(boundary types.InsightBoundary) (int, error) {
	p.mu.Lock()
	p.version++
	version, publish := p.version, p.publish
	publication := types.InsightDiagnosticPublication{Boundary: boundary, Sequence: uint64(version),
		WireVersion: version, ReportedVersion: &version}
	p.latest = publication
	p.mu.Unlock()
	publish(publication)
	return version, nil
}
func (p *cancelledPortProvider) AwaitDiagnostics(context.Context, string) (types.InsightDiagnosticPublication, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.latest, p.latest.Boundary.Source.DocumentID != ""
}
func (p *cancelledPortProvider) OpenFile(_ context.Context, _, _, _ string, boundary types.InsightBoundary) (int, error) {
	return p.sync(boundary)
}
func (p *cancelledPortProvider) UpdateFile(_ context.Context, _, _ string, boundary types.InsightBoundary) (int, error) {
	return p.sync(boundary)
}
func (p *cancelledPortProvider) CloseFile(context.Context, string) {}
func (p *cancelledPortProvider) NotifyWatchedFilesChanged(context.Context, string, int) {
}
func (p *cancelledPortProvider) RequestDocumentSymbols(context.Context, string) []workspace.LSPSymbol {
	return nil
}
func (p *cancelledPortProvider) RequestSignatureHelp(ctx context.Context, _ string, _, _ int) (types.LSPSignatureHelp, error) {
	retired := make(chan struct{})
	p.entered <- retired
	<-ctx.Done()
	close(retired)
	return types.LSPSignatureHelp{}, ctx.Err()
}

type cancelledPortRuntime struct{ provider *cancelledPortProvider }

func (r cancelledPortRuntime) NewClient(_ types.LSPServerSpec, _ string, admit func() error) workspace.LSPClient {
	r.provider.admit = admit
	return r.provider
}

func TestSignatureRemoteServiceCancellationRetiresProviderAndKeepsAttachment(t *testing.T) {
	provider := &cancelledPortProvider{entered: make(chan chan struct{}, 1)}
	managers := make(chan *workspace.LSPManager, 1)
	retained, err := core.New(func(root string) (*workspace.Workspace, error) {
		ws := workspace.NewWorkspace(root, "", "test", types.Settings{}, nil)
		ws.LSPMgr.SetRuntime(cancelledPortRuntime{provider})
		managers <- ws.LSPMgr
		return ws, nil
	}, filepath.Join(t.TempDir(), "receipts.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := retained.Close(); err != nil {
			t.Error(err)
		}
	})
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	if err := os.WriteFile(path, []byte("package main\n"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	// Keep the server alive through the client's explicit session-end handshake.
	// testing.T.Context is cancelled before Cleanup callbacks run.
	serverCtx, stopServer := context.WithCancel(context.Background())
	var servers sync.WaitGroup
	t.Cleanup(func() { stopServer(); servers.Wait() })
	port, err := Attach(ctx, Options{Root: root, Dial: func(ctx context.Context, id string) (*coreclient.Client, error) {
		server, client := net.Pipe()
		servers.Add(1)
		go func() {
			defer servers.Done()
			_ = core.ServeConnection(serverCtx, retained, server)
		}()
		return coreclient.Dial(ctx, coreclient.SingleCarrier(client), id)
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := port.Close(); err != nil {
			t.Error(err)
		}
	})
	if err := port.call(ctx, coreprotocol.WorkspaceOverridesSet, &coreprotocol.WorkspacePreferencesRequest{
		Edits: []types.SettingEdit{{Path: []string{"analysis", "trust"}, After: json.RawMessage(`"project_execution_allowed"`)}}}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := port.OpenDocument(ctx, path); err != nil {
		t.Fatal(err)
	}
	if _, settled, _, err := (<-managers).ProbeDiagnostics(ctx, path); err != nil || !settled {
		t.Fatal("canonical provider boundary did not synchronize", err)
	}
	client, workspaceID := port.conn()
	owner := client.Owner()
	for i := range 24 {
		request, retire := context.WithCancel(ctx)
		done := make(chan error, 1)
		go func() {
			_, err := port.LSPMgr().RequestSignatureHelp(request, path, 0, 0)
			done <- err
		}()
		var retired chan struct{}
		select {
		case retired = <-provider.entered:
		case err := <-done:
			retire()
			t.Fatalf("request %d never reached provider: %v", i, err)
		case <-ctx.Done():
			retire()
			t.Fatal(ctx.Err())
		}
		retire()
		select {
		case err := <-done:
			if !errors.Is(err, context.Canceled) {
				t.Fatal("remote cancellation lost its cause or failed to join", err)
			}
		case <-time.After(time.Second):
			t.Fatal("remote cancellation did not promptly join its provider")
		}
		select {
		case <-retired:
		default:
			t.Fatal("remote service only abandoned its response stream; provider context is still live")
		}
		if next, id := port.conn(); next != client || id != workspaceID || next.Owner() != owner || provider.stops.Load() != 0 {
			t.Fatal("cancellation replaced the attachment or stopped its shared provider")
		}
	}
	if err := port.call(ctx, coreprotocol.StatusSnapshotMethod, &coreprotocol.Empty{}, &coreprotocol.StatusSnapshot{}); err != nil {
		t.Fatal("repeated aborted remote-service queries exhausted the live attachment", err)
	}
}
