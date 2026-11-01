package predictionbudget

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

var ctx = context.Background()

type fakeClock struct{ ns atomic.Int64 }

func newClock() *fakeClock {
	c := &fakeClock{}
	c.ns.Store(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).UnixNano())
	return c
}
func (c *fakeClock) now() time.Time      { return time.Unix(0, c.ns.Load()) }
func (c *fakeClock) add(d time.Duration) { c.ns.Add(int64(d)) }

func openDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	u := url.URL{Scheme: "file", Path: path}
	db, err := sql.Open("sqlite", u.String()+"?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)")
	check(t, err)
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	return db
}
func attach(t *testing.T, db *sql.DB, c *fakeClock) *Store {
	t.Helper()
	s, err := Attach(db, c.now)
	check(t, err)
	return s
}
func fixture(t *testing.T) (*Store, *sql.DB, *fakeClock) {
	t.Helper()
	c := newClock()
	db := openDB(t, filepath.Join(t.TempDir(), "ledger.db"))
	return attach(t, db, c), db, c
}
func req(id string) Request {
	return Request{RequestID: id, Owner: "core-1", Producer: "account:model:protocol",
		InputUpperTokens: 6, OutputLimitTokens: 4}
}
func reserve(t *testing.T, s *Store, id string, limit int64) Ticket {
	t.Helper()
	ticket, _, err := s.Reserve(ctx, req(id), limit)
	check(t, err)
	return ticket
}
func begin(t *testing.T, s *Store, id string, limit int64) Ticket {
	t.Helper()
	ticket := reserve(t, s, id, limit)
	_, err := s.Start(ctx, ticket, limit)
	check(t, err)
	return ticket
}
func snapshot(t *testing.T, s *Store, limit int64) Meter {
	t.Helper()
	m, err := s.Snapshot(ctx, limit)
	check(t, err)
	return m
}
func check(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
func wantErr(t *testing.T, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("error = %v; want %v", err, want)
	}
}
func execSQL(t *testing.T, db *sql.DB, statement string) {
	t.Helper()
	if _, err := db.Exec(statement); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentAggregateAndSingleStart(t *testing.T) {
	c := newClock()
	path := filepath.Join(t.TempDir(), "shared.db")
	s1 := attach(t, openDB(t, path), c)
	s2 := attach(t, openDB(t, path), c)
	stores := []*Store{s1, s2}
	var admitted atomic.Int64
	var wg sync.WaitGroup
	tickets := make(chan Ticket, 24)
	for i := range 24 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r := req(fmt.Sprintf("r-%d", i))
			r.Owner = fmt.Sprintf("core-%d", i)
			r.Producer = fmt.Sprintf("producer-%d", i)
			ticket, _, err := stores[i%2].Reserve(ctx, r, 30)
			if err == nil {
				admitted.Add(1)
				tickets <- ticket
			} else if !errors.Is(err, ErrLimit) {
				t.Errorf("reserve: %v", err)
			} else if ticket != "" {
				t.Error("refused ticket")
			}
		}(i)
	}
	wg.Wait()
	close(tickets)
	if admitted.Load() != 3 {
		t.Fatalf("admissions=%d, want exactly 3", admitted.Load())
	}
	var starts atomic.Int64
	for ticket := range tickets {
		for i := range 8 {
			wg.Add(1)
			go func(i int, ticket Ticket) {
				defer wg.Done()
				_, err := stores[i%2].Start(ctx, ticket, 30)
				if err == nil {
					starts.Add(1)
				} else if !errors.Is(err, ErrState) {
					t.Errorf("start: %v", err)
				}
			}(i, ticket)
		}
	}
	wg.Wait()
	m := snapshot(t, s2, 30)
	if starts.Load() != 3 || m.StartedRecords != 3 || m.ActiveTokens != 30 || m.RemainingTokens != 0 {
		t.Fatalf("starts=%d meter=%+v", starts.Load(), m)
	}
}

