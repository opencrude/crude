package provider

import (
	"context"
	"crude/internal/retry"
	"time"
)

// A native POST has no portable idempotency contract. Keep a short-lived
// write-ahead marker for the exact source, even when the core dies between
// receiving the response and publishing the replacement. No content is saved.
type attemptKey struct{ scope, source, account string }

// attemptHold spaces automatic resubmission of one possibly paid request by
// the shared retry ceiling. Auto writes a portable checkpoint meanwhile; /compact may retry.
const attemptHold = retry.MaxCooldown

type earlierAttemptError struct{ until time.Time }

func (e *earlierAttemptError) Error() string {
	return "an earlier compaction request for this conversation did not finish"
}

func checkpointSource(owner string, msgs []ChatMessage) string {
	h := newPrefixHasher()
	h.add(NewTextMessage("system", owner))
	for _, m := range msgs {
		switch m.Origin {
		case OriginRuntimeContext, OriginTurnStopped:
			continue
		}
		h.add(m)
	}
	return h.value()
}
func (c *supportCache) guardAttempt(ctx context.Context, key attemptKey, flight *supportFlight, send bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.flights[key.scope] != flight {
		return errAccountChanged
	}
	if err := context.Cause(ctx); err != nil {
		return err
	}
	now := time.Now().Unix()
	if until := c.attempts[key]; until > now {
		return &earlierAttemptError{until: time.Unix(until, 0)}
	}
	if !send {
		return nil
	}
	expires := now + int64(attemptHold/time.Second)
	if c.db != nil {
		if _, err := c.db.ExecContext(ctx, `INSERT OR REPLACE INTO compaction_attempts(scope,source,account,expires) VALUES(?,?,?,?)`, key.scope, key.source, key.account, expires); err != nil {
			return err
		}
	}
	if c.attempts == nil {
		c.attempts = map[attemptKey]int64{}
	}
	for k, v := range c.attempts {
		if v <= now {
			delete(c.attempts, k)
		}
	}
	c.attempts[key] = expires
	return nil
}
func (c *supportCache) releaseRejected(key attemptKey, flight *supportFlight) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.flights[key.scope] != flight {
		return errAccountChanged
	}
	if c.db != nil {
		if _, err := c.db.Exec(`DELETE FROM compaction_attempts WHERE scope=? AND source=?`, key.scope, key.source); err != nil {
			return err
		}
	}
	delete(c.attempts, key)
	return nil
}
func (c *supportCache) loadAttempts() error {
	now := time.Now().Unix()
	if _, err := c.db.Exec(`DELETE FROM compaction_attempts WHERE expires<=?`, now); err != nil {
		return err
	}
	rows, err := c.db.Query(`SELECT scope,source,account,expires FROM compaction_attempts`)
	if err != nil {
		return err
	}
	defer rows.Close()
	c.attempts = map[attemptKey]int64{}
	for rows.Next() {
		var k attemptKey
		var v int64
		if err := rows.Scan(&k.scope, &k.source, &k.account, &v); err != nil {
			return err
		}
		c.attempts[k] = v
	}
	return rows.Err()
}
