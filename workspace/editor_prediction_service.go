package workspace

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"crude/document"
	"crude/fileio"
	"crude/insight/catalog"
	"crude/insight/predictionbudget"
	"crude/insight/predictioncontext"
	"crude/insight/textindex"
	"crude/prompt"
	"crude/provider"
	"crude/session"
	"crude/types"
)

const (
	predictionMaxViews      = 16
	predictionMaxSequences  = 128
	predictionCacheEntries  = 32
	predictionContextTokens = 2400
	predictionLifetime      = time.Minute
)

var (
	errPredictionOff      = errors.New("suggestions are off in this workspace")
	errPredictionSequence = errors.New("a newer suggestion request replaced this one")
	errPredictionLimit    = errors.New("too many suggestion requests at once")
	errPredictionCapped   = errors.New("suggestions reached this workspace's hourly limit")
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
	// chain holds the remaining changes of the last answer. After the user
	// accepts one change, the next one shows at once without a request.
	chain *predictionChain
}

type predictionChain struct {
	document string
	expect   [32]byte // hash of the text after the shown change
	hunks    []suggestionHunk
}

type predictionCached struct {
	digest string
	hunks  []suggestionHunk // relative to the region start
}

// predictionRuntimePolicy is the workspace's consent and the bound connection.
// Consented names the connection identity that the workspace allowed.
type predictionRuntimePolicy struct {
	settings            types.WorkspacePredictionSettings
	rootIdentity, grant string
	consented           string
	completer           session.EditorPredictionCompleter
	completerErr        error
}

// editorPredictionService owns suggestion requests: sequence fencing per
// editor view, the response cache, the hourly ledger and a constant-size
// status. Canonical text, facts, edit history, the text index and account
// credentials stay with their owners. Requests run off the actor and UI.
type editorPredictionService struct {
	mu         sync.Mutex
	docs       *DocumentService
	index      *textindex.Index
	root       string
	owner      string
	budget     *predictionbudget.Store
	policy     predictionRuntimePolicy
	generation uint64
	closed     bool
	views      map[predictionViewKey]*predictionSequence
	cache      []predictionCached
	status     types.EditorPredictionStatus
	jobs       sync.WaitGroup
}

func newEditorPredictionService(root string, docs *DocumentService, index *textindex.Index, db *sql.DB) (*editorPredictionService, error) {
	if docs == nil || db == nil {
		return nil, errors.New("suggestions need the workspace database")
	}
	budget, err := predictionbudget.Attach(db, time.Now)
	if err != nil {
		return nil, err
	}
	return &editorPredictionService{docs: docs, index: index, root: root, owner: "prediction-" + rand.Text(),
		budget: budget, views: make(map[predictionViewKey]*predictionSequence)}, nil
}

// Configure runs on the workspace owner and does no I/O. A changed choice,
// grant or connection revokes every running request and cached answer.
func (p *editorPredictionService) Configure(next predictionRuntimePolicy) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return
	}
	if p.generation != 0 && p.policy.settings == next.settings && p.policy.rootIdentity == next.rootIdentity &&
		p.policy.grant == next.grant && p.policy.consented == next.consented &&
		p.policy.completer.ConnectionIdentity == next.completer.ConnectionIdentity &&
		p.policy.completer.TimeoutSeconds == next.completer.TimeoutSeconds &&
		p.policy.completer.MaxOutputTokens == next.completer.MaxOutputTokens &&
		errors.Is(p.policy.completerErr, next.completerErr) {
		return
	}
	for _, view := range p.views {
		if view.job != nil {
			view.job.cancel()
		}
		view.chain = nil
	}
	p.generation++
	p.policy, p.cache = next, nil
	p.status.Mode, p.status.PolicyGeneration = next.settings.Mode, p.generation
	p.status.LimitTokens = next.settings.HourlyTokenLimit
	p.status.Model = next.completer.Model
	p.status.Ready, p.status.Reason = false, ""
	switch {
	case next.settings.Mode == types.PredictionOff:
	case next.completerErr != nil:
		p.status.Reason = sentence(next.completerErr.Error())
	case !next.completer.Bound():
		p.status.Reason = sentence(session.ErrSuggestionsUnconfigured.Error())
	case next.grant == "" || next.rootIdentity == "":
		p.status.Reason = "Turn on suggestions again to allow this workspace."
	case next.consented != next.completer.ConnectionIdentity:
		p.status.Reason = "The suggestion connection changed. Turn on suggestions again to allow the new connection."
	case types.ValidatePredictionSettings(next.settings) != nil:
		p.status.Reason = "Set a positive hourly token limit for suggestions."
	default:
		p.status.Ready = true
	}
	p.status.Generation++
}

