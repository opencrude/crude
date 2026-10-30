package lspclient

import (
	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol"
	"github.com/stretchr/testify/assert"
	"path/filepath"
	"testing"
)

func TestLSPIntegration_New(t *testing.T) {
	l := NewLSPIntegration("gopls", nil, "/test")
	assert.NotNil(t, l)
	diags := l.Diagnostics()
	assert.Empty(t, diags)
}

func TestLSPIntegration_UpdateDiagnostics(t *testing.T) {
	l := NewLSPIntegration("gopls", nil, "/test")
	// Open a file so that diagnostics for it are accepted.
	l.mu.Lock()
	l.openFiles["/tmp/test.go"] = &openFileState{uri: "file:///tmp/test.go"}
	l.mu.Unlock()

	var got []LSPDiagnostic
	l.OnDiagnostics(func(fp string, diags []LSPDiagnostic) {
		got = diags
	})

	// Simulate updateDiagnostics via callback.
	l.mu.Lock()
	cb := l.onDiagnostics
	l.mu.Unlock()
	if cb != nil {
		cb("/tmp/test.go", []LSPDiagnostic{
			{Line: 0, Col: 0, EndCol: 5, Severity: DiagError, Message: "test error"},
		})
	}

	assert.Len(t, got, 1)
	assert.Equal(t, "test error", got[0].Message)
	assert.Equal(t, DiagError, got[0].Severity)
}

func TestLSPIntegration_OnDiagnosticsCallback(t *testing.T) {
	l := NewLSPIntegration("gopls", nil, "/test")
	called := false
	l.OnDiagnostics(func(fp string, diags []LSPDiagnostic) {
		called = true
		assert.Len(t, diags, 1)
		assert.Equal(t, DiagWarning, diags[0].Severity)
	})

	l.mu.Lock()
	cb := l.onDiagnostics
	l.mu.Unlock()
	if cb != nil {
		cb("/tmp/test.go", []LSPDiagnostic{
			{Severity: DiagWarning},
		})
	}

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
	l.OpenFile(ctx, "/tmp/test.go", "go", "package main")
	l.UpdateFile(ctx, "/tmp/test.go", "package main\nfunc main() {}")
	l.CloseFile(ctx, "/tmp/test.go")
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
