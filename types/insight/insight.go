package insight

import "crude/types/syntaxdata"

// State names the progress of one derived insight publication.
type State string

const (
	// Pending means the insight is not ready yet.
	Pending State = "pending"
	// Provisional means the answer comes from a shell-private source.
	Provisional State = "provisional" // shell-private source, never canonical facts
	// Current means the published insight matches its source.
	Current State = "current"
	// Unsupported means no producer can answer for this source.
	Unsupported State = "unsupported"
	// Failed means the last attempt to derive the insight failed.
	Failed State = "failed"
	// Limited means the answer is incomplete.
	Limited State = "limited"
)

// DocumentInsight is a derived view, never a text or semantic authority. Version
// orders publications independently of Revision (which identifies source).
// A nil Syntax result does not mean a document is free of errors.
type DocumentInsight struct {
	DocumentID string
	Path       string
	Revision   uint64
	Generation string
	Version    uint64
	State      State
	Detail     string
	Syntax     *syntaxdata.Result
}
