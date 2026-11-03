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
			m.activeFilePath = filepath.Join(root, "notes.unknownext")
			m.documents[m.activeFilePath] = &lspDocument{}
		}, types.LanguageNoServer, "", types.LanguageActionOpenSettings},
		{"starting", func(m *LSPManager, _ string) { m.activeStatus = LSPStarting }, types.LanguageStarting, "Checking…", types.LanguageActionNone},
		{"ready", func(m *LSPManager, _ string) { m.activeStatus = LSPConnected }, types.LanguageReady, "", types.LanguageActionNone},
		{"failed", func(m *LSPManager, _ string) { m.activeStatus = LSPFailed; m.activeError = "not found" }, types.LanguageFailed, "Language server failed", types.LanguageActionRestartServer},
		{"large", func(m *LSPManager, _ string) {
			m.activeStatus = LSPIdle
			m.activeError = "Document exceeds the configured LSP size limit."
			m.activeFilePath = "large-source"
			m.settings.MaxDocumentBytes = 4
			m.documents[m.activeFilePath] = &lspDocument{text: "12345"}
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

func TestWaitingLanguageServerIsNotReportedAsLargeOrFailed(t *testing.T) {
	m, r, root := lifecycleManager(t)
	settings := m.Settings()
	settings.MaxServers = 1
	m.SetSettings(settings)
	first := filepath.Join(root, "first.go")
	second := filepath.Join(root, "second.py")
	editorSource(t, m, first, "first")
	awaitLSP(t, func() bool {
		clients := r.snapshot()
		return len(clients) == 1 && len(clients[0].snapshot()) == 1
	})
	editorSource(t, m, second, "second")
	m.SetActiveFile(second)
	awaitLSP(t, func() bool {
		for _, server := range m.ServerHealthSnapshots() {
			if server.Status == "Waiting for slot" {
				return true
			}
		}
		return false
	})
	view := m.ProjectInsights()
	if view.ServerFailed || view.Language.State != types.LanguageStarting ||
		view.Language.Label != "Waiting to start" || !strings.Contains(view.Language.Message, "limit") ||
		strings.Contains(view.Language.Message, "too large") {
		t.Fatalf("process capacity was misreported as a file or server failure: %+v", view)
	}
	for _, server := range m.ServerHealthSnapshots() {
		if server.Status == "Waiting for slot" && server.Error != "" {
			t.Fatalf("a queued server carries a failure into Problems: %+v", server)
		}
	}
	closeEditor(t, m, first)
	awaitLSP(t, func() bool { return m.ServerReady() })
	if view := m.ProjectInsights(); view.ServerFailed || view.Language.State != types.LanguageReady {
		t.Fatalf("released capacity did not clear the waiting explanation: %+v", view)
	}
}
