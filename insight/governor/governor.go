// Package governor provides bounded process-slot admission across workspaces.
//
// A runtime owns one Governor and injects it into process lifecycle owners. This
// package never starts, observes, stops or retries processes. A slot is reserved
// before Begin and stays occupied after Begin until the lifecycle owner positively
// verifies that no owned process remains and explicitly calls Lease.Release.
// Cancellation, disconnect, inactivity and shutdown are not that verification.
// Admission is neither execution permission nor native resource containment.
package governor

import (
	"errors"
	"sort"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"
)

// Hard bounds protect admission metadata even from invalid runtime settings.
const (
	MaxProcesses   = 1024
	MaxWaiting     = 4096
	MaxIDBytes     = 128
	MaxReasonBytes = 512
)

var (
	ErrInvalidLimits          = errors.New("governor: invalid limits")
	ErrInvalidOwner           = errors.New("governor: invalid owner identity")
	ErrInvalidWorkspaceLimit  = errors.New("governor: invalid workspace limit")
	ErrWorkspaceLimitConflict = errors.New("governor: workspace limit differs from retained work")
	ErrDuplicateOwner         = errors.New("governor: owner already retained")
	ErrQueueFull              = errors.New("governor: waiting queue full")
	ErrClosed                 = errors.New("governor: closed")
	ErrInvalidTicket          = errors.New("governor: invalid ticket")
	ErrQueued                 = errors.New("governor: ticket still queued")
	ErrCancelled              = errors.New("governor: ticket cancelled")
	ErrAlreadyBegun           = errors.New("governor: ticket already begun")
	ErrReleased               = errors.New("governor: ticket released")
)

// Owner identifies one producer lifetime, not a command, path or environment.
// All fields are required, at most MaxIDBytes bytes, and contain only ASCII
// letters, digits, '.', '_', ':' or '-'. Callers must not put secrets in IDs.
type Owner struct {
	Workspace  string
	Generation string
	Producer   string
}

// Limits bounds reservations and waiting tickets. MaxProcesses must be in
// [1, MaxProcesses]; MaxWaiting must be in [0, MaxWaiting]. Zero waiting capacity
// still permits submissions that can be granted immediately.
type Limits struct {
	MaxProcesses int
	MaxWaiting   int
}

// Request captures a workspace quota in [1, MaxProcesses]. All retained work for
// a workspace must agree with its current quota, across generations. Submit is
// not a quota update operation; ConfigureWorkspace changes a retained quota.
type Request struct {
	Owner          Owner
	WorkspaceLimit int
}

type State string

const (
	Queued     State = "queued"
	Granted    State = "granted"
	Started    State = "started"
	Cancelled  State = "cancelled"
	Released   State = "released"
	Unverified State = "unverified"
)

// Observation is detached from mutable governor state.
type Observation struct {
	Owner          Owner
	WorkspaceLimit int
	State          State
	Reason         string
}

// Counts distinguishes waiting work from every kind of occupied reservation.
type Counts struct {
	Queued     int
	Granted    int
	Started    int
	Unverified int
}

// Reserved includes unstarted grants and uncertain teardown, not just processes
// the lifecycle owner currently considers healthy.
func (c Counts) Reserved() int { return c.Granted + c.Started + c.Unverified }

type WorkspaceSnapshot struct {
	Workspace string
	Limit     int
	Counts    Counts
}

// Snapshot contains retained work only, sorted by owner identity (not FIFO
// order). Terminal entries are removed, not journaled. Reductions may leave
// counts above current limits; the absolute row bound is MaxProcesses+MaxWaiting.
// A closed governor with any reservations is not evidence of verified shutdown.
type Snapshot struct {
	Limits     Limits
	Closed     bool
	Counts     Counts
	Entries    []Observation
	Workspaces []WorkspaceSnapshot
}

// Governor must be constructed with New and must not be copied.
type Governor struct {
	mu         sync.Mutex
	limits     Limits
	closed     bool
	used       int
	entries    map[Owner]*entry
	workspaces map[string]*workspace
	queue      []*entry
}

type workspace struct {
	limit    int
	used     int
	retained int
}

type entry struct {
	observation Observation
	changed     chan struct{}
}

