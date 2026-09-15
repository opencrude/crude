package session

// WakeEventType categorizes events that can wake the main agent from deep sleep.
type WakeEventType string

const (
	// WakeSubAgentDone is emitted when a sub-agent completes its execution.
	WakeSubAgentDone WakeEventType = "sub_agent_done"
	// WakeShutdown signals the session loop to terminate.
	WakeShutdown WakeEventType = "shutdown"
)

// WakeEvent represents an event that can wake the main agent from deep sleep.
// When the session main loop is in deep sleep (no active agent turn), it blocks
// on a select of user input and wake events. A wake event causes the session
// to process the event and potentially re-enter the agent loop.
type WakeEvent struct {
	Type             WakeEventType
	Data             any
	SourceID         string
	HistoryMessageID string
}

func commonWakeHistoryMessage(events []WakeEvent) string {
	messageID := ""
	for _, event := range events {
		if event.HistoryMessageID == "" {
			return ""
		}
		if messageID == "" {
			messageID = event.HistoryMessageID
			continue
		}
		if messageID != event.HistoryMessageID {
			return ""
		}
	}
	return messageID
}
