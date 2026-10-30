package types

import "crude/types/syntax"

type InsightState string

const (
	InsightPending     InsightState = "pending"
	InsightProvisional InsightState = "provisional" // shell-private source, never canonical facts
	InsightCurrent     InsightState = "current"
	InsightUnsupported InsightState = "unsupported"
	InsightFailed      InsightState = "failed"
	InsightLimited     InsightState = "limited"
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
	State      InsightState
	Detail     string
	Syntax     *syntax.Result
}
