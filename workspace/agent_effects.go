package workspace

// AgentEffects describes notifications produced by one event reduction. Changed
// flags distinguish an explicit reset from an event that leaves a value alone.
// The caller owns presentation and scheduling; these values contain no commands
// or callbacks.
type AgentEffects struct {
	Status          string
	StatusChanged   bool
	ThinkingChanged bool
	Thinking        bool
	Streaming       bool
	Blocking        bool
	Prompt          *AgentPrompt
	DismissBlockID  string
	DismissPrompt   bool
	Done            *AgentDoneResult
	Error           *AgentErrorResult
}

// AgentPrompt retains the exact pending block identity and prompt payload.
type AgentPrompt struct {
	BlockID       string
	Question      string
	Operation     string
	Tool          string
	Target        string
	Risk          string
	Reason        string
	Suggestion    string
	QuestionsJSON string
	IsQuestion    bool
}

type AgentDoneResult struct {
	AgentID string
	Reason  string
}

type AgentErrorResult struct {
	AgentID string
	Err     string
}

func (e *AgentEffects) setStatus(status string) {
	e.Status, e.StatusChanged = status, true
}

func (e *AgentEffects) setThinking(thinking, streaming, blocking bool) {
	e.ThinkingChanged = true
	e.Thinking, e.Streaming, e.Blocking = thinking, streaming, blocking
}
