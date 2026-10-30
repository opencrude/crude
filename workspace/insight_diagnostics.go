package workspace

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"unicode/utf16"

	"crude/document"
	"crude/insight/facts"
	"crude/types"
)

func sameInsightBoundary(a, b types.InsightBoundary) bool {
	return a.Source == b.Source && a.Producer == b.Producer &&
		a.Context.ID == b.Context.ID && a.Context.Root == b.Context.Root &&
		a.Context.Generation == b.Context.Generation &&
		slices.Equal(a.Context.UnknownInputs, b.Context.UnknownInputs)
}

func (s *InsightService) DiagnosticSnapshot() map[string]types.InsightDiagnostics {
	out := make(map[string]types.InsightDiagnostics)
	for _, batch := range s.diagnostics.All() {
		out[batch.Boundary.Source.Path] = batch
	}
	return out
}

func (m *LSPManager) DiagnosticCursor() (string, uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.sources == nil || m.sources.Insight == nil {
		return "", m.version
	}
	return m.sources.Insight.generation, m.version
}

func (m *LSPManager) AllDiagnostics() map[string]types.InsightDiagnostics {
	m.mu.Lock()
	sources := m.sources
	m.mu.Unlock()
	if sources == nil || sources.Insight == nil {
		return nil
	}
	return sources.Insight.DiagnosticSnapshot()
}

func (m *LSPManager) FileDiagnostics(path string) (types.InsightDiagnostics, bool) {
	m.mu.Lock()
	sources := m.sources
	m.mu.Unlock()
	if sources == nil || sources.Insight == nil {
		return types.InsightDiagnostics{}, false
	}
	return sources.Insight.diagnostics.SnapshotPath(filepath.Clean(path))
}

// One derived summary per fact generation and display policy. Status reads and
// paint never re-export the entire diagnostic store while nothing has changed.
func (s *InsightService) diagnosticSummaryFor(settings types.LSPSettings) types.ProjectInsights {
	s.mu.Lock()
	defer s.mu.Unlock()
	version := s.diagnostics.Version()
	if !s.diagnosticSummaryCached || s.diagnosticSummaryVersion != version ||
		s.diagnosticSummaryEnabled != settings.Diagnostics || s.diagnosticSummaryFloor != settings.MinSeverity {
		s.diagnosticSummary = types.ProjectInsights{}
		summarizeDiagnosticFacts(&s.diagnosticSummary, s.DiagnosticSnapshot(), settings)
		s.diagnosticSummaryVersion, s.diagnosticSummaryEnabled = version, settings.Diagnostics
		s.diagnosticSummaryFloor, s.diagnosticSummaryCached = settings.MinSeverity, true
	}
	out := s.diagnosticSummary
	if s.diagnosticDetail != "" {
		out.DiagnosticsLimited++
		out.CoverageDetail += " " + s.diagnosticDetail
	}
	return out
}

// The caller holds only the producer lock. This path never acquires document
// authority, exports text or publishes through a synchronous UI event bus.
func (m *LSPManager) invalidateDiagnosticsLocked(reason string) {
	if m.sources != nil && m.sources.Insight != nil {
		m.sources.Insight.diagnostics.InvalidateAll(reason)
	}
	m.bumpVersionLocked()
}

func (m *LSPManager) advanceContextLocked(reason string) {
	m.contextGeneration++
	m.invalidateDiagnosticsLocked(reason)
}

func (m *LSPManager) diagnosticBoundaryLocked(doc *lspDocument, state *lspClientState) types.InsightBoundary {
	return types.InsightBoundary{Source: doc.source,
		Context: types.InsightContext{ID: doc.key, Root: filePathFromURI(state.rootURI),
			Generation: m.contextGeneration, UnknownInputs: []string{
				"Complete dependency and build graph has not been established.",
				"Inherited environment and toolchain identity are not fully tracked.",
				"Executable digest is unverified; tool identity describes launch configuration only.",
			}},
		Producer: types.InsightProducer{InstanceID: state.instance, Provider: state.cmd, ToolIdentity: "launch-config:" + doc.key}}
}