func sentence(text string) string {
	if text == "" {
		return ""
	}
	text = strings.ToUpper(text[:1]) + text[1:]
	if !strings.HasSuffix(text, ".") {
		text += "."
	}
	return text
}

// Snapshot returns the current status.
func (p *editorPredictionService) Snapshot() types.EditorPredictionStatus {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.status
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
	p.status.Generation++
}

// RefreshMeter reads the ledger off the actor.
func (p *editorPredictionService) RefreshMeter(ctx context.Context) error {
	p.mu.Lock()
	cfg, generation, closed := p.policy, p.generation, p.closed
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

// Release retires a view's sequence: the shell no longer wants an answer.
func (p *editorPredictionService) Release(owner document.Owner, viewID string, sequence uint64) {
	if owner.ClientID == "" || owner.Epoch == 0 || !validLSPView(viewID, sequence) {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return
	}
	p.pruneLocked(time.Now())
	key := predictionViewKey{owner, viewID}
	view := p.views[key]
	if view == nil {
		if len(p.views) >= predictionMaxSequences {
			return
		}
		view = &predictionSequence{}
		p.views[key] = view
	}
	if view.sequence > sequence {
		return
	}
	view.sequence, view.expires, view.retired, view.chain = sequence, time.Now().Add(5*time.Minute), true, nil
	if view.job != nil {
		view.job.cancel()
	}
}

// ReleaseOwner retires every view of a detached shell.
func (p *editorPredictionService) ReleaseOwner(owner document.Owner) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for key, view := range p.views {
		if key.owner == owner {
			view.retired, view.chain = true, nil
			if view.job != nil {
				view.job.cancel()
			}
		}
	}
}

// Close cancels and joins every request, then retires this lifetime's ledger.
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
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return p.budget.RetireOwner(ctx, p.owner)
}

func (p *editorPredictionService) pruneLocked(now time.Time) {
	for key, view := range p.views {
		if view.job == nil && now.After(view.expires) {
			delete(p.views, key)
		}
	}
}

func (p *editorPredictionService) begin(ctx context.Context, owner document.Owner, request types.EditorPredictionRequest) (context.Context, *predictionInvocation, predictionRuntimePolicy, error) {
	key := predictionViewKey{owner, request.ViewID}
	p.mu.Lock()
	p.pruneLocked(time.Now())
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
	view.sequence, view.expires, view.retired = request.Sequence, time.Now().Add(5*time.Minute), false
	admission := p.generation
	// A newer keystroke cancels the older request at once, and the newest
	// waiter begins only after the old request has joined.
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
	if p.closed || view.sequence != request.Sequence || view.retired || p.generation != admission {
		p.mu.Unlock()
		return nil, nil, predictionRuntimePolicy{}, errPredictionSequence
	}
	if !p.status.Ready {
		reason := p.status.Reason
		p.mu.Unlock()
		if reason == "" {
			return nil, nil, predictionRuntimePolicy{}, errPredictionOff
		}
		return nil, nil, predictionRuntimePolicy{}, errors.New(reason)
	}
	if request.Automatic && p.policy.settings.Mode != types.PredictionType {
		p.mu.Unlock()
		return nil, nil, predictionRuntimePolicy{}, errPredictionOff
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
	p.status.Generation++
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
	p.status.Generation++
	close(job.done)
	p.mu.Unlock()
	p.jobs.Done()
}

// predictionPathAllowed keeps secrets and paths outside the workspace out of
// every source: the edited file, recent edits and related snippets.
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

// current reports whether the invocation may still publish.
func (p *editorPredictionService) current(owner document.Owner, request types.EditorPredictionRequest, job *predictionInvocation) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	view := p.views[predictionViewKey{owner, request.ViewID}]
	return !p.closed && p.generation == job.generation && view != nil &&
		view.sequence == request.Sequence && view.job == job && !view.retired
}

