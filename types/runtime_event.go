package types

// RuntimeEventIdentity binds semantic and lifecycle events to one exact owner
// and execution. Sequence is monotonic within that run; EventID is stable across
// persistence and duplicate delivery.
type RuntimeEventIdentity struct {
	EventID      string `json:"event_id"`
	OwnerID      string `json:"owner_id"`
	AgentID      string `json:"agent_id"`
	RunID        string `json:"run_id,omitempty"`
	RuntimeEpoch uint64 `json:"runtime_epoch"`
	Generation   uint64 `json:"generation"`
	Sequence     uint64 `json:"sequence"`
}

func (id RuntimeEventIdentity) Valid() bool {
	return id.EventID != "" && id.OwnerID != "" && id.AgentID != "" && id.RuntimeEpoch != 0 && id.Generation != 0 && id.Sequence != 0
}
