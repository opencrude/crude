// Package facts owns bounded, published diagnostic answers, not text, servers,
// project discovery, permissions or admission. A single coordinator must feed
// ordered immutable boundaries and recheck source/producer authority when using
// answers. Observe is not an authentication mechanism.
package facts

import (
	"crypto/rand"
	"errors"
	"fmt"
	"slices"
	"sort"
	"sync"
	"unicode/utf8"

	"crude/types"
)

const (
	MaxTextBytes     = 64 << 10
	MaxIdentityBytes = 4096
	MaxUnknownInputs = 64
	MaxRetainedBytes = 16 << 20
	MaxReasonBytes   = 512
)

var (
	ErrInvalid  = errors.New("invalid diagnostic fact")
	ErrBoundary = errors.New("diagnostic boundary is not active")
	ErrSequence = errors.New("diagnostic sequence is not increasing")
	ErrVersion  = errors.New("diagnostic version mismatch")
	ErrLimit    = errors.New("diagnostic fact capacity exceeded")
)

type entry struct {
	boundary types.InsightBoundary
	active   bool
	sequence uint64
	answer   types.InsightDiagnostics
	weight   int
}

// Store retains one batch per document, including stale batches. There is no
// eviction: capacity exhaustion must be surfaced by the coordinator. Inputs
// must not be mutated concurrently with a call; all retained and returned slices
// and pointers are detached, so mutation after return is safe.
type Store struct {
	mu              sync.RWMutex
	documents       map[string]*entry
	maxDocuments    int
	maxDiagnostics  int
	diagnosticCount int
	version         uint64
	retainedBytes   int
}

// Version changes only with retained observations, never with reads or focus.
func (s *Store) Version() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.version
}

// New bounds document slots and the TOTAL number of retained diagnostics.
// Zero diagnostic capacity is valid (only observed empty batches fit).
func New(maxDocuments, maxDiagnostics int) (*Store, error) {
	if maxDocuments <= 0 || maxDiagnostics < 0 {
		return nil, fmt.Errorf("%w: invalid capacities", ErrInvalid)
	}
	return &Store{documents: make(map[string]*entry), maxDocuments: maxDocuments,
		maxDiagnostics: maxDiagnostics}, nil
}

// Observe installs the coordinator's current boundary. A changed boundary keeps
// the old batch and its ORIGINAL provenance as stale until replacement. Identical
// observations never reset sequence ordering. After Invalidate, Observe may
// reactivate the same boundary, but the old batch stays stale.
//
// Lifetime/producer tokens must never be reused; revisions and context generations
// must advance under their authority. Arbitrary rollback/reinstallation through
// Observe is not guarded by an unbounded history of retired boundaries.
func (s *Store) Observe(b types.InsightBoundary) error {
	if err := validateBoundary(b); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.documents[b.Source.DocumentID]
	if !ok {
		if len(s.documents) >= s.maxDocuments {
			return ErrLimit
		}
		answer := types.InsightDiagnostics{Boundary: b, State: types.InsightPending}
		weight := boundaryWeight(b) + answerWeight(answer)
		if s.retainedBytes+weight > MaxRetainedBytes {
			return ErrLimit
		}
		s.documents[b.Source.DocumentID] = &entry{
			boundary: cloneBoundary(b), active: true,
			answer: types.InsightDiagnostics{Boundary: cloneBoundary(b), State: types.InsightPending},
			weight: weight,
		}
		s.retainedBytes += weight
		s.version++
		return nil
	}
	if !equalBoundary(e.boundary, b) {
		answer := e.answer
		if answer.ID == "" {
			answer = types.InsightDiagnostics{Boundary: b, State: types.InsightPending}
		}
		weight := boundaryWeight(b) + answerWeight(answer)
		if s.retainedBytes-e.weight+weight > MaxRetainedBytes {
			return ErrLimit
		}
		s.retainedBytes += weight - e.weight
		e.weight = weight
		s.version++
		e.boundary = cloneBoundary(b)
		e.sequence = 0
		if e.answer.ID == "" {
			e.answer = types.InsightDiagnostics{Boundary: cloneBoundary(b), State: types.InsightPending}
		} else {
			e.answer.State = types.InsightStale
			e.answer.Detail = "source, context or producer boundary changed"
		}
	}
	e.active = true
	return nil
}

