package workspace

import (
	"fmt"
	"unicode"
	"unicode/utf8"

	"crude/insight/governor"
	"crude/types"
)

// SetAnalysisPolicy installs the human-owned root binding without filesystem I/O.
// Replaying the same persisted choice cannot repair a failed identity check.
func (m *LSPManager) SetAnalysisPolicy(settings types.WorkspaceAnalysisSettings, rootIdentity string) {
	settings = types.NormalizeWorkspaceAnalysis(settings)
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || m.analysisSettings == settings && m.analysisRootIdentity == rootIdentity {
		return
	}
	m.analysisSettings, m.analysisRootIdentity = settings, rootIdentity
	m.analysisBlocked = ""
	if settings.Trust == types.AnalysisProjectExecutionAllowed && !validAnalysisIdentity(rootIdentity) {
		m.analysisBlocked = "The workspace root has no valid execution binding. Grant Project execution allowed again in Workspace settings."
	}
	m.analysisBoundaryLocked("Workspace analysis execution policy changed.")
}

func validAnalysisIdentity(identity string) bool {
	if identity == "" || len(identity) > 512 || !utf8.ValidString(identity) {
		return false
	}
	for _, r := range identity {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func (m *LSPManager) AnalysisPolicy() types.WorkspaceAnalysisSettings {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.analysisTrustedLocked() {
		return types.DefaultWorkspaceAnalysisSettings()
	}
	return m.analysisSettings
}

func (m *LSPManager) analysisTrustedLocked() bool {
	return m.analysisSettings.Trust == types.AnalysisProjectExecutionAllowed &&
		validAnalysisIdentity(m.analysisRootIdentity) && m.analysisBlocked == ""
}

func (m *LSPManager) analysisErrorLocked() error {
	if m.analysisBlocked != "" {
		return fmt.Errorf("workspace analysis execution blocked: %s", m.analysisBlocked)
	}
	if !m.analysisTrustedLocked() {
		return fmt.Errorf("no workspace execution grant: Static only; grant Project execution allowed in Workspace settings to run language servers")
	}
	if m.analysisGovernor == nil {
		return fmt.Errorf("analysis process governor unavailable; reopen this workspace in a core with analysis admission configured")
	}
	if m.analysisGovernorError != "" {
		return fmt.Errorf("analysis process admission unavailable: %s", m.analysisGovernorError)
	}
	return nil
}

func (m *LSPManager) analysisBoundaryLocked(reason string) {
	m.settingsGeneration++
	m.advanceContextLocked(reason)
	m.docSymbols = make(map[string][]LSPSymbol)
	m.symbolCount, m.symbolsCached = 0, false
	m.cancelUpdatesLocked()
	for _, state := range m.states {
		m.retireLocked(state)
	}
	for _, doc := range m.documents {
		doc.key, doc.opened = "", false
	}
	m.recomputeActiveStatusLocked()
	if len(m.states) > 0 || len(m.documents) > 0 {
		m.signalLocked()
	}
}

// SetAnalysisGovernor binds the core's shared capacity authority. No fallback
// governor is created by a manager. A retained lifetime must never change owners.
func (m *LSPManager) SetAnalysisGovernor(g *governor.Governor, workspaceID, generation string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if g == nil || !validAnalysisOwnerID(workspaceID) || !validAnalysisOwnerID(generation) {
		return fmt.Errorf("analysis governor requires a core authority and valid workspace/generation identities")
	}
	if m.analysisGovernor == g && m.analysisWorkspace == workspaceID && m.analysisGeneration == generation {
		return nil
	}
	if m.closed || m.analysisRetained {
		return fmt.Errorf("analysis governor cannot be replaced after work is retained")
	}
	if err := g.ConfigureWorkspace(workspaceID, m.settings.MaxServers); err != nil {
		return err
	}
	m.analysisGovernor, m.analysisWorkspace, m.analysisGeneration = g, workspaceID, generation
	m.analysisGovernorError = ""
	m.recomputeActiveStatusLocked()
	return nil
}

func validAnalysisOwnerID(id string) bool {
	if len(id) == 0 || len(id) > governor.MaxIDBytes {
		return false
	}
	for _, c := range id {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9',
			c == '.', c == '_', c == ':', c == '-':
		default:
			return false
		}
	}
	return true
}

// checkAnalysisRootLocked temporarily drops mu for native metadata I/O. Its
// caller must revalidate any document/state pointers after it returns. Identity
// metadata detects observed replacement; it is not a sandbox or TOCTOU immunity.
func (m *LSPManager) checkAnalysisRootLocked() bool {
	if m.analysisErrorLocked() != nil || m.closed || m.paused {
		return false
	}
	root, binding, epoch := m.workspaceRoot, m.analysisRootIdentity, m.settingsGeneration
	identity := m.analysisIdentity
	// Even metadata work belongs to this governor binding; replacement must not
	// race its off-lock return and silently transfer the launch to a new owner.
	m.analysisRetained = true
	m.mu.Unlock()
	actual, err := identity(root)
	m.mu.Lock()
	if epoch != m.settingsGeneration || root != m.workspaceRoot || m.closed || m.paused {
		return false
	}
	if err != nil || actual != binding {
		m.analysisBlocked = "Workspace root identity changed or is unavailable. Static only is active; verify the root and grant Project execution allowed again in Workspace settings."
		m.analysisBoundaryLocked(m.analysisBlocked)
		return false
	}
	return m.analysisErrorLocked() == nil
}

// startAdmission is called immediately before native spawn. Only memory and
// exact owner/epoch checks run here; the worker already observed root metadata.
func (m *LSPManager) startAdmission(key string, state *lspClientState, epoch uint64) func() error {
	return func() error {
		m.mu.Lock()
		defer m.mu.Unlock()
		if err := m.analysisErrorLocked(); err != nil {
			return err
		}
		if m.closed || m.paused || epoch != m.settingsGeneration || m.states[key] != state ||
			state.closing || state.stopped || state.lease == nil || !m.keyWantedLocked(key) {
			return fmt.Errorf("language server launch admission revoked; retry with current workspace settings")
		}
		return nil
	}
}