func (p *editorPredictionService) fence(ctx context.Context, owner document.Owner, request types.EditorPredictionRequest, job *predictionInvocation, policy predictionRuntimePolicy) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !p.current(owner, request, job) {
		return errPredictionSequence
	}
	if err := policy.completer.Current(); err != nil {
		return errors.New(sentence(err.Error()))
	}
	p.docs.mu.Lock()
	_, err := p.docs.readOnlyLSPSourceLocked(owner, request.Source)
	p.docs.mu.Unlock()
	if err != nil {
		return err
	}
	identity, err := fileio.DirectoryIdentity(p.root)
	if err != nil || identity != policy.rootIdentity || !predictionPathAllowed(p.root, request.Source.Path) ||
		fileio.CheckDirectoryNoSymlinks(filepath.Dir(request.Source.Path)) != nil {
		return errors.New("suggestions are not sent for this file")
	}
	return nil
}

// watchSource cancels a request when its canonical source changes or closes.
// It joins the watcher before returning, including on provider failure.
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
				p.docs.mu.Lock()
				_, err := p.docs.readOnlyLSPSourceLocked(owner, source)
				p.docs.mu.Unlock()
				if err != nil {
					cancel()
					return
				}
			}
		}
	}()
	return func() { cancel(); <-done; release() }, nil
}

// Request answers one suggestion request. An empty result means "nothing to
// show"; errors are written for the user.
func (p *editorPredictionService) Request(ctx context.Context, owner document.Owner, request types.EditorPredictionRequest) (types.EditorPredictionResult, error) {
	began := time.Now()
	if ctx == nil || !validLSPView(request.ViewID, request.Sequence) || owner.ClientID == "" || owner.Epoch == 0 {
		return types.EditorPredictionResult{}, document.ErrOwner
	}
	jobCtx, job, policy, err := p.begin(ctx, owner, request)
	if err != nil {
		return types.EditorPredictionResult{}, err
	}
	defer p.finish(owner, request, job)
	stop, err := p.watchSource(jobCtx, job.cancel, owner, request.Source)
	if err != nil {
		return types.EditorPredictionResult{}, err
	}
	defer stop()
	if err := p.fence(jobCtx, owner, request, job, policy); err != nil {
		return types.EditorPredictionResult{}, err
	}
	result := types.EditorPredictionResult{Source: request.Source, ViewID: request.ViewID, Sequence: request.Sequence,
		PolicyGeneration: job.generation, Caret: request.Caret}

	// Context: everything comes from memory and nothing waits.
	contextBegan := time.Now()
	p.docs.mu.Lock()
	text, err := p.docs.readOnlyLSPSourceLocked(owner, request.Source)
	text = bytes.Clone(text)
	var edits []types.RecentEdit
	if err == nil {
		edits = p.docs.recentEditsLocked(owner, 16, "")
	}
	p.docs.mu.Unlock()
	if err != nil {
		return types.EditorPredictionResult{}, err
	}
	if request.Caret < 0 || request.Caret > len(text) {
		return types.EditorPredictionResult{}, errors.New("the caret is outside the document")
	}
	rel := filepath.ToSlash(request.Source.Path)
	if r, err := filepath.Rel(p.root, request.Source.Path); err == nil {
		rel = filepath.ToSlash(r)
	}
	edits = allowedEdits(p.root, edits)
	var own []types.RecentEdit
	for _, e := range edits {
		if e.Author == types.RecentEditYou && e.Path == rel {
			own = append(own, e)
		}
	}

	// A chained change from the last answer needs no request.
	if hunk, kind, ok := p.chained(owner, request, text); ok {
		result.Kind, result.Start, result.End, result.Text, result.CacheHit = kind, hunk.start, hunk.end, hunk.text, true
		result.Sources = []string{types.PredictionSourceChain}
		return p.publish(jobCtx, owner, request, job, policy, result, began, true)
	}
	if request.Follow {
		return p.publish(jobCtx, owner, request, job, policy, result, began, true)
	}

	diagnostics, _ := p.docs.Insight.diagnostics.SnapshotPath(request.Source.Path)
	var facts []types.LSPDiagnostic
	if diagnostics.HasExactSource() && diagnostics.Boundary.Source == request.Source {
		facts = diagnostics.Diagnostics
	}
	language := ""
	if lang, ok := catalog.Current().LanguageForPath(request.Source.Path, nil); ok {
		language = lang.Name
	}
	in := predictioncontext.Input{Path: rel, Language: language, Text: text, Caret: request.Caret,
		Diagnostics: facts, RecentEdits: edits, Completions: request.Completions}
	if p.index != nil {
		block := string(text[max(0, request.Caret-1500):min(len(text), request.Caret+500)])
		for _, s := range p.index.Related(block, 4, func(path string, line int) bool {
			return path == request.Source.Path || !predictionPathAllowed(p.root, path)
		}) {
			in.Snippets = append(in.Snippets, predictioncontext.Snippet{Path: s.Rel, Line: s.Line, Text: s.Text})
		}
	}
	selected, err := predictioncontext.Build(in, predictionContextTokens)
	if err != nil {
		return types.EditorPredictionResult{}, err
	}
	if prompt.ContainsSuggestionMarker(selected.Region) {
		return p.publish(jobCtx, owner, request, job, policy, result, began, false)
	}
	result.ContextDigest = selected.Digest
	result.Sources = contextSources(selected, policy.completer.Format == types.SuggestionFormatChat)
	result.Stages.ContextUS = time.Since(contextBegan).Microseconds()

	var hunks []suggestionHunk
	p.mu.Lock()
	for _, cached := range p.cache {
		if cached.digest == selected.Digest {
			hunks = rebaseCached(cached, selected.RegionStart)
			result.CacheHit = true
			break
		}
	}
	p.mu.Unlock()
	if !result.CacheHit {
		hunks, err = p.ask(jobCtx, owner, request, job, policy, selected, &result.Stages)
		if err != nil {
			return types.EditorPredictionResult{}, err
		}
		if err := p.fence(jobCtx, owner, request, job, policy); err != nil {
			return types.EditorPredictionResult{}, err
		}
		p.remember(selected, hunks, job.generation)
	}

	checkBegan := time.Now()
	var kept []suggestionHunk
	now := time.Now()
	for _, h := range orderHunks(hunks, request.Caret) {
		if checkSuggestion(text, request.Caret, h, own) && !agentBusy(text, h, rel, edits, now) {
			kept = append(kept, h)
		}
	}
	result.Stages.CheckUS = time.Since(checkBegan).Microseconds()
	if len(kept) > 0 {
		first := kept[0]
		result.Kind = classifySuggestion(text, request.Caret, first)
		result.Start, result.End, result.Text = first.start, first.end, first.text
		p.setChain(owner, request, text, first, kept[1:])
	}
	return p.publish(jobCtx, owner, request, job, policy, result, began, result.CacheHit)
}

