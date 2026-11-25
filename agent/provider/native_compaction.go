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
	InputTokens                                                int
	Boundary                                                   BoundaryTurn
	Receipt                                                    *ThresholdReceipt
	Owner                                                      string
	Explicit                                                   bool
	Model                                                      string
	Messages                                                   []ChatMessage
	Tools                                                      []ToolDef
	Limit                                                      int
	Directive, Verify, ProbeSource, ProbeQuestion, ProbeAnswer string
	// Shorter asks for the same checkpoint in less space. It is read back from
	// the same paid window when the first read-back stops at its output limit.
	Shorter          string
	ValidatePortable func(string) error
	Usage            func(string, TokenUsage, bool)
	Stage            func(string)
}

// NativeCheckpointResult distinguishes a safe portable fallback from an
// uncertain, possibly billable real request. A fallback never installs state:
// the caller writes a full portable checkpoint. Transient marks a fallback
// after an outage of this route, so the route should not write it either.
type NativeCheckpointResult struct {
	Turn      bool
	Receipt   *ThresholdReceipt
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
	return withBetaHeader(c, compactBeta)
}
func withBetaHeader(c *Client, beta string) *Client {
	clone := *c
	source := c.headerSource
	clone.headerSource = func(ctx context.Context) (http.Header, error) {
		h, err := source(ctx)
		if err != nil {
			return nil, err
		}
		h = h.Clone()
		if !strings.Contains(","+h.Get("anthropic-beta")+",", ","+beta+",") {
			if prior := h.Get("anthropic-beta"); prior != "" {
				h.Set("anthropic-beta", prior+","+beta)
			} else {
				h.Set("anthropic-beta", beta)
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
// A capability refusal allows a portable checkpoint. An account rejection
// returns its typed cause: the caller knows whether a distinct writer can run.
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
	if len(r.Messages) < 2 || r.Messages[0].Role != "system" || r.Limit <= 0 || r.ValidatePortable == nil || len(r.ProbeSource) > 512 || len(r.ProbeQuestion) > 512 || len(r.ProbeAnswer) > 96 || r.ProbeAnswer == "" || r.Directive == "" || r.Verify == "" {
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
	edits := p.configuredThreshold("")
	for key := range p.extraBody {
		switch key {
		case "context_management":
			if edits == nil {
				return fallback(nativeOptionsReason, true)
			}
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
	record := func(state string, cause error) error {
		if err := p.nativeReady(ctx, r.Model); err != nil {
			return err
		}
		return cache.recordWithDelay(key, flight, p, r.Model, state, op.path, AdvisedRetryDelay(cause))
	}
	attempt := attemptKey{scope: key, source: checkpointSource(r.Owner, r.Messages), account: p.AccountID}
	if err := cache.guardAttempt(ctx, attempt, flight, r.Explicit, false); err != nil {
		return p.earlierAttempt(err, fallback, cache.fact(key).failures == 0)
	}
	// initial is the evidence before this call; a probe below may refresh fact.
	initial := cache.fact(key)
	fact := initial
	defaultPath := op.path
	// The standalone operation has no context_management parameter. Do not
	// silently discard other configured edits to make that operation fit.
	alternate := responses && (edits == nil || edits.ordinary == nil)
	if responses && (!alternate || fact.method == proto.Info().ChatPath) {
		op.path = proto.Info().ChatPath
		op.codec = &triggerWire{enc: proto}
	}
	// A missing standalone operation, or an explicit refusal of the option,
	// proves that no work was done. The other Responses operation may exist.
	missing := func(err error) bool {
		var status *StatusError
		return responses && !continuationUnavailable(err) && (compactRejection(err) || errors.As(err, &status) && (status.StatusCode == 404 || status.StatusCode == 405))
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
	if fact.retryAt > time.Now().Unix() {
		if p.CompactionMethod() == CompactionProviderOnly {
			return zero, &nativeCooldown{until: time.Unix(fact.retryAt, 0)}
		}
		return NativeCheckpointResult{Fallback: "Native compaction could not be verified. Crude will check again later.", Transient: true}, nil
	}
	if fact.fresh() && fact.state == supportUnavailable && r.Receipt == nil {
		return fallback("This route did not support native compaction at its last check.", false)
	}
	req := &ChatRequest{Model: r.Model, Messages: FilterReplayMessages(r.Messages, p), Tools: r.Tools, MaxOutputTokens: r.Limit, ResponseContinuationScope: p.ResponseContinuationScope(r.Model)}
	// A small proven delta is safe as the first real attempt. A stale anchor
	// rejection must never trigger an automatic full-history upload.
	anchored := false
	if pl := PlanResponseContinuation(req); pl.PreviousResponseID != "" && alternate {
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
		if rejection := RejectedAccount(err); rejection != nil {
			return zero, rejection
		}
		definite := compactRejection(err) || errors.Is(err, errIgnoredCompaction) || errors.Is(err, errNoWindow) || errors.Is(err, errUnusableCheckpoint)
		state := supportCooling
		if probe && compactRejection(err) || definite && initial.failures > 0 {
			state = supportUnavailable
		}
		if e := record(state, err); e != nil {
			return zero, e
		}
		failure := newNativeFailure(err)
		if p.CompactionMethod() == CompactionProviderOnly && failure.cause != nil {
			return zero, failure
		}
		return NativeCheckpointResult{Fallback: failure.text, Changed: initial.failures == 0, Transient: failure.cause != nil}, nil
	}
	threshold := func() (NativeCheckpointResult, error) {
		op.path = thresholdMethod
		if r.Receipt != nil {
			if len(r.Receipt.seal) == 0 {
				return fallback("The provider returned a reply without a usable checkpoint.", true)
			}
			if r.Receipt.generation != flight.generation {
				return fallback("The compaction check changed during the request.", true)
			}
			w, e := r.Receipt.window(p, r.Messages)
			if e != nil {
				return fallback("The provider checkpoint no longer matches this conversation.", true)
			}
			if e := cache.guardAttempt(ctx, attempt, flight, false, true); e != nil {
				return p.earlierAttempt(e, fallback, false)
			}
			return op.export(ctx, w, false, fail, record)
		}
		if !fact.fresh() || fact.method != thresholdMethod {
			if e := record(supportCandidate, nil); e != nil {
				return zero, e
			}
		}
		return op.threshold(ctx, attempt, cache, flight, fail, record)
	}
	if r.Receipt != nil || fact.fresh() && fact.method == thresholdMethod {
		return threshold()
	}
	thresholdDeclared := edits != nil && edits.declared
	verified := fact.fresh() && fact.state == supportVerified && fact.failures == 0
	if !verified && (!anchored || fact.failures > 0) {
		op.stage("Checking native compaction")
		probeCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		advertised := false
		// After a failure the declaration was not enough: test the route.
		if proto.Info().ID == "anthropic" && (fact.failures == 0 || fact.method == thresholdMethod) {
			decl, e := c.advertisedCompaction(probeCtx, r.Model)
			thresholdDeclared = thresholdDeclared || decl.threshold
			if e != nil {
				return fail(e, true)
			}
			if decl.demand != nil && !*decl.demand {
				if thresholdDeclared {
					return threshold()
				}
				if e = record(supportUnavailable, nil); e != nil {
					return zero, e
				}
				return fallback("The model does not advertise native compaction support.", true)
			}
			advertised = decl.demand != nil && *decl.demand
		}
		if !advertised {
			probe := &ChatRequest{Model: r.Model, Messages: []ChatMessage{NewTextMessage("user", r.ProbeSource)}, MaxOutputTokens: min(512, r.Limit)}
			w, e := op.post(probeCtx, probe, r.ProbeSource, "compaction_probe")
			// A probe is synthetic, so a normal answer or an unusable item may
			// also try the other operation without risk to the user's data.
			if e != nil && alternate && RejectedAccount(e) == nil && (missing(e) || responses && (errors.Is(e, errNoWindow) || errors.Is(e, errIgnoredCompaction))) {
				switchOperation()
				w, e = op.post(probeCtx, probe, r.ProbeSource, "compaction_probe")
			}
			if e != nil {
				if thresholdDeclared && RejectedAccount(e) == nil && (missing(e) || compactRejection(e) || errors.Is(e, errNoWindow) || errors.Is(e, errIgnoredCompaction)) {
					return threshold()
				}
				return fail(e, true)
			}
			text, e := op.follow(probeCtx, w, r.ProbeQuestion, min(96, r.Limit), "compaction_probe_replay")
			if e != nil {
				if thresholdDeclared && RejectedAccount(e) == nil && compactRejection(e) {
					return threshold()
				}
				return fail(e, true)
			}
			if text != r.ProbeAnswer {
				if thresholdDeclared {
					return threshold()
				}
				return fail(errIgnoredCompaction, true)
			}
			if e = record(supportVerified, nil); e != nil {
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
	if err != nil && !verified && alternate && RejectedAccount(err) == nil && missing(err) {
		// An unverified anchored attempt reached a missing operation; resend
		// the same small delta to the other one.
		switchOperation()
		w, err = op.post(ctx, req, r.Directive, "native_compaction")
	}
	if err != nil {
		if confirmedNativeRejection(err) {
			if e := cache.releaseRejected(attempt, flight); e != nil {
				return zero, e
			}
			// A real request with a stored prefix can discover a missing
			// operation without a synthetic probe. After a definite refusal,
			// use the declared threshold on the pending turn, just as in
			// discovery. Never do this for a lost prefix or an uncertain write.
			if thresholdDeclared && RejectedAccount(err) == nil && !continuationUnavailable(err) && (missing(err) || compactRejection(err)) {
				return threshold()
			}
		}
		return fail(err, false)
	}
	return op.export(ctx, w, verified, fail, record)
}

func (x *compactExchange) export(ctx context.Context, w nativeWindow, verified bool, fail func(error, bool) (NativeCheckpointResult, error), record func(string, error) error) (NativeCheckpointResult, error) {
	r, p := x.request, x.provider
	var err error
	var zero NativeCheckpointResult
	text := w.text
	if text == "" || !verified || r.ValidatePortable(text) != nil {
		x.stage("Reading the provider checkpoint")
		text, err = x.follow(ctx, w, r.Verify, r.Limit, "native_checkpoint_replay")
		if errors.Is(err, errCheckpointOutputLimit) && r.Shorter != "" {
			// The window is already paid for; reading it again costs far less
			// than writing a portable checkpoint from the whole source.
			x.stage("Shortening the provider checkpoint")
			text, err = x.follow(ctx, w, r.Shorter, r.Limit, "native_checkpoint_replay")
		}
		if err != nil {
			return fail(err, false)
		}
	}
	if err := r.ValidatePortable(text); err != nil {
		return fail(fmt.Errorf("%w: %w", errUnusableCheckpoint, err), false)
	}
	if err = record(supportVerified, nil); err != nil {
		return zero, err
	}
	return NativeCheckpointResult{Message: nativeMessage(p, r.Model, w, text, r.Messages[0])}, nil
}

// nativeCooldown is a temporary provider-route failure, not a new HTTP
// response. Run recovery can wait for this route without resending early.
type nativeCooldown struct{ until time.Time }

func (e *nativeCooldown) Error() string {
	return "Provider compaction is not available now. Crude retries at " + e.until.Local().Format("15:04:05") + "."
}
func (*nativeCooldown) Timeout() bool   { return false }
func (*nativeCooldown) Temporary() bool { return true }

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

type compactDeclaration struct {
	demand    *bool
	threshold bool
}

func (c *Client) advertisedCompaction(ctx context.Context, model string) (compactDeclaration, error) {
	if strings.ContainsAny(model, "/\\?#%") || model == "." || model == ".." {
		return compactDeclaration{}, nil
	}
	resp, err := c.do(ctx, http.MethodGet, joinPath(c.base, "models/"+model), nil)
	if err != nil {
		return compactDeclaration{}, err
	}
	defer resp.Body.Close()
	if (resp.StatusCode == 404 || resp.StatusCode == 405) && !strings.EqualFold(resp.Header.Get("x-should-retry"), "false") {
		return compactDeclaration{}, nil
	}
	if resp.StatusCode/100 != 2 {
		return compactDeclaration{}, readStatusError(resp, 8192)
	}
	raw, err := readBounded(resp.Body, 64<<10)
	if err != nil {
		return compactDeclaration{}, err
	}
	var m struct {
		ID           string
		Capabilities struct {
			ContextManagement struct {
				Supported *bool
				Compact   struct{ Supported bool } `json:"compact_20260112"`
			} `json:"context_management"`
			Compaction *struct {
				Supported *bool
				Summarize struct{ Supported *bool }
			}
		}
	}
	if json.Unmarshal(raw, &m) != nil || m.ID != model {
		return compactDeclaration{}, nil
	}
	cm := m.Capabilities.ContextManagement
	threshold := cm.Compact.Supported && (cm.Supported == nil || *cm.Supported)
	v := m.Capabilities.Compaction
	if v == nil {
		return compactDeclaration{threshold: threshold}, nil
	}
	if v.Supported != nil && !*v.Supported {
		return compactDeclaration{demand: v.Supported, threshold: threshold}, nil
	}
	if v.Summarize.Supported != nil {
		return compactDeclaration{demand: v.Summarize.Supported, threshold: threshold}, nil
	}
	return compactDeclaration{threshold: threshold}, nil
}

func confirmedNativeRejection(err error) bool {
	var status *StatusError
	var first *firstEventRateLimit
	return errors.As(err, &first) || errors.As(err, &status) && definiteRefusal(status.StatusCode)
}

// A gateway timeout, conflict, or nonstandard 4xx may follow accepted work.
func definiteRefusal(code int) bool {
	switch code {
	case http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusMethodNotAllowed, http.StatusRequestEntityTooLarge, http.StatusUnsupportedMediaType, http.StatusUnprocessableEntity, http.StatusTooManyRequests:
		return true
	}
	return false
}
