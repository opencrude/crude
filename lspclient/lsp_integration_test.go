package lspclient

import (
	"context"
	"path/filepath"
	"testing"

	"crude/types"

	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol"
	"github.com/stretchr/testify/assert"
)

func testBoundary(path string) types.InsightBoundary {
	return types.InsightBoundary{
		Source: types.InsightSource{WorkspaceGeneration: "workspace-1", DocumentID: "document-1",
			Path: path, Lifetime: "open-1", Revision: 1, Kind: "canonical"},
		Context: types.InsightContext{ID: "context-1", Root: filepath.Dir(path), Generation: 1,
			UnknownInputs: []string{"build configuration"}},
		Producer: types.InsightProducer{InstanceID: "launch-1", Provider: "test-lsp", ToolIdentity: "test-binary"},
	}
}

func openTestFile(t *testing.T, l *LSPIntegration, ctx context.Context, path, language, text string) int {
	t.Helper()
	version, err := l.OpenFile(ctx, path, language, text, testBoundary(path))
	if err != nil {
		t.Fatal(err)
	}
	return version
}

func TestLSPIntegration_New(t *testing.T) {
	l := NewLSPIntegration("gopls", nil, "/test")
	assert.NotNil(t, l)
	diags := l.Diagnostics()
	assert.Empty(t, diags)
}

func TestLSPIntegration_UpdateDiagnostics(t *testing.T) {
	l, launch, path := settleFixture(t)

	var got []LSPDiagnostic
	l.OnDiagnostics(func(p types.InsightDiagnosticPublication) {
		got = p.Diagnostics
	})

	l.updateDiagnostics(launch, protocol.URIFromPath(path), nil, []protocol.Diagnostic{
		{Range: protocol.Range{End: protocol.Position{Character: 5}}, Severity: DiagError, Message: "test error"},
	})

	assert.Len(t, got, 1)
	assert.Equal(t, "test error", got[0].Message)
	assert.Equal(t, DiagError, got[0].Severity)
}

func TestLSPIntegration_OnDiagnosticsCallback(t *testing.T) {
	l, launch, path := settleFixture(t)
	called := false
	l.OnDiagnostics(func(p types.InsightDiagnosticPublication) {
		called = true
		assert.Len(t, p.Diagnostics, 1)
		assert.Equal(t, DiagWarning, p.Diagnostics[0].Severity)
	})

	l.updateDiagnostics(launch, protocol.URIFromPath(path), nil, []protocol.Diagnostic{{Severity: DiagWarning}})

	assert.True(t, called, "onDiagnostics callback should have been called")
}

func TestLSPIntegration_URIToPathRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "with space", "main.go")
	uri := protocol.URIFromPath(path)
	l := NewLSPIntegration("gopls", nil, string(protocol.URIFromPath(filepath.Dir(path))))

	assert.Equal(t, filepath.Clean(path), l.uriToPath(uri))
}

// ── LSPIntegration start-failure tests ────────────────────────────

func TestLSPIntegration_StartFailsGracefully(t *testing.T) {
	l := NewLSPIntegration("nonexistent-lsp-server-12345", nil, "/tmp")
	ctx := t.Context()
	err := l.Start(ctx)
	assert.Error(t, err)
	diags := l.Diagnostics()
	assert.Empty(t, diags)
}

func TestLSPIntegration_StartNotCalled(t *testing.T) {
	l := NewLSPIntegration("gopls", nil, "/tmp")
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "test.go")
	version, err := l.OpenFile(ctx, path, "go", "package main", testBoundary(path))
	assert.Error(t, err)
	assert.Zero(t, version)
	version, err = l.UpdateFile(ctx, path, "package main\nfunc main() {}", testBoundary(path))
	assert.Error(t, err)
	assert.Zero(t, version)
	l.CloseFile(ctx, path)
	diags := l.Diagnostics()
	assert.Empty(t, diags)
}

func TestLSPIntegration_DoubleStart(t *testing.T) {
	l := NewLSPIntegration("nonexistent-lsp-server-12345", nil, "/tmp")
	ctx := t.Context()
	err1 := l.Start(ctx)
	assert.Error(t, err1)
	err2 := l.Start(ctx)
	assert.Error(t, err2)
}

func TestLSPIntegration_StopTwice(t *testing.T) {
	l := NewLSPIntegration("nonexistent-lsp-server-12345", nil, "/tmp")
	assert.NoError(t, l.Stop())
	assert.NoError(t, l.Stop())
}

func TestLSPIntegration_RequestCompletionsWithoutStart(t *testing.T) {
	l := NewLSPIntegration("gopls", nil, "/tmp")
	ctx := t.Context()
	items := l.RequestCompletions(ctx, "/tmp/test.go", 0, 0)
	assert.Nil(t, items)
}
