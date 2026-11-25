package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// NativeCheckpointRequest contains the canonical conversation and the portable
// handoff instructions. The synthetic challenge contains no session content.
type NativeCheckpointRequest struct {
	Owner                                                      string
	Explicit                                                   bool
	Model                                                      string
	Messages                                                   []ChatMessage
	Tools                                                      []ToolDef
	Limit                                                      int
	Directive, Verify, ProbeSource, ProbeQuestion, ProbeAnswer string
	ValidatePortable                                           func(string) error
	Usage                                                      func(string, TokenUsage, bool)
	Stage                                                      func(string)
}

// NativeCheckpointResult distinguishes a safe portable fallback from an
// uncertain, possibly billable real request. A fallback never installs state:
// the caller writes a full portable checkpoint. Transient marks a fallback
// after an outage of this route, so the route should not write it either.
type NativeCheckpointResult struct {
	Message   ChatMessage
	Fallback  string
	Changed   bool
	Transient bool
}

func (p *Provider) nativeReady(ctx context.Context, model string) error {
	if err := context.Cause(ctx); err != nil {
		return err
	}
	if err := p.currentBinding(); err != nil {
		return errAccountChanged
	}
	if model == "" || p.Model != "" && p.Model != model {
		return errors.New("the compaction model changed")
	}
	return nil
}
func withCompactHeaders(c *Client) *Client {
	clone := *c
	source := c.headerSource
	clone.headerSource = func(ctx context.Context) (http.Header, error) {
		h, err := source(ctx)
		if err != nil {
			return nil, err
		}
		h = h.Clone()
		if !strings.Contains(","+h.Get("anthropic-beta")+",", ","+compactBeta+",") {
			if prior := h.Get("anthropic-beta"); prior != "" {
				h.Set("anthropic-beta", prior+","+compactBeta)
			} else {
				h.Set("anthropic-beta", compactBeta)
			}
		}
		return h, nil
	}
	return &clone
}
func withoutRedirects(c *Client) *Client {
	clone := *c
	hc := *c.hc
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	clone.hc = &hc
	return &clone
}
func (c *Client) checkpointClient(req *ChatRequest) *Client {
	if req == nil {
		return c
	}
	wire := nativeStandard(c.proto)
	for _, m := range req.Messages[:min(2, len(req.Messages))] {
		if nativeItem(m, wire) == nil {
			continue
		}
		c = withoutRedirects(c)
		if c.proto.Info().ID == "anthropic" {
			return withCompactHeaders(c)
		}
		return c
	}
	return c
}