// Publish atomically accepts a complete batch or returns an error. Rejection
// never replaces prior diagnostics with an empty or truncated set. Capacity
// rejection marks the retained view limited, keeps its original provenance, and
// consumes the sequence so an older batch cannot undo the limit observation.
func (s *Store) Publish(p types.InsightDiagnosticPublication) (types.InsightDiagnostics, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.documents[p.Boundary.Source.DocumentID]
	if !ok || !e.active || !equalBoundary(e.boundary, p.Boundary) {
		return types.InsightDiagnostics{}, ErrBoundary
	}
	if p.Sequence == 0 || p.Sequence <= e.sequence {
		return cloneAnswer(e.answer), ErrSequence
	}
	if p.WireVersion <= 0 || (p.ReportedVersion != nil && *p.ReportedVersion != p.WireVersion) {
		return cloneAnswer(e.answer), ErrVersion
	}
	err := validatePublication(p, s.maxDiagnostics-s.diagnosticCount+len(e.answer.Diagnostics))
	if err == nil && s.retainedBytes-e.weight+boundaryWeight(e.boundary)+publicationWeight(p) > MaxRetainedBytes {
		err = ErrLimit
	}
	if err != nil {
		if errors.Is(err, ErrLimit) {
			e.sequence = p.Sequence
			e.answer.State = types.InsightLimited
			e.answer.Limited = true
			e.answer.Detail = err.Error()
			s.version++
		}
		return cloneAnswer(e.answer), err
	}
	answer := types.InsightDiagnostics{
		ID: rand.Text(), Boundary: cloneBoundary(p.Boundary), Sequence: p.Sequence,
		WireVersion: p.WireVersion, Binding: "unversioned", State: types.InsightProvisional,
		Detail: p.Detail, Diagnostics: append([]types.LSPDiagnostic{}, p.Diagnostics...), Limited: p.Limited,
	}
	if p.ReportedVersion != nil {
		v := *p.ReportedVersion
		answer.ReportedVersion = &v
		answer.Binding = "exact-version"
		if len(p.Boundary.Context.UnknownInputs) == 0 {
			answer.State = types.InsightCurrent
		}
	}
	if p.Limited {
		answer.State = types.InsightLimited
	}
	for i := range answer.Diagnostics {
		if answer.Diagnostics[i].Severity == 0 {
			answer.Diagnostics[i].Severity = types.DiagError
		}
	}
	s.diagnosticCount += len(answer.Diagnostics) - len(e.answer.Diagnostics)
	e.answer, e.sequence = answer, p.Sequence
	weight := boundaryWeight(e.boundary) + answerWeight(answer)
	s.retainedBytes += weight - e.weight
	e.weight = weight
	s.version++
	return cloneAnswer(answer), nil
}

// Invalidate fences publications until the coordinator next calls Observe.
// Failure/blocked reasons retain all old facts as stale, not as a clean result.
// Repeated retirement of an inactive boundary preserves its first stale reason.
func (s *Store) Invalidate(documentID, reason string) error {
	if err := boundedString(reason, MaxReasonBytes); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.documents[documentID]
	if !ok {
		return ErrBoundary
	}
	if invalidateEntry(e, reason) {
		s.version++
	}
	return nil
}

func invalidateEntry(e *entry, reason string) bool {
	if !e.active {
		return false
	}
	e.active = false
	e.answer.State, e.answer.Detail = types.InsightStale, reason
	return true
}

// InvalidateSource is the bounded identity-only canonical mutation path. It
// never exports or copies the retained diagnostic payload.
func (s *Store) InvalidateSource(source types.InsightSource) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.documents[source.DocumentID]
	if e == nil || e.boundary.Source == source {
		return false
	}
	changed := invalidateEntry(e, "Canonical source changed.")
	if changed {
		s.version++
	}
	return changed
}

