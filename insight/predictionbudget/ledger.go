// Package predictionbudget is an aggregate token ledger, not a provider client.
// Run SQL off UI/core hot paths; join operations before settlement/retirement.
//
// Storage is never closed; no migrations/liveness inference. IDs must be public,
// never source/prompts/paths/credentials.
//
// Time has a durable high-water mark. Forward-clock correctness is assumed:
// the ledger cannot distinguish elapsed time from a false jump.
package predictionbudget

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"math"
	"time"
)

const (
	Window           = time.Hour
	ReservationTTL   = time.Minute
	MaxRequestTokens = int64(1 << 30)
	MaxLimitTokens   = int64(1 << 40)
	MaxRecords       = 8192
	MaxIdentityBytes = 160
	// MaxUsageTokens prevents overflow even when every retained row has debt.
	MaxUsageTokens = int64(math.MaxInt64 / MaxRecords)
	maxTime        = int64(math.MaxInt64) - int64(Window+ReservationTTL)
)

var (
	ErrInvalid   = errors.New("prediction budget: invalid input")
	ErrLimit     = errors.New("prediction budget: cap exceeded")
	ErrFull      = errors.New("prediction budget: storage full")
	ErrDuplicate = errors.New("prediction budget: duplicate ID")
	ErrNotFound  = errors.New("prediction budget: ticket not retained")
	ErrState     = errors.New("prediction budget: transition refused")
	ErrExpired   = errors.New("prediction budget: reservation expired")
	ErrConflict  = errors.New("prediction budget: conflicting settlement")
)

const (
	reserved = iota
	started
	settled
	_
	expired
)

// Request freezes public producing identity and exact core-lifetime Owner.
// Token bounds are positive, with sum <= MaxRequestTokens.
type Request struct {
	RequestID, Owner, Producer          string
	InputUpperTokens, OutputLimitTokens int64
}

// Ticket is secret: never log it. Only successful Reserve AND Start authorize
// sending; any error, including commit, refuses.
type Ticket string

// Usage is final observation after joining. Unreported requires Tokens=0;
// Complete requires Reported. Only complete usage (including zero) can refund.
type Usage struct {
	Reported, Complete bool
	Tokens             int64
}

// Meter is detached/constant-size. ActiveTokens covers reservations/starts,
// ChargedTokens settlements, ObservedTokens only ReportedRecords.
// UnknownRecords counts incomplete settlements. No price is known.
type Meter struct {
	EffectiveTime                                            time.Time
	LimitTokens, ActiveTokens, ChargedTokens, ObservedTokens int64
	RemainingTokens                                          int64
	Records, ReservedRecords, StartedRecords, SettledRecords int64
	AbandonedRecords, ExpiredRecords                         int64
	ReportedRecords, CompleteRecords, UnknownRecords         int64
}

type Store struct {
	db    *sql.DB
	clock func() time.Time
}

const schema = `
CREATE TABLE IF NOT EXISTS editor_prediction_clock (
 id INTEGER PRIMARY KEY CHECK(id=1),
 high_water INTEGER NOT NULL CHECK(typeof(high_water)='integer' AND high_water>=0)
);
INSERT OR IGNORE INTO editor_prediction_clock(id,high_water) VALUES(1,0);
CREATE TABLE IF NOT EXISTS editor_prediction_budget (
 request_id TEXT PRIMARY KEY CHECK(length(request_id) BETWEEN 1 AND 160),
 ticket TEXT NOT NULL UNIQUE CHECK(length(ticket)=64),
 owner TEXT NOT NULL CHECK(length(owner) BETWEEN 1 AND 160),
 producer TEXT NOT NULL CHECK(length(producer) BETWEEN 1 AND 160),
 state INTEGER NOT NULL CHECK(state BETWEEN 0 AND 4),
 reservation INTEGER NOT NULL CHECK(reservation BETWEEN 1 AND 1073741824),
 deadline INTEGER NOT NULL,
 retain_until INTEGER NOT NULL,
 charge INTEGER NOT NULL DEFAULT 0 CHECK(charge BETWEEN 0 AND 1125899906842623),
 reported INTEGER NOT NULL DEFAULT 0 CHECK(reported IN (0,1)),
 complete INTEGER NOT NULL DEFAULT 0 CHECK(complete IN (0,1) AND complete<=reported),
 observed INTEGER NOT NULL DEFAULT 0 CHECK(observed BETWEEN 0 AND 1125899906842623)
);
CREATE INDEX IF NOT EXISTS editor_prediction_budget_expiry
 ON editor_prediction_budget(state,retain_until);
CREATE INDEX IF NOT EXISTS editor_prediction_budget_owner
 ON editor_prediction_budget(owner,state);`

