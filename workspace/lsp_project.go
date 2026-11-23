package workspace

import (
	"errors"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"crude/insight/facts"
	"crude/internal/fileuri"
	"crude/types/insight"
	"crude/types/setting"
)

// projectFactPrefix marks facts that a project producer reported for a file
// that no document holds. The prefix keeps them apart from canonical and
// probe sources, which use document identities. checkedFactPrefix marks the
// result Crude kept from a closed view of the file as it is on disk, from a
// server that does not report unopened files itself: Crude, not the server,
// must check that file again when it changes.
const (
	projectFactPrefix = "unopened:"
	checkedFactPrefix = "checked:"
)

// lspBusyClient is implemented by transports that report the server's own
// work-done progress.
type lspBusyClient interface{ Busy() bool }

// A server's project report is settled after it has been idle and quiet this
// long. Older results for files that no view holds then give way to it.
const lspProjectSettleQuiet = 3 * time.Second

// lspUnopenedDiagnostics is implemented by transports that report
// diagnostics for files the client has not opened.
type lspUnopenedDiagnostics interface {
	OnUnopenedDiagnostics(func(path string, p insight.DiagnosticPublication))
}

func isProjectFact(b insight.Boundary) bool {
	return b.Source.Kind == insight.SourceDisk &&
		(strings.HasPrefix(b.Source.DocumentID, projectFactPrefix) || isCheckedFact(b))
}

func isCheckedFact(b insight.Boundary) bool {
	return b.Source.Kind == insight.SourceDisk && strings.HasPrefix(b.Source.DocumentID, checkedFactPrefix)
}

// SetProjectProducers replaces the set of project-scoped servers. A project
// producer starts without an open document, inside the same process
// admission as document servers, when language servers may start for this
// workspace. Producers outside the workspace root are ignored. A removed
// producer stops when no document uses its server, and its facts go away.
func (m *LSPManager) SetProjectProducers(producers []insight.LSPProjectProducer) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.setProjectProducersLocked(producers)
}

func (m *LSPManager) setProjectProducersLocked(producers []insight.LSPProjectProducer) {
	if m.closed {
		return
	}
	next := make(map[string]insight.LSPProjectProducer, len(producers))
	for _, producer := range producers {
		root := filepath.Clean(producer.Root)
		if !filepath.IsAbs(root) || strings.TrimSpace(producer.Spec.Command) == "" ||
			m.workspaceRoot == "" || !pathWithin(m.workspaceRoot, root) {
			continue
		}
		producer.Root = root
		producer.Spec = setting.CloneLSPServerSpec(producer.Spec)
		next[lspServerKey(fileuri.FromPath(root), producer.Spec)] = producer
	}
	for key := range m.projects {
		if _, kept := next[key]; kept {
			continue
		}
		if state := m.states[key]; state != nil && m.documentCountLocked(key) == 0 {
			m.retireLocked(state)
		}
		m.removeProjectFactsLocked(func(b insight.Boundary) bool { return b.Context.ID == key })
	}
	m.projects = next
	m.requestProjectCheckLocked()
	m.recomputeActiveStatusLocked()
	if len(next) > 0 || len(m.states) > 0 {
		m.signalLocked()
	}
}

// ProjectProducers returns the current project-scoped servers.
func (m *LSPManager) ProjectProducers() []insight.LSPProjectProducer {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]insight.LSPProjectProducer, 0, len(m.projects))
	for _, key := range m.projectKeysLocked() {
		producer := m.projects[key]
		producer.Spec = setting.CloneLSPServerSpec(producer.Spec)
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
	return ok && !m.projectWatchUnreliable && !m.paused && m.editorActiveLocked()
}

func (m *LSPManager) removeProjectFactsLocked(match func(insight.Boundary) bool) {
	if m.sources == nil || m.sources.Insight == nil {
		return
	}
	if m.sources.Insight.diagnostics.RemoveMatching(func(b insight.Boundary) bool { return isProjectFact(b) && match(b) }) > 0 {
		m.bumpVersionLocked()
	}
}

