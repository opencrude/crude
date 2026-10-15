package provider

import (
	"sync"
	"time"
)

// Connections differ in how many requests they accept at once; many cheap or
// self-hosted endpoints serve one. Crude learns a refusal (429 or overload
// while another request of the same connection was in flight) and keeps work
// for that connection serial afterwards, the way output ceilings are learned.

const learnedConcurrencyLifetime = 7 * 24 * time.Hour

var transientConcurrencyRefusals = struct {
	sync.Mutex
	entries map[string]time.Time
}{entries: make(map[string]time.Time)}

func concurrencyKey(connection *Provider, model string) (scope, account string, ok bool) {
	if connection == nil || model == "" {
		return "", "", false
	}
	return connection.ReplayScope(), connection.AccountID, true
}

// ConcurrencyRefused reports whether this connection and model recently refused
// a concurrent request.
func ConcurrencyRefused(connection *Provider, model string) bool {
	scope, account, ok := concurrencyKey(connection, model)
	if !ok {
		return false
	}
	cutoff := time.Now().Add(-learnedConcurrencyLifetime)
	if catalog := activeModelCatalog.Load(); catalog != nil {
		var updated int64
		if catalog.db.QueryRow(`SELECT updated_at FROM connection_concurrency_refusals WHERE provider_scope=? AND account_id=? AND model_id=?`, scope, account, model).Scan(&updated) == nil {
			return updated >= cutoff.Unix()
		}
		return false
	}
	transientConcurrencyRefusals.Lock()
	defer transientConcurrencyRefusals.Unlock()
	observed, exists := transientConcurrencyRefusals.entries[scope+"\x00"+account+"\x00"+model]
	return exists && observed.After(cutoff)
}

// RecordConcurrencyRefused retains evidence that this connection serves one
// request at a time.
func RecordConcurrencyRefused(connection *Provider, model string) error {
	scope, account, ok := concurrencyKey(connection, model)
	if !ok {
		return nil
	}
	now := time.Now()
	if catalog := activeModelCatalog.Load(); catalog != nil {
		_, err := catalog.db.Exec(`INSERT INTO connection_concurrency_refusals(provider_scope,account_id,model_id,updated_at) VALUES(?,?,?,?)
 ON CONFLICT(provider_scope,account_id,model_id) DO UPDATE SET updated_at=excluded.updated_at`, scope, account, model, now.Unix())
		return err
	}
	transientConcurrencyRefusals.Lock()
	defer transientConcurrencyRefusals.Unlock()
	transientConcurrencyRefusals.entries[scope+"\x00"+account+"\x00"+model] = now
	return nil
}

// SameConnection reports whether two providers send requests to the same
// endpoint and account, so their requests compete for one concurrency limit.
func SameConnection(first, second *Provider) bool {
	return first != nil && second != nil && first.ReplayScope() == second.ReplayScope() && first.AccountID == second.AccountID
}
