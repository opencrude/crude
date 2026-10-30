package types

import "slices"

const (
	InsightStale   InsightState = "stale"
	InsightBlocked InsightState = "blocked"
)

// InsightSource identifies an immutable source, not just equal text or a path.
// Kind is canonical or disk. Lifetime fences close/reopen and disk leases.
type InsightSource struct {
	WorkspaceGeneration string
	DocumentID          string
	Path                string
	Lifetime            string
	Revision            uint64
	Kind                string
}

// InsightContext qualifies an answer; ID/generation are supplied by the context
// owner, not inferred from syntax. UnknownInputs must remain visible to readers.
// An empty UnknownInputs list alone is not evidence of complete build context.
type InsightContext struct {
	ID            string
	Root          string
	Generation    uint64
	UnknownInputs []string
}

// InsightProducer identifies an actual instance, not merely a provider name.
type InsightProducer struct {
	InstanceID   string
	Provider     string
	ToolIdentity string
}

// InsightBoundary is an authoritative coordinator's immutable observation.
// Possession of these caller-supplied fields is not proof of authenticity.
type InsightBoundary struct {
	Source   InsightSource
	Context  InsightContext
	Producer InsightProducer
}

// InsightDiagnosticPublication describes a published batch in the producer's
// UTF-16 coordinate space. WireVersion is the source's synchronized LSP version;
// a nil ReportedVersion is unverified, never an exact version match.
type InsightDiagnosticPublication struct {
	Boundary        InsightBoundary
	Sequence        uint64
	WireVersion     int
	ReportedVersion *int
	Diagnostics     []LSPDiagnostic
	Limited         bool
	Detail          string
}

// InsightDiagnostics is a detached retained batch. ID is an opaque publication
// identity shared by consumers, never a permission token. Empty ID means no
// batch was observed; only a nonempty ID can represent an observed empty batch.
// Binding is exact-version or unversioned. Even current, exact-version empty
// results do not establish project cleanliness or complete context.
type InsightDiagnostics struct {
	ID              string
	Boundary        InsightBoundary
	Sequence        uint64
	WireVersion     int
	ReportedVersion *int
	Binding         string
	State           InsightState
	Detail          string
	Diagnostics     []LSPDiagnostic
	Limited         bool
}

// HasExactSource describes source association, not semantic correctness or
// complete build context. Consumers must still match their actual source owner.
func (d InsightDiagnostics) HasExactSource() bool {
	return d.ID != "" && d.Binding == "exact-version" && d.WireVersion > 0 &&
		d.ReportedVersion != nil && *d.ReportedVersion == d.WireVersion &&
		(d.State == InsightCurrent || d.State == InsightProvisional) && !d.Limited
}

func CloneInsightDiagnostics(d InsightDiagnostics) InsightDiagnostics {
	d.Boundary.Context.UnknownInputs = slices.Clone(d.Boundary.Context.UnknownInputs)
	d.Diagnostics = slices.Clone(d.Diagnostics)
	if d.ReportedVersion != nil {
		v := *d.ReportedVersion
		d.ReportedVersion = &v
	}
	return d
}