// A callback can beat syncDocument's send receipt. Retain one pending envelope
// for that exact send, then adjudicate it after recording the actual wire version.
// No unqualified diagnostic answer is installed in the producer.
func (m *LSPManager) receiveDiagnostics(key string, owner *lspClientState, p types.InsightDiagnosticPublication) {
	m.mu.Lock()
	sources := m.sources
	doc := m.documents[p.Boundary.Source.Path]
	if owner == nil || m.states[key] != owner || doc == nil || doc.source != p.Boundary.Source {
		m.mu.Unlock()
		return
	}
	text := doc.text // immutable send-intent string, not another source export
	m.mu.Unlock()
	if sources == nil {
		return
	}
	validSource := p.ReportedVersion == nil || *p.ReportedVersion != p.WireVersion ||
		validDiagnosticSource(text, p.Diagnostics)
	sources.mu.Lock()
	defer sources.mu.Unlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	path := p.Boundary.Source.Path
	doc = m.documents[path]
	if m.states[key] != owner || owner.closing || owner.stopped || owner.teardownErr != nil ||
		m.paused || m.closed || doc == nil || doc.key != key || doc.source != p.Boundary.Source ||
		!sameInsightBoundary(m.diagnosticBoundaryLocked(doc, owner), p.Boundary) {
		return
	}
	if p.Boundary.Source.Kind == "canonical" {
		d := sources.docs[p.Boundary.Source.DocumentID]
		if d == nil || sources.analysisIdentityLocked(d) != p.Boundary.Source {
			return
		}
	}
	if doc.syncPending {
		if doc.pendingPublication == nil || p.Sequence > doc.pendingPublication.Sequence {
			copy := p
			copy.Diagnostics = slices.Clone(p.Diagnostics)
			copy.Boundary.Context.UnknownInputs = slices.Clone(p.Boundary.Context.UnknownInputs)
			if p.ReportedVersion != nil {
				v := *p.ReportedVersion
				copy.ReportedVersion = &v
			}
			doc.pendingPublication = &copy
		}
		return
	}
	if !doc.opened || doc.wireVersion != p.WireVersion || !sameInsightBoundary(doc.boundary, p.Boundary) {
		return
	}
	if !validSource {
		_ = sources.Insight.diagnostics.Invalidate(doc.source.DocumentID, "Diagnostic source range is outside the synchronized source or splits a UTF-16 surrogate.")
		m.bumpVersionLocked()
		return
	}
	_, err := sources.Insight.diagnostics.Publish(p)
	if errors.Is(err, facts.ErrSequence) {
		return // Await re-observes the same callback, not a new product fact.
	}
	if err != nil && !errors.Is(err, facts.ErrLimit) {
		if errors.Is(err, facts.ErrInvalid) {
			_ = sources.Insight.diagnostics.Invalidate(doc.source.DocumentID, "Malformed diagnostic publication rejected.")
			m.bumpVersionLocked()
		}
		return
	}
	if errors.Is(err, facts.ErrLimit) {
		sources.Insight.mu.Lock()
		sources.Insight.diagnosticDetail = err.Error()
		sources.Insight.mu.Unlock()
	}
	m.bumpVersionLocked()
}

// Only requested endpoints are retained. Walk source once off authority locks;
// never allocate an index for every newline in a potentially large document.
func validDiagnosticSource(text string, diagnostics []types.LSPDiagnostic) bool {
	type point struct{ line, column int }
	points := make([]point, 0, 2*len(diagnostics))
	for _, d := range diagnostics {
		if d.Line < 0 || d.Col < 0 || d.EndLine < d.Line || d.EndCol < 0 ||
			d.Line == d.EndLine && d.EndCol < d.Col {
			return false
		}
		points = append(points, point{d.Line, d.Col}, point{d.EndLine, d.EndCol})
	}
	sort.Slice(points, func(i, j int) bool {
		return points[i].line < points[j].line || points[i].line == points[j].line && points[i].column < points[j].column
	})
	currentLine := 0
	for len(points) > 0 {
		for currentLine < points[0].line {
			_, rest, ok := strings.Cut(text, "\n")
			if !ok {
				return false
			}
			text = rest
			currentLine++
		}
		line, rest, more := strings.Cut(text, "\n")
		line = strings.TrimSuffix(line, "\r")
		units := 0
		for _, r := range line {
			for len(points) > 0 && points[0] == (point{currentLine, units}) {
				points = points[1:]
			}
			units += utf16.RuneLen(r)
			if len(points) > 0 && points[0].line == currentLine && points[0].column < units {
				return false
			}
		}
		for len(points) > 0 && points[0] == (point{currentLine, units}) {
			points = points[1:]
		}
		if len(points) > 0 && (points[0].line == currentLine || !more) {
			return false
		}
		text = rest
		currentLine++
	}
	return true
}

func summarizeDiagnosticFacts(ins *types.ProjectInsights, batches map[string]types.InsightDiagnostics, settings types.LSPSettings) {
	ins.CoverageDetail = "Observed units only; project coverage and complete build context are unknown."
	counts := make(map[string]int)
	for _, batch := range batches {
		if batch.ID != "" {
			ins.DiagnosticsObserved++
		}
		switch batch.State {
		case types.InsightStale:
			ins.DiagnosticsStale++
		case types.InsightLimited:
			ins.DiagnosticsLimited++
		case types.InsightProvisional:
			ins.DiagnosticsUnverified++
		}
		if !batch.HasExactSource() {
			continue
		}
		affected := false
		for _, d := range batch.Diagnostics {
			if !settings.Diagnostics || !settings.SeverityAtLeast(d.Severity) {
				continue
			}
			affected = true
			switch d.Severity {
			case types.DiagError:
				ins.Errors++
			case types.DiagWarning:
				ins.Warnings++
			case types.DiagInfo:
				ins.Info++
			default:
				ins.Hints++
			}
			counts[d.Message]++
		}
		if affected {
			ins.FilesAffected++
		}
	}
	ins.FilesTracked = len(batches)
	top := 0
	for message, count := range counts {
		if count > top || count == top && message < ins.TopIssue {
			top, ins.TopIssue = count, message
		}
	}
	if ins.DiagnosticsObserved == 0 {
		ins.CoverageDetail = "No diagnostic publication observed; project cleanliness is unverified."
	}
}

func (m *LSPManager) probeDiagnosticFacts(source types.InsightSource) (types.InsightDiagnostics, error) {
	m.mu.Lock()
	sources := m.sources
	m.mu.Unlock()
	if sources == nil {
		return types.InsightDiagnostics{}, document.ErrOwner
	}
	answer, ok := sources.Insight.diagnostics.Snapshot(source.DocumentID)
	if !ok || answer.ID == "" || answer.Boundary.Source != source || answer.State == types.InsightStale {
		return answer, fmt.Errorf("no diagnostic publication for the requested immutable source")
	}
	return answer, nil
}