func TestTicketNonReuseAndCapReduction(t *testing.T) {
	s, _, _ := fixture(t)
	ticket := reserve(t, s, "same", 10)
	duplicate, _, err := s.Reserve(ctx, req("same"), 10)
	wantErr(t, err, ErrDuplicate)
	if duplicate != "" {
		t.Fatal("duplicate ticket")
	}
	_, err = s.Start(ctx, ticket, 9)
	wantErr(t, err, ErrLimit)
	if m := snapshot(t, s, 9); m.ReservedRecords != 1 || m.ActiveTokens != 10 || m.RemainingTokens != 0 {
		t.Fatalf("cap: %+v", m)
	}
	if err = s.Settle(ctx, ticket, Usage{Reported: true, Complete: true}); err != ErrState {
		t.Fatalf("unstarted settlement: %v", err)
	}
	check(t, s.Abandon(ctx, ticket))
	wantErr(t, s.Abandon(ctx, ticket), ErrState)
	_, err = s.Start(ctx, ticket, 10)
	wantErr(t, err, ErrState)
	_, _, err = s.Reserve(ctx, req("same"), 10)
	wantErr(t, err, ErrDuplicate)
	startedTicket := begin(t, s, "other", 10)
	wantErr(t, s.Abandon(ctx, startedTicket), ErrState)
	if m := snapshot(t, s, 10); m.ActiveTokens != 10 || m.AbandonedRecords != 1 {
		t.Fatalf("abandon: %+v", m)
	}
}

func TestTTLBoundaryAndRollback(t *testing.T) {
	s, db, c := fixture(t)
	first := reserve(t, s, "first", 20)
	second := reserve(t, s, "second", 20)
	c.add(60*time.Second - time.Nanosecond)
	if _, err := s.Start(ctx, first, 20); err != nil {
		t.Fatal(err)
	}
	c.add(time.Nanosecond)
	_, err := s.Start(ctx, second, 20)
	wantErr(t, err, ErrExpired)
	c.add(-time.Hour)
	s = attach(t, db, c)
	_, err = s.Start(ctx, second, 20)
	wantErr(t, err, ErrExpired)
	_, _, err = s.Reserve(ctx, req("second"), 20)
	wantErr(t, err, ErrDuplicate)
	m := snapshot(t, s, 20)
	if m.ActiveTokens != 10 || m.ExpiredRecords != 1 || m.StartedRecords != 1 {
		t.Fatalf("TTL: %+v", m)
	}
}

