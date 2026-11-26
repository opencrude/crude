package provider

import (
	"cmp"
	"context"
	"crude/internal/retry"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// CompactionProviderOnly never delegates a checkpoint to another model.
	CompactionProviderOnly = "provider"
	// CompactionErrandOnly skips native discovery as well as native compaction.
	CompactionErrandOnly = "errand"
	supportCandidate     = "candidate" // Positive declaration, not a verified checkpoint.
	supportVerified      = "verified"
	supportUnavailable   = "unavailable"
	supportCooling       = "cooling"
)

// CompactionMethod returns the explicit account policy; empty means Auto.
func (p *Provider) CompactionMethod() string {
	if p == nil {
		return ""
	}
	p.authMu.RLock()
	b := p.binding
	p.authMu.RUnlock()
	if b != nil {
		b.manager.mu.RLock()
		if b.manager.accounts[b.id] == b.slot {
			method := accountCompactionMethod(b.slot.account)
			b.manager.mu.RUnlock()
			return method
		}
		b.manager.mu.RUnlock()
	}
	if p.protocolSettings == nil {
		return ""
	}
	return p.protocolSettings.Compaction
}

// UsesNativeCompaction reports whether a checkpoint should negotiate on the main connection.
func (p *Provider) UsesNativeCompaction() bool {
	if p == nil {
		return false
	}
	switch p.CompactionMethod() {
	case CompactionProviderOnly:
		return true
	case CompactionErrandOnly:
		return false
	default:
		return p.Protocol().Info().Options.NativeCompaction != ""
	}
}

// NativeCompactionPlanned reports whether the next checkpoint for model will
// try the native route. In Auto a fresh refusal or a cooldown means Errand
// writes it, so background preparation stays useful.
func (p *Provider) NativeCompactionPlanned(model string) bool {
	if !p.UsesNativeCompaction() {
		return false
	}
	if p.CompactionMethod() == CompactionProviderOnly {
		return true
	}
	f := currentSupport().fact(p.compactionScope(model))
	return f.retryAt <= time.Now().Unix() && (!f.fresh() || f.state != supportUnavailable)
}

type supportFact struct {
	account, model, state, method string
	expires, retryAt              int64
	failures                      int
}
type supportFlight struct {
	ctx        context.Context
	cancel     context.CancelCauseFunc
	account    string
	generation uint64
	done       chan struct{}
}
type supportCache struct {
	observations atomic.Pointer[[]CompactionObservation]
	attempts     map[attemptKey]int64
	mu           sync.Mutex
	db           *sql.DB
	facts        map[string]supportFact
	flights      map[string]*supportFlight
	generations  map[string]uint64
}

var volatileSupport supportCache

func currentSupport() *supportCache {
	if c := activeModelCatalog.Load(); c != nil {
		return &c.compaction
	}
	return &volatileSupport
}
func (c *supportCache) load(db *sql.DB) error {
	c.db = db
	if err := c.loadAttempts(); err != nil {
		return err
	}
	c.facts = make(map[string]supportFact)
	now := time.Now().Unix()
	if _, err := db.Exec(`DELETE FROM compaction_support WHERE max(expires,retry_at)<=?`, now); err != nil {
		return err
	}
	rows, err := db.Query(`SELECT scope,account,model,state,expires,failures,retry_at,method FROM compaction_support`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var k string
		var f supportFact
		if err := rows.Scan(&k, &f.account, &f.model, &f.state, &f.expires, &f.failures, &f.retryAt, &f.method); err != nil {
			return err
		}
		c.facts[k] = f
	}
	c.publishObservations()
	return rows.Err()
}

// Include request routing and API-version headers. Store only a digest, never
// endpoints, routing fields, credentials, prompts or artifact bytes.
func (p *Provider) compactionScope(model string) string {
	credential := p.compactionCredential
	if credential == "" {
		p.authMu.RLock()
		credential = p.APIKey
		p.authMu.RUnlock()
	}
	raw, err := json.Marshal([]any{"compact-v1", p.ReplayScope(), p.AccountID, model, credential, p.headers, p.extraBody, protocolHeaders(p.Protocol()), p.authType, p.authHeader})
	if err != nil {
		return ""
	}
	h := sha256.Sum256(raw)
	return hex.EncodeToString(h[:])
}
func (c *supportCache) fact(key string) supportFact {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.facts[key]
}
func (f supportFact) usable() bool { return f.state == supportVerified && f.failures == 0 && f.fresh() }
func (f supportFact) fresh() bool  { return f.expires > time.Now().Unix() }
func (c *supportCache) begin(ctx context.Context, key, account string) (*supportFlight, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if f := c.flights[key]; f != nil {
		return f, false
	}
	if c.flights == nil {
		c.flights = map[string]*supportFlight{}
	}
	op, cancel := context.WithCancelCause(ctx)
	f := &supportFlight{account: account, generation: c.generations[account], done: make(chan struct{}), ctx: op, cancel: cancel}
	c.flights[key] = f
	return f, true
}
func (c *supportCache) end(key string, f *supportFlight) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.flights[key] == f {
		delete(c.flights, key)
	}
	f.cancel(nil)
	close(f.done)
}

var errAccountChanged = errors.New("the account changed. Try the new connection")

func (c *supportCache) record(key string, flight *supportFlight, p *Provider, model, state, method string) error {
	return c.recordWithDelay(key, flight, p, model, state, method, 0)
}

