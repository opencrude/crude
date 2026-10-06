package recovery

import "time"

// State is the durable lifecycle of one recoverable run incident.
type State string

const (
	Recovering     State = "recovering"
	NeedsAttention State = "needs_attention"
	Recovered      State = "recovered"
	Interrupted    State = "interrupted"
	Cancelled      State = "cancelled"
)

// Status is the presentation-neutral, durable projection of one recovery
// incident. ID stays stable across every transition for that incident.
type Status struct {
	ID         string    `json:"id"`
	AgentID    string    `json:"agent_id"`
	Generation uint64    `json:"generation"`
	State      State     `json:"state"`
	Attempt    int       `json:"attempt"`
	RetryAt    time.Time `json:"retry_at,omitempty"`
	StartedAt  time.Time `json:"started_at"`
	UpdatedAt  time.Time `json:"updated_at"`
	Summary    string    `json:"summary"`
	Detail     string    `json:"detail"`
	Action     string    `json:"action"`
}

// CanRetry reports whether the incident is awaiting an owner action or cooldown.
// Recovering without a deadline is an acknowledged, active replacement attempt:
// retain its incident row, but let ordinary request events own live activity.
// Only the owner's acknowledgement, not the local clock, clears the deadline.
func (s Status) CanRetry() bool {
	if s.ID == "" {
		return false
	}
	return s.State == NeedsAttention || s.State == Interrupted ||
		s.State == Recovering && !s.RetryAt.IsZero()
}
