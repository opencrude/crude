package viewmodel

import (
	"crude/document"
	"crude/shellport"
	"crude/types"
)

func (m *App) applyDocumentInsight(msg shellport.DocumentInsightMsg) {
	finish := func(err error) {
		if msg.Done != nil {
			msg.Done(err)
		}
	}
	if msg.Owner != m.ws || msg.Valid != nil && !msg.Valid() {
		finish(document.ErrOwner)
		return
	}
	found := false
	for ed := range m.liveEditors {
		if ed.IsOpen() && ed.DocumentState().DocumentID == msg.View.DocumentID {
			found = true
			ed.InstallDocumentInsight(msg.View)
		}
	}
	if !found {
		// Open registration can publish before its asynchronous editor mounts.
		// Cache one derived result per actual subscribed document, with the same
		// exact-open token. ACKing it cannot mutate or acknowledge source.
		if m.pendingInsights == nil {
			m.pendingInsights = make(map[string]shellport.DocumentInsightMsg)
		}
		cached := msg
		cached.Done = nil
		m.pendingInsights[msg.View.DocumentID] = cached
	}
	finish(nil)
	if found && (msg.View.State == types.InsightFailed || msg.View.State == types.InsightLimited) {
		if m.Feedback != nil {
			m.Feedback.NotifyError("Syntax highlighting is unavailable. The syntax service reported an error. Reopen the file and try again. " + msg.View.Detail)
		}
	}
}
