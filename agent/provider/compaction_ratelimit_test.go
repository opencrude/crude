package provider

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"crude/internal/providersim"
)

func nativeRateFixture(t *testing.T, wire providersim.Wire, mode string) (*providersim.Server, *Provider, NativeCheckpointRequest) {
	t.Helper()
	standard, model := "openai-responses", "fixture-model"
	switch wire {
	case providersim.Messages:
		standard, model = "anthropic", "claude-sonnet-4-6"
	case providersim.Codex:
		standard = "codex"
	}
	// Exercise discovery on a custom connection that serves the model but
	// has no model-detail declaration. An advertised Messages model skips
	// the synthetic probe, which is a different route from this fixture.
	sim := providersim.New(t, providersim.WithModelCatalog(providersim.Messages))
	p := &Provider{ID: "custom", AccountID: t.Name(), StandardName: standard, Model: model, APIKey: "fixture", BaseURL: sim.BaseURL(wire)}
	p.SetProtocolSettings(&ProtocolSettings{Compaction: mode})
	req := compactFixture(p)
	req.Messages = append(req.Messages, NewTextMessage("user", "Continue the work."))
	req.ValidatePortable = func(text string) error {
		if text != "verified summary" {
			return fmt.Errorf("wrong checkpoint: %q", text)
		}
		return nil
	}
	t.Cleanup(func() {
		if err := currentSupport().clearAccount(p.AccountID, true); err != nil {
			t.Error(err)
		}
	})
	return sim, p, req
}

// Move just the route cooldown into the past. The source, verdict and paid
// request marker must survive into the next eligible automatic compaction.
func expireNativeCooldown(p *Provider) {
	c, key := currentSupport(), p.compactionScope(p.Model)
	c.mu.Lock()
	defer c.mu.Unlock()
	f := c.facts[key]
	f.retryAt = 0
	c.facts[key] = f
}

func TestNativeRateLimitRecovery(t *testing.T) {
	for _, tc := range []struct {
		wire   providersim.Wire
		phase  string
		stream bool
	}{
		{providersim.Responses, "probe", false},
		{providersim.Messages, "probe", false},
		{providersim.Responses, "write", false},
		{providersim.Messages, "write", false},
		// Messages native operations and the Responses standalone POST are
		// non-streaming. Responses streams the probe read-back, and the
		// Codex Responses trigger streams the actual write as well.
		{providersim.Responses, "probe_read", true},
		{providersim.Codex, "write", true},
	} {
		t.Run(string(tc.wire)+"/"+tc.phase, func(t *testing.T) {
			sim, p, req := nativeRateFixture(t, tc.wire, CompactionProviderOnly)
			probe := providersim.Text(req.ProbeAnswer)
			summary := providersim.Text("verified summary")
			fault := providersim.HTTPError(providersim.RateLimit).WithRetryHint(providersim.RetryAfterSeconds(420))
			if tc.stream {
				fault = providersim.StreamError(0, providersim.RateLimit)
			}
			if tc.phase != "probe" {
				sim.Script(probe)
			}
			if tc.phase == "write" {
				sim.Script(probe)
			}
			sim.Script(fault)
			start := time.Now()
			got, err := p.NativeCheckpoint(t.Context(), req)
			if err == nil || Classify(err) != FailureTransient || got.Fallback != "" {
				t.Errorf("Provider only lost the throttling cause: fallback=%q err=%v", got.Fallback, err)
			}
			if tc.stream {
				var api *APIError
				if !errors.As(err, &api) || api.Code != "rate_limit_exceeded" {
					t.Errorf("stream rate-limit code lost: %v", err)
				}
			} else {
				var status *StatusError
				if !errors.As(err, &status) || status.StatusCode != 429 || AdvisedRetryDelay(err) != 5*time.Minute {
					t.Errorf("missing capped 429 advice: %v (%v)", err, AdvisedRetryDelay(err))
				}
			}
			f := currentSupport().fact(p.compactionScope(req.Model))
			rows := CompactionObservations(map[string]Account{p.AccountID: {}})
			if len(rows) != 1 || rows[0].Status != supportCooling || f.state == supportUnavailable {
				t.Errorf("throttling changed support: verdict=%q observations=%v", f.state, rows)
			}
			if !tc.stream && (f.retryAt < start.Add(5*time.Minute).Unix() || f.retryAt > time.Now().Add(5*time.Minute).Unix()) {
				t.Errorf("route cooldown is %ds; server advised 420s, capped at 300s", f.retryAt-start.Unix())
			}
			before := len(sim.Requests())
			until := time.Unix(f.retryAt, 0)
			maxWait := time.Until(until)
			if _, err := p.NativeCheckpoint(t.Context(), req); len(sim.Requests()) != before || err == nil || Classify(err) != FailureTransient {
				t.Errorf("Provider only did not hold the source for recovery during cooldown: err=%v", err)
			} else if wait := AdvisedRetryDelay(err); wait < time.Until(until) || wait > maxWait {
				t.Errorf("recovery was advised %v, not the remaining cooldown of %v", wait, time.Until(until))
			}
			p.SetProtocolSettings(nil)
			auto, err := p.NativeCheckpoint(t.Context(), req)
			if err != nil || auto.Fallback == "" || !auto.Transient || auto.Changed || len(sim.Requests()) != before {
				t.Errorf("Auto did not quietly select the other writer during cooldown: fallback=%q transient=%v err=%v", auto.Fallback, auto.Transient, err)
			}
			p.SetProtocolSettings(&ProtocolSettings{Compaction: CompactionProviderOnly})
			expireNativeCooldown(p)
			sim.Script(probe, probe, summary, summary)
			got, err = p.NativeCheckpoint(t.Context(), req)
			if err != nil || got.Fallback != "" || got.Message.ContentString() != "verified summary" || len(got.Message.ProviderItems) == 0 {
				t.Fatalf("next eligible compaction did not install the native checkpoint: %q, %v", got.Fallback, err)
			}
			real := 0
			reqs := sim.Requests()
			for _, sent := range reqs {
				if bytes.Contains(sent.Raw, []byte("real-history-secret")) {
					real++
				}
			}
			want := 1
			if tc.phase == "write" {
				want = 2
			}
			if real != want || !bytes.Contains(reqs[before].Raw, []byte(req.ProbeSource)) {
				t.Errorf("wanted a synthetic recheck and %d real submissions total, got %d; first retry: %s", want, real, reqs[before].Raw)
			}
		})
	}
}

