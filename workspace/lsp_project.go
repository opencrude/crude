package workspace

import (
	"errors"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"crude/insight/facts"
	"crude/internal/fileuri"
	ttypes "crude/types"
)

// projectFactPrefix marks facts that a project producer reported for a file
// that no document holds. The prefix keeps them apart from canonical and
// probe sources, which use document identities.
const projectFactPrefix = "unopened:"

// lspUnopenedDiagnostics is implemented by transports that report
// diagnostics for files the client has not opened.
type lspUnopenedDiagnostics interface {
	OnUnopenedDiagnostics(func(path string, p ttypes.InsightDiagnosticPublication))
}

func isProjectFact(b ttypes.InsightBoundary) bool {
	return b.Source.Kind == ttypes.InsightSourceDisk && strings.HasPrefix(b.Source.DocumentID, projectFactPrefix)
}

// SetProjectProducers replaces the set of project-scoped servers. A project
// producer starts without an open document, inside the same process
// admission as document servers, when language servers may start for this
// workspace. Producers outside the workspace root are ignored. A removed
// producer stops when no document uses its server, and its facts go away.
func (m *LSPManager) SetProjectProducers(producers []ttypes.LSPProjectProducer) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.setProjectProducersLocked(producers)
}

func (m *LSPManager) setProjectProducersLocked(producers []ttypes.LSPProjectProducer) {
	if m.closed {
		return
	}
	next := make(map[string]ttypes.LSPProjectProducer, len(producers))
	for _, producer := range producers {
		root := filepath.Clean(producer.Root)
		if !filepath.IsAbs(root) || strings.TrimSpace(producer.Spec.Command) == "" ||
			m.workspaceRoot == "" || !pathWithin(m.workspaceRoot, root) {
			continue
		}
		producer.Root = root
		producer.Spec = ttypes.CloneLSPServerSpec(producer.Spec)
		next[lspServerKey(fileuri.FromPath(root), producer.Spec)] = producer
	}
	for key := range m.projects {
		if _, kept := next[key]; kept {
			continue
		}
		if state := m.states[key]; state != nil && m.documentCountLocked(key) == 0 {
			m.retireLocked(state)
		}
		m.removeProjectFactsLocked(func(b ttypes.InsightBoundary) bool { return b.Context.ID == key })
	}
	m.projects = next
	m.recomputeActiveStatusLocked()
	if len(next) > 0 || len(m.states) > 0 {
		m.signalLocked()
	}
}

// ProjectProducers returns the current project-scoped servers.
func (m *LSPManager) ProjectProducers() []ttypes.LSPProjectProducer {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]ttypes.LSPProjectProducer, 0, len(m.projects))
	for _, key := range m.projectKeysLocked() {
		producer := m.projects[key]
		producer.Spec = ttypes.CloneLSPServerSpec(producer.Spec)
		out = append(out, producer)
	}
	return out
}

func (m *LSPManager) projectKeysLocked() []string {
	keys := make([]string, 0, len(m.projects))
	for key := range m.projects {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// projectWantedLocked reports whether a project producer may run now. It uses
// the same switches as editor documents: trust, the master switch, automatic
// start or an explicit start, and pause.
func (m *LSPManager) projectWantedLocked(key string) bool {
	_, ok := m.projects[key]
	return ok && !m.paused && m.editorActiveLocked()
}

func (m *LSPManager) removeProjectFactsLocked(match func(ttypes.InsightBoundary) bool) {
	if m.sources == nil || m.sources.Insight == nil {
		return
	}
	if m.sources.Insight.diagnostics.RemoveMatching(func(b ttypes.InsightBoundary) bool { return isProjectFact(b) && match(b) }) > 0 {
		m.bumpVersionLocked()
	}
}

// receiveUnopenedDiagnostics publishes a server's report about a file that no
// document holds. The fact is bound to the producer instance and the file
// path, not to a text revision. An open document always owns its own file,
// and the first producer that reports a file keeps it until it stops.
func (m *LSPManager) receiveUnopenedDiagnostics(key string, owner *lspClientState, path string, p ttypes.InsightDiagnosticPublication) {
	path = filepath.Clean(path)
	m.mu.Lock()
	defer m.mu.Unlock()
	sources := m.sources
	if sources == nil || sources.Insight == nil || m.closed || m.paused || m.analysisErrorLocked() != nil ||
		m.states[key] != owner || owner.closing || owner.stopped || owner.teardownErr != nil || owner.instance == "" {
		return
	}
	root, err := fileuri.ToPath(owner.rootURI)
	if err != nil || !filepath.IsAbs(path) || m.workspaceRoot == "" || !pathWithin(m.workspaceRoot, path) || !pathWithin(root, path) {
		return
	}
	if m.documents[path] != nil {
		return
	}
	store := sources.Insight.diagnostics
	boundary := ttypes.InsightBoundary{
		Source: ttypes.InsightSource{WorkspaceGeneration: sources.Insight.generation, DocumentID: projectFactPrefix + path,
			Path: path, Lifetime: owner.instance, Revision: 1, Kind: ttypes.InsightSourceDisk},
		Context: ttypes.InsightContext{ID: key, Root: root, Generation: owner.epoch + 1, UnknownInputs: []string{
			"The file is not open; the server reported it from its view of the project.",
			"Complete dependency and build graph has not been established.",
			"Executable digest is unverified; tool identity describes launch configuration only.",
		}},
		Producer: ttypes.InsightProducer{InstanceID: owner.instance, Provider: owner.cmd, ToolIdentity: "launch-config:" + key},
	}
	if current, ok := store.Snapshot(boundary.Source.DocumentID); ok && current.Boundary.Context.ID != key && current.State != ttypes.InsightStale {
		return
	}
	// A fact from a closed editor view of this file is older than the
	// project's report. One fact per file keeps Problems unambiguous.
	store.RemoveMatching(func(b ttypes.InsightBoundary) bool {
		return b.Source.Path == path && b.Source.DocumentID != boundary.Source.DocumentID
	})
	if err := store.Observe(boundary); err != nil {
		m.noteDiagnosticLimitLocked(sources, err)
		return
	}
	p.Boundary = boundary
	p.ReportedVersion = nil
	p.Diagnostics = slices.Clone(p.Diagnostics)
	if _, err := store.Publish(p); err != nil && !errors.Is(err, facts.ErrSequence) {
		m.noteDiagnosticLimitLocked(sources, err)
	}
	m.bumpVersionLocked()
}

func (m *LSPManager) noteDiagnosticLimitLocked(sources *DocumentService, err error) {
	if !errors.Is(err, facts.ErrLimit) {
		return
	}
	sources.Insight.mu.Lock()
	sources.Insight.diagnosticDetail = err.Error()
	sources.Insight.mu.Unlock()
	m.bumpVersionLocked()
}
