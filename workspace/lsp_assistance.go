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

// FileProducer returns the connected producer for this open path without
// copying completion/signature trigger lists on every editor update.
func (m *LSPManager) FileProducer(path string) types.InsightProducer {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, producer := m.assistanceStateLocked(filepath.Clean(path))
	return producer
}

// FileSynchronized reports whether the exact source reached its connected
// producer in the current project context. It never sends a document update.
func (m *LSPManager) FileSynchronized(source types.InsightSource, context uint64) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.fileSynchronizationLocked(filepath.Clean(source.Path)).MatchesSynchronizedSource(source, context)
}

func (m *LSPManager) fileSynchronizationLocked(path string) types.LSPAssistanceCapabilities {
	state, producer := m.assistanceStateLocked(path)
	if state == nil {
		return types.LSPAssistanceCapabilities{}
	}
	out := types.LSPAssistanceCapabilities{Producer: producer}
	doc := m.documents[path]
	if !doc.syncPending && doc.syncedRevision == doc.revision && doc.syncedContext == m.contextGeneration {
		out.SynchronizedSource = doc.boundary.Source
		out.SynchronizedContext = doc.syncedContext
	}
	return out
}

func (m *LSPManager) assistanceStateLocked(path string) (*lspClientState, types.InsightProducer) {
	if m.closed || m.paused || !m.settings.Enabled || m.analysisErrorLocked() != nil {
		return nil, types.InsightProducer{}
	}
	doc := m.documents[path]
	if doc == nil || !doc.opened {
		return nil, types.InsightProducer{}
	}
	state := m.states[doc.key]
	if state == nil || doc.boundary.Producer.InstanceID != state.instance || state.status != LSPConnected || state.client == nil || state.closing || state.stopped || state.teardownErr != nil {
		return nil, types.InsightProducer{}
	}
	return state, doc.boundary.Producer
}

func (m *LSPManager) assistanceCapabilitiesLocked(path string) types.LSPAssistanceCapabilities {
	state, producer := m.assistanceStateLocked(path)
	if state == nil {
		return types.LSPAssistanceCapabilities{}
	}
	out := types.CloneLSPAssistance(state.assistance)
	synchronized := m.fileSynchronizationLocked(path)
	out.Producer = producer
	out.SynchronizedSource, out.SynchronizedContext = synchronized.SynchronizedSource, synchronized.SynchronizedContext
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
