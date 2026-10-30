package governor_test

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"unicode"
	"unicode/utf8"

	"crude/insight/governor"
)

func newGovernor(t *testing.T, processes, waiting int) *governor.Governor {
	t.Helper()
	g, err := governor.New(governor.Limits{MaxProcesses: processes, MaxWaiting: waiting})
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func request(ws, producer string, limit int) governor.Request {
	return governor.Request{
		Owner:          governor.Owner{Workspace: ws, Generation: "g1", Producer: producer},
		WorkspaceLimit: limit,
	}
}

func submit(t *testing.T, g *governor.Governor, ws, producer string, limit int) *governor.Ticket {
	t.Helper()
	ticket, err := g.Submit(request(ws, producer, limit))
	if err != nil {
		t.Fatal(err)
	}
	return ticket
}

func begin(t *testing.T, ticket *governor.Ticket) *governor.Lease {
	t.Helper()
	lease, err := ticket.Begin()
	if err != nil {
		t.Fatal(err)
	}
	return lease
}

func state(t *testing.T, ticket *governor.Ticket, want governor.State) {
	t.Helper()
	got, _ := ticket.Observe()
	if got.State != want {
		t.Fatalf("owner %v: state %q, want %q", got.Owner, got.State, want)
	}
}

func counts(t *testing.T, g *governor.Governor, want governor.Counts) governor.Snapshot {
	t.Helper()
	s := g.Snapshot()
	if s.Counts != want {
		t.Fatalf("counts %+v, want %+v; entries %+v", s.Counts, want, s.Entries)
	}
	// Independently account every retained reservation, including grants and
	// uncertain teardown. Row count alone would miss premature slot reclamation.
	var global governor.Counts
	local := make(map[string]governor.Counts)
	for _, row := range s.Entries {
		c := local[row.Owner.Workspace]
		switch row.State {
		case governor.Queued:
			global.Queued++
			c.Queued++
		case governor.Granted:
			global.Granted++
			c.Granted++
		case governor.Started:
			global.Started++
			c.Started++
		case governor.Unverified:
			global.Unverified++
			c.Unverified++
		default:
			t.Fatalf("terminal state retained: %+v", row)
		}
		local[row.Owner.Workspace] = c
	}
	if global != want || len(local) != len(s.Workspaces) {
		t.Fatalf("row accounting: %+v, workspaces %+v", global, s.Workspaces)
	}
	for _, ws := range s.Workspaces {
		if local[ws.Workspace] != ws.Counts {
			t.Fatalf("workspace accounting: %+v vs %+v", ws, local[ws.Workspace])
		}
	}
	return s
}

func signalled(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	default:
		t.Fatal("mutation broadcast not closed")
	}
}

func quiet(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	if ch == nil {
		t.Fatal("valid observation has nil broadcast")
	}
	select {
	case <-ch:
		t.Fatal("broadcast closed without mutation")
	default:
	}
}

func TestEligibleFIFOAcrossThreeWorkspaces(t *testing.T) {
	g := newGovernor(t, 2, 8)
	a1 := submit(t, g, "a", "1", 1)
	b1 := submit(t, g, "b", "1", 1)
	la, lb := begin(t, a1), begin(t, b1)
	a2 := submit(t, g, "a", "2", 1)
	a3 := submit(t, g, "a", "3", 1)
	b2 := submit(t, g, "b", "2", 1)
	c1 := submit(t, g, "c", "1", 1)
	counts(t, g, governor.Counts{Started: 2, Queued: 4})
	lb.Release()
	state(t, a2, governor.Queued)
	state(t, a3, governor.Queued)
	state(t, b2, governor.Granted) // skips saturated a, ahead of eligible c
	state(t, c1, governor.Queued)
	counts(t, g, governor.Counts{Started: 1, Granted: 1, Queued: 3})
	begin(t, b2).Release()
	state(t, c1, governor.Granted)
	la.Release()
	state(t, a2, governor.Granted)
	state(t, a3, governor.Queued)
	a2.Cancel() // returning an unstarted grant admits the next a in FIFO
	state(t, a3, governor.Granted)
	counts(t, g, governor.Counts{Granted: 2})
	a3.Cancel()
	c1.Cancel()
	counts(t, g, governor.Counts{})
}

