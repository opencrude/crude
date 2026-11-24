package command

import (
	"fmt"
	"time"
)

type Outcome struct {
	TaskID      string        `json:"task_id,omitempty"`
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
	TimedOut    bool          `json:"timed_out,omitempty"`
	Uncertain   bool          `json:"uncertain,omitempty"`
	// Output is a bounded immutable excerpt captured at settlement. It survives
	// session/core restart even when the live process manager no longer exists.
	Output *Result `json:"output,omitempty"`
}

// Percent is the observed fraction in whole percent; callers must distinguish
// an unknown total from a measured zero before displaying a percentage.
func (p Progress) Percent() int {
	if p.Total <= 0 {
		return 0
	}
	return int(100 * (float64(max(int64(0), min(p.Current, p.Total))) / float64(p.Total)))
}

// ExecutionID is independent of a reusable handle and of promotion to async.
// Missing owner evidence cannot identify a command and never permits a merge.
func ExecutionID(pid int, started time.Time) string {
	if pid <= 0 || started.IsZero() {
		return ""
	}
	return fmt.Sprintf("process:%d:%d", pid, started.UnixNano())
}

// JobID identifies an admitted operation even when it spans multiple processes.
// Process-backed commands retain their original process lifetime identity.
func JobID(taskID string, pid int, started time.Time) string {
	if taskID != "" && pid == 0 && !started.IsZero() {
		return "task:" + taskID
	}
	if taskID != "" {
		return ""
	}
	return ExecutionID(pid, started)
}

// Progress describes observed work. Total zero means no measured total exists.
type Progress struct {
	Phase   string `json:"phase,omitempty"`
	Current int64  `json:"current,omitempty"`
	Total   int64  `json:"total,omitempty"`
}

func Clone(value *Outcome) *Outcome {
	if value == nil {
		return nil
	}
	out := *value
	if value.Output != nil {
		result := *value.Output
		result.OutputDescriptor.ShownRanges = append([]OutputRange(nil), result.OutputDescriptor.ShownRanges...)
		if result.ExitCode != nil {
			code := *result.ExitCode
			result.ExitCode = &code
		}
		if result.Progress != nil {
			progress := *result.Progress
			result.Progress = &progress
		}
		if result.Page != nil {
			page := *result.Page
			result.Page = &page
		}
		out.Output = &result
	}
	return &out
}