func TestNativeRateLimitKeepsUnconfirmedSubmission(t *testing.T) {
	for _, tc := range []struct {
		name     string
		wire     providersim.Wire
		fault    providersim.Turn
		paidRead bool
	}{
		{"http_503", providersim.Responses, providersim.HTTPError(providersim.Overload), false},
		{"started_trigger", providersim.Codex, providersim.StreamError(3, providersim.RateLimit), false},
		{"paid_window_read", providersim.Responses, providersim.StreamError(0, providersim.RateLimit), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sim, p, req := nativeRateFixture(t, tc.wire, "")
			probe := providersim.Text(req.ProbeAnswer)
			sim.Script(probe, probe)
			if tc.paidRead {
				sim.Script(providersim.Turn{})
			}
			sim.Script(tc.fault)
			r, err := p.NativeCheckpoint(t.Context(), req)
			if err != nil || r.Fallback == "" || !r.Transient || !r.Changed {
				t.Errorf("Auto did not request a portable fallback for an outage: %+v err=%v", r, err)
			}
			before := len(sim.Requests())
			expireNativeCooldown(p)
			r, err = p.NativeCheckpoint(t.Context(), req)
			if err != nil || r.Fallback == "" || len(sim.Requests()) != before || r.Changed {
				t.Errorf("Auto resubmitted or renotified a possibly paid source: %+v err=%v calls=%d->%d", r, err, before, len(sim.Requests()))
			}
		})
	}
}

func TestNativeRateLimitPermanentEvidence(t *testing.T) {
	for _, failure := range []string{"daily_quota", "http_veto", "stream_veto"} {
		t.Run(failure, func(t *testing.T) {
			sim, p, req := nativeRateFixture(t, providersim.Responses, CompactionProviderOnly)
			// A provider's daily quota has a retry window over an hour. An
			// explicit no-retry header also outranks an otherwise short 429.
			wantRequests := 1
			switch failure {
			case "daily_quota":
				sim.Script(providersim.HTTPError(providersim.RateLimit).WithRetryHint(providersim.RetryAfterSeconds(7200)))
			case "http_veto":
				sim.Script(providersim.HTTPError(providersim.RateLimit).WithRetryHint(providersim.RetryAfterSeconds(2)))
			case "stream_veto":
				sim.Script(providersim.Turn{}, providersim.StreamError(0, providersim.RateLimit))
				wantRequests = 2
			}
			client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				resp, err := http.DefaultTransport.RoundTrip(r)
				if err == nil && failure != "daily_quota" {
					resp.Header.Set("x-should-retry", "false")
				}
				return resp, err
			})}
			_, err := p.NativeCheckpoint(t.Context(), req, WithHTTPClient(client))
			if err == nil || Classify(err) != FailurePermanent || len(sim.Requests()) != wantRequests {
				t.Fatalf("permanent 429 became a retry: %v, requests=%d", err, len(sim.Requests()))
			}
		})
	}
}
