package workspace

import (
	"crude/types"
	"path/filepath"
)

// Each open path observes only its own selected, initialized server. This is
// detached memory, independent of the shell's most recently focused file.
func (m *LSPManager) AssistanceCapabilities(path string) types.LSPAssistanceCapabilities {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.assistanceCapabilitiesLocked(filepath.Clean(path))
}

func (m *LSPManager) assistanceCapabilitiesLocked(path string) types.LSPAssistanceCapabilities {
	if m.closed || m.paused || !m.settings.Enabled || m.analysisErrorLocked() != nil {
		return types.LSPAssistanceCapabilities{}
	}
	doc := m.documents[path]
	if doc == nil || !doc.opened {
		return types.LSPAssistanceCapabilities{}
	}
	state := m.states[doc.key]
	if state == nil || doc.boundary.Producer.InstanceID != state.instance || state.status != LSPConnected || state.client == nil || state.closing || state.stopped || state.teardownErr != nil {
		return types.LSPAssistanceCapabilities{}
	}
	out := types.CloneLSPAssistance(state.assistance)
	out.Producer = doc.boundary.Producer
	return out
}

func (m *LSPManager) AssistanceSnapshots() map[string]types.LSPAssistanceCapabilities {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string]types.LSPAssistanceCapabilities)
	for path := range m.documents {
		if caps := m.assistanceCapabilitiesLocked(path); caps.Producer.InstanceID != "" {
			out[path] = caps
		}
	}
	return out
}
