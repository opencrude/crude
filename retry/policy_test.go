package retry

import (
	"context"
	"errors"
	"math"
	"net/http"
	"testing"
	"time"
)

func TestCooldownNeverExceedsFiveMinutes(t *testing.T) {
	for _, attempt := range []int{0, 1, 2, 8, 32, 1000, math.MaxInt} {
		for _, base := range []time.Duration{-1, 0, time.Millisecond, time.Second, 24 * time.Hour, math.MaxInt64} {
			for _, ceiling := range []time.Duration{0, time.Millisecond, time.Minute, 24 * time.Hour, math.MaxInt64} {
				delay := Exponential(attempt, base, ceiling)
				if delay < 0 || delay > 5*time.Minute {
					t.Fatalf("attempt=%d base=%s ceiling=%s: %s", attempt, base, ceiling, delay)
				}
				jitter := Jitter(attempt, base, ceiling)
				if jitter < delay*3/4 || jitter > delay {
					t.Fatalf("jitter=%s outside bounded delay=%s", jitter, delay)
				}
			}
		}
	}
}

func TestServerHintsHaveTheSameAbsoluteCeiling(t *testing.T) {
	now := time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name, header, value string
		want                time.Duration
	}{
		{"day", "Retry-After", "86400", 5 * time.Minute},
		{"huge", "retry-after-ms", "1e300", 5 * time.Minute},
		{"date", "Retry-After", now.Add(24 * time.Hour).Format(http.TimeFormat), 5 * time.Minute},
		{"past", "Retry-After", now.Add(-time.Hour).Format(http.TimeFormat), time.Second},
		{"fraction", "retry-after-ms", "1250", 1250 * time.Millisecond},
		{"zero", "Retry-After", "0", time.Second},
		{"invalid", "Retry-After", "later", time.Second},
		{"infinity", "Retry-After", "Inf", time.Second},
		{"nan", "Retry-After", "NaN", time.Second},
		{"negative", "Retry-After", "-3", time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := &http.Response{Header: http.Header{}}
			response.Header.Set(tc.header, tc.value)
			if got := ServerDelay(response, time.Second, now); got != tc.want {
				t.Fatalf("delay=%s want=%s", got, tc.want)
			}
		})
	}
}

func TestWaitAlwaysHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, delay := range []time.Duration{0, -time.Second, 24 * time.Hour} {
		if err := Wait(ctx, delay); !errors.Is(err, context.Canceled) {
			t.Fatalf("delay=%s: %v", delay, err)
		}
	}
	ctx, cancel = context.WithCancel(context.Background())
	time.AfterFunc(time.Millisecond, cancel)
	started := time.Now()
	if err := Wait(ctx, 24*time.Hour); !errors.Is(err, context.Canceled) || time.Since(started) > time.Second {
		t.Fatalf("long cooldown is not promptly cancellable: %v", err)
	}
}