// NativeCheckpoint performs lazy discovery, at most one real compaction, and
// a continuation from the exact returned artifact. There are no HTTP retries or
// redirects on either billable call. The text is portable if the route changes.
//
// A run never stops here for a refusal: when the native route cannot finish
// this checkpoint, it returns a Fallback and the caller writes a full portable
// checkpoint (Provider only with the serving model itself). Only an outage in
// Provider only is an error, so run recovery retries it like any request.
func (p *Provider) NativeCheckpoint(ctx context.Context, r NativeCheckpointRequest, opts ...ClientOption) (NativeCheckpointResult, error) {
	var zero NativeCheckpointResult
	fallback := func(why string, changed bool) (NativeCheckpointResult, error) {
		return NativeCheckpointResult{Fallback: why, Changed: changed}, nil
	}
	if err := p.nativeReady(ctx, r.Model); err != nil {
		return zero, err
	}
	if !p.UsesNativeCompaction() {
		return fallback("This connection uses Errand for compaction.", false)
	}
	if len(r.Messages) < 2 || r.Messages[0].Role != "system" || r.Limit <= 0 || r.Limit > 8192 || len(r.ProbeSource) > 512 || len(r.ProbeQuestion) > 512 || len(r.ProbeAnswer) > 96 || r.ProbeAnswer == "" || r.Directive == "" || r.Verify == "" {
		return zero, errors.New("incomplete compaction request")
	}
	proto := p.Protocol()
	codec, ok := proto.(compactWire)
	path := proto.Info().Options.NativeCompaction
	if !ok || path == "" {
		return fallback("This wire protocol has no native compaction operation.", false)
	}
	if catalogUnavailable.Load() {
		// Without the saved attempt records a lost result could be paid twice.
		return fallback("Crude cannot read its saved compaction checks.", true)
	}
	// An arbitrary extra field can replace the history, route, store policy or
	// output limit. Only carry fields for which this operation has a contract.
	for key := range p.headers {
		if strings.EqualFold(key, "Idempotency-Key") || strings.EqualFold(key, "X-Idempotency-Key") {
			return fallback(nativeOptionsReason, true)
		}
	}
	for key := range p.extraBody {
		switch key {
		case "provider", "service_tier":
		default:
			return fallback(nativeOptionsReason, true)
		}
	}
	// A streamed body carries every delta; the window itself stays bounded.
	opts = append(opts, WithMaxAttempts(1), WithResponseLimit(16*maxNativeWindowBytes))
	c, err := p.Client(opts...)
	if err != nil {
		return zero, err
	}
	c = withoutRedirects(c)
	responses := path == "responses/compact"
	if proto.Info().ID == "anthropic" {
		c = withCompactHeaders(c)
	}
	op := compactExchange{provider: p, client: c, request: r, codec: codec, path: path}
	key := p.compactionScope(r.Model)
	cache := currentSupport()
	var flight *supportFlight
	for {
		f, owner := cache.begin(ctx, key, p.AccountID)
		if owner {
			flight = f
			break
		}
		if err := waitSupport(ctx, f); err != nil {
			return zero, err
		}
	}
	defer cache.end(key, flight)
	ctx = flight.ctx
	if err := p.nativeReady(ctx, r.Model); err != nil {
		return zero, err
	}
	record := func(state string) error {
		if err := p.nativeReady(ctx, r.Model); err != nil {
			return err
		}
		return cache.record(key, flight, p, r.Model, state, op.path)
	}
	attempt := attemptKey{scope: key, source: checkpointSource(r.Owner, r.Messages), account: p.AccountID}
	if err := cache.guardAttempt(ctx, attempt, flight, r.Explicit, false); err != nil {
		return p.earlierAttempt(err, fallback, cache.fact(key).failures == 0)
	}
	// initial is the evidence before this call; a probe below may refresh fact.
	initial := cache.fact(key)
	fact := initial
	defaultPath := op.path
	if responses && fact.method == proto.Info().ChatPath {
		op.path = fact.method
		op.codec = &triggerWire{enc: proto}
	}
	// A missing standalone operation, or an explicit refusal of the option,
	// proves that no work was done. The other Responses operation may exist.
	missing := func(err error) bool {
		var status *StatusError
		return responses && (compactRejection(err) || errors.As(err, &status) && (status.StatusCode == 404 || status.StatusCode == 405))
	}
	switchOperation := func() {
		if op.path == defaultPath {
			op.codec, op.path = &triggerWire{enc: proto}, proto.Info().ChatPath
		} else {
			op.codec, op.path = proto.(compactWire), defaultPath
		}
	}
	if p.CompactionMethod() == CompactionErrandOnly {
		return fallback("This connection uses Errand for compaction.", false)
	}
	if fact.retryAt > time.Now().Unix() || fact.fresh() && fact.state == supportUnavailable {
		if fact.state == supportUnavailable && fact.retryAt <= time.Now().Unix() {
			return fallback("This route did not support native compaction at its last check.", false)
		}
		return fallback("Native compaction could not be verified. Crude will check again later.", false)
	}
	req := &ChatRequest{Model: r.Model, Messages: FilterReplayMessages(r.Messages, p), Tools: r.Tools, MaxOutputTokens: r.Limit, ResponseContinuationScope: p.ResponseContinuationScope(r.Model)}
	// A small proven delta is safe as the first real attempt. A stale anchor
	// rejection must never trigger an automatic full-history upload.
	anchored := false
	if pl := PlanResponseContinuation(req); pl.PreviousResponseID != "" && responses {
		b, e := op.codec.compactBody(req, r.Directive, c.requestExtras)
		if e == nil {
			raw, _ := json.Marshal(b)
			anchored = len(raw) <= 32<<10
		}
	}
	// fail records the outcome once. Only a probe refusal or a repeated
	// definite failure is negative evidence; anything else is a cooldown.
	fail := func(err error, probe bool) (NativeCheckpointResult, error) {
		if ctx.Err() != nil {
			return zero, context.Cause(ctx)
		}
		if errors.Is(err, errAccountChanged) {
			return zero, err
		}
		definite := compactRejection(err) || errors.Is(err, errIgnoredCompaction) || errors.Is(err, errNoWindow) || errors.Is(err, errUnusableCheckpoint)
		state := supportCooling
		if probe && compactRejection(err) || definite && initial.failures > 0 {
			state = supportUnavailable
		}
		if e := record(state); e != nil {
			return zero, e
		}
		failure := newNativeFailure(err)
		if p.CompactionMethod() == CompactionProviderOnly && failure.cause != nil {
			return zero, failure
		}
		return NativeCheckpointResult{Fallback: failure.text, Changed: initial.failures == 0, Transient: failure.cause != nil}, nil
	}
	verified := fact.fresh() && fact.state == supportVerified && fact.failures == 0
	if !verified && (!anchored || fact.failures > 0) {
		op.stage("Checking native compaction")
		probeCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		advertised := false
		// After a failure the declaration was not enough: test the route.
		if proto.Info().ID == "anthropic" && fact.failures == 0 {
			yes, known, e := c.advertisedCompaction(probeCtx, r.Model)
			if e != nil {
				return fail(e, true)
			}
			if known && !yes {
				if e = record(supportUnavailable); e != nil {
					return zero, e
				}
				return fallback("The model does not advertise native compaction support.", true)
			}
			advertised = yes
		}
		if !advertised {
			probe := &ChatRequest{Model: r.Model, Messages: []ChatMessage{NewTextMessage("user", r.ProbeSource)}, MaxOutputTokens: min(512, r.Limit)}
			w, e := op.post(probeCtx, probe, r.ProbeSource, "compaction_probe")
			// A probe is synthetic, so a normal answer or an unusable item may
			// also try the other operation without risk to the user's data.
			if e != nil && (missing(e) || responses && (errors.Is(e, errNoWindow) || errors.Is(e, errIgnoredCompaction))) {
				switchOperation()
				w, e = op.post(probeCtx, probe, r.ProbeSource, "compaction_probe")
			}
			if e != nil {
				return fail(e, true)
			}
			text, e := op.follow(probeCtx, w, r.ProbeQuestion, min(96, r.Limit), "compaction_probe_replay")
			if e != nil {
				return fail(e, true)
			}
			if text != r.ProbeAnswer {
				return fail(errIgnoredCompaction, true)
			}
			if e = record(supportVerified); e != nil {
				return zero, e
			}
			verified = true
			fact = cache.fact(key)
		}
	}
	op.stage("Compacting with the provider")
	if err := cache.guardAttempt(ctx, attempt, flight, r.Explicit, true); err != nil {
		return p.earlierAttempt(err, fallback, cache.fact(key).failures == 0)
	}
	w, err := op.post(ctx, req, r.Directive, "native_compaction")
	if err != nil && !verified && missing(err) {
		// An unverified anchored attempt reached a missing operation; resend
		// the same small delta to the other one.
		switchOperation()
		w, err = op.post(ctx, req, r.Directive, "native_compaction")
	}
	if err != nil {
		var rejection *StatusError
		if errors.As(err, &rejection) && definiteRefusal(rejection.StatusCode) {
			if e := cache.releaseRejected(attempt, flight); e != nil {
				return zero, e
			}
		}
		return fail(err, false)
	}
	text := w.text
	if text == "" || !verified || r.ValidatePortable != nil && r.ValidatePortable(text) != nil {
		op.stage("Reading the provider checkpoint")
		text, err = op.follow(ctx, w, r.Verify, r.Limit, "native_checkpoint_replay")
		if err != nil {
			return fail(err, false)
		}
	}
	if r.ValidatePortable != nil {
		if err := r.ValidatePortable(text); err != nil {
			return fail(fmt.Errorf("%w: %w", errUnusableCheckpoint, err), false)
		}
	}
	if EstimateTokens(text, r.Model) > r.Limit {
		return fail(errUnusableCheckpoint, false)
	}
	if err = record(supportVerified); err != nil {
		return zero, err
	}
	return NativeCheckpointResult{Message: nativeMessage(p, r.Model, w, text, r.Messages[0])}, nil
}