// Ticket is an opaque handle. Its zero value is invalid. Copying a handle does
// not create a new reservation. Keep the returned pointer, not a copied value.
type Ticket struct {
	governor *Governor
	entry    *entry
}

// Lease is the exactly-once transfer of an unstarted reservation to its process
// lifecycle owner. Its zero value is inert; copying it cannot duplicate release.
type Lease struct {
	governor *Governor
	entry    *entry
}

func New(limits Limits) (*Governor, error) {
	if !validLimits(limits) {
		return nil, ErrInvalidLimits
	}
	return &Governor{
		limits:     limits,
		entries:    make(map[Owner]*entry),
		workspaces: make(map[string]*workspace),
	}, nil
}

// Submit never waits for capacity. Eligible submissions reserve immediately;
// others join a bounded FIFO. Ineligible workspaces never block eligible ones.
// A rejected submission retains no identity, queue position or reservation.
func (g *Governor) Submit(request Request) (*Ticket, error) {
	if !validOwner(request.Owner) {
		return nil, ErrInvalidOwner
	}
	if request.WorkspaceLimit < 1 || request.WorkspaceLimit > MaxProcesses {
		return nil, ErrInvalidWorkspaceLimit
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return nil, ErrClosed
	}
	if _, exists := g.entries[request.Owner]; exists {
		return nil, ErrDuplicateOwner
	}
	ws := g.workspaces[request.Owner.Workspace]
	if ws != nil && ws.limit != request.WorkspaceLimit {
		return nil, ErrWorkspaceLimitConflict
	}
	eligible := g.used < g.limits.MaxProcesses && (ws == nil || ws.used < ws.limit)
	if !eligible && len(g.queue) >= g.limits.MaxWaiting {
		return nil, ErrQueueFull
	}
	// Do not retain large backing strings through short caller-supplied IDs.
	request.Owner = Owner{
		Workspace:  strings.Clone(request.Owner.Workspace),
		Generation: strings.Clone(request.Owner.Generation),
		Producer:   strings.Clone(request.Owner.Producer),
	}
	if ws == nil {
		ws = &workspace{limit: request.WorkspaceLimit}
		g.workspaces[request.Owner.Workspace] = ws
	}
	e := &entry{
		observation: Observation{
			Owner: request.Owner, WorkspaceLimit: request.WorkspaceLimit,
			State: Queued, Reason: "waiting for process capacity",
		},
		changed: make(chan struct{}),
	}
	g.entries[request.Owner] = e
	ws.retained++
	if eligible {
		g.grant(e)
	} else {
		g.queue = append(g.queue, e)
	}
	return &Ticket{governor: g, entry: e}, nil
}

// Observe captures state and its mutation broadcast under the same lock. Wait
// outside the governor, then observe again: broadcasts may coalesce mutations.
// Terminal observations return a closed channel. Invalid handles return a
// cancelled observation and a nil channel; Begin reports ErrInvalidTicket.
func (t *Ticket) Observe() (Observation, <-chan struct{}) {
	if !t.valid() {
		return Observation{State: Cancelled, Reason: ErrInvalidTicket.Error()}, nil
	}
	g := t.governor
	g.mu.Lock()
	defer g.mu.Unlock()
	return t.entry.observation, t.entry.changed
}

// Begin transfers a granted reservation exactly once. It does not start a
// process. After it succeeds Cancel and Close cannot reclaim the slot, even if
// startup fails or the caller's context expires. The returned lease must be
// retained until positively verified cleanup permits Release.
func (t *Ticket) Begin() (*Lease, error) {
	if !t.valid() {
		return nil, ErrInvalidTicket
	}
	g := t.governor
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return nil, ErrClosed
	}
	switch t.entry.observation.State {
	case Granted:
		g.change(t.entry, Started, "")
		return &Lease{governor: g, entry: t.entry}, nil
	case Queued:
		return nil, ErrQueued
	case Cancelled:
		return nil, ErrCancelled
	case Released:
		return nil, ErrReleased
	default:
		return nil, ErrAlreadyBegun
	}
}

// Cancel releases only queued work or an unstarted grant. It is idempotent and
// has no effect after Begin, including when teardown is unverified.
func (t *Ticket) Cancel() {
	if !t.valid() {
		return
	}
	g := t.governor
	g.mu.Lock()
	defer g.mu.Unlock()
	switch t.entry.observation.State {
	case Queued:
		g.removeQueued(t.entry)
		g.remove(t.entry, Cancelled, "cancelled before begin")
	case Granted:
		g.remove(t.entry, Cancelled, "cancelled before begin")
	default:
		return
	}
	g.schedule()
}