// contextSources names the optional sources that a built context carries.
// contextSources names the optional context that the request sends. A
// fill-in-the-middle request sends only the code around the caret.
func contextSources(c predictioncontext.Context, chat bool) []string {
	if !chat {
		return nil
	}
	var out []string
	if len(c.Edits) > 0 {
		out = append(out, types.PredictionSourceEdits)
	}
	if len(c.Diagnostics) > 0 {
		out = append(out, types.PredictionSourceDiagnostics)
	}
	if len(c.Completions) > 0 {
		out = append(out, types.PredictionSourceCompletions)
	}
	if len(c.Snippets) > 0 {
		out = append(out, types.PredictionSourceSnippets)
	}
	return out
}

func allowedEdits(root string, edits []types.RecentEdit) []types.RecentEdit {
	out := edits[:0]
	for _, e := range edits {
		if predictionPathAllowed(root, filepath.Join(root, filepath.FromSlash(e.Path))) {
			out = append(out, e)
		}
	}
	return out
}

func rebaseCached(cached predictionCached, regionStart int) []suggestionHunk {
	out := make([]suggestionHunk, len(cached.hunks))
	for i, h := range cached.hunks {
		out[i] = suggestionHunk{start: h.start + regionStart, end: h.end + regionStart, text: h.text}
	}
	return out
}

