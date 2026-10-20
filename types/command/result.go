package command

// Status describes the process, not the success of a tool which inspected it.
type Status string

const (
	Running   Status = "running"
	Completed Status = "completed"
	Failed    Status = "failed"
	Killed    Status = "killed"
)

// OutputRange addresses the exact half-open raw byte interval shown in a result.
type OutputRange struct {
	StartByte int `json:"start_byte"`
	EndByte   int `json:"end_byte"`
}

type OutputDescriptor struct {
	Handle         string        `json:"handle,omitempty"`
	Status         Status        `json:"status"`
	TotalBytes     int           `json:"total_bytes"`
	ShownRanges    []OutputRange `json:"shown_ranges,omitempty"`
	OmittedBytes   int           `json:"omitted_bytes,omitempty"`
	RetentionError string        `json:"retention_error,omitempty"`
}

type Page struct {
	SinceByte int  `json:"since_byte"`
	NextByte  int  `json:"next_byte"`
	HasMore   bool `json:"has_more"`
}

// Result is the command tool wire contract. All execution modes and output reads
// keep process facts separate from captured bytes and model-facing retrieval
// guidance. The original result is retained in the conversation audit log.
type Result struct {
	Command            string           `json:"command,omitempty"`
	CWD                string           `json:"cwd,omitempty"`
	PID                int              `json:"pid,omitempty"`
	WallTimeSeconds    float64          `json:"wall_time_seconds,omitempty"`
	ExitCode           *int             `json:"exit_code,omitempty"`
	SessionID          int              `json:"session_id,omitempty"`
	OriginalTokenCount int              `json:"original_token_count,omitempty"`
	OutputDescriptor   OutputDescriptor `json:"output_descriptor"`
	Page               *Page            `json:"page,omitempty"`
	Error              string           `json:"error,omitempty"`
	Cancelled          bool             `json:"cancelled,omitempty"`
	Retrieval          string           `json:"retrieval,omitempty"`
	OutputEncoding     string           `json:"output_encoding,omitempty"`
	Output             string           `json:"output"`
}

func (r Result) Failed() bool {
	return !r.Cancelled && (r.OutputDescriptor.Status == Failed || r.OutputDescriptor.Status == Killed || r.ExitCode != nil && *r.ExitCode != 0)
}