// Release is idempotent, explicit disposal of this exact reservation. The caller
// MUST positively verify that no owned process remains, including failed-start
// and descendant cleanup, before calling it. Elapsed time, Done(), disconnect,
// cancellation and shutdown are not verification. A later verified cleanup may
// release an Unverified lease; neither settings nor Close do so automatically.
func (l *Lease) Release() {
	if l == nil || l.governor == nil || l.entry == nil {
		return
	}
	g := l.governor
	g.mu.Lock()
	defer g.mu.Unlock()
	e := l.entry
	if e.observation.State != Started && e.observation.State != Unverified {
		return
	}
	g.remove(e, Released, "")
	g.schedule()
}

// Retain marks uncertain teardown without releasing capacity. The reason must be
// a safe human-readable summary, not raw command/environment/secret data. It is
// normalized to valid UTF-8, stripped of control characters and byte-bounded;
// even an empty or malformed reason retains the reservation. Retain cannot
// resurrect a released lease or reset Unverified to Started.
func (l *Lease) Retain(reason string) {
	if l == nil || l.governor == nil || l.entry == nil {
		return
	}
	reason = boundedReason(reason)
	g := l.governor
	g.mu.Lock()
	defer g.mu.Unlock()
	e := l.entry
	if e.observation.State == Started || e.observation.State == Unverified {
		g.change(e, Unverified, reason)
	}
}

// Configure rejects invalid policy atomically. Reductions neither evict nor
// reclaim existing work (including queued tickets), so counts can temporarily
// exceed limits. New waiting work is refused until the queue is below its new
// cap. Increases immediately schedule eligible tickets.
func (g *Governor) Configure(limits Limits) error {
	if !validLimits(limits) {
		return ErrInvalidLimits
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return ErrClosed
	}
	g.limits = limits
	g.schedule()
	return nil
}

// ConfigureWorkspace changes the authoritative quota for retained work, without
// evicting reservations or forgetting uncertain teardown. A reduction may leave
// occupancy above quota; an increase immediately schedules eligible waiters.
// Existing observations reflect the current quota, not their old request value.
//
// Workspace obeys Owner's ID rules; limit is in [1, MaxProcesses].
// With no retained workspace, valid input is a no-op (no idle policy registry).
// The runtime settings owner supplies accepted policy and fences stale writes,
// including before Begin/start. A process generation is not a policy credential:
// reopened workspaces must be able to update quota while an older generation's
// uncertain teardown still occupies it. This method is not authorization.
func (g *Governor) ConfigureWorkspace(workspaceID string, limit int) error {
	if !validID(workspaceID) {
		return ErrInvalidOwner
	}
	if limit < 1 || limit > MaxProcesses {
		return ErrInvalidWorkspaceLimit
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return ErrClosed
	}
	ws := g.workspaces[workspaceID]
	if ws == nil {
		return nil
	}
	if ws.limit == limit {
		return nil
	}
	ws.limit = limit
	for owner, e := range g.entries {
		if owner.Workspace == workspaceID {
			e.observation.WorkspaceLimit = limit
			g.notify(e)
		}
	}
	g.schedule()
	return nil
}

// Close is terminal and idempotent. It cancels queued/unstarted reservations,
// forbids Submit and Begin, and leaves started/unverified leases inspectable.
// It does not stop processes, wait for cleanup, or claim shutdown succeeded.
func (g *Governor) Close() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return
	}
	g.closed = true
	for _, e := range g.entries {
		if e.observation.State == Queued || e.observation.State == Granted {
			g.remove(e, Cancelled, "governor closed before begin")
		}
	}
	g.queue = nil
}

