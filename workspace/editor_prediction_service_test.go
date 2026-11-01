package workspace

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"crude/document"
	"crude/fileio"
	"crude/insight/predictionbudget"
	"crude/insight/predictioncontext"
	"crude/provider"
	"crude/session"
	"crude/types"
)

const predictionFixtureWire = "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\" literal\"},\"finish_reason\":\"stop\"}]}\n\n" +
	"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":7,\"completion_tokens\":2}}\n\n" +
	"data: [DONE]\n\n"

func predictionServiceFixture(t *testing.T, handler http.Handler) (*editorPredictionService, document.Owner, document.Owner, types.EditorPredictionRequest, predictionRuntimePolicy) {
	t.Helper()
	docs, snapshot, owner, peer := documentFixture(t, "source")
	db := openTestWorkspaceDB(t)
	service, err := newEditorPredictionService(docs.root, docs, db.DB)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := service.Close(); err != nil {
			t.Error(err)
		}
	})
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	account := provider.Account{ID: "prediction-fixture", ProviderID: "openai", APIKey: "fixture-only",
		Connection: provider.Template{Name: "Local fixture", ProviderURL: server.URL, APIStandard: "openai", AuthType: provider.AuthBearer}}
	accounts := map[string]provider.Account{account.ID: account}
	cfg := &session.Config{Accounts: accounts, AccountManager: provider.NewAccountManager(accounts, nil),
		Agents: session.AgentsConfig{ActiveProfile: "fixture", Profiles: map[string]session.ProfileConfig{
			"fixture": {Categories: map[string]session.CategoryConfig{
				session.CategoryAeron: {AccountID: account.ID, Model: "literal-model", ModelMaxOutput: 32},
			}},
		}}}
	completer, err := cfg.AeronPredictionCompleter()
	if err != nil {
		t.Fatal(err)
	}
	root, err := fileio.DirectoryIdentity(docs.root)
	if err != nil {
		t.Fatal(err)
	}
	policy := predictionRuntimePolicy{settings: types.WorkspacePredictionSettings{
		Mode: types.PredictionLight, HourlyTokenLimit: 100000, Protocol: types.PredictionChat},
		rootIdentity: root, grant: "fixture-grant", completer: completer}
	service.Configure(policy)
	docs.mu.Lock()
	source := docs.analysisIdentityLocked(docs.docs[snapshot.ID])
	docs.mu.Unlock()
	return service, owner, peer, types.EditorPredictionRequest{Source: source, ViewID: "editor", Sequence: 1, Caret: 6}, policy
}

func TestPredictionServiceLiteralCacheBudgetMeterAndLocalMatch(t *testing.T) {
	var calls atomic.Int32
	service, owner, _, request, _ := predictionServiceFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, predictionFixtureWire)
	}))
	first, err := service.Request(t.Context(), owner, request)
	if err != nil || first.Text != " literal" || first.CacheHit || first.Source != request.Source ||
		first.Caret != 6 || first.ContextDigest == "" || len(first.MissingInputs) == 0 {
		t.Fatal("literal invocation failed", first, err)
	}
	request.Sequence++
	cached, err := service.Request(t.Context(), owner, request)
	if err != nil || cached.Text != " literal" || !cached.CacheHit || calls.Load() != 1 {
		t.Fatal("context digest did not suppress duplicate egress", cached, err, calls.Load())
	}
	request.Sequence++
	request.LocalExactMatch = true
	suppressed, err := service.Request(t.Context(), owner, request)
	if err != nil || suppressed.Text != "" || calls.Load() != 1 {
		t.Fatal("local exact match still sent source", suppressed, err)
	}
	status := service.Snapshot()
	if status.ActiveTokens != 0 || status.ChargedTokens != 9 || status.ObservedTokens != 9 ||
		status.RemainingTokens != 99991 || status.InFlight != 0 || status.MeasuredLatencyMS <= 0 || status.PauseMS < 16 {
		t.Fatal("meter does not reflect original usage or measured pause", status)
	}
}

