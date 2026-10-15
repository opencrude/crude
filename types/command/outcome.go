// Package command carries immutable command lifecycle evidence across runtime,
// persistence and presentation without coupling consumers to process managers.
package command

import "time"

type Outcome struct {
	Handle      string        `json:"handle"`
	Command     string        `json:"command"`
	PID         int           `json:"pid"`
	Status      string        `json:"status"`
	StartedAt   time.Time     `json:"started_at"`
	EndedAt     time.Time     `json:"ended_at"`
	Duration    time.Duration `json:"duration"`
	ExitCode    int           `json:"exit_code"`
	ExitKnown   bool          `json:"exit_known"`
	OutputBytes int           `json:"output_bytes"`
	Error       string        `json:"error,omitempty"`
}

func Clone(value *Outcome) *Outcome {
	if value == nil {
		return nil
	}
	out := *value
	return &out
}
