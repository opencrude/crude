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
	assert.Empty(t, all)
}

func TestLSPManager_AllDiagnostics_ReturnsCopy(t *testing.T) {
	m, client, snapshot := diagnosticFixture(t)
	client.emit(client.publication(snapshot.Path, []model.LSPDiagnostic{
		{Line: 1, Col: 2, EndLine: 1, EndCol: 3, Severity: model.DiagError, Message: "boom"},
	}))
	got := m.AllDiagnostics()
	assert.Len(t, got, 1)

	// Mutating the returned map must not affect internal state.
	batch := got[snapshot.Path]
	batch.Diagnostics[0].Message = "injected"
	batch.Boundary.Context.UnknownInputs[0] = "injected"
	*batch.ReportedVersion = -1
	delete(got, snapshot.Path)
	actual := requireDiagnostic(t, m, snapshot.Path, "boom")
	assert.NotEqual(t, "injected", actual.Boundary.Context.UnknownInputs[0])
}

func TestLSPManager_InvalidateDiagnosticsRetainsEvidence(t *testing.T) {
	m, client, snapshot := diagnosticFixture(t)
	client.emit(client.publication(snapshot.Path, []model.LSPDiagnostic{{Severity: model.DiagError, Message: "x"}}))
	before := requireDiagnostic(t, m, snapshot.Path, "x")
	m.InvalidateFile(snapshot.Path)
	after, ok := m.FileDiagnostics(snapshot.Path)
	assert.True(t, ok)
	assert.Equal(t, before.ID, after.ID)
	assert.Equal(t, model.InsightStale, after.State)
	assert.False(t, after.HasExactSource())
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
