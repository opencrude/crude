package workspace

import (
	"crude/types/insight"
	"path/filepath"
)

// Each open path observes only its own selected, initialized server. This is
// detached memory, independent of the shell's most recently focused file.
func (m *LSPManager) AssistanceCapabilities(path string) insight.LSPAssistanceCapabilities {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.assistanceCapabilitiesLocked(filepath.Clean(path))
}

// FileProducer returns the connected producer for this open path without
// copying completion/signature trigger lists on every editor update.
func (m *LSPManager) FileProducer(path string) insight.Producer {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, producer := m.assistanceStateLocked(filepath.Clean(path))
	return producer
}

// FileSynchronized reports whether the exact source reached its connected
// producer in the current project context. It never sends a document update.
func (m *LSPManager) FileSynchronized(source insight.Source, context uint64) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.fileSynchronizationLocked(filepath.Clean(source.Path)).MatchesSynchronizedSource(source, context)
}

func (m *LSPManager) fileSynchronizationLocked(path string) insight.LSPAssistanceCapabilities {
	state, producer := m.assistanceStateLocked(path)
	if state == nil {
		return insight.LSPAssistanceCapabilities{}
	}
	out := insight.LSPAssistanceCapabilities{Producer: producer}
	doc := m.documents[path]
	if !doc.syncPending && doc.syncedRevision == doc.revision && doc.syncedContext == m.contextGeneration {
		out.SynchronizedSource = doc.boundary.Source
		out.SynchronizedContext = doc.syncedContext
	}
	return out
}

func (m *LSPManager) assistanceStateLocked(path string) (*lspClientState, insight.Producer) {
	if m.closed || m.paused || !m.settings.Enabled || m.analysisErrorLocked() != nil {
		return nil, insight.Producer{}
	}
	doc := m.documents[path]
	if doc == nil || !doc.opened {
		return nil, insight.Producer{}
	}
	state := m.states[doc.key]
	if state == nil || doc.boundary.Producer.InstanceID != state.instance || state.status != LSPConnected || state.client == nil || state.closing || state.stopped || state.teardownErr != nil {
		return nil, insight.Producer{}
	}
	return state, doc.boundary.Producer
}

func (m *LSPManager) assistanceCapabilitiesLocked(path string) insight.LSPAssistanceCapabilities {
	state, producer := m.assistanceStateLocked(path)
	if state == nil {
		return insight.LSPAssistanceCapabilities{}
	}
	out := insight.CloneLSPAssistance(state.assistance)
	synchronized := m.fileSynchronizationLocked(path)
	out.Producer = producer
	out.SynchronizedSource, out.SynchronizedContext = synchronized.SynchronizedSource, synchronized.SynchronizedContext
	return out
}

// AssistanceSnapshots returns the language assistance capabilities of each open document.
func (m *LSPManager) AssistanceSnapshots() map[string]insight.LSPAssistanceCapabilities {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string]insight.LSPAssistanceCapabilities)
	for path := range m.documents {
		if caps := m.assistanceCapabilitiesLocked(path); caps.Producer.InstanceID != "" {
			out[path] = caps
		}
	}
	return out
}
