package types

import "time"

// Loop is a session-owned recurring prompt. The core, not a shell, owns its
// schedule. Paused loops do not start turns until explicitly resumed.
type Loop struct {
	ID        string        `json:"id"`
	Prompt    string        `json:"prompt"`
	Interval  time.Duration `json:"interval"`
	NextAt    time.Time     `json:"next_at"`
	ExpiresAt time.Time     `json:"expires_at"`
	Paused    bool          `json:"paused"`
	Reason    string        `json:"reason,omitempty"`
}