func (c *supportCache) recordWithDelay(key string, flight *supportFlight, p *Provider, model, state, method string, advised time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.flights[key] != flight {
		return errAccountChanged
	}
	now := time.Now()
	f := supportFact{account: p.AccountID, model: model, state: state, method: method}
	prior := c.facts[key]
	switch state {
	case supportVerified:
		f.expires = now.Add(7 * 24 * time.Hour).Unix()
	case supportCandidate, supportUnavailable:
		f.expires = now.Add(24 * time.Hour).Unix()
	default:
		f.state, f.expires = prior.state, prior.expires
		f.failures = min(prior.failures+1, 8)
		delay := max(retry.Exponential(f.failures, time.Minute, retry.MaxCooldown), retry.Clamp(advised))
		f.retryAt = now.Add(delay).Unix()
	}
	if c.db != nil {
		if _, err := c.db.Exec(`INSERT OR REPLACE INTO compaction_support(scope,account,model,state,expires,failures,retry_at,method) VALUES(?,?,?,?,?,?,?,?)`, key, f.account, model, f.state, f.expires, f.failures, f.retryAt, f.method); err != nil {
			return err
		}
	}
	if c.facts == nil {
		c.facts = map[string]supportFact{}
	}
	for k, entry := range c.facts {
		if max(entry.expires, entry.retryAt) <= now.Unix() && k != key {
			delete(c.facts, k)
		}
	}
	c.facts[key] = f
	c.publishObservations()
	return nil
}
func (c *supportCache) forget(account string) error { return c.clearAccount(account, false) }
func (c *supportCache) clearAccount(account string, removed bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cancelFlightsLocked(account)
	defer c.publishObservations()
	if removed {
		for k := range c.attempts {
			if k.account == account {
				delete(c.attempts, k)
			}
		}
	}
	for k, f := range c.facts {
		if f.account == account {
			delete(c.facts, k)
		}
	}
	if c.db != nil {
		tx, err := c.db.Begin()
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback() }()
		if _, err = tx.Exec(`DELETE FROM compaction_support WHERE account=?`, account); err != nil {
			return err
		}
		if removed {
			if _, err = tx.Exec(`DELETE FROM compaction_attempts WHERE account=?`, account); err != nil {
				return err
			}
		}
		return tx.Commit()
	}
	return nil
}
func (p *Provider) currentBinding() error {
	p.authMu.RLock()
	b := p.binding
	p.authMu.RUnlock()
	if b != nil {
		_, err := b.snapshot()
		return err
	}
	return nil
}
func waitSupport(ctx context.Context, f *supportFlight) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-f.done:
		return nil
	}
}

// CompactionObservation is a read-only Settings hint, not an account preference.
type CompactionObservation struct {
	AccountID, Model, Status string
	Until                    int64
}

// CompactionObservations returns only current, non-secret facts for saved accounts.
func CompactionObservations(accounts map[string]Account) []CompactionObservation {
	snapshot := currentSupport().observations.Load()
	if snapshot == nil {
		return nil
	}
	now := time.Now().Unix()
	var result []CompactionObservation
	for _, row := range *snapshot {
		if _, ok := accounts[row.AccountID]; ok && row.Until > now {
			result = append(result, row)
		}
	}
	return result
}

// Publish off the core actor. Reading Settings must not wait for a database write.
func (c *supportCache) publishObservations() {
	var result []CompactionObservation
	now := time.Now().Unix()
	for _, f := range c.facts {
		status, until := f.state, f.expires
		if f.retryAt > now {
			status, until = supportCooling, f.retryAt
		} else if f.failures != 0 || f.state == supportCandidate {
			continue
		}
		if until > now {
			result = append(result, CompactionObservation{f.account, f.model, status, until})
		}
	}
	slices.SortFunc(result, func(a, b CompactionObservation) int {
		if n := cmp.Compare(a.AccountID, b.AccountID); n != 0 {
			return n
		}
		return cmp.Compare(a.Model, b.Model)
	})
	c.observations.Store(&result)
}

// ForgetCompactionSupport removes evidence and fences late probe completions. It never changes chat history.
func ForgetCompactionSupport(account string) error {
	return errors.Join(currentSupport().forget(account), activeLearnedLimits().forget(account))
}

func (c *supportCache) cancelFlights(account string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cancelFlightsLocked(account)
}
func (c *supportCache) cancelFlightsLocked(account string) {
	if c.generations == nil {
		c.generations = make(map[string]uint64)
	}
	c.generations[account]++
	for k, f := range c.flights {
		if f.account == account {
			f.cancel(errAccountChanged)
			delete(c.flights, k)
		}
	}
}

// PruneCompactionSupport removes orphaned checks after account changes made
// while the core was offline. Detached checkpoints still have portable text.
func (c *ModelCatalog) PruneCompactionSupport(accounts map[string]Account) error {
	cache := &c.compaction
	cache.mu.Lock()
	defer cache.mu.Unlock()
	defer cache.publishObservations()
	for key := range cache.attempts {
		if _, ok := accounts[key.account]; !ok {
			if _, err := c.db.Exec(`DELETE FROM compaction_attempts WHERE scope=? AND source=?`, key.scope, key.source); err != nil {
				return err
			}
			delete(cache.attempts, key)
		}
	}
	for key, f := range cache.facts {
		a, ok := accounts[f.account]
		if ok {
			p := &Provider{ID: a.ProviderID, AccountID: a.ID, Model: f.model}
			if a.Connection.apply(p) == nil {
				p.compactionCredential = compactionCredential(a)
				if p.compactionScope(f.model) == key {
					continue
				}
			}
		}
		if _, err := c.db.Exec(`DELETE FROM compaction_support WHERE scope=?`, key); err != nil {
			return err
		}
		delete(cache.facts, key)
	}
	return nil
}