func (p *editorPredictionService) remember(selected predictioncontext.Context, hunks []suggestionHunk, generation uint64) {
	relative := make([]suggestionHunk, len(hunks))
	for i, h := range hunks {
		relative[i] = suggestionHunk{start: h.start - selected.RegionStart, end: h.end - selected.RegionStart, text: h.text}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || p.generation != generation {
		return
	}
	p.cache = append(p.cache, predictionCached{digest: selected.Digest, hunks: relative})
	if len(p.cache) > predictionCacheEntries {
		p.cache = append([]predictionCached(nil), p.cache[len(p.cache)-predictionCacheEntries:]...)
	}
}

func (p *editorPredictionService) chained(owner document.Owner, request types.EditorPredictionRequest, text []byte) (suggestionHunk, types.EditorPredictionKind, bool) {
	p.mu.Lock()
	view := p.views[predictionViewKey{owner, request.ViewID}]
	var chain *predictionChain
	if view != nil {
		chain, view.chain = view.chain, nil
	}
	p.mu.Unlock()
	if chain == nil || chain.document != request.Source.DocumentID || len(chain.hunks) == 0 || sha256.Sum256(text) != chain.expect {
		return suggestionHunk{}, "", false
	}
	first := chain.hunks[0]
	if first.end > len(text) || !types.ValidEditorPredictionText(first.text) {
		return suggestionHunk{}, "", false
	}
	p.setChain(owner, request, text, first, chain.hunks[1:])
	return first, classifySuggestion(text, request.Caret, first), true
}

func (p *editorPredictionService) setChain(owner document.Owner, request types.EditorPredictionRequest, text []byte, shown suggestionHunk, rest []suggestionHunk) {
	if len(rest) == 0 {
		return
	}
	next := &predictionChain{document: request.Source.DocumentID, expect: sha256.Sum256(applyHunk(text, shown)), hunks: shiftHunks(shown, rest)}
	p.mu.Lock()
	if view := p.views[predictionViewKey{owner, request.ViewID}]; view != nil {
		view.chain = next
	}
	p.mu.Unlock()
}

// ask sends one request through the ledger and the bound connection.
func (p *editorPredictionService) ask(ctx context.Context, owner document.Owner, request types.EditorPredictionRequest, job *predictionInvocation, policy predictionRuntimePolicy, selected predictioncontext.Context, stages *types.EditorPredictionStages) ([]suggestionHunk, error) {
	system, input := "", ""
	chat := policy.completer.Format == types.SuggestionFormatChat
	if chat {
		system = prompt.EditorSuggestionSystem()
		input = prompt.EditorSuggestionInput(promptOf(selected))
	}
	accountBegan := time.Now()
	reservation := int64(len(system) + len(input) + 512)
	if !chat {
		reservation = int64(len(selected.Prefix) + len(selected.Suffix) + 512)
	}
	ticket, meter, err := p.budget.Reserve(ctx, predictionbudget.Request{RequestID: "prediction-" + rand.Text(),
		Owner: p.owner, Producer: policy.completer.ConnectionIdentity, InputUpperTokens: reservation,
		OutputLimitTokens: int64(policy.completer.MaxOutputTokens)}, policy.settings.HourlyTokenLimit)
	p.publishMeter(meter, job.generation)
	if errors.Is(err, predictionbudget.ErrLimit) {
		return nil, errPredictionCapped
	}
	if err != nil {
		return nil, err
	}
	started := false
	defer func() {
		if !started {
			cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = p.budget.Abandon(cleanup, ticket)
		}
	}()
	if err := p.fence(ctx, owner, request, job, policy); err != nil {
		return nil, err
	}
	if meter, err = p.budget.Start(ctx, ticket, policy.settings.HourlyTokenLimit); err != nil {
		return nil, err
	}
	started = true
	stages.AccountUS = time.Since(accountBegan).Microseconds()
	p.publishMeter(meter, job.generation)

	modelBegan := time.Now()
	var answer string
	var usage *provider.TokenUsage
	complete := false
	if chat {
		answer, usage, complete, err = streamRewrite(ctx, policy.completer, system, input, func() {
			stages.FirstByteUS = time.Since(modelBegan).Microseconds()
		})
	} else {
		var fim provider.FIMOnceResult
		fim, err = policy.completer.CompleteFIM(ctx, selected.Prefix, selected.Suffix, types.EditorPredictionMaxBytes)
		answer, usage, complete = fim.Text, fim.Usage, fim.UsageComplete
		stages.FirstByteUS = time.Since(modelBegan).Microseconds()
	}
	stages.ModelUS = time.Since(modelBegan).Microseconds()
	cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	settleErr := p.budget.Settle(cleanup, ticket, predictionObservedUsage(usage, complete))
	if settleErr == nil {
		_ = p.RefreshMeter(cleanup)
	}
	cancel()
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		// Provider errors can contain endpoints or headers; show none of it.
		return nil, errors.New("the suggestion request failed; check the connection in Settings, Agents, Aeron")
	}
	if settleErr != nil {
		return nil, errors.New("suggestion usage could not be recorded")
	}
	if !chat {
		return fimHunks(selected, answer), nil
	}
	rewrite, err := parseSuggestionRewrite(answer, selected.Region)
	if err != nil {
		return nil, nil // an unusable answer shows nothing
	}
	return suggestionHunks(selected, rewrite), nil
}