func TestPredictionServiceBudgetAndSensitiveSourceRefuseBeforeEgress(t *testing.T) {
	var calls atomic.Int32
	service, owner, _, request, policy := predictionServiceFixture(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	policy.settings.HourlyTokenLimit = 1
	service.Configure(policy)
	if result, err := service.Request(t.Context(), owner, request); !errors.Is(err, predictionbudget.ErrLimit) || result.Text != "" || calls.Load() != 0 {
		t.Fatal("insufficient cap admitted source egress", result, err, calls.Load())
	}
	policy.settings.HourlyTokenLimit = 100000
	service.Configure(policy)
	path := filepath.Join(service.root, ".env")
	if err := os.WriteFile(path, []byte("fake fixture secret, not a real credential"), 0600); err != nil {
		t.Fatal(err)
	}
	snapshot, err := service.docs.Open(owner, path)
	if err != nil {
		t.Fatal(err)
	}
	service.docs.mu.Lock()
	request.Source = service.docs.analysisIdentityLocked(service.docs.docs[snapshot.ID])
	service.docs.mu.Unlock()
	request.Sequence++
	request.Caret = 0
	if result, err := service.Request(t.Context(), owner, request); err == nil || result.Text != "" || calls.Load() != 0 {
		t.Fatal("sensitive canonical source admitted egress", result, err, calls.Load())
	}
	for _, path := range []string{filepath.Join(service.root, "..", "outside.go"),
		filepath.Join(service.root, ".ssh", "config"), filepath.Join(service.root, "x.pem")} {
		if predictionPathAllowed(service.root, path) {
			t.Fatal("excluded source admitted", path)
		}
	}
}

func TestPredictionServicePeerMutationCancelsAndRetainsUnknownCharge(t *testing.T) {
	entered, joined := make(chan struct{}), make(chan struct{})
	service, owner, peer, request, _ := predictionServiceFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		close(entered)
		select {
		case <-r.Context().Done():
		case <-t.Context().Done():
		}
		close(joined)
	}))
	done := make(chan error, 1)
	go func() {
		result, err := service.Request(t.Context(), owner, request)
		if result.Text != "" {
			err = errors.New("retired source returned a candidate")
		}
		done <- err
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("fixture provider was not entered")
	}
	documentEdit(t, service.docs, peer, request.Source.DocumentID, "peer-source", request.Source.Revision, 0, 0, "peer ")
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("peer source change did not retire the prediction")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("canonical mutation did not cancel and join prediction")
	}
	select {
	case <-joined:
	case <-time.After(5 * time.Second):
		t.Fatal("local HTTP body was not cancelled")
	}
	status := service.Snapshot()
	if status.ActiveTokens != 0 || status.ChargedTokens <= 0 || status.UnknownRecords != 1 || status.InFlight != 0 {
		t.Fatal("cancelled request refunded unreported usage", status)
	}
}

func TestPredictionServiceExactReleasePolicyAndNoOverlappingLocalBodies(t *testing.T) {
	entered := make(chan struct{}, 2)
	var calls atomic.Int32
	service, owner, _, request, policy := predictionServiceFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		entered <- struct{}{}
		if n == 1 {
			select {
			case <-r.Context().Done():
			case <-t.Context().Done():
			}
			return
		}
		_, _ = io.WriteString(w, predictionFixtureWire)
	}))
	done := make(chan error, 1)
	request.Sequence = 2
	go func() { _, err := service.Request(t.Context(), owner, request); done <- err }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("first request did not start")
	}
	// A stale release never cancels a newer invocation.
	service.Release(owner, request.ViewID, request.Sequence-1)
	if service.Snapshot().InFlight != 1 {
		t.Fatal("stale release disturbed current owner")
	}
	second := request
	second.Sequence++
	answer, err := service.Request(t.Context(), owner, second)
	if err != nil || answer.Text != " literal" || service.Snapshot().InFlight != 0 {
		t.Fatal("supersession did not join the prior body before the next invocation", answer, err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("superseded request succeeded")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("superseded request did not join")
	}
	policy.settings.Mode = types.PredictionOff
	service.Configure(policy)
	second.Sequence++
	if answer, err := service.Request(t.Context(), owner, second); err == nil || answer.Text != "" || calls.Load() != 2 {
		t.Fatal("revoked policy reused cache or sent source", answer, err, calls.Load())
	}
}