// receiveUnopenedDiagnostics publishes a server's report about a file that no
// document holds. The fact is bound to the producer instance and the file
// path, not to a text revision. An open document always owns its own file,
// and the first producer that reports a file keeps it until it stops.
func (m *LSPManager) receiveUnopenedDiagnostics(key string, owner *lspClientState, path string, p insight.DiagnosticPublication) {
	path = filepath.Clean(path)
	m.mu.Lock()
	defer m.mu.Unlock()
	sources := m.sources
	if sources == nil || sources.Insight == nil || m.closed || m.paused || m.projectWatchUnreliable || m.analysisErrorLocked() != nil ||
		m.states[key] != owner || owner.closing || owner.stopped || owner.teardownErr != nil || owner.instance == "" {
		return
	}
	root, err := fileuri.ToPath(owner.rootURI)
	if err != nil || !filepath.IsAbs(path) || m.workspaceRoot == "" || !pathWithin(m.workspaceRoot, path) || !pathWithin(root, path) {
		return
	}
	// Servers that check only open files clear a file's results when it
	// closes (the protocol asks them to). That empty report says nothing about
	// the file on disk, so it neither replaces Crude's own result nor marks
	// the server as one that reports the project. Only a server that reports
	// problems in closed files, or is known to, speaks for the project.
	if len(p.Diagnostics) == 0 && !p.Limited && !m.reportsProjectLocked(key, owner) {
		return
	}
	owner.reportedAt, owner.reportsProject = time.Now(), true
	if m.documents[path] != nil {
		return
	}
	m.publishProjectFactLocked(sources, key, owner, root, path, projectFactPrefix, p)
}

// publishProjectFactLocked stores one file's result that no open view owns.
// One fact per file keeps Problems unambiguous: it replaces any other result
// for the path, unless another live producer reported the file first.
func (m *LSPManager) publishProjectFactLocked(sources *DocumentService, key string, owner *lspClientState, root, path, prefix string, p insight.DiagnosticPublication) {
	store := sources.Insight.diagnostics
	unknown := []string{
		"The file is not open; the server reported it from its view of the project.",
		"Complete dependency and build graph has not been established.",
		"Executable digest is unverified; tool identity describes launch configuration only.",
	}
	boundary := insight.Boundary{
		Source: insight.Source{WorkspaceGeneration: sources.Insight.generation, DocumentID: prefix + path,
			Path: path, Lifetime: owner.instance, Revision: 1, Kind: insight.SourceDisk},
		Context:  insight.Context{ID: key, Root: root, Generation: owner.epoch + 1, UnknownInputs: unknown},
		Producer: insight.Producer{InstanceID: owner.instance, Provider: owner.cmd, ToolIdentity: "launch-config:" + key},
	}
	for _, id := range []string{projectFactPrefix + path, checkedFactPrefix + path} {
		if current, ok := store.Snapshot(id); ok && current.Boundary.Context.ID != key && current.State != insight.Stale {
			return
		}
	}
	store.RemoveMatching(func(b insight.Boundary) bool {
		return b.Source.Path == path && b.Source.DocumentID != boundary.Source.DocumentID
	})
	if err := store.Observe(boundary); err != nil {
		m.noteDiagnosticLimitLocked(sources, err)
		return
	}
	m.factSequence++
	p.Boundary, p.Sequence, p.WireVersion = boundary, m.factSequence, 1
	p.ReportedVersion = nil
	p.Diagnostics = slices.Clone(p.Diagnostics)
	if _, err := store.Publish(p); err != nil && !errors.Is(err, facts.ErrSequence) {
		m.noteDiagnosticLimitLocked(sources, err)
	} else if err == nil && !p.Limited {
		delete(m.checkResults, path)
	}
	m.bumpVersionLocked()
}

