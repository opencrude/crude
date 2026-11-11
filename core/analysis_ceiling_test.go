package core

import (
	"crude/types"
	"crude/workspace"
	"path/filepath"
	"testing"
)

func TestAnalysisProcessCeilingHonorsEveryWorkspaceQuota(t *testing.T) {
	r, err := New(func(string) (*workspace.Workspace, error) { t.Fatal("unexpected workspace launch"); return nil, nil }, filepath.Join(t.TempDir(), "receipts.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := r.Close(); err != nil {
			t.Error(err)
		}
	})
	ceiling := r.analysisGovernor.Snapshot().Limits.MaxProcesses
	for quota := 1; quota <= types.MaxLanguageServers; quota++ {
		settings := types.DefaultLSPSettings()
		settings.MaxServers = quota
		if err := settings.Validate(); err != nil {
			t.Fatal(err)
		}
		if ceiling < quota {
			t.Fatalf("core ceiling %d cannot honor allowed workspace quota %d", ceiling, quota)
		}
	}
}
