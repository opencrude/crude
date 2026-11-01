package workspace

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"crude/document"
	"crude/fileio"
	"crude/insight/predictionbudget"
	"crude/insight/predictioncontext"
	"crude/prompt"
	"crude/provider"
	"crude/session"
	"crude/types"
)

const (
	predictionMaxViews      = 16
	predictionMaxSequences  = 128
	predictionCacheEntries  = 32
	predictionOutputBytes   = types.EditorPredictionMaxBytes
	predictionContextTokens = 2000
	predictionLifetime      = time.Minute
)

var (
	errPredictionUnavailable = errors.New("editor prediction is unavailable; review Workspace Code Intelligence and the Aeron connection")
	errPredictionSequence    = errors.New("editor prediction invocation was superseded or released")
	errPredictionLimit       = errors.New("editor prediction invocation capacity exceeded")
)

type predictionViewKey struct {
	owner document.Owner
	view  string
}

type predictionInvocation struct {
	cancel     context.CancelFunc
	done       chan struct{}
	generation uint64
}

type predictionSequence struct {
	sequence uint64
	expires  time.Time
	job      *predictionInvocation
	retired  bool
}

type predictionCached struct {
	digest, text string
	missing      []string
}

// The workspace owner supplies the exact bound choice and captured connection.
// This is not an application/session inheritance or a consent mutation API.
type predictionRuntimePolicy struct {
	settings            types.WorkspacePredictionSettings
	rootIdentity, grant string
	completer           session.EditorPredictionCompleter
}

// editorPredictionService owns invocation lifetimes and a constant-size meter.
// Canonical text/facts, account credentials and SQL remain with their existing
// owners. Construction, Request, RefreshMeter and Close run off the actor/UI.
type editorPredictionService struct {
	mu          sync.Mutex
	docs        *DocumentService
	root, owner string
	budget      *predictionbudget.Store
	policy      predictionRuntimePolicy
	generation  uint64
	closed      bool
	views       map[predictionViewKey]*predictionSequence
	cache       []predictionCached
	status      types.EditorPredictionStatus
	wakes       map[chan struct{}]bool
	jobs        sync.WaitGroup
}

func newEditorPredictionService(root string, docs *DocumentService, db *sql.DB) (*editorPredictionService, error) {
	if docs == nil {
		return nil, errPredictionUnavailable
	}
	budget, err := predictionbudget.Attach(db, time.Now)
	if err != nil {
		return nil, err
	}
	return &editorPredictionService{docs: docs, root: root, owner: "prediction-" + rand.Text(),
		budget: budget, views: make(map[predictionViewKey]*predictionSequence),
		wakes: make(map[chan struct{}]bool)}, nil
}

// Configure is memory-only and belongs on the workspace owner. A changed grant,
// connection or exact choice revokes every old request and cached candidate.
func (p *editorPredictionService) Configure(next predictionRuntimePolicy) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return
	}
	if p.generation != 0 && p.policy.settings == next.settings &&
		p.policy.rootIdentity == next.rootIdentity && p.policy.grant == next.grant &&
		p.policy.completer.ConnectionIdentity == next.completer.ConnectionIdentity {
		return
	}
	for _, view := range p.views {
		if view.job != nil {
			view.job.cancel()
		}
	}
	p.generation++
	p.policy, p.cache = next, nil
	p.status.Mode, p.status.PolicyGeneration = next.settings.Mode, p.generation
	p.status.LimitTokens = next.settings.HourlyTokenLimit
	p.status.MeasuredLatencyMS, p.status.PauseMS = 0, 0
	p.status.Ready = next.settings.Mode != types.PredictionOff && next.grant != "" &&
		next.rootIdentity != "" && types.ValidatePredictionSettings(next.settings) == nil &&
		next.completer.Current() == nil
	p.status.Reason = ""
	if next.settings.Mode != types.PredictionOff && !p.status.Ready {
		p.status.Reason = errPredictionUnavailable.Error()
	}
	p.publishLocked()
}

func (p *editorPredictionService) publishLocked() {
	p.status.Generation++
	for wake := range p.wakes {
		select {
		case wake <- struct{}{}:
		default:
		}
	}
}

func (p *editorPredictionService) Snapshot() types.EditorPredictionStatus {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.status
}