func TestReservationsQueueBoundsAndEligibleBypass(t *testing.T) {
	g := newGovernor(t, 1, 1)
	a1 := submit(t, g, "a", "1", 1)
	a2 := submit(t, g, "a", "2", 1)
	if _, err := g.Submit(request("a", "2", 1)); !errors.Is(err, governor.ErrDuplicateOwner) {
		t.Fatalf("duplicate queued owner: %v", err)
	}
	if _, err := a2.Begin(); !errors.Is(err, governor.ErrQueued) {
		t.Fatalf("queued Begin: %v", err)
	}
	if ticket, err := g.Submit(request("b", "1", 1)); ticket != nil || !errors.Is(err, governor.ErrQueueFull) {
		t.Fatalf("full queue: %v, %v", ticket, err)
	}
	counts(t, g, governor.Counts{Granted: 1, Queued: 1})
	if err := g.Configure(governor.Limits{MaxProcesses: 2, MaxWaiting: 1}); err != nil {
		t.Fatal(err)
	}
	// Full waiting queue is not a blanket admission block: a is locally full,
	// while b can use the newly available global reservation.
	b1 := submit(t, g, "b", "1", 1)
	state(t, b1, governor.Granted)
	a1.Cancel()
	state(t, a2, governor.Granted)
	counts(t, g, governor.Counts{Granted: 2})
	a2.Cancel()
	b1.Cancel()
	// Zero waiting capacity is a supported immediate-only policy.
	if err := g.Configure(governor.Limits{MaxProcesses: 1}); err != nil {
		t.Fatal(err)
	}
	a1 = submit(t, g, "a", "1", 1)
	if _, err := g.Submit(request("b", "1", 1)); !errors.Is(err, governor.ErrQueueFull) {
		t.Fatalf("zero waiting: %v", err)
	}
	a1.Cancel()
}

func TestExactlyOnceStaleHandlesAndDuplicateIdentity(t *testing.T) {
	g := newGovernor(t, 1, 2)
	a := submit(t, g, "a", "1", 1)
	duplicate := func() {
		t.Helper()
		if _, err := g.Submit(request("a", "1", 1)); !errors.Is(err, governor.ErrDuplicateOwner) {
			t.Fatalf("duplicate retained owner: %v", err)
		}
	}
	duplicate()
	lease := begin(t, a)
	duplicate()
	if _, err := a.Begin(); !errors.Is(err, governor.ErrAlreadyBegun) {
		t.Fatalf("second Begin: %v", err)
	}
	lease.Retain("join not verified")
	duplicate()
	a.Cancel()
	counts(t, g, governor.Counts{Unverified: 1})
	lease.Release() // this test's fake lifecycle has now positively verified cleanup
	lease.Release()
	lease.Retain("stale retain")
	if _, err := a.Begin(); !errors.Is(err, governor.ErrReleased) {
		t.Fatalf("released Begin: %v", err)
	}
	replacement := submit(t, g, "a", "1", 1)
	a.Cancel()
	lease.Release()
	state(t, replacement, governor.Granted)
	counts(t, g, governor.Counts{Granted: 1})
	replacement.Cancel()
	again := submit(t, g, "a", "1", 1)
	replacement.Cancel()
	if _, err := replacement.Begin(); !errors.Is(err, governor.ErrCancelled) {
		t.Fatalf("cancelled Begin: %v", err)
	}
	state(t, again, governor.Granted)
	again.Cancel()
	counts(t, g, governor.Counts{})
}

func TestCancelledQueueAndBroadcasts(t *testing.T) {
	g := newGovernor(t, 1, 3)
	a := submit(t, g, "a", "1", 1)
	b := submit(t, g, "b", "1", 1)
	c := submit(t, g, "c", "1", 1)
	_, bChanged := b.Observe()
	b.Cancel()
	b.Cancel()
	signalled(t, bChanged)
	_, terminal := b.Observe()
	signalled(t, terminal)
	counts(t, g, governor.Counts{Granted: 1, Queued: 1})
	a.Cancel()
	state(t, c, governor.Granted)
	c.Cancel()
	counts(t, g, governor.Counts{})
}