// InvalidateAll conservatively retires tracked context. It preserves original
// provenance and warnings, and does no result-sized allocation.
func (s *Store) InvalidateAll(reason string) bool {
	if len(reason) > MaxReasonBytes || !utf8.ValidString(reason) {
		reason = "Diagnostic context invalidated; the producer reason exceeded its bounded status field."
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := false
	for _, e := range s.documents {
		if invalidateEntry(e, reason) {
			changed = true
		}
	}
	if changed {
		s.version++
	}
	return changed
}

func boundaryWeight(b types.InsightBoundary) int {
	n := 256 + len(b.Source.WorkspaceGeneration) + len(b.Source.DocumentID) + len(b.Source.Path) + len(b.Source.Lifetime) +
		len(b.Context.ID) + len(b.Context.Root) + len(b.Producer.InstanceID) + len(b.Producer.Provider) + len(b.Producer.ToolIdentity)
	for _, v := range b.Context.UnknownInputs {
		n += 16 + len(v)
	}
	return n
}

func publicationWeight(p types.InsightDiagnosticPublication) int {
	n := 512 + MaxReasonBytes + boundaryWeight(p.Boundary) + max(0, len(p.Detail)-MaxReasonBytes)
	for _, d := range p.Diagnostics {
		n += 96 + len(d.Message) + len(d.Source) + len(d.Code)
	}
	return n
}

func answerWeight(a types.InsightDiagnostics) int {
	return publicationWeight(types.InsightDiagnosticPublication{Boundary: a.Boundary, Detail: a.Detail, Diagnostics: a.Diagnostics})
}

// SnapshotPath reads one retained answer without exporting every document.
// Paths are presentation keys; document identity remains the primary key.
func (s *Store) SnapshotPath(path string) (types.InsightDiagnostics, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, e := range s.documents {
		if e.answer.Boundary.Source.Path == path {
			return cloneAnswer(e.answer), true
		}
	}
	return types.InsightDiagnostics{}, false
}

func (s *Store) Snapshot(documentID string) (types.InsightDiagnostics, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.documents[documentID]
	if !ok {
		return types.InsightDiagnostics{}, false
	}
	return cloneAnswer(e.answer), true
}

// All returns detached snapshots ordered by document ID, including pending and
// stale entries. It does not compute unqualified project diagnostic totals.
func (s *Store) All() []types.InsightDiagnostics {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]types.InsightDiagnostics, 0, len(s.documents))
	for _, e := range s.documents {
		out = append(out, cloneAnswer(e.answer))
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Boundary.Source.DocumentID < out[j].Boundary.Source.DocumentID
	})
	return out
}

func cloneBoundary(b types.InsightBoundary) types.InsightBoundary {
	b.Context.UnknownInputs = slices.Clone(b.Context.UnknownInputs)
	return b
}

func cloneAnswer(a types.InsightDiagnostics) types.InsightDiagnostics {
	a.Boundary = cloneBoundary(a.Boundary)
	a.Diagnostics = slices.Clone(a.Diagnostics)
	if a.ReportedVersion != nil {
		v := *a.ReportedVersion
		a.ReportedVersion = &v
	}
	return a
}

func equalBoundary(a, b types.InsightBoundary) bool {
	return a.Source == b.Source && a.Producer == b.Producer &&
		a.Context.ID == b.Context.ID && a.Context.Root == b.Context.Root &&
		a.Context.Generation == b.Context.Generation &&
		slices.Equal(a.Context.UnknownInputs, b.Context.UnknownInputs)
}

func boundedString(v string, limit int) error {
	if len(v) > limit {
		return ErrLimit
	}
	if !utf8.ValidString(v) {
		return fmt.Errorf("%w: invalid UTF-8", ErrInvalid)
	}
	return nil
}

func validateBoundary(b types.InsightBoundary) error {
	if b.Source.Revision == 0 || b.Context.Generation == 0 ||
		(b.Source.Kind != "canonical" && b.Source.Kind != "disk") {
		return fmt.Errorf("%w: source kind or generation", ErrInvalid)
	}
	for _, v := range []string{b.Source.WorkspaceGeneration, b.Source.DocumentID, b.Source.Path,
		b.Source.Lifetime, b.Context.ID, b.Context.Root, b.Producer.InstanceID,
		b.Producer.Provider, b.Producer.ToolIdentity} {
		if v == "" {
			return fmt.Errorf("%w: empty boundary identity", ErrInvalid)
		}
		if err := boundedString(v, MaxIdentityBytes); err != nil {
			return err
		}
	}
	if len(b.Context.UnknownInputs) > MaxUnknownInputs {
		return ErrLimit
	}
	for _, v := range b.Context.UnknownInputs {
		if err := boundedString(v, MaxIdentityBytes); err != nil {
			return err
		}
	}
	return nil
}

func validatePublication(p types.InsightDiagnosticPublication, available int) error {
	if len(p.Diagnostics) > available {
		return ErrLimit
	}
	if err := boundedString(p.Detail, MaxTextBytes); err != nil {
		return err
	}
	for _, d := range p.Diagnostics {
		if d.Severity < 0 || d.Severity > types.DiagHint ||
			d.Line < 0 || d.Col < 0 || d.EndLine < d.Line || d.EndCol < 0 ||
			(d.EndLine == d.Line && d.EndCol < d.Col) {
			return fmt.Errorf("%w: severity or range", ErrInvalid)
		}
		for _, v := range []struct {
			text  string
			limit int
		}{{d.Message, MaxTextBytes}, {d.Source, MaxIdentityBytes}, {d.Code, MaxIdentityBytes}} {
			if err := boundedString(v.text, v.limit); err != nil {
				return err
			}
		}
	}
	return nil
}