func (p *editorPredictionService) Subscribe() (<-chan struct{}, func()) {
	p.mu.Lock()
	defer p.mu.Unlock()
	wake := make(chan struct{}, 1)
	p.wakes[wake] = true
	var once sync.Once
	return wake, func() { once.Do(func() { p.mu.Lock(); delete(p.wakes, wake); p.mu.Unlock() }) }
}

func (p *editorPredictionService) publishMeter(m predictionbudget.Meter, generation uint64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || p.generation != generation {
		return
	}
	p.status.ActiveTokens, p.status.ChargedTokens = m.ActiveTokens, m.ChargedTokens
	p.status.ObservedTokens, p.status.RemainingTokens = m.ObservedTokens, min(m.RemainingTokens, p.status.LimitTokens)
	p.status.UnknownRecords = m.UnknownRecords
	p.publishLocked()
}

func (p *editorPredictionService) RefreshMeter(ctx context.Context) error {
	p.mu.Lock()
	cfg, generation := p.policy, p.generation
	closed := p.closed
	p.mu.Unlock()
	if closed {
		return context.Canceled
	}
	meter, err := p.budget.Snapshot(ctx, max(int64(1), cfg.settings.HourlyTokenLimit))
	if err == nil {
		p.publishMeter(meter, generation)
	}
	return err
}

func (p *editorPredictionService) Release(owner document.Owner, viewID string, sequence uint64) {
	if owner.ClientID == "" || owner.Epoch == 0 || !validLSPView(viewID, sequence) {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return
	}
	now := time.Now()
	for candidate, view := range p.views {
		if view.job == nil && now.After(view.expires) {
			delete(p.views, candidate)
		}
	}
	key := predictionViewKey{owner, viewID}
	view := p.views[key]
	if view == nil {
		if len(p.views) >= predictionMaxSequences {
			return // A new invocation cannot enter a full sequence table either.
		}
		view = &predictionSequence{}
		p.views[key] = view
	}
	if view.sequence > sequence {
		return
	}
	view.sequence, view.expires, view.retired = sequence, now.Add(5*time.Minute), true
	if view.job != nil {
		view.job.cancel()
	}
}

func (p *editorPredictionService) ReleaseOwner(owner document.Owner) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for key, view := range p.views {
		if key.owner == owner {
			view.retired = true
			if view.job != nil {
				view.job.cancel()
			}
		}
	}
}

func (p *editorPredictionService) Close() error {
	p.mu.Lock()
	p.closed = true
	for _, view := range p.views {
		if view.job != nil {
			view.job.cancel()
		}
	}
	p.mu.Unlock()
	p.jobs.Wait()
	// Only this joined lifetime is retired; previous-core rows remain charged
	// until their own exact owner has supplied retirement evidence.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return p.budget.RetireOwner(ctx, p.owner)
}

func (p *editorPredictionService) begin(ctx context.Context, owner document.Owner, request types.EditorPredictionRequest) (context.Context, *predictionInvocation, predictionRuntimePolicy, error) {
	key := predictionViewKey{owner, request.ViewID}
	p.mu.Lock()
	now := time.Now()
	for candidate, view := range p.views {
		if view.job == nil && now.After(view.expires) {
			delete(p.views, candidate)
		}
	}
	if p.closed {
		p.mu.Unlock()
		return nil, nil, predictionRuntimePolicy{}, context.Canceled
	}
	view := p.views[key]
	if view == nil {
		if len(p.views) >= predictionMaxSequences {
			p.mu.Unlock()
			return nil, nil, predictionRuntimePolicy{}, errPredictionLimit
		}
		view = &predictionSequence{}
		p.views[key] = view
	}
	if request.Sequence <= view.sequence {
		p.mu.Unlock()
		return nil, nil, predictionRuntimePolicy{}, errPredictionSequence
	}
	view.sequence, view.expires = request.Sequence, now.Add(5*time.Minute)
	view.retired = false
	admissionGeneration := p.generation
	// Supersession cancels immediately but never overlaps the local provider
	// lifetime. Only the newest waiter may begin after the old body has joined.
	if previous := view.job; previous != nil {
		previous.cancel()
		p.mu.Unlock()
		select {
		case <-previous.done:
		case <-ctx.Done():
			return nil, nil, predictionRuntimePolicy{}, ctx.Err()
		}
		p.mu.Lock()
	}
	if p.closed || view.sequence != request.Sequence || view.retired || p.generation != admissionGeneration {
		p.mu.Unlock()
		return nil, nil, predictionRuntimePolicy{}, errPredictionSequence
	}
	if !p.status.Ready || request.Automatic && (p.policy.settings.Mode != types.PredictionFull || p.status.PauseMS == 0) {
		p.mu.Unlock()
		return nil, nil, predictionRuntimePolicy{}, errPredictionUnavailable
	}
	if p.status.InFlight >= predictionMaxViews {
		p.mu.Unlock()
		return nil, nil, predictionRuntimePolicy{}, errPredictionLimit
	}
	jobCtx, cancel := context.WithTimeout(ctx, predictionLifetime)
	job := &predictionInvocation{cancel: cancel, done: make(chan struct{}), generation: p.generation}
	view.job = job
	p.jobs.Add(1)
	p.status.InFlight++
	p.publishLocked()
	policy := p.policy
	p.mu.Unlock()
	return jobCtx, job, policy, nil
}