func TestUnverifiedBlocksOtherWorkspaceUntilPositiveRelease(t *testing.T) {
	g := newGovernor(t, 1, 4)
	a := submit(t, g, "a", "1", 1)
	lease := begin(t, a)
	b := submit(t, g, "b", "1", 1)
	_, changed := b.Observe()
	lease.Retain("process-tree cleanup not verified")
	a.Cancel() // context cancellation is not process death
	if err := g.Configure(governor.Limits{MaxProcesses: 1, MaxWaiting: 1}); err != nil {
		t.Fatal(err)
	}
	state(t, b, governor.Queued)
	quiet(t, changed)
	counts(t, g, governor.Counts{Unverified: 1, Queued: 1})
	lease.Release()
	signalled(t, changed)
	state(t, b, governor.Granted)
	counts(t, g, governor.Counts{Granted: 1})
	b.Cancel()
}

func TestGlobalReductionDoesNotEvictAnyReservation(t *testing.T) {
	g := newGovernor(t, 3, 3)
	a := submit(t, g, "a", "1", 1)
	b := submit(t, g, "b", "1", 1)
	c := submit(t, g, "c", "1", 1)
	la, lb := begin(t, a), begin(t, b)
	lb.Retain("wait failed")
	d := submit(t, g, "d", "1", 1)
	e := submit(t, g, "e", "1", 1)
	if err := g.Configure(governor.Limits{MaxProcesses: 1, MaxWaiting: 0}); err != nil {
		t.Fatal(err)
	}
	counts(t, g, governor.Counts{Started: 1, Unverified: 1, Granted: 1, Queued: 2})
	// A preexisting grant is not revoked by reduction, even before Begin.
	lc := begin(t, c)
	if _, err := g.Submit(request("f", "1", 1)); !errors.Is(err, governor.ErrQueueFull) {
		t.Fatalf("reduced waiting cap: %v", err)
	}
	la.Release()
	lc.Release()
	counts(t, g, governor.Counts{Unverified: 1, Queued: 2})
	state(t, d, governor.Queued)
	if err := g.Configure(governor.Limits{MaxProcesses: 2, MaxWaiting: 1}); err != nil {
		t.Fatal(err)
	}
	state(t, d, governor.Granted)
	state(t, e, governor.Queued)
	lb.Release()
	state(t, e, governor.Granted)
	d.Cancel()
	e.Cancel()
	counts(t, g, governor.Counts{})
}

func TestLiveWorkspacePolicyAndCrossGenerationAccounting(t *testing.T) {
	g := newGovernor(t, 3, 8)
	a1 := submit(t, g, "a", "1", 2)
	a2 := submit(t, g, "a", "2", 2)
	old := begin(t, a1)
	old.Retain("old generation cleanup unverified")
	live := begin(t, a2)
	newRequest := request("a", "3", 2)
	newRequest.Owner.Generation = "g2"
	next, err := g.Submit(newRequest)
	if err != nil {
		t.Fatal(err)
	}
	state(t, next, governor.Queued) // g2 does not get a fresh workspace bucket
	b := submit(t, g, "b", "1", 1)
	counts(t, g, governor.Counts{Unverified: 1, Started: 1, Queued: 1, Granted: 1})
	_, changed := next.Observe()
	if err := g.ConfigureWorkspace("a", 1); err != nil {
		t.Fatal(err)
	}
	signalled(t, changed)
	o, _ := next.Observe()
	if o.WorkspaceLimit != 1 {
		t.Fatalf("stale captured quota in observation: %+v", o)
	}
	if _, err := g.Submit(request("a", "4", 2)); !errors.Is(err, governor.ErrWorkspaceLimitConflict) {
		t.Fatalf("stale request cannot rewrite quota: %v", err)
	}
	live.Release()
	state(t, next, governor.Queued) // old uncertainty still exhausts reduced quota
	counts(t, g, governor.Counts{Unverified: 1, Granted: 1, Queued: 1})
	if err := g.ConfigureWorkspace("a", 2); err != nil {
		t.Fatal(err)
	}
	state(t, next, governor.Granted)
	newLease := begin(t, next)
	if err := g.ConfigureWorkspace("a", 1); err != nil {
		t.Fatal(err)
	}
	state(t, next, governor.Started) // no live eviction on local reduction
	old.Release()
	newLease.Release()
	if err := g.ConfigureWorkspace("a", 3); err != nil {
		t.Fatal(err)
	}
	// No idle registry: next accepted submission establishes its own quota.
	fresh := submit(t, g, "a", "fresh", 1)
	s := counts(t, g, governor.Counts{Granted: 2})
	if s.Workspaces[0].Limit != 1 {
		t.Fatalf("idle settings leaked into next lifetime: %+v", s.Workspaces)
	}
	fresh.Cancel()
	b.Cancel()
	counts(t, g, governor.Counts{})
}