func (g *Governor) Snapshot() Snapshot {
	g.mu.Lock()
	defer g.mu.Unlock()
	s := Snapshot{
		Limits: g.limits, Closed: g.closed,
		Entries:    make([]Observation, 0, len(g.entries)),
		Workspaces: make([]WorkspaceSnapshot, 0, len(g.workspaces)),
	}
	counts := make(map[string]Counts, len(g.workspaces))
	for _, e := range g.entries {
		o := e.observation
		s.Entries = append(s.Entries, o)
		s.Counts.add(o.State)
		c := counts[o.Owner.Workspace]
		c.add(o.State)
		counts[o.Owner.Workspace] = c
	}
	for id, ws := range g.workspaces {
		s.Workspaces = append(s.Workspaces, WorkspaceSnapshot{
			Workspace: id, Limit: ws.limit, Counts: counts[id],
		})
	}
	sort.Slice(s.Entries, func(i, j int) bool {
		a, b := s.Entries[i].Owner, s.Entries[j].Owner
		if a.Workspace != b.Workspace {
			return a.Workspace < b.Workspace
		}
		if a.Generation != b.Generation {
			return a.Generation < b.Generation
		}
		return a.Producer < b.Producer
	})
	sort.Slice(s.Workspaces, func(i, j int) bool {
		return s.Workspaces[i].Workspace < s.Workspaces[j].Workspace
	})
	return s
}

func (t *Ticket) valid() bool { return t != nil && t.governor != nil && t.entry != nil }

// All helpers below, except validators and boundedReason, run under mu.
func (g *Governor) change(e *entry, state State, reason string) {
	if e.observation.State == state && e.observation.Reason == reason {
		return
	}
	e.observation.State, e.observation.Reason = state, reason
	g.notify(e)
}

func (g *Governor) notify(e *entry) {
	close(e.changed)
	if e.observation.State != Cancelled && e.observation.State != Released {
		e.changed = make(chan struct{})
	}
}

func (g *Governor) grant(e *entry) {
	g.used++
	g.workspaces[e.observation.Owner.Workspace].used++
	g.change(e, Granted, "")
}

func (g *Governor) remove(e *entry, terminal State, reason string) {
	ws := g.workspaces[e.observation.Owner.Workspace]
	if e.observation.State != Queued {
		g.used--
		ws.used--
	}
	ws.retained--
	if ws.retained == 0 {
		delete(g.workspaces, e.observation.Owner.Workspace)
	}
	delete(g.entries, e.observation.Owner)
	g.change(e, terminal, reason)
}

func (g *Governor) removeQueued(e *entry) {
	for i, queued := range g.queue {
		if queued == e {
			copy(g.queue[i:], g.queue[i+1:])
			g.queue[len(g.queue)-1] = nil
			g.queue = g.queue[:len(g.queue)-1]
			return
		}
	}
}

func (g *Governor) schedule() {
	if g.closed || g.used >= g.limits.MaxProcesses {
		return
	}
	n := 0
	for _, e := range g.queue {
		ws := g.workspaces[e.observation.Owner.Workspace]
		if g.used < g.limits.MaxProcesses && ws.used < ws.limit {
			g.grant(e)
		} else {
			g.queue[n] = e
			n++
		}
	}
	clear(g.queue[n:])
	g.queue = g.queue[:n]
}

func (c *Counts) add(state State) {
	switch state {
	case Queued:
		c.Queued++
	case Granted:
		c.Granted++
	case Started:
		c.Started++
	case Unverified:
		c.Unverified++
	}
}

func validLimits(l Limits) bool {
	return l.MaxProcesses >= 1 && l.MaxProcesses <= MaxProcesses &&
		l.MaxWaiting >= 0 && l.MaxWaiting <= MaxWaiting
}

func validOwner(o Owner) bool {
	return validID(o.Workspace) && validID(o.Generation) && validID(o.Producer)
}

func validID(id string) bool {
	if len(id) == 0 || len(id) > MaxIDBytes {
		return false
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9', r == '.', r == '_', r == ':', r == '-':
		default:
			return false
		}
	}
	return true
}

func boundedReason(reason string) string {
	// Slice before normalization so even an oversized caller string cannot
	// cause an unbounded defensive allocation.
	if len(reason) > MaxReasonBytes {
		reason = reason[:MaxReasonBytes]
	}
	reason = strings.ToValidUTF8(reason, "\uFFFD")
	reason = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, reason)
	if len(reason) > MaxReasonBytes {
		reason = reason[:MaxReasonBytes]
		for !utf8.ValidString(reason) {
			reason = reason[:len(reason)-1]
		}
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return "process cleanup not verified"
	}
	return strings.Clone(reason)
}