func (p *editorPredictionService) finish(owner document.Owner, request types.EditorPredictionRequest, job *predictionInvocation) {
	job.cancel()
	p.mu.Lock()
	if view := p.views[predictionViewKey{owner, request.ViewID}]; view != nil && view.job == job {
		view.job = nil
	}
	p.status.InFlight--
	close(job.done)
	p.publishLocked()
	p.mu.Unlock()
	p.jobs.Done()
}

func (p *editorPredictionService) canonical(owner document.Owner, source types.InsightSource) error {
	p.docs.mu.Lock()
	defer p.docs.mu.Unlock()
	_, err := p.docs.readOnlyLSPSourceLocked(owner, source)
	return err
}

func predictionPathAllowed(root, path string) bool {
	if !documentUnder(root, path) || path == root {
		return false
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	for _, part := range strings.Split(filepath.ToSlash(strings.ToLower(rel)), "/") {
		switch part {
		case ".git", ".crude", ".ssh", ".aws", ".azure", ".kube":
			return false
		}
		if part == ".env" || strings.HasPrefix(part, ".env.") || part == "credentials" || part == "secrets" {
			return false
		}
	}
	base := strings.ToLower(filepath.Base(path))
	for _, suffix := range []string{".pem", ".key", ".p12", ".pfx", ".jks", ".keystore"} {
		if strings.HasSuffix(base, suffix) {
			return false
		}
	}
	return !strings.HasPrefix(base, "id_rsa") && !strings.HasPrefix(base, "id_ed25519")
}

func (p *editorPredictionService) fence(ctx context.Context, owner document.Owner, request types.EditorPredictionRequest, job *predictionInvocation, policy predictionRuntimePolicy) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	p.mu.Lock()
	view := p.views[predictionViewKey{owner, request.ViewID}]
	current := !p.closed && p.generation == job.generation && view != nil &&
		view.sequence == request.Sequence && view.job == job && !view.retired
	p.mu.Unlock()
	if !current {
		return errPredictionSequence
	}
	if policy.completer.Current() != nil {
		return errPredictionUnavailable
	}
	if err := p.canonical(owner, request.Source); err != nil {
		return err
	}
	identity, err := fileio.DirectoryIdentity(p.root)
	if err != nil || identity != policy.rootIdentity || !predictionPathAllowed(p.root, request.Source.Path) ||
		fileio.CheckDirectoryNoSymlinks(filepath.Dir(request.Source.Path)) != nil {
		return errors.New("prediction excludes external, sensitive or replaced workspace sources")
	}
	return nil
}

func (p *editorPredictionService) watchSource(ctx context.Context, cancel context.CancelFunc, owner document.Owner, source types.InsightSource) (func(), error) {
	wake, release, err := p.docs.SubscribeDocument(owner, source.DocumentID)
	if err != nil {
		return nil, err
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-ctx.Done():
				return
			case <-wake:
				if p.canonical(owner, source) != nil {
					cancel()
					return
				}
			}
		}
	}()
	return func() { cancel(); <-done; release() }, nil
}

