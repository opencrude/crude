package workspace

import (
	"path/filepath"
	"strings"
	"testing"

	"crude/types"
)

func TestLanguageStatusOwnerStates(t *testing.T) {
	for _, tc := range []struct {
		name   string
		setup  func(*LSPManager, string)
		state  types.LanguageServiceState
		label  string
		action types.LanguageServiceAction
	}{
		{"restricted", func(m *LSPManager, _ string) { m.analysisSettings = types.WorkspaceAnalysisSettings{} }, types.LanguageRestricted, "Restricted mode", types.LanguageActionTrustFolder},
		{"blocked", func(m *LSPManager, _ string) {
			m.analysisBlocked = "Language servers could not start. This folder changed. Trust this folder again."
		}, types.LanguageFailed, "Language server failed", types.LanguageActionTrustFolder},
		{"governor", func(m *LSPManager, _ string) { m.analysisGovernor = nil }, types.LanguageFailed, "Language server failed", types.LanguageActionNone},
		{"disabled", func(m *LSPManager, _ string) { m.settings.Enabled = false }, types.LanguageOff, "Language servers off", types.LanguageActionOpenSettings},
		{"stopped", func(m *LSPManager, _ string) { m.paused = true }, types.LanguageOff, "Language servers stopped", types.LanguageActionStartServers},
		{"idle", func(m *LSPManager, _ string) {}, types.LanguageIdle, "", types.LanguageActionNone},
		{"no server", func(m *LSPManager, root string) {
			m.activeFilePath = filepath.Join(root, "readme.md")
			m.documents[m.activeFilePath] = &lspDocument{}
		}, types.LanguageNoServer, "", types.LanguageActionOpenSettings},
		{"starting", func(m *LSPManager, _ string) { m.activeStatus = LSPStarting }, types.LanguageStarting, "Checking…", types.LanguageActionNone},
		{"ready", func(m *LSPManager, _ string) { m.activeStatus = LSPConnected }, types.LanguageReady, "", types.LanguageActionNone},
		{"failed", func(m *LSPManager, _ string) { m.activeStatus = LSPFailed; m.activeError = "not found" }, types.LanguageFailed, "Language server failed", types.LanguageActionRestartServer},
		{"large", func(m *LSPManager, _ string) {
			m.activeStatus = LSPIdle
			m.activeError = "Document exceeds the configured LSP size limit."
		}, types.LanguageFailed, "Language server failed", types.LanguageActionOpenSettings},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, _, root := lifecycleManager(t)
			m.mu.Lock()
			tc.setup(m, root)
			m.recomputeLanguageStatusLocked()
			got := m.language
			m.mu.Unlock()
			if got.State != tc.state || got.Label != tc.label || got.Action != tc.action || got.Message == "" {
				t.Fatalf("status = %+v", got)
			}
			for _, banned := range []string{"pending", "unverified", "partial", "grant", "admission", "publication", "generation", "hidden", "Static only", "Project execution", "Syntax shared"} {
				if strings.Contains(got.Label+" "+got.Message+" "+got.ActionLabel, banned) {
					t.Fatalf("internal wording %q: %+v", banned, got)
				}
			}
		})
	}
}
