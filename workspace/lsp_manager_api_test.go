package workspace

import (
	"testing"

	"github.com/stretchr/testify/assert"

	model "crude/types"
)

func TestLSPManager_IsConfigured_FreshManager(t *testing.T) {
	m := NewLSPManager(testLogger{})
	assert.False(t, m.IsConfigured(), "fresh manager with no clients and LSPIdle should not be configured")
}

func TestLSPManager_AllDiagnostics_Empty(t *testing.T) {
	m := NewLSPManager(testLogger{})
	all := m.AllDiagnostics()
	assert.NotNil(t, all)
	assert.Empty(t, all)
}

func TestLSPManager_AllDiagnostics_ReturnsCopy(t *testing.T) {
	m := NewLSPManager(testLogger{})
	// Seed some diagnostics directly via the internal setter.
	m.setFileDiagnostics("/tmp/foo.go", []model.LSPDiagnostic{
		{Line: 1, Col: 2, Severity: model.DiagError, Message: "boom"},
	})
	got := m.AllDiagnostics()
	assert.Len(t, got, 1)

	// Mutating the returned map must not affect internal state.
	got["/tmp/other.go"] = []model.LSPDiagnostic{{Message: "injected"}}
	assert.Empty(t, m.AllDiagnostics()["/tmp/other.go"])
}

func TestLSPManager_ClearDiagnostics(t *testing.T) {
	m := NewLSPManager(testLogger{})
	m.setFileDiagnostics("/tmp/a.go", []model.LSPDiagnostic{
		{Line: 0, Severity: model.DiagError, Message: "x"},
	})
	m.setFileDiagnostics("/tmp/b.go", []model.LSPDiagnostic{
		{Line: 0, Severity: model.DiagError, Message: "y"},
	})
	assert.Len(t, m.AllDiagnostics(), 2)

	m.ClearDiagnostics()
	assert.Empty(t, m.AllDiagnostics())
}

func TestLSPManager_Stop_ResetsState(t *testing.T) {
	m := NewLSPManager(testLogger{})
	// Simulate a server having been started and connected.
	m.activeConnected = true
	m.activeStatus = model.LSPConnected
	m.activeError = "fake"

	m.Stop()

	assert.False(t, m.activeConnected, "Stop should reset activeConnected")
	assert.Equal(t, model.LSPIdle, m.activeStatus, "Stop should reset status to LSPIdle")
	assert.Empty(t, m.activeError, "Stop should clear error")
	assert.Equal(t, 0, m.inflight, "Stop should reset in-flight count")
	assert.False(t, m.syncing, "Stop should clear syncing flag")
}

func TestLSPManager_FileLastSync_NoEntry(t *testing.T) {
	m := NewLSPManager(testLogger{})
	assert.Equal(t, int64(0), m.FileLastSync("/tmp/nonexistent.go"))
}
