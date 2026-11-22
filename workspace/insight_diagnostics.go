package workspace

import (
	"crude/internal/fileuri"
	"crude/types/insight"
	"crude/types/setting"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"unicode/utf16"

	"crude/insight/facts"
	"crude/workspace/document"
)

func sameInsightBoundary(a, b insight.Boundary) bool {
	return a.Source == b.Source && a.Producer == b.Producer &&
		a.Context.ID == b.Context.ID && a.Context.Root == b.Context.Root &&
		a.Context.Generation == b.Context.Generation &&
		slices.Equal(a.Context.UnknownInputs, b.Context.UnknownInputs)
}

// DiagnosticSnapshot returns the diagnostics of every document, keyed by path.
func (s *InsightService) DiagnosticSnapshot() map[string]insight.Diagnostics {
	out := make(map[string]insight.Diagnostics)
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

// AllDiagnostics returns every document diagnostic, or nil without an insight source.
func (m *LSPManager) AllDiagnostics() map[string]insight.Diagnostics {
	m.mu.Lock()
	sources := m.sources
	m.mu.Unlock()
	if sources == nil || sources.Insight == nil {
		return nil
	}
	return sources.Insight.DiagnosticSnapshot()
}

// FileDiagnostics returns the diagnostics of one file.
func (m *LSPManager) FileDiagnostics(path string) (insight.Diagnostics, bool) {
	m.mu.Lock()
	sources := m.sources
	m.mu.Unlock()
	if sources == nil || sources.Insight == nil {
		return insight.Diagnostics{}, false
	}
	return sources.Insight.diagnostics.SnapshotPath(filepath.Clean(path))
}

// One derived summary per fact generation and display policy. Status reads and
// paint never re-export the entire diagnostic store while nothing has changed.
func (s *InsightService) diagnosticSummaryFor(settings setting.LSP) insight.ProjectInsights {
	s.mu.Lock()
	defer s.mu.Unlock()
	version := s.diagnostics.Version()
	if !s.diagnosticSummaryCached || s.diagnosticSummaryVersion != version ||
		s.diagnosticSummaryEnabled != settings.Diagnostics || s.diagnosticSummaryFloor != settings.MinSeverity {
		s.diagnosticSummary = insight.ProjectInsights{}
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

// advanceContextLocked retires editor-bound facts. Project facts belong to
// their producer: it republishes the files an edit affects, and the facts go
// away when the producer stops or a document opens for the file.
func (m *LSPManager) advanceContextLocked(reason string) {
	m.contextGeneration++
	if m.sources != nil && m.sources.Insight != nil {
		m.sources.Insight.diagnostics.InvalidateMatching(func(b insight.Boundary) bool { return !isProjectFact(b) }, reason)
	}
	m.bumpVersionLocked()
}

func (m *LSPManager) diagnosticBoundaryLocked(doc *lspDocument, state *lspClientState) insight.Boundary {
	root, err := fileuri.ToPath(state.rootURI)
	if err != nil {
		root = "" // Invalid roots cannot claim a native source boundary.
	}
	return insight.Boundary{Source: doc.source,
		Context: insight.Context{ID: doc.key, Root: root,
			Generation: m.contextGeneration, UnknownInputs: []string{
				"Complete dependency and build graph has not been established.",
				"Inherited environment and toolchain identity are not fully tracked.",
				"Executable digest is unverified; tool identity describes launch configuration only.",
			}},
		Producer: insight.Producer{InstanceID: state.instance, Provider: state.cmd, ToolIdentity: "launch-config:" + doc.key}}
}

// A callback can beat syncDocument's send receipt. Retain one pending envelope
// for that exact send, then adjudicate it after recording the actual wire version.
// No unqualified diagnostic answer is installed in the producer.
func (m *LSPManager) receiveDiagnostics(key string, owner *lspClientState, p insight.DiagnosticPublication) {
	m.mu.Lock()
	sources := m.sources
	doc := m.documents[p.Boundary.Source.Path]
	if m.analysisErrorLocked() != nil || m.projectWatchUnreliable && m.projects[key].Root != "" || owner == nil || m.states[key] != owner || doc == nil || doc.source != p.Boundary.Source {
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
		m.analysisErrorLocked() != nil || m.projectWatchUnreliable && m.projects[key].Root != "" || m.paused || m.closed || doc == nil || doc.key != key || doc.source != p.Boundary.Source ||
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
	if doc.diskView && path == m.checkPath && !m.projectPublicationFitsLocked(p) {
		p.Diagnostics, p.Limited, p.Detail = nil, true, "The diagnostic store is full. Closed-file coverage is limited."
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
	if err == nil && !p.Limited {
		delete(m.checkResults, path)
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
func validDiagnosticSource(text string, diagnostics []insight.LSPDiagnostic) bool {
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

func summarizeDiagnosticFacts(ins *insight.ProjectInsights, batches map[string]insight.Diagnostics, settings setting.LSP) {
	ins.CoverageDetail = "Observed units only; project coverage and complete build context are unknown."
	counts := make(map[string]int)
	for _, batch := range batches {
		if batch.ID != "" {
			ins.DiagnosticsObserved++
		}
		switch batch.State {
		case insight.Stale:
			ins.DiagnosticsStale++
		case insight.Limited:
			ins.DiagnosticsLimited++
		case insight.Provisional:
			ins.DiagnosticsUnverified++
		}
		// A project check reports files that are not open. Its batch is bound
		// to the producer's view of the project, not to an exact text, and it
		// still counts: that is the point of checking the project.
		if !batch.HasExactSource() && !batch.FromProjectCheck() {
			continue
		}
		affected := false
		for _, d := range batch.Diagnostics {
			if !settings.Diagnostics || !settings.SeverityAtLeast(d.Severity) {
				continue
			}
			affected = true
			switch d.Severity {
			case insight.DiagError:
				ins.Errors++
			case insight.DiagWarning:
				ins.Warnings++
			case insight.DiagInfo:
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

func (m *LSPManager) probeDiagnosticFacts(source insight.Source) (insight.Diagnostics, error) {
	m.mu.Lock()
	sources := m.sources
	m.mu.Unlock()
	if sources == nil {
		return insight.Diagnostics{}, document.ErrOwner
	}
	answer, ok := sources.Insight.diagnostics.Snapshot(source.DocumentID)
	if !ok || answer.ID == "" || answer.Boundary.Source != source || answer.State == insight.Stale {
		return answer, fmt.Errorf("no diagnostic publication for the requested immutable source")
	}
	return answer, nil
}