func (p *editorPredictionService) Request(ctx context.Context, owner document.Owner, request types.EditorPredictionRequest) (types.EditorPredictionResult, error) {
	if ctx == nil || !validLSPView(request.ViewID, request.Sequence) ||
		owner.ClientID == "" || owner.Epoch == 0 {
		return types.EditorPredictionResult{}, document.ErrOwner
	}
	jobCtx, job, policy, err := p.begin(ctx, owner, request)
	if err != nil {
		return types.EditorPredictionResult{}, err
	}
	defer p.finish(owner, request, job)
	if request.LocalExactMatch {
		return types.EditorPredictionResult{}, nil
	}
	stop, err := p.watchSource(jobCtx, job.cancel, owner, request.Source)
	if err != nil {
		return types.EditorPredictionResult{}, err
	}
	defer stop()
	if err := p.fence(jobCtx, owner, request, job, policy); err != nil {
		return types.EditorPredictionResult{}, err
	}
	p.docs.mu.Lock()
	text, err := p.docs.readOnlyLSPSourceLocked(owner, request.Source)
	text = bytes.Clone(text)
	p.docs.mu.Unlock()
	if err != nil {
		return types.EditorPredictionResult{}, err
	}
	diagnostics, _ := p.docs.Insight.diagnostics.SnapshotPath(request.Source.Path)
	contextInput := predictioncontext.Input{Source: request.Source, Text: text, Caret: request.Caret, Diagnostics: diagnostics}
	factContext, system, input, err := buildPredictionRequest(contextInput)
	if err != nil {
		return types.EditorPredictionResult{}, err
	}
	result := types.EditorPredictionResult{Source: request.Source, ViewID: request.ViewID, Sequence: request.Sequence,
		PolicyGeneration: job.generation, Caret: request.Caret, ContextDigest: factContext.Digest, MissingInputs: slices.Clone(factContext.MissingInputs)}
	p.mu.Lock()
	for _, cached := range p.cache {
		if cached.digest == factContext.Digest {
			result.Text, result.CacheHit = cached.text, true
			break
		}
	}
	p.mu.Unlock()
	if result.CacheHit {
		if err := p.fence(jobCtx, owner, request, job, policy); err != nil {
			return types.EditorPredictionResult{}, err
		}
		return result, nil
	}
	outputLimit := policy.completer.OutputTokenLimit()
	// Charge a conservative byte-based reservation plus framing, not the
	// estimated 2,000-token context alone. Remote tokenizer/billing remains an
	// external observation; hidden provider input is not known to this client.
	inputReservation := int64(len(system) + len(input) + 512)
	if policy.settings.Protocol != types.PredictionChat {
		inputReservation = int64(len(factContext.Prefix) + len(factContext.Suffix) + 512)
	}
	ticket, meter, err := p.budget.Reserve(jobCtx, predictionbudget.Request{RequestID: "prediction-" + rand.Text(),
		Owner: p.owner, Producer: policy.completer.ConnectionIdentity, InputUpperTokens: inputReservation, OutputLimitTokens: int64(outputLimit)}, policy.settings.HourlyTokenLimit)
	p.publishMeter(meter, job.generation)
	if err != nil {
		return types.EditorPredictionResult{}, err
	}
	started := false
	defer func() {
		if !started {
			cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = p.budget.Abandon(cleanup, ticket)
		}
	}()
	if err := p.fence(jobCtx, owner, request, job, policy); err != nil {
		return types.EditorPredictionResult{}, err
	}
	meter, err = p.budget.Start(jobCtx, ticket, policy.settings.HourlyTokenLimit)
	if err != nil {
		return types.EditorPredictionResult{}, err
	}
	started = true
	p.publishMeter(meter, job.generation)
	begin := time.Now()
	answer, err := completeEditorPrediction(jobCtx, policy, factContext, system, input, outputLimit)
	latency := time.Since(begin)
	// Adapter return joins the local body, not remote inference. Unknown/incomplete
	// usage retains the full reservation, including on cancellation and errors.
	cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	settleErr := p.budget.Settle(cleanup, ticket, predictionObservedUsage(answer.Usage, answer.UsageComplete))
	if settleErr == nil {
		_ = p.RefreshMeter(cleanup)
	}
	cancel()
	if err != nil || settleErr != nil {
		// Provider errors can contain endpoints/headers; keep this projection
		// nonsecret and never make a failed literal candidate usable.
		return types.EditorPredictionResult{}, errors.New("prediction request failed or accounting could not be settled; its reservation remains protected")
	}
	if err := p.fence(jobCtx, owner, request, job, policy); err != nil {
		return types.EditorPredictionResult{}, err
	}
	if !types.ValidEditorPredictionInsertion(answer.Text) {
		return types.EditorPredictionResult{}, errors.New("prediction was not a safe single-line literal insertion")
	}
	result.Text = answer.Text
	p.mu.Lock()
	view := p.views[predictionViewKey{owner, request.ViewID}]
	if p.closed || p.generation != job.generation || jobCtx.Err() != nil || view == nil ||
		view.sequence != request.Sequence || view.job != job || view.retired {
		p.mu.Unlock()
		return types.EditorPredictionResult{}, errPredictionSequence
	}
	p.cache = append(p.cache, predictionCached{factContext.Digest, answer.Text, slices.Clone(factContext.MissingInputs)})
	if len(p.cache) > predictionCacheEntries {
		p.cache = slices.Clone(p.cache[len(p.cache)-predictionCacheEntries:])
	}
	ms := max(int64(1), latency.Milliseconds())
	if p.status.MeasuredLatencyMS == 0 {
		p.status.MeasuredLatencyMS = ms
	} else {
		p.status.MeasuredLatencyMS += (ms - p.status.MeasuredLatencyMS) / 4
	}
	p.status.PauseMS = max(int64(16), min(int64(2000), p.status.MeasuredLatencyMS))
	p.publishLocked()
	p.mu.Unlock()
	return result, nil
}