// Attach borrows db and initializes tables, never retiring owners. clock must be
// nonnil/concurrency-safe. Initialization has a five-second deadline.
func Attach(db *sql.DB, clock func() time.Time) (*Store, error) {
	if db == nil || clock == nil {
		return nil, ErrInvalid
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, schema); err != nil {
		return nil, err
	}
	s := &Store{db: db, clock: clock}
	if _, err = s.prepare(ctx, tx); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return s, nil
}

func clockValue(t time.Time) (int64, error) {
	if t.Before(time.Unix(0, 0)) || t.After(time.Unix(0, maxTime)) {
		return 0, ErrInvalid
	}
	return t.UnixNano(), nil
}

func identity(s string) bool {
	if len(s) == 0 || len(s) > MaxIdentityBytes {
		return false
	}
	for _, c := range []byte(s) {
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') &&
			(c < '0' || c > '9') && c != '.' && c != '_' && c != '-' &&
			c != ':' && c != '@' && c != '+' {
			return false
		}
	}
	return true
}

func validTicket(t Ticket) bool {
	if len(t) != 64 {
		return false
	}
	for _, c := range []byte(t) {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func validLimit(limit int64) bool { return limit > 0 && limit <= MaxLimitTokens }

// Acquire SQLite's writer lock BEFORE admission reads, across Stores/processes.
func (s *Store) prepare(ctx context.Context, tx *sql.Tx) (int64, error) {
	raw, err := clockValue(s.clock())
	if err != nil {
		return 0, err
	}
	var now int64
	if err = tx.QueryRowContext(ctx, `UPDATE editor_prediction_clock
 SET high_water=max(high_water,?) WHERE id=1 RETURNING high_water`, raw).Scan(&now); err != nil {
		return 0, err
	}
	if now < 0 || now > maxTime {
		return 0, ErrInvalid
	}
	var count int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM
 (SELECT 1 FROM editor_prediction_budget LIMIT 8193)`).Scan(&count); err != nil {
		return 0, err
	}
	if count > MaxRecords {
		return 0, ErrFull
	}
	if _, err = tx.ExecContext(ctx, `UPDATE editor_prediction_budget
 SET state=4,retain_until=deadline+? WHERE state=0 AND deadline<=?`, int64(Window), now); err != nil {
		return 0, err
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM editor_prediction_budget
 WHERE state IN (2,3,4) AND retain_until<=?`, now)
	return now, err
}

func meter(ctx context.Context, tx *sql.Tx, now, limit int64) (Meter, error) {
	m := Meter{EffectiveTime: time.Unix(0, now).UTC(), LimitTokens: limit}
	err := tx.QueryRowContext(ctx, `SELECT count(*),
 coalesce(sum(CASE WHEN state IN (0,1) THEN reservation ELSE 0 END),0),
 coalesce(sum(CASE WHEN state=2 THEN charge ELSE 0 END),0),
 coalesce(sum(observed),0),
 coalesce(sum(state=0),0),coalesce(sum(state=1),0),coalesce(sum(state=2),0),
 coalesce(sum(state=3),0),coalesce(sum(state=4),0),
 coalesce(sum(reported),0),coalesce(sum(complete),0),
 coalesce(sum(state=2 AND complete=0),0)
 FROM editor_prediction_budget`).Scan(&m.Records, &m.ActiveTokens,
		&m.ChargedTokens, &m.ObservedTokens, &m.ReservedRecords, &m.StartedRecords,
		&m.SettledRecords, &m.AbandonedRecords, &m.ExpiredRecords,
		&m.ReportedRecords, &m.CompleteRecords, &m.UnknownRecords)
	if err != nil {
		return Meter{}, err
	}
	m.RemainingTokens = max(int64(0), limit-m.ActiveTokens-m.ChargedTokens)
	return m, nil
}

// Refusals preserve clock maintenance; SQL errors roll back.
func (s *Store) transact(ctx context.Context, limit int64, op func(*sql.Tx, int64, Meter) error) (Meter, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Meter{}, err
	}
	defer func() { _ = tx.Rollback() }()
	now, err := s.prepare(ctx, tx)
	if err != nil {
		return Meter{}, err
	}
	m, err := meter(ctx, tx, now, limit)
	if err != nil {
		return Meter{}, err
	}
	var refusal error
	if op != nil {
		refusal = op(tx, now, m)
		if refusal != nil && !semantic(refusal) {
			return Meter{}, refusal
		}
		m, err = meter(ctx, tx, now, limit)
		if err != nil {
			return Meter{}, err
		}
	}
	if err = tx.Commit(); err != nil {
		return Meter{}, err
	}
	return m, refusal
}

