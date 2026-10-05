// Package retry owns the cooldown contract for every Crude-managed automatic
// retry. Attempt budgets and replay safety remain with the operation owner.
package retry

import (
	"context"
	"math"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// MaxCooldown is an absolute ceiling, including server hints and settings.
// It limits each wait, not a task's lifetime or a request's attempt budget.
const MaxCooldown = 5 * time.Minute

// Clamp makes a cooldown safe before duration arithmetic or timer creation.
func Clamp(delay time.Duration) time.Duration {
	return max(0, min(delay, MaxCooldown))
}

// Exponential saturates without overflowing, even after a multi-day outage.
// Attempts are one-based. A nonpositive ceiling means the absolute ceiling.
func Exponential(attempt int, base, ceiling time.Duration) time.Duration {
	if ceiling <= 0 {
		ceiling = MaxCooldown
	}
	ceiling = Clamp(ceiling)
	delay := min(Clamp(base), ceiling)
	for remaining := max(0, attempt-1); remaining > 0 && delay > 0 && delay < ceiling; remaining-- {
		if delay >= ceiling/2 {
			return ceiling
		}
		delay *= 2
	}
	return min(delay, ceiling)
}

// Jitter keeps an exponential cooldown within 75–100% of its bounded value.
func Jitter(attempt int, base, ceiling time.Duration) time.Duration {
	delay := Exponential(attempt, base, ceiling)
	spread := delay / 4
	if spread <= 0 {
		return delay
	}
	return delay - spread + time.Duration(rand.Int64N(int64(spread)+1))
}

// ServerDelay accepts Retry-After (seconds or HTTP date) and retry-after-ms.
// Server advice supplies a minimum, but never exceeds the five-minute ceiling.
// A zero/past hint cannot remove the local backoff and create a hot retry loop.
// Invalid/nonfinite hints fall back to the already-bounded local policy.
func ServerDelay(resp *http.Response, fallback time.Duration, now time.Time) time.Duration {
	fallback = Clamp(fallback)
	if resp == nil {
		return Clamp(fallback)
	}
	for _, hint := range []struct {
		header string
		unit   time.Duration
	}{
		{"retry-after-ms", time.Millisecond},
		{"Retry-After", time.Second},
	} {
		value := strings.TrimSpace(resp.Header.Get(hint.header))
		if value == "" {
			continue
		}
		if count, err := strconv.ParseFloat(value, 64); err == nil && count >= 0 && !math.IsNaN(count) && !math.IsInf(count, 0) {
			if count >= float64(MaxCooldown)/float64(hint.unit) {
				return MaxCooldown
			}
			return max(fallback, Clamp(time.Duration(count*float64(hint.unit))))
		}
		if hint.header == "Retry-After" {
			if when, err := http.ParseTime(value); err == nil {
				return max(fallback, Clamp(when.Sub(now)))
			}
		}
	}
	return Clamp(fallback)
}

// Wait is cancellation-aware and applies the ceiling again at the timer boundary.
func Wait(ctx context.Context, delay time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	delay = Clamp(delay)
	if delay == 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return ctx.Err()
	}
}