func predictionObservedUsage(usage *provider.TokenUsage, complete bool) predictionbudget.Usage {
	if usage == nil {
		return predictionbudget.Usage{}
	}
	input, output := int64(usage.PromptTokens), int64(usage.CompletionTokens)
	if input < 0 || output < 0 || input > predictionbudget.MaxUsageTokens-output {
		return predictionbudget.Usage{}
	}
	return predictionbudget.Usage{Reported: true, Complete: complete, Tokens: input + output}
}

func completeEditorPrediction(ctx context.Context, policy predictionRuntimePolicy, facts predictioncontext.Context, system, input string, limit int) (provider.TextOnceResult, error) {
	if policy.settings.Protocol == types.PredictionChat {
		return policy.completer.CompleteText(ctx, provider.TextOnceRequest{Model: policy.completer.Model,
			System: system, Input: input, MaxOutputTokens: limit, MaxOutputBytes: predictionOutputBytes})
	}
	answer, err := policy.completer.CompleteFIM(ctx, provider.FIMOnceRequest{Model: policy.completer.Model,
		Dialect: provider.FIMDialect(policy.settings.Protocol), EndpointPath: policy.settings.EndpointPath,
		Prefix: facts.Prefix, Suffix: facts.Suffix, MaxOutputTokens: limit, MaxOutputBytes: predictionOutputBytes})
	return provider.TextOnceResult{Text: answer.Text, Usage: answer.Usage, UsageComplete: answer.UsageComplete,
		Accepted: answer.Accepted, FinishReason: answer.FinishReason}, err
}

func buildPredictionRequest(in predictioncontext.Input) (predictioncontext.Context, string, string, error) {
	system := prompt.EditorPredictionSystem()
	// The COMPLETE escaped chat request (including instructions and framing),
	// not just the facts helper, must fit the context estimate.
	for budget := predictionContextTokens; budget > 0; budget -= 64 {
		facts, err := predictioncontext.Build(in, budget)
		if err != nil {
			return predictioncontext.Context{}, "", "", err
		}
		// Provider input does not need native absolute paths, canonical document
		// IDs, account identities or producer executable locations. Those remain
		// in the local digest/fences, not the model's fact envelope.
		var diagnostics []types.LSPDiagnostic
		if facts.Facts != nil {
			diagnostics = facts.Facts.Diagnostics
		}
		modelFacts, err := json.Marshal(struct {
			Builder     string                     `json:"builder"`
			Coverage    predictioncontext.Coverage `json:"coverage"`
			Missing     []string                   `json:"missing_inputs"`
			Diagnostics []types.LSPDiagnostic      `json:"diagnostics"`
		}{facts.BuilderVersion, facts.Coverage, facts.MissingInputs, diagnostics})
		if err != nil {
			return predictioncontext.Context{}, "", "", err
		}
		input := prompt.EditorPredictionRequest(facts.Language, facts.Prefix, facts.Suffix, string(modelFacts))
		if provider.EstimateTokens(system, "")+provider.EstimateTokens(input, "") <= predictionContextTokens {
			return facts, system, input, nil
		}
	}
	return predictioncontext.Context{}, "", "", predictioncontext.ErrBudget
}