func TestSettlementUsageAndDebt(t *testing.T) {
	cases := []struct {
		name                                          string
		u                                             Usage
		charge, observed, reported, complete, unknown int64
	}{
		{"complete-zero", Usage{true, true, 0}, 0, 0, 1, 1, 0},
		{"absent", Usage{}, 10, 0, 0, 0, 1},
		{"partial-zero", Usage{true, false, 0}, 10, 0, 1, 0, 1},
		{"partial-low", Usage{true, false, 3}, 10, 3, 1, 0, 1},
		{"complete-low", Usage{true, true, 3}, 3, 3, 1, 1, 0},
		{"partial-debt", Usage{true, false, 25}, 25, 25, 1, 0, 1},
		{"complete-debt", Usage{true, true, 25}, 25, 25, 1, 1, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, _, _ := fixture(t)
			ticket := begin(t, s, "bill", 10)
			check(t, s.Settle(ctx, ticket, tc.u))
			m := snapshot(t, s, 20)
			if m.ActiveTokens != 0 || m.ChargedTokens != tc.charge || m.ObservedTokens != tc.observed ||
				m.ReportedRecords != tc.reported || m.CompleteRecords != tc.complete ||
				m.UnknownRecords != tc.unknown || m.SettledRecords != 1 {
				t.Fatalf("settlement: %+v", m)
			}
			next, _, err := s.Reserve(ctx, req("next"), 20)
			if tc.charge > 10 {
				wantErr(t, err, ErrLimit)
				if next != "" || m.RemainingTokens != 0 {
					t.Fatal("debt overspend")
				}
			} else if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestConflictingSettlementAndNoRedate(t *testing.T) {
	s, db, c := fixture(t)
	s2 := attach(t, db, c)
	ticket := begin(t, s, "bill", 10)
	var successes, conflicts atomic.Int64
	var wg sync.WaitGroup
	for i := range 2 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			err := []*Store{s, s2}[i].Settle(ctx, ticket,
				Usage{Reported: true, Complete: true, Tokens: int64(i + 3)})
			if err == nil {
				successes.Add(1)
			} else if errors.Is(err, ErrConflict) {
				conflicts.Add(1)
			} else {
				t.Errorf("settle: %v", err)
			}
		}(i)
	}
	wg.Wait()
	if successes.Load() != 1 || conflicts.Load() != 1 {
		t.Fatalf("success=%d conflict=%d", successes.Load(), conflicts.Load())
	}
	m := snapshot(t, s, 10)
	if m.ChargedTokens != 3 && m.ChargedTokens != 4 {
		t.Fatalf("unexpected charge: %+v", m)
	}
	c.add(59 * time.Minute)
	check(t, s.Settle(ctx, ticket, Usage{Reported: true, Complete: true, Tokens: m.ChargedTokens}))
	c.add(time.Minute)
	if m := snapshot(t, s, 10); m.Records != 0 || m.RemainingTokens != 10 {
		t.Fatalf("redate: %+v", m)
	}
}

func TestLongWorkReopenAndSettlementWindow(t *testing.T) {
	s, db, c := fixture(t)
	ticket := begin(t, s, "long", 10)
	c.add(2 * time.Hour)
	// Reopen must not infer owner death.
	path := dbPath(t, db)
	check(t, db.Close())
	s = attach(t, openDB(t, path), c)
	m := snapshot(t, s, 10)
	if m.ActiveTokens != 10 || m.StartedRecords != 1 {
		t.Fatalf("long start: %+v", m)
	}
	check(t, s.Settle(ctx, ticket, Usage{}))
	c.add(-3 * time.Hour)
	check(t, s.db.Close())
	s = attach(t, openDB(t, path), c)
	if m := snapshot(t, s, 10); m.ChargedTokens != 10 || m.EffectiveTime != time.Date(2026, 1, 1, 2, 0, 0, 0, time.UTC) {
		t.Fatalf("rollback: %+v", m)
	}
	c.add(4*time.Hour - time.Nanosecond)
	if m := snapshot(t, s, 10); m.ChargedTokens != 10 {
		t.Fatalf("early expiry: %+v", m)
	}
	c.add(time.Nanosecond)
	if m := snapshot(t, s, 10); m.Records != 0 || m.RemainingTokens != 10 {
		t.Fatalf("expiry: %+v", m)
	}
	_, err := s.Start(ctx, ticket, 10)
	wantErr(t, err, ErrNotFound)
}

func dbPath(t *testing.T, db *sql.DB) string {
	t.Helper()
	var seq int
	var name, path string
	check(t, db.QueryRow("PRAGMA database_list").Scan(&seq, &name, &path))
	return path
}

func TestExactRetirementPreservesForeignAndSettled(t *testing.T) {
	s, _, c := fixture(t)
	old := begin(t, s, "old-started", 100)
	prior := begin(t, s, "old-settled", 100)
	check(t, s.Settle(ctx, prior, Usage{Reported: true, Complete: true, Tokens: 7}))
	c.add(30 * time.Minute)
	_ = reserve(t, s, "old-unstarted", 100)
	for _, owner := range []string{"core-10", "core-2"} {
		r := req(owner)
		r.Owner = owner
		ticket, _, err := s.Reserve(ctx, r, 100)
		check(t, err)
		if _, err = s.Start(ctx, ticket, 100); err != nil {
			t.Fatal(err)
		}
	}
	check(t, s.RetireOwner(ctx, "core-1"))
	m := snapshot(t, s, 100)
	if m.ActiveTokens != 20 || m.ChargedTokens != 17 || m.UnknownRecords != 1 || m.StartedRecords != 2 || m.AbandonedRecords != 1 {
		t.Fatalf("retirement scope: %+v", m)
	}
	_, err := s.Start(ctx, old, 100)
	wantErr(t, err, ErrState)
	c.add(30 * time.Minute)
	check(t, s.RetireOwner(ctx, "core-1"))
	if m = snapshot(t, s, 100); m.ChargedTokens != 10 {
		t.Fatalf("settled redate: %+v", m)
	}
	c.add(30 * time.Minute)
	if m = snapshot(t, s, 100); m.ChargedTokens != 0 || m.ActiveTokens != 20 || m.Records != 2 {
		t.Fatalf("repeat retirement: %+v", m)
	}
}

func TestValidationBoundsAndDetachedMeter(t *testing.T) {
	s, db, c := fixture(t)
	for _, bad := range []Request{
		{}, {strings.Repeat("x", 161), "o", "p", 1, 1},
		{"id", "/tmp/private", "p", 1, 1}, {"id", "o", "prompt\n", 1, 1},
		{"id", "o", "p", -1, 2}, {"id", "o", "p", 0, 2},
		{"id", "o", "p", math.MaxInt64, 1}, {"id", "o", "p", 1073741824, 1},
	} {
		_, _, err := s.Reserve(ctx, bad, 10)
		wantErr(t, err, ErrInvalid)
	}
	for _, limit := range []int64{0, -1, 1099511627777, math.MaxInt64} {
		_, _, err := s.Reserve(ctx, req("bad"), limit)
		wantErr(t, err, ErrInvalid)
		_, err = s.Snapshot(ctx, limit)
		wantErr(t, err, ErrInvalid)
	}
	ticket := begin(t, s, "valid", 10)
	for _, u := range []Usage{
		{Complete: true}, {Tokens: 1}, {Reported: true, Tokens: -1},
		{Reported: true, Tokens: math.MaxInt64},
	} {
		wantErr(t, s.Settle(ctx, ticket, u), ErrInvalid)
	}
	_, err := s.Start(ctx, "not-a-ticket", 10)
	wantErr(t, err, ErrInvalid)
	_, err = s.Start(ctx, Ticket(strings.Repeat("0", 64)), 10)
	wantErr(t, err, ErrNotFound)
	wantErr(t, s.RetireOwner(ctx, ""), ErrInvalid)
	m := snapshot(t, s, 10)
	m.ActiveTokens = 0
	m.RemainingTokens = 999
	if fresh := snapshot(t, s, 10); fresh.ActiveTokens != 10 || fresh.RemainingTokens != 0 {
		t.Fatalf("detachment: %+v", fresh)
	}
	if _, err = Attach(nil, c.now); err != ErrInvalid {
		t.Fatalf("nil DB: %v", err)
	}
	if _, err = Attach(db, nil); err != ErrInvalid {
		t.Fatalf("nil clock: %v", err)
	}
	for _, badTime := range []time.Time{
		{}, time.Unix(-1, 0), time.Unix(0, math.MaxInt64),
	} {
		if _, err = Attach(db, func() time.Time { return badTime }); err != ErrInvalid {
			t.Fatalf("invalid time %v: %v", badTime, err)
		}
	}
}

func TestMaximumTimeAndUsageArithmetic(t *testing.T) {
	s, _, c := fixture(t)
	c.ns.Store(math.MaxInt64 - int64(61*time.Minute))
	r := req("max")
	r.InputUpperTokens, r.OutputLimitTokens = 1073741823, 1
	ticket, _, err := s.Reserve(ctx, r, 1099511627776)
	check(t, err)
	if _, err = s.Start(ctx, ticket, 1099511627776); err != nil {
		t.Fatal(err)
	}
	check(t, s.Settle(ctx, ticket, Usage{Reported: true, Tokens: 1125899906842623}))
	m := snapshot(t, s, 1099511627776)
	if m.ChargedTokens != 1125899906842623 || m.RemainingTokens != 0 {
		t.Fatalf("large debt: %+v", m)
	}
	c.add(time.Nanosecond)
	_, err = s.Snapshot(ctx, 10)
	wantErr(t, err, ErrInvalid)
}

func TestSQLRollbackCancelBusyClosed(t *testing.T) {
	s, db, c := fixture(t)
	execSQL(t, db, `CREATE TRIGGER fail_reserve BEFORE INSERT ON editor_prediction_budget
 BEGIN SELECT RAISE(ABORT,'injected write failure'); END`)
	ticket, _, err := s.Reserve(ctx, req("rollback"), 10)
	if err == nil || ticket != "" {
		t.Fatal("failed reserve ticket")
	}
	execSQL(t, db, `DROP TRIGGER fail_reserve`)
	if m := snapshot(t, s, 10); m.Records != 0 || m.RemainingTokens != 10 {
		t.Fatalf("write rollback: %+v", m)
	}
	ticket = begin(t, s, "rollback", 10)
	execSQL(t, db, `CREATE TRIGGER fail_settle BEFORE UPDATE OF charge ON editor_prediction_budget
 BEGIN SELECT RAISE(ABORT,'injected settlement failure'); END`)
	if err = s.Settle(ctx, ticket, Usage{Reported: true, Complete: true}); err == nil {
		t.Fatal("settlement succeeded")
	}
	execSQL(t, db, `DROP TRIGGER fail_settle`)
	if m := snapshot(t, s, 10); m.StartedRecords != 1 || m.ActiveTokens != 10 {
		t.Fatalf("settlement rollback: %+v", m)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	_, err = s.Start(cancelled, ticket, 10)
	wantErr(t, err, context.Canceled)
	// Cancel inside a transaction.
	cancelled, cancel = context.WithCancel(ctx)
	s.clock = func() time.Time { cancel(); return c.now() }
	_, err = s.Snapshot(cancelled, 10)
	if err == nil {
		t.Fatal("cancellation succeeded")
	}
	s.clock = c.now
	if m := snapshot(t, s, 10); m.ActiveTokens != 10 {
		t.Fatalf("cancellation: %+v", m)
	}
	other := openDB(t, dbPath(t, db))
	execSQL(t, other, `PRAGMA busy_timeout=0`)
	s2 := attach(t, other, c)
	tx, err := db.Begin()
	check(t, err)
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.Exec(`UPDATE editor_prediction_clock SET high_water=high_water`); err != nil {
		t.Fatal(err)
	}
	refused, _, err := s2.Reserve(ctx, req("busy"), 20)
	if err == nil || refused != "" {
		t.Fatal("busy ticket")
	}
	check(t, tx.Rollback())
	check(t, db.Close())
	refused, _, err = s.Reserve(ctx, req("closed"), 20)
	if err == nil || refused != "" {
		t.Fatal("closed DB ticket")
	}
	if _, err = Attach(db, c.now); err == nil {
		t.Fatal("attached closed DB")
	}
}

func TestCommitFailureNeverGrantsPermission(t *testing.T) {
	s, db, _ := fixture(t)
	// Deferred FK failure exercises real COMMIT and driver rollback.
	execSQL(t, db, `CREATE TABLE parent_for_failure(id INTEGER PRIMARY KEY);
 CREATE TABLE child_for_failure(id INTEGER REFERENCES parent_for_failure(id)
 DEFERRABLE INITIALLY DEFERRED);
 CREATE TRIGGER commit_failure AFTER INSERT ON editor_prediction_budget
 BEGIN INSERT INTO child_for_failure VALUES(1); END;`)
	ticket, m, err := s.Reserve(ctx, req("commit"), 10)
	if err == nil || ticket != "" || m != (Meter{}) {
		t.Fatalf("commit: ticket=%t meter=%+v err=%v", ticket != "", m, err)
	}
	execSQL(t, db, `DROP TRIGGER commit_failure`)
	if m = snapshot(t, s, 10); m.Records != 0 || m.RemainingTokens != 10 {
		t.Fatalf("commit rollback: %+v", m)
	}
	_ = begin(t, s, "commit", 10)
}

func TestSaturationPrunesButRetainsStarted(t *testing.T) {
	s, db, c := fixture(t)
	// Seed bounded literal tombstones to avoid O(N^2) fixture construction.
	execSQL(t, db, fmt.Sprintf(`WITH RECURSIVE n(i) AS
 (VALUES(1) UNION ALL SELECT i+1 FROM n WHERE i<8191)
 INSERT INTO editor_prediction_budget
 (request_id,ticket,owner,producer,state,reservation,deadline,retain_until)
 SELECT printf('seed-%%d',i),printf('%%064x',i),'old','public',3,10,0,%d FROM n`,
		c.now().Add(time.Hour).UnixNano()))
	live := begin(t, s, "live", 10)
	failed, _, err := s.Reserve(ctx, req("overflow"), 10)
	wantErr(t, err, ErrFull)
	if failed != "" {
		t.Fatal("full ticket")
	}
	m := snapshot(t, s, 10)
	if m.Records != 8192 || m.AbandonedRecords != 8191 || m.StartedRecords != 1 {
		t.Fatalf("storage saturation: %+v", m)
	}
	c.add(time.Hour)
	if m = snapshot(t, s, 10); m.Records != 1 || m.ActiveTokens != 10 {
		t.Fatalf("prune: %+v", m)
	}
	check(t, s.Settle(ctx, live, Usage{Reported: true, Complete: true}))
	fresh := reserve(t, s, "overflow", 10)
	if fresh == live {
		t.Fatal("ticket reused")
	}
}

func TestTombstonePruningRequiresNewTicket(t *testing.T) {
	s, _, c := fixture(t)
	old := reserve(t, s, "same", 10)
	c.add(61*time.Minute - time.Nanosecond)
	_, _, err := s.Reserve(ctx, req("same"), 10)
	wantErr(t, err, ErrDuplicate)
	c.add(time.Nanosecond)
	fresh := reserve(t, s, "same", 10)
	if fresh == old {
		t.Fatal("ticket reused")
	}
	_, err = s.Start(ctx, old, 10)
	wantErr(t, err, ErrNotFound)
	if _, err = s.Start(ctx, fresh, 10); err != nil {
		t.Fatal(err)
	}
}