func semantic(err error) bool {
	return err == ErrLimit || err == ErrFull || err == ErrDuplicate ||
		err == ErrNotFound || err == ErrState || err == ErrExpired || err == ErrConflict
}

// Reserve holds input-upper + output-max. Retained duplicates never get a ticket.
func (s *Store) Reserve(ctx context.Context, r Request, limit int64) (Ticket, Meter, error) {
	if !validLimit(limit) || !identity(r.RequestID) || !identity(r.Owner) ||
		!identity(r.Producer) || r.InputUpperTokens <= 0 || r.OutputLimitTokens <= 0 ||
		r.InputUpperTokens > MaxRequestTokens || r.OutputLimitTokens > MaxRequestTokens ||
		r.InputUpperTokens > MaxRequestTokens-r.OutputLimitTokens {
		return "", Meter{}, ErrInvalid
	}
	var secret [32]byte
	if _, err := rand.Read(secret[:]); err != nil {
		return "", Meter{}, err
	}
	ticket := Ticket(hex.EncodeToString(secret[:]))
	total := r.InputUpperTokens + r.OutputLimitTokens
	m, err := s.transact(ctx, limit, func(tx *sql.Tx, now int64, m Meter) error {
		var exists int
		err := tx.QueryRowContext(ctx, `SELECT 1 FROM editor_prediction_budget
 WHERE request_id=?`, r.RequestID).Scan(&exists)
		if err == nil {
			return ErrDuplicate
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if m.Records >= MaxRecords {
			return ErrFull
		}
		if total > m.RemainingTokens {
			return ErrLimit
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO editor_prediction_budget
 (request_id,ticket,owner,producer,state,reservation,deadline,retain_until)
 VALUES(?,?,?,?,0,?,?,?)`, r.RequestID, string(ticket), r.Owner, r.Producer,
			total, now+int64(ReservationTTL), now+int64(ReservationTTL+Window))
		return err
	})
	if err != nil {
		return "", m, err
	}
	return ticket, m, nil
}

type record struct {
	state, reservation, observed int64
	reported, complete           bool
}

func lookup(ctx context.Context, tx *sql.Tx, t Ticket) (record, error) {
	var r record
	err := tx.QueryRowContext(ctx, `SELECT state,reservation,observed,reported,complete
 FROM editor_prediction_budget WHERE ticket=?`, string(t)).
		Scan(&r.state, &r.reservation, &r.observed, &r.reported, &r.complete)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return r, err
}

// Start grants one attempt; a lowered cap can refuse a valid reservation.
func (s *Store) Start(ctx context.Context, t Ticket, currentLimit int64) (Meter, error) {
	if !validTicket(t) || !validLimit(currentLimit) {
		return Meter{}, ErrInvalid
	}
	return s.transact(ctx, currentLimit, func(tx *sql.Tx, _ int64, m Meter) error {
		r, err := lookup(ctx, tx, t)
		if err != nil {
			return err
		}
		if r.state == expired {
			return ErrExpired
		}
		if r.state != reserved {
			return ErrState
		}
		if m.ActiveTokens+m.ChargedTokens > currentLimit {
			return ErrLimit
		}
		_, err = tx.ExecContext(ctx, `UPDATE editor_prediction_budget SET state=1 WHERE ticket=?`, string(t))
		return err
	})
}

// Abandon refunds only unstarted work, retaining a one-hour ID tombstone.
func (s *Store) Abandon(ctx context.Context, t Ticket) error {
	if !validTicket(t) {
		return ErrInvalid
	}
	_, err := s.transact(ctx, MaxLimitTokens, func(tx *sql.Tx, now int64, _ Meter) error {
		r, err := lookup(ctx, tx, t)
		if err != nil {
			return err
		}
		if r.state != reserved {
			return ErrState
		}
		_, err = tx.ExecContext(ctx, `UPDATE editor_prediction_budget
 SET state=3,retain_until=? WHERE ticket=?`, now+int64(Window), string(t))
		return err
	})
	return err
}

// Settle requires joining. Partial billing keeps max(reserved, observed); complete
// billing reconciles usage/debt. Identical repeats never rewrite or redate.
func (s *Store) Settle(ctx context.Context, t Ticket, u Usage) error {
	if !validTicket(t) || u.Tokens < 0 || u.Tokens > MaxUsageTokens ||
		!u.Reported && (u.Complete || u.Tokens != 0) {
		return ErrInvalid
	}
	_, err := s.transact(ctx, MaxLimitTokens, func(tx *sql.Tx, now int64, _ Meter) error {
		r, err := lookup(ctx, tx, t)
		if err != nil {
			return err
		}
		if r.state == settled {
			if r.reported == u.Reported && r.complete == u.Complete && r.observed == u.Tokens {
				return nil
			}
			return ErrConflict
		}
		if r.state != started {
			return ErrState
		}
		charge := u.Tokens
		if !u.Complete {
			charge = max(charge, r.reservation)
		}
		_, err = tx.ExecContext(ctx, `UPDATE editor_prediction_budget
 SET state=2,charge=?,reported=?,complete=?,observed=?,retain_until=? WHERE ticket=?`,
			charge, u.Reported, u.Complete, u.Tokens, now+int64(Window), string(t))
		return err
	})
	return err
}

// Snapshot may expire/prune records, never started work. Debt remains visible;
// RemainingTokens is floored at zero.
func (s *Store) Snapshot(ctx context.Context, limit int64) (Meter, error) {
	if !validLimit(limit) {
		return Meter{}, ErrInvalid
	}
	return s.transact(ctx, limit, nil)
}

// RetireOwner requires proof this owner's jobs are joined. It abandons unstarted
// work, charges started work from now, and leaves settled/foreign rows unchanged.
func (s *Store) RetireOwner(ctx context.Context, exactOwner string) error {
	if !identity(exactOwner) {
		return ErrInvalid
	}
	_, err := s.transact(ctx, MaxLimitTokens, func(tx *sql.Tx, now int64, _ Meter) error {
		_, err := tx.ExecContext(ctx, `UPDATE editor_prediction_budget
 SET state=CASE WHEN state=0 THEN 3 ELSE 2 END,
 charge=CASE WHEN state=1 THEN reservation ELSE 0 END,retain_until=?
 WHERE owner=? AND state IN (0,1)`, now+int64(Window), exactOwner)
		return err
	})
	return err
}
