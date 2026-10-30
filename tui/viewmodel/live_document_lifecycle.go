package viewmodel

import (
	"crude/document"
	"crude/shellport"
)

// Fence before clearing the UI's accepted publications: tokens held by the
// ingress queue become stale too, including a switch away and back to this port.
func (m *App) fenceLiveDocumentTopics() {
	if epoch, ok := m.ws.(shellport.LiveDocumentEpoch); ok {
		epoch.FenceDocumentTopics()
	}
	clear(m.pendingInsights)
	for id, msg := range m.pendingDocumentTopics {
		if msg.Done != nil {
			msg.Done(document.ErrOwner)
		}
		delete(m.pendingDocumentTopics, id)
	}
	for _, state := range m.liveEditors {
		if state.topic != nil && state.topic.Done != nil {
			state.topic.Done(document.ErrOwner)
		}
		state.topic = nil
		state.topicReady = false
		state.sync = false
	}
}

func (m *App) resumeLiveDocumentTopics() {
	if epoch, ok := m.ws.(shellport.LiveDocumentEpoch); ok {
		epoch.ResumeDocumentTopics()
	}
}