func TestAcceptedPolicyForReopenedWorkspaceWithUnverifiedOldLease(t *testing.T) {
	g := newGovernor(t, 2, 4)
	old := submit(t, g, "workspace", "old", 2)
	lease := begin(t, old)
	lease.Retain("old workspace lifetime teardown unverified")
	// Accepted settings for a reopened workspace must apply while old uncertain
	// work still counts. A process lifetime is not a policy-authority credential.
	if err := g.ConfigureWorkspace("workspace", 1); err != nil {
		t.Fatalf("accepted current settings could not configure reopened workspace: %v", err)
	}
	next := request("workspace", "new", 1)
	next.Owner.Generation = "new-lifetime"
	ticket, err := g.Submit(next)
	if err != nil {
		t.Fatal(err)
	}
	state(t, ticket, governor.Queued)
	counts(t, g, governor.Counts{Unverified: 1, Queued: 1})
	lease.Release()
	state(t, ticket, governor.Granted)
	ticket.Cancel()
	counts(t, g, governor.Counts{})
}

func TestCloseKeepsStartedAndUnverifiedInspectable(t *testing.T) {
	g := newGovernor(t, 3, 2)
	a := submit(t, g, "a", "1", 1)
	b := submit(t, g, "b", "1", 1)
	c := submit(t, g, "c", "1", 1)
	la, lb := begin(t, a), begin(t, b)
	lb.Retain("uncertain teardown")
	d := submit(t, g, "d", "1", 1)
	_, cChanged := c.Observe()
	_, dChanged := d.Observe()
	g.Close()
	g.Close()
	signalled(t, cChanged)
	signalled(t, dChanged)
	state(t, c, governor.Cancelled)
	state(t, d, governor.Cancelled)
	if _, err := c.Begin(); !errors.Is(err, governor.ErrClosed) {
		t.Fatalf("Begin after Close: %v", err)
	}
	if _, err := g.Submit(request("e", "1", 1)); !errors.Is(err, governor.ErrClosed) {
		t.Fatalf("Submit after Close: %v", err)
	}
	if err := g.Configure(governor.Limits{MaxProcesses: 4}); !errors.Is(err, governor.ErrClosed) {
		t.Fatalf("Configure after Close: %v", err)
	}
	if err := g.ConfigureWorkspace("a", 4); !errors.Is(err, governor.ErrClosed) {
		t.Fatalf("ConfigureWorkspace after Close: %v", err)
	}
	a.Cancel()
	b.Cancel()
	s := counts(t, g, governor.Counts{Started: 1, Unverified: 1})
	if !s.Closed || s.Entries[1].Reason != "uncertain teardown" {
		t.Fatalf("closed snapshot lost uncertainty: %+v", s)
	}
	la.Retain("shutdown cleanup pending")
	counts(t, g, governor.Counts{Unverified: 2})
	la.Release()
	lb.Release()
	counts(t, g, governor.Counts{})
}