func TestPredictionServiceFullModeNeedsMeasuredConnectionNotSyntheticPause(t *testing.T) {
	var calls atomic.Int32
	service, owner, _, request, policy := predictionServiceFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, predictionFixtureWire)
	}))
	policy.settings.Mode = types.PredictionFull
	service.Configure(policy)
	request.Automatic = true
	if _, err := service.Request(t.Context(), owner, request); err == nil || calls.Load() != 0 {
		t.Fatal("automatic request invented an unmeasured pause")
	}
	request.Sequence++
	request.Automatic = false
	if _, err := service.Request(t.Context(), owner, request); err != nil {
		t.Fatal(err)
	}
	if service.Snapshot().PauseMS <= 0 {
		t.Fatal("explicit local fixture measurement did not tune the connection pause")
	}
}

func TestPredictionServiceRequestContextAndLiteralBounds(t *testing.T) {
	for _, text := range []string{"\n", "x\t", "x\u202e", strings.Repeat("x", 4097), string([]byte{0xff})} {
		if types.ValidEditorPredictionInsertion(text) {
			t.Fatal("unsafe multiline/control literal accepted")
		}
	}
	if !types.ValidEditorPredictionInsertion(" literal 👩‍💻") {
		t.Fatal("valid literal grapheme sequence refused")
	}
	if usage := predictionObservedUsage(nil, true); usage.Reported || usage.Complete {
		t.Fatal("missing usage became complete")
	}
	source := types.InsightSource{Kind: "canonical", WorkspaceGeneration: "generation-fixture",
		DocumentID: "/private-native-directory/source.go", Path: "/private-native-directory/source.go", Lifetime: "source-lifetime", Revision: 1}
	_, system, input, err := buildPredictionRequest(predictioncontext.Input{Source: source, Text: []byte(strings.Repeat("<&", 4096)), Caret: 4096})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(input, "private-native-directory") || strings.Contains(input, "source-lifetime") ||
		provider.EstimateTokens(system, "")+provider.EstimateTokens(input, "") > 2000 {
		t.Fatal("request leaked private source metadata or exceeded its fully escaped context bound")
	}
}

func TestPredictionServiceWaitingInvocationCannotOutliveReleaseOrPolicyGeneration(t *testing.T) {
	for _, transition := range []string{"release-before-join", "policy-change-before-join"} {
		t.Run(transition, func(t *testing.T) {
			service, owner, _, request, policy := predictionServiceFixture(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("state-machine regression must not send source") }))
			firstCtx, first, _, err := service.begin(t.Context(), owner, request)
			if err != nil {
				t.Fatal(err)
			}
			second := request
			second.Sequence++
			done := make(chan error, 1)
			go func() {
				_, job, _, err := service.begin(t.Context(), owner, second)
				if err == nil {
					service.finish(owner, second, job)
				}
				done <- err
			}()
			// Cancellation is the first invocation's authoritative notification
			// that sequence 2 has superseded it and is awaiting local join.
			select {
			case <-firstCtx.Done():
			case <-time.After(5 * time.Second):
				t.Fatal("new invocation did not cancel its predecessor")
			}
			if transition == "release-before-join" {
				service.Release(owner, second.ViewID, second.Sequence)
			} else {
				policy.grant = "new-explicit-policy-generation"
				service.Configure(policy)
			}
			service.finish(owner, request, first)
			select {
			case err := <-done:
				if !errors.Is(err, errPredictionSequence) && !errors.Is(err, context.Canceled) {
					t.Fatal("waiting invocation borrowed release or a newer grant after its original lifetime", transition, err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("retired invocation did not finish")
			}
		})
	}
}

func TestPredictionServiceReleaseBeforeAdmissionIsAuthoritative(t *testing.T) {
	var calls atomic.Int32
	service, owner, _, request, _ := predictionServiceFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, predictionFixtureWire)
	}))
	// The close command may reach the owner before a queued request's worker.
	// No cancellation relay or provider cooperation establishes this tombstone.
	service.Release(owner, request.ViewID, request.Sequence)
	result, err := service.Request(t.Context(), owner, request)
	if !errors.Is(err, errPredictionSequence) || result.Text != "" || calls.Load() != 0 {
		t.Fatal("released queued invocation still exported source or returned code", result, err, calls.Load())
	}
	request.Sequence++
	result, err = service.Request(t.Context(), owner, request)
	if err != nil || result.Text != " literal" || calls.Load() != 1 {
		t.Fatal("retired sequence poisoned a later explicit invocation", result, err, calls.Load())
	}
}