// earlierAttempt keeps a possibly paid request from being sent again
// automatically; a portable checkpoint is written instead. A recorded failure
// already told the user; a marker left by a stopped core did not.
func (p *Provider) earlierAttempt(err error, fallback func(string, bool) (NativeCheckpointResult, error), unreported bool) (NativeCheckpointResult, error) {
	var earlier *earlierAttemptError
	if !errors.As(err, &earlier) {
		return NativeCheckpointResult{}, err
	}
	return fallback("An earlier native compaction request for this conversation did not finish.", unreported)
}

func (c *Client) advertisedCompaction(ctx context.Context, model string) (bool, bool, error) {
	if strings.ContainsAny(model, "/\\?#%") || model == "." || model == ".." {
		return false, false, nil
	}
	resp, err := c.do(ctx, http.MethodGet, joinPath(c.base, "models/"+model), nil)
	if err != nil {
		return false, false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 || resp.StatusCode == 405 {
		return false, false, nil
	}
	if resp.StatusCode/100 != 2 {
		return false, false, readStatusError(resp, 8192)
	}
	raw, err := readBounded(resp.Body, 64<<10)
	if err != nil {
		return false, false, err
	}
	var m struct {
		ID           string
		Capabilities struct {
			Compaction *struct {
				Supported *bool
				Summarize struct{ Supported *bool }
			}
		}
	}
	if json.Unmarshal(raw, &m) != nil || m.ID != model || m.Capabilities.Compaction == nil {
		return false, false, nil
	}
	v := m.Capabilities.Compaction
	if v.Supported != nil && !*v.Supported {
		return false, true, nil
	}
	if v.Summarize.Supported != nil {
		return *v.Summarize.Supported, true, nil
	}
	return false, false, nil
}

// A gateway timeout, conflict, or nonstandard 4xx may follow accepted work.
func definiteRefusal(code int) bool {
	switch code {
	case http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusMethodNotAllowed, http.StatusRequestEntityTooLarge, http.StatusUnsupportedMediaType, http.StatusUnprocessableEntity, http.StatusTooManyRequests:
		return true
	}
	return false
}