func TestLocalReductionPreservesUnstartedGrants(t *testing.T) {
	g := newGovernor(t, 3, 2)
	a1 := submit(t, g, "a", "1", 2)
	a2 := submit(t, g, "a", "2", 2)
	a3 := submit(t, g, "a", "3", 2)
	if err := g.ConfigureWorkspace("a", 1); err != nil {
		t.Fatal(err)
	}
	l1, l2 := begin(t, a1), begin(t, a2)
	counts(t, g, governor.Counts{Started: 2, Queued: 1})
	l1.Release()
	state(t, a3, governor.Queued)
	l2.Release()
	state(t, a3, governor.Granted)
	a3.Cancel()
	counts(t, g, governor.Counts{})
}

func TestDetachedObservationsAndNoLostWakeups(t *testing.T) {
	g := newGovernor(t, 1, 2)
	a := submit(t, g, "a", "1", 1)
	b := submit(t, g, "b", "1", 1)
	old, changed := b.Observe()
	s := g.Snapshot()
	s.Entries[0].State = governor.Unverified
	s.Workspaces[0].Limit = 999
	a.Cancel() // mutation happens before caller starts waiting
	signalled(t, changed)
	if old.State != governor.Queued {
		t.Fatalf("observation changed in place: %+v", old)
	}
	current, fresh := b.Observe()
	if current.State != governor.Granted || fresh == changed {
		t.Fatalf("new observation/broadcast: %+v", current)
	}
	quiet(t, fresh)
	lease := begin(t, b)
	signalled(t, fresh)
	_, changed = b.Observe()
	lease.Retain("cleanup uncertain")
	signalled(t, changed)
	_, changed = b.Observe()
	lease.Retain("cleanup uncertain") // no mutation
	quiet(t, changed)
	lease.Release()
	signalled(t, changed)
	_, changed = b.Observe()
	signalled(t, changed)
	counts(t, g, governor.Counts{})
}

func TestMetadataValidationAndSafeRetentionReason(t *testing.T) {
	valid := governor.Limits{MaxProcesses: 1, MaxWaiting: 1}
	badLimits := []governor.Limits{
		{}, {MaxProcesses: -1}, {MaxProcesses: governor.MaxProcesses + 1},
		{MaxProcesses: 1, MaxWaiting: -1}, {MaxProcesses: 1, MaxWaiting: governor.MaxWaiting + 1},
	}
	g := newGovernor(t, 1, 1)
	before := g.Snapshot()
	for _, limits := range badLimits {
		if got, err := governor.New(limits); got != nil || !errors.Is(err, governor.ErrInvalidLimits) {
			t.Fatalf("invalid New %+v: %v, %v", limits, got, err)
		}
		if err := g.Configure(limits); !errors.Is(err, governor.ErrInvalidLimits) {
			t.Fatalf("invalid Configure %+v: %v", limits, err)
		}
	}
	if !reflect.DeepEqual(before, g.Snapshot()) {
		t.Fatal("invalid policy mutated owner")
	}
	for _, bad := range []string{"", "space id", "/raw/path", "bad\nid", "\xff", "é", strings.Repeat("x", governor.MaxIDBytes+1)} {
		for field := range 3 {
			r := request("a", "1", 1)
			switch field {
			case 0:
				r.Owner.Workspace = bad
			case 1:
				r.Owner.Generation = bad
			case 2:
				r.Owner.Producer = bad
			}
			if _, err := g.Submit(r); !errors.Is(err, governor.ErrInvalidOwner) {
				t.Fatalf("invalid owner %+v: %v", r.Owner, err)
			}
		}
	}
	for _, limit := range []int{-1, 0, governor.MaxProcesses + 1} {
		if _, err := g.Submit(request("a", "1", limit)); !errors.Is(err, governor.ErrInvalidWorkspaceLimit) {
			t.Fatalf("invalid workspace limit %d: %v", limit, err)
		}
		if err := g.ConfigureWorkspace("a", limit); !errors.Is(err, governor.ErrInvalidWorkspaceLimit) {
			t.Fatalf("invalid workspace policy %d: %v", limit, err)
		}
	}
	if err := g.ConfigureWorkspace("", 1); !errors.Is(err, governor.ErrInvalidOwner) {
		t.Fatalf("invalid workspace policy ID: %v", err)
	}
	if err := g.Configure(valid); err != nil {
		t.Fatal(err)
	}
	r := request(strings.Repeat("a", governor.MaxIDBytes), "p._:-9", governor.MaxProcesses)
	ticket, err := g.Submit(r)
	if err != nil {
		t.Fatal(err)
	}
	lease := begin(t, ticket)
	for _, reason := range []string{"", "\xff\x00\n\t", strings.Repeat("界", governor.MaxReasonBytes), strings.Repeat("\xff", governor.MaxReasonBytes*2)} {
		lease.Retain(reason)
		o, _ := ticket.Observe()
		if o.State != governor.Unverified || o.Reason == "" ||
			!utf8.ValidString(o.Reason) || len(o.Reason) > governor.MaxReasonBytes ||
			strings.ContainsFunc(o.Reason, unicode.IsControl) {
			t.Fatalf("unsafe/unbounded reason: %+v", o)
		}
		counts(t, g, governor.Counts{Unverified: 1})
	}
	lease.Release()
	var zero governor.Ticket
	if _, err := zero.Begin(); !errors.Is(err, governor.ErrInvalidTicket) {
		t.Fatalf("forged zero ticket: %v", err)
	}
	zero.Cancel()
	if o, ch := zero.Observe(); o.State != governor.Cancelled || ch != nil {
		t.Fatalf("zero observation: %+v, %v", o, ch)
	}
	var zeroLease governor.Lease
	zeroLease.Retain("zero")
	zeroLease.Release()
	counts(t, g, governor.Counts{})
}

