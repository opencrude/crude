package recovery

import (
	"testing"
	"time"
)

func TestCanRetryRequiresAnUnacknowledgedIncident(t *testing.T) {
	for _, test := range []struct {
		name   string
		status Status
		want   bool
	}{
		{"no identity", Status{State: NeedsAttention}, false},
		{"attention", Status{ID: "incident", State: NeedsAttention}, true},
		{"passive interruption", Status{ID: "incident", State: Interrupted}, true},
		{"cooldown", Status{ID: "incident", State: Recovering, RetryAt: time.Now().Add(time.Minute)}, true},
		{"elapsed cooldown still needs acknowledgement", Status{ID: "incident", State: Recovering, RetryAt: time.Unix(1, 0)}, true},
		{"acknowledged active attempt", Status{ID: "incident", State: Recovering}, false},
		{"recovered", Status{ID: "incident", State: Recovered}, false},
		{"cancelled", Status{ID: "incident", State: Cancelled}, false},
		{"internal checkpoint", Status{ID: "incident"}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := test.status.CanRetry(); got != test.want {
				t.Fatalf("CanRetry() = %v, want %v", got, test.want)
			}
		})
	}
}
