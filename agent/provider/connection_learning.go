package provider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"crude/internal/logger"
	"crude/internal/retry"
)

// SameConnection compares replay identities. An opaque checkpoint must never
// cross wires. This is intentionally NOT the provider's concurrency identity.
func SameConnection(a, b *Provider) bool {
	return a != nil && b != nil && a.ReplayScope() == b.ReplayScope() && a.AccountID == b.AccountID
}

// capacityKey is shared across models and wires of the same account and
// configured endpoint. Switching encoders does not buy another request slot.
// No credentials, raw URL or Provider pointer is retained in the gate.
func capacityKey(p *Provider) learnedLimitKey {
	endpoint := sha256.Sum256([]byte(strings.TrimRight(strings.TrimSpace(p.BaseURL), "/")))
	return learnedLimitKey{scope: hex.EncodeToString(endpoint[:]), account: p.AccountID, kind: LearnedConcurrency}
}

// ConcurrencyLimited distinguishes a concurrency refusal from a quota, retry
// veto, API outage, or generic per-minute rate limit. Status alone cannot
// distinguish them. It consumes normalized evidence, not a wire envelope.
func ConcurrencyLimited(err error) bool {
	if err == nil || RejectedAccount(err) != nil || Permanent(err) {
		return false
	}
	_, text, _, ok := rejectionText(err)
	return ok && containsAny(text, []string{"concurren", "too many simultaneous", "too many in-flight", "too many in flight", "parallel request limit"})
}

// ConcurrencyAdjusted reports a definite pre-output refusal that tightened
// admission. Only a caller that owns replay can retry that request.
func ConcurrencyAdjusted(err error) bool {
	var httpErr *StatusError
	var streamErr *APIError
	return errors.As(err, &httpErr) && httpErr.capacityAdjusted || errors.As(err, &streamErr) && streamErr.capacityAdjusted
}

// SerializesWith reports a learned one-request limit shared by two connections.
func SerializesWith(writer, serving *Provider) bool {
	if writer == nil || serving == nil {
		return false
	}
	key := capacityKey(writer)
	return key == capacityKey(serving) && activeLearnedLimits().get(key) == 1
}

type concurrencyGate struct {
	mu sync.Mutex
	// Start with one request. Successful admission earns the next place;
	// do not probe a cold endpoint with every part of a long checkpoint.
	width, running int
	releases       uint64 // a departure during a probe invalidates its overlap evidence
	changed        chan struct{}
	notBefore      time.Time
}

func (s *learnedLimitStore) gate(key learnedLimitKey) *concurrencyGate {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.gates == nil {
		s.gates = make(map[learnedLimitKey]*concurrencyGate)
	}
	g := s.gates[key]
	if g == nil {
		g = &concurrencyGate{width: 1, changed: make(chan struct{})}
		s.gates[key] = g
	}
	return g
}
func (g *concurrencyGate) wake() { close(g.changed); g.changed = make(chan struct{}) }

type modelSlot struct {
	gate     *concurrencyGate
	key      learnedLimitKey
	store    *learnedLimitStore
	width    int
	releases uint64
	once     sync.Once
}

func (s *learnedLimitStore) acquire(ctx context.Context, key learnedLimitKey) (*modelSlot, error) {
	g := s.gate(key)
	g.mu.Lock()
	defer g.mu.Unlock()
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if delay := time.Until(g.notBefore); delay > 0 {
			g.mu.Unlock()
			err := retry.Wait(ctx, delay)
			g.mu.Lock()
			if err != nil {
				return nil, err
			}
			continue
		}
		limit := s.get(key)
		width := g.width
		if limit > 0 {
			width = limit
		}
		if g.running < width {
			g.running++
			return &modelSlot{gate: g, key: key, store: s, width: g.running, releases: g.releases}, nil
		}
		wake := g.changed
		g.mu.Unlock()
		select {
		case <-ctx.Done():
		case <-wake:
		}
		g.mu.Lock()
	}
}
func (s *modelSlot) accepted() {
	g := s.gate
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.width == s.width && g.releases == s.releases {
		g.width++
		g.wake()
	}
}
func (s *modelSlot) release() {
	s.once.Do(func() {
		g := s.gate
		g.mu.Lock()
		defer g.mu.Unlock()
		g.running--
		g.releases++
		g.wake()
	})
}
func (s *modelSlot) refused(err error) bool {
	if !ConcurrencyLimited(err) {
		return false
	}
	g := s.gate
	g.mu.Lock()
	defer g.mu.Unlock()
	// Local overlap is an upper bound, not a measurement of the quota. A
	// single pending request supplies no evidence for a lower local limit.
	if until := time.Now().Add(AdvisedRetryDelay(err)); until.After(g.notBefore) {
		g.notBefore = until
	}
	bound := max(s.width, g.running) - 1
	if bound < 1 {
		return false
	}
	old := s.store.get(s.key)
	if old > 0 && old <= bound {
		return false
	}
	if e := s.store.record(s.key, bound, InferredLimitLifetime); e != nil {
		logIf(func(l *logger.Logger) { l.Warn("PROVIDER", "capacity_not_saved", "error", e.Error()) })
	}
	g.wake()
	return true
}

type modelResponseBody struct {
	io.ReadCloser
	slot *modelSlot
}

func (b *modelResponseBody) Close() error { defer b.slot.release(); return b.ReadCloser.Close() }

// modelPost admits all configured model calls BEFORE starting the transport
// watchdog and holds the place until the HTTP body closes, including streams.
// It never replays a POST. Listing models does not occupy an inference slot.
func (c *Client) modelPost(ctx context.Context, endpoint *url.URL, body []byte) (*http.Response, error) {
	if c.capacity.kind == "" {
		return c.do(ctx, http.MethodPost, endpoint, body)
	}
	slot, err := activeLearnedLimits().acquire(ctx, c.capacity)
	if err != nil {
		return nil, err
	}
	resp, err := c.do(ctx, http.MethodPost, endpoint, body)
	if err != nil {
		slot.release()
		return nil, err
	}
	if resp.StatusCode/100 == 2 {
		slot.accepted()
	}
	resp.Body = &modelResponseBody{resp.Body, slot}
	return resp, nil
}