func TestNoTerminalJournalAcrossRepeatedLifetimes(t *testing.T) {
	g := newGovernor(t, 1, 1)
	for i := range 200 {
		ws := fmt.Sprintf("workspace-%d", i)
		a := submit(t, g, ws, "producer", 1)
		b := submit(t, g, ws, "queued", 1)
		b.Cancel()
		begin(t, a).Release()
		s := counts(t, g, governor.Counts{})
		if len(s.Entries) != 0 || len(s.Workspaces) != 0 {
			t.Fatalf("terminal history retained: %+v", s)
		}
	}
}

func TestAbsoluteMetadataBound(t *testing.T) {
	g := newGovernor(t, governor.MaxProcesses, governor.MaxWaiting)
	for i := range governor.MaxProcesses {
		submit(t, g, fmt.Sprintf("w%d", i), "live", 1)
	}
	for i := range governor.MaxWaiting {
		submit(t, g, fmt.Sprintf("queued%d", i), "wait", 1)
	}
	s := counts(t, g, governor.Counts{Granted: governor.MaxProcesses, Queued: governor.MaxWaiting})
	if len(s.Entries) != governor.MaxProcesses+governor.MaxWaiting {
		t.Fatalf("hard metadata bound: %d", len(s.Entries))
	}
	if _, err := g.Submit(request("overflow", "wait", 1)); !errors.Is(err, governor.ErrQueueFull) {
		t.Fatalf("hard queue bound: %v", err)
	}
	g.Close()
	counts(t, g, governor.Counts{})
}

// racing starts all contenders together and joins every test goroutine.
func racing(actions ...func()) {
	start := make(chan struct{})
	var wg sync.WaitGroup
	for _, action := range actions {
		wg.Go(func() { <-start; action() })
	}
	close(start)
	wg.Wait()
}

func TestConcurrentBeginAndCancellation(t *testing.T) {
	for range 100 {
		g := newGovernor(t, 1, 1)
		a := submit(t, g, "a", "1", 1)
		b := submit(t, g, "b", "1", 1)
		leases := make(chan *governor.Lease, 8)
		errs := make(chan error, 8)
		tryBegin := func() {
			lease, err := a.Begin()
			if err != nil {
				errs <- err
			} else {
				leases <- lease
			}
		}
		racing(tryBegin, tryBegin, tryBegin, tryBegin, a.Cancel, a.Cancel)
		close(leases)
		close(errs)
		if len(leases) > 1 {
			t.Fatal("reservation begun twice")
		}
		for err := range errs {
			if !errors.Is(err, governor.ErrAlreadyBegun) && !errors.Is(err, governor.ErrCancelled) {
				t.Fatalf("unexpected concurrent Begin error: %v", err)
			}
		}
		if len(leases) == 1 {
			state(t, b, governor.Queued)
			counts(t, g, governor.Counts{Started: 1, Queued: 1})
			(<-leases).Release()
		}
		state(t, b, governor.Granted)
		b.Cancel()
		counts(t, g, governor.Counts{})
	}
}

