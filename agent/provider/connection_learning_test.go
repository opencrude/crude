package provider

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestCapacitySharesAnAccountAcrossWiresButNeverSharesReplay(t *testing.T) {
	a := &Provider{ID: "gateway", AccountID: "first", BaseURL: "https://example.test", StandardName: "openai-responses", Model: "a"}
	b := &Provider{ID: "gateway", AccountID: "first", BaseURL: "https://example.test/", StandardName: "ollama", Model: "b"}
	other := &Provider{ID: "gateway", AccountID: "other", BaseURL: a.BaseURL, StandardName: "ollama"}
	if SameConnection(a, b) || capacityKey(a) != capacityKey(b) || capacityKey(a) == capacityKey(other) {
		t.Fatal("capacity and replay have different scopes")
	}
	store := &learnedLimitStore{}
	key := capacityKey(a)
	if err := store.record(key, 1, time.Hour); err != nil {
		t.Fatal(err)
	}
	slot, err := store.acquire(t.Context(), key)
	if err != nil {
		t.Fatal(err)
	}
	defer slot.release()
	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Millisecond)
	defer cancel()
	if _, err := store.acquire(ctx, capacityKey(b)); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("second wire bypassed the busy account: %v", err)
	}
	separate, err := store.acquire(t.Context(), capacityKey(other))
	if err != nil {
		t.Fatal(err)
	}
	separate.release()
	slot.release()
	next, err := store.acquire(t.Context(), capacityKey(b))
	if err != nil {
		t.Fatal(err)
	}
	next.release()
}

func TestOnlyExplicitConcurrencyRefusalsChangeCapacity(t *testing.T) {
	for _, tc := range []struct {
		e    error
		want bool
	}{
		{&StatusError{StatusCode: 429, Message: "Too many concurrent requests"}, true},
		{&APIError{Code: "rate_limit_exceeded", Message: "Too many concurrent requests"}, true},
		{&StatusError{StatusCode: 429, Message: "Too many concurrent requests", RetryHint: "false"}, false},
		{&StatusError{StatusCode: 429, Message: "Requests per minute exceeded"}, false},
		{&StatusError{StatusCode: 429, Code: "insufficient_quota", Message: "Too many concurrent requests"}, false},
		{&StatusError{StatusCode: 503, Message: "Temporarily overloaded"}, false},
	} {
		if got := ConcurrencyLimited(tc.e); got != tc.want {
			t.Errorf("%v: concurrency=%v want %v", tc.e, got, tc.want)
		}
	}
}

// A new process does not have to pay for another refusal. The learned
// concurrency is portable between wires, but not between provider accounts.
func TestAccountCapacitySurvivesCatalogReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "model-catalog.db")
	open := func() *ModelCatalog {
		t.Helper()
		c, err := OpenModelCatalog(path, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = c.Close() })
		return c
	}
	first := open()
	p := &Provider{AccountID: "account-A", BaseURL: "https://api.example.test", StandardName: "anthropic"}
	key := capacityKey(p)
	// Two slots are in use and a third is refused by the service.
	var slots []*modelSlot
	for range 3 {
		slot, err := first.limits.acquire(t.Context(), key)
		if err != nil {
			t.Fatal(err)
		}
		slots = append(slots, slot)
		if len(slots) < 3 {
			slot.accepted()
		}
	}
	if !slots[2].refused(&StatusError{StatusCode: 429, Message: "Too many concurrent requests"}) {
		t.Fatal("concurrent refusal not recorded")
	}
	for _, slot := range slots {
		slot.release()
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := open()
	p.StandardName = "gemini-interactions"
	if got := reopened.limits.get(capacityKey(p)); got != 2 {
		t.Fatalf("reopened capacity=%d, want 2", got)
	}
	if err := reopened.limits.forget(p.AccountID); err != nil {
		t.Fatal(err)
	}
	if got := reopened.limits.get(key); got != 0 {
		t.Fatalf("forgotten capacity=%d", got)
	}
}