func promptOf(c predictioncontext.Context) prompt.SuggestionPrompt {
	out := prompt.SuggestionPrompt{Path: c.Path, Language: c.Language, Before: c.Before, Region: c.Region,
		After: c.After, Caret: c.Caret, Problems: c.Diagnostics, Completions: c.Completions}
	for _, e := range c.Edits {
		out.Edits = append(out.Edits, prompt.SuggestionEdit{Path: e.Path, Author: e.Author, Line: e.Line, Removed: e.Removed, Inserted: e.Inserted})
	}
	for _, s := range c.Snippets {
		out.Snippets = append(out.Snippets, prompt.SuggestionSnippet{Path: s.Path, Line: s.Line, Text: s.Text})
	}
	return out
}

// fimHunks turns a fill-in-the-middle answer into an insertion at the caret.
func fimHunks(c predictioncontext.Context, answer string) []suggestionHunk {
	lines := strings.SplitAfter(answer, "\n")
	if len(lines) > types.EditorPredictionMaxLines {
		answer = strings.Join(lines[:types.EditorPredictionMaxLines], "")
	}
	answer = strings.TrimRight(answer, "\n")
	if strings.TrimSpace(answer) == "" {
		return nil
	}
	at := c.RegionStart + c.Caret
	return []suggestionHunk{{start: at, end: at, text: answer}}
}

// streamRewrite reads the chat answer. After the region's end marker the
// model has nothing more to say; the stream is read to its end only for the
// reported usage, and stops at once if text keeps coming.
func streamRewrite(ctx context.Context, completer session.EditorPredictionCompleter, system, input string, firstByte func()) (string, *provider.TokenUsage, bool, error) {
	stream, err := completer.Stream(ctx, system, input)
	if err != nil {
		return "", nil, false, err
	}
	defer stream.Close()
	var answer strings.Builder
	var usage *provider.TokenUsage
	seen, closed, after := false, false, 0
	for {
		update, err := stream.Next()
		if errors.Is(err, io.EOF) {
			return answer.String(), usage, usage != nil, nil
		}
		if err != nil {
			return answer.String(), usage, false, err
		}
		if update.Usage != nil {
			usage = update.Usage
		}
		if update.ContentDelta != "" {
			if !seen {
				seen = true
				firstByte()
			}
			if closed {
				if after += len(update.ContentDelta); after > 256 {
					return answer.String(), usage, false, nil
				}
				continue
			}
			answer.WriteString(update.ContentDelta)
			if answer.Len() > types.EditorPredictionMaxBytes*4 {
				return answer.String(), usage, false, nil
			}
			closed = strings.Contains(answer.String(), prompt.SuggestionRegionEnd)
		}
		if update.Done {
			return answer.String(), usage, usage != nil, nil
		}
	}
}

// publish fences the result and updates the status.
func (p *editorPredictionService) publish(ctx context.Context, owner document.Owner, request types.EditorPredictionRequest, job *predictionInvocation, policy predictionRuntimePolicy, result types.EditorPredictionResult, began time.Time, reused bool) (types.EditorPredictionResult, error) {
	if err := p.fence(ctx, owner, request, job, policy); err != nil {
		return types.EditorPredictionResult{}, err
	}
	total := time.Since(began)
	result.Stages.TotalUS = total.Microseconds()
	p.mu.Lock()
	defer p.mu.Unlock()
	view := p.views[predictionViewKey{owner, request.ViewID}]
	if p.closed || p.generation != job.generation || view == nil || view.sequence != request.Sequence || view.job != job || view.retired {
		return types.EditorPredictionResult{}, errPredictionSequence
	}
	observeSuggestionStages(result.Stages, total, reused)
	p.status.Requests++
	if !result.Empty() {
		p.status.Shown++
	}
	if !reused {
		ms := max(int64(1), total.Milliseconds())
		if p.status.MeasuredLatencyMS == 0 {
			p.status.MeasuredLatencyMS = ms
		} else {
			p.status.MeasuredLatencyMS += (ms - p.status.MeasuredLatencyMS) / 4
		}
	}
	p.status.Generation++
	result.Status = p.status
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