func TestConcurrentCloseGrantAndBegin(t *testing.T) {
	for range 100 {
		g := newGovernor(t, 1, 1)
		a := submit(t, g, "a", "1", 1)
		la := begin(t, a)
		b := submit(t, g, "b", "1", 1)
		var lb *governor.Lease
		var beginErr error
		_, changed := b.Observe()
		racing(la.Release, g.Close, func() { lb, beginErr = b.Begin() })
		signalled(t, changed)
		if lb != nil {
			counts(t, g, governor.Counts{Started: 1})
			b.Cancel()
			state(t, b, governor.Started)
			lb.Release()
		} else if !errors.Is(beginErr, governor.ErrClosed) && !errors.Is(beginErr, governor.ErrQueued) {
			t.Fatalf("Close/Begin error: %v", beginErr)
		}
		counts(t, g, governor.Counts{})
	}
}

func TestConcurrentObservationCapturesMutationBroadcast(t *testing.T) {
	for range 100 {
		g := newGovernor(t, 1, 1)
		a := submit(t, g, "a", "1", 1)
		b := submit(t, g, "b", "1", 1)
		var observation governor.Observation
		var changed <-chan struct{}
		racing(a.Cancel, func() { observation, changed = b.Observe() })
		// Either Observe preceded the grant and its channel is closed, or it
		// saw the grant and returned the fresh channel. No third outcome can
		// cause a caller to miss a transition while waiting on queued work.
		switch observation.State {
		case governor.Queued:
			signalled(t, changed)
		case governor.Granted:
			quiet(t, changed)
		default:
			t.Fatalf("impossible observation: %+v", observation)
		}
		b.Cancel()
		counts(t, g, governor.Counts{})
	}
}

func TestConcurrentReleaseRetainObserveAndPolicy(t *testing.T) {
	for range 100 {
		g := newGovernor(t, 1, 2)
		a := submit(t, g, "a", "1", 1)
		lease := begin(t, a)
		b := submit(t, g, "b", "1", 1)
		_, changed := b.Observe()
		racing(
			lease.Release, lease.Release,
			func() { lease.Retain("join uncertain") },
			a.Cancel,
			func() { _, _ = a.Observe(); _ = g.Snapshot() },
			func() {
				if err := g.Configure(governor.Limits{MaxProcesses: 1, MaxWaiting: 1}); err != nil {
					t.Error(err)
				}
			},
		)
		signalled(t, changed)
		state(t, a, governor.Released)
		state(t, b, governor.Granted)
		counts(t, g, governor.Counts{Granted: 1})
		b.Cancel()
	}
}

func TestConcurrentSubmissionBounds(t *testing.T) {
	g := newGovernor(t, 2, 3)
	accepted := make(chan *governor.Ticket, 64)
	rejected := make(chan error, 64)
	var actions []func()
	for i := range 64 {
		actions = append(actions, func() {
			ticket, err := g.Submit(request(fmt.Sprintf("w%d", i%3), fmt.Sprintf("p%d", i), 1))
			if err != nil {
				rejected <- err
			} else {
				accepted <- ticket
			}
		})
	}
	racing(actions...)
	close(accepted)
	close(rejected)
	counts(t, g, governor.Counts{Granted: 2, Queued: 3})
	if len(accepted) != 5 || len(rejected) != 59 {
		t.Fatalf("unbounded admission: accepted %d rejected %d", len(accepted), len(rejected))
	}
	for err := range rejected {
		if !errors.Is(err, governor.ErrQueueFull) {
			t.Fatal(err)
		}
	}
	for ticket := range accepted {
		ticket.Cancel()
	}
	counts(t, g, governor.Counts{})
}