// retainDiskViewLocked keeps the result of a closed view of the file on disk
// as the file's project result, bound to the same server instance. A result
// that is out of date or from another instance is marked out of date instead.
func (m *LSPManager) retainDiskViewLocked(path string, doc *lspDocument) {
	if m.sources == nil || m.sources.Insight == nil {
		return
	}
	store := m.sources.Insight.diagnostics
	answer, ok := store.Snapshot(doc.source.DocumentID)
	state := m.states[doc.key]
	root, err := "", error(nil)
	if state != nil {
		root, err = fileuri.ToPath(state.rootURI)
	}
	if !ok || answer.ID == "" || answer.State == insight.Stale || answer.Limited || err != nil || state == nil ||
		state.closing || state.stopped || state.instance == "" || answer.Boundary.Producer.InstanceID != state.instance ||
		answer.Boundary.Source != doc.source {
		if store.RemoveMatching(func(b insight.Boundary) bool { return b.Source == doc.source }) > 0 {
			m.bumpVersionLocked()
		}
		return
	}
	prefix := checkedFactPrefix
	if m.reportsProjectLocked(doc.key, state) {
		prefix = projectFactPrefix
	}
	store.RemoveMatching(func(b insight.Boundary) bool { return b.Source == doc.source })
	m.publishProjectFactLocked(m.sources, doc.key, state, root, path, prefix, insight.DiagnosticPublication{
		Diagnostics: answer.Diagnostics, Detail: answer.Detail})
}

// reportsProjectLocked reports whether the server reports files that are not
// open: the catalog says so, the server was seen doing it, or it offers
// workspace diagnostics that Crude requests.
func (m *LSPManager) reportsProjectLocked(key string, state *lspClientState) bool {
	if state != nil && (state.reportsProject || state.spec.DiagnosticScope == insight.CoverageProject || m.workspacePullLocked(state)) {
		return true
	}
	_, project := m.projects[key]
	return project && state != nil && state.spec.DiagnosticScope == insight.CoverageProject
}

// settleProjectReportLocked runs once per server instance, when it is idle
// and has reported nothing for lspProjectSettleQuiet. A new instance reports
// only files that still have problems, so results that earlier, stopped
// instances of this server gave for files no view holds would otherwise stay
// as out-of-date rows forever.
func (m *LSPManager) settleProjectReportLocked(state *lspClientState, now time.Time) {
	if state.settled || state.status != LSPConnected || state.client == nil || state.closing || state.stopped || state.instance == "" {
		return
	}
	last := state.connectedAt
	if state.reportedAt.After(last) {
		last = state.reportedAt
	}
	if now.Sub(last) < lspProjectSettleQuiet {
		return
	}
	if busy, ok := state.client.(lspBusyClient); ok && busy.Busy() {
		return
	}
	state.settled = true
	if m.sources == nil || m.sources.Insight == nil {
		return
	}
	root, err := fileuri.ToPath(state.rootURI)
	if err != nil {
		return
	}
	live := make(map[string]bool, len(m.states))
	for _, other := range m.states {
		if other.instance != "" && other.client != nil && !other.closing && !other.stopped {
			live[other.instance] = true
		}
	}
	// Crude's current results for closed files are not the old server's
	// report: they stay until the file or the configuration changes.
	store := m.sources.Insight.diagnostics
	checked := make(map[string]bool)
	for _, fact := range store.All() {
		if isCheckedFact(fact.Boundary) && fact.State != insight.Stale {
			checked[fact.Boundary.Source.DocumentID] = true
		}
	}
	if store.RemoveMatching(func(b insight.Boundary) bool {
		path := b.Source.Path
		if m.documents[path] != nil || live[b.Producer.InstanceID] || checked[b.Source.DocumentID] || !pathWithin(root, path) {
			return false
		}
		spec, ok := m.resolveServerLocked(path)
		return ok && spec.Command == state.cmd
	}) > 0 {
		m.bumpVersionLocked()
		m.requestProjectCheckLocked()
	} else if len(m.projectCheckQueueLocked()) > 0 {
		m.requestProjectCheckLocked()
	}
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
