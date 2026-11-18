package workspace

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"crude/agent/prompt"
	"crude/agent/provider"
	"crude/agent/session"
	"crude/insight/predictioncontext"
	"crude/types"
	"crude/workspace/document"
	"crude/workspace/fileio"
)

// suggestionServer answers every chat request with the rewrite that answer
// returns for the request's user message.
type suggestionServer struct {
	mu     sync.Mutex
	inputs []string
	calls  atomic.Int32
	answer func(input string) string
}

func (s *suggestionServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.calls.Add(1)
	raw, _ := io.ReadAll(r.Body)
	var body struct {
		Messages []struct {
			Role    string `json:"role"`
			Content any    `json:"content"`
		} `json:"messages"`
	}
	_ = json.Unmarshal(raw, &body)
	input := ""
	for _, m := range body.Messages {
		if m.Role == "user" {
			if text, ok := m.Content.(string); ok {
				input = text
			} else if parts, ok := m.Content.([]any); ok {
				for _, part := range parts {
					if p, ok := part.(map[string]any); ok {
						if text, ok := p["text"].(string); ok {
							input += text
						}
					}
				}
			}
		}
	}
	s.mu.Lock()
	s.inputs = append(s.inputs, input)
	s.mu.Unlock()
	content, _ := json.Marshal(s.answer(input))
	w.Header().Set("Content-Type", "text/event-stream")
	fmt.Fprintf(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":%s}}]}\n\n", content)
	fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":40,\"completion_tokens\":8}}\n\ndata: [DONE]\n\n")
}

// rewriteOf returns the editable region of input with fn applied, framed as
// the model answers.
func rewriteOf(input string, fn func(region string) string) string {
	start := strings.Index(input, prompt.SuggestionRegionStart+"\n")
	end := strings.Index(input, prompt.SuggestionRegionEnd)
	region := strings.Replace(input[start+len(prompt.SuggestionRegionStart)+1:end], prompt.SuggestionCursor, "", 1)
	return prompt.SuggestionRegionStart + "\n" + fn(region) + prompt.SuggestionRegionEnd
}

type suggestionFixture struct {
	service *editorPredictionService
	docs    *DocumentService
	owner   document.Owner
	id      string
	server  *suggestionServer
	policy  predictionRuntimePolicy
	seq     uint64
}

func newSuggestionFixture(t *testing.T, text string, answer func(string) string) *suggestionFixture {
	t.Helper()
	docs, snapshot, owner, _ := documentFixture(t, text)
	db := openTestWorkspaceDB(t)
	service, err := newEditorPredictionService(docs.root, docs, nil, db.DB)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := service.Close(); err != nil {
			t.Error(err)
		}
	})
	fake := &suggestionServer{answer: answer}
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	account := provider.Account{ID: "suggest", ProviderID: "openai", APIKey: "fixture-only",
		Connection: provider.Template{Name: "Local fixture", ProviderURL: server.URL, APIStandard: "openai", AuthType: provider.AuthBearer}}
	accounts := map[string]provider.Account{account.ID: account}
	cfg := &session.Config{Accounts: accounts, AccountManager: provider.NewAccountManager(accounts, nil),
		Agents: session.AgentsConfig{ActiveProfile: "p", Profiles: map[string]session.ProfileConfig{
			"p": {Categories: map[string]session.CategoryConfig{session.CategoryAeron: {AccountID: account.ID, Model: "fast"}}},
		}}}
	completer, err := cfg.SuggestionCompleter()
	if err != nil {
		t.Fatal(err)
	}
	root, err := fileio.DirectoryIdentity(docs.root)
	if err != nil {
		t.Fatal(err)
	}
	policy := predictionRuntimePolicy{settings: types.WorkspacePredictionSettings{Mode: types.PredictionType, HourlyTokenLimit: 100000},
		rootIdentity: root, grant: "grant", consented: completer.ConnectionIdentity, completer: completer}
	service.Configure(policy)
	return &suggestionFixture{service: service, docs: docs, owner: owner, id: snapshot.ID, server: fake, policy: policy}
}

func (f *suggestionFixture) request(t *testing.T, caret int) (types.EditorPredictionResult, error) {
	t.Helper()
	f.docs.mu.Lock()
	source := f.docs.analysisIdentityLocked(f.docs.docs[f.id])
	f.docs.mu.Unlock()
	f.seq++
	return f.service.Request(t.Context(), f.owner, types.EditorPredictionRequest{Source: source, ViewID: "editor", Sequence: f.seq, Caret: caret})
}

func (f *suggestionFixture) edit(t *testing.T, start, end int, text string) {
	t.Helper()
	f.docs.mu.Lock()
	revision := f.docs.docs[f.id].snapshot.Revision
	f.docs.mu.Unlock()
	f.seq++
	documentEdit(t, f.docs, f.owner, f.id, fmt.Sprintf("edit-%d", f.seq), revision, start, end, text)
}

// A multi-line continuation at the caret is an insertion with its exact
// text; the same context is answered from the cache without a request.
func TestSuggestionInsertsMultipleLinesAndReusesTheAnswer(t *testing.T) {
	text := "func total(xs []int) int {\n\t\n}\n"
	caret := strings.Index(text, "\t\n") + 1
	f := newSuggestionFixture(t, text, func(input string) string {
		return rewriteOf(input, func(region string) string {
			return strings.Replace(region, "\t\n", "\tsum := 0\n\tfor _, x := range xs {\n\t\tsum += x\n\t}\n\treturn sum\n", 1)
		})
	})
	first, err := f.request(t, caret)
	if err != nil {
		t.Fatal(err)
	}
	want := "sum := 0\n\tfor _, x := range xs {\n\t\tsum += x\n\t}\n\treturn sum"
	if first.Kind != types.PredictionInsert || first.Start != caret || first.End != caret || first.Text != want {
		t.Fatalf("result = %+v", first)
	}
	if first.Stages.ModelUS <= 0 || first.Stages.TotalUS < first.Stages.ModelUS || first.CacheHit {
		t.Fatalf("stages = %+v", first.Stages)
	}
	again, err := f.request(t, caret)
	if err != nil || !again.CacheHit || again.Text != want || f.server.calls.Load() != 1 {
		t.Fatalf("cache: %+v %v calls=%d", again, err, f.server.calls.Load())
	}
	status := f.service.Snapshot()
	if status.ChargedTokens != 48 || status.Requests != 2 || status.Shown != 2 || status.MeasuredLatencyMS <= 0 {
		t.Fatalf("status = %+v", status)
	}
}

// After a rename, the model changes a later use; the answer is a jump there,
// and the following use is chained without another request.
func TestSuggestionJumpsToTheNextUseAfterARename(t *testing.T) {
	text := "count := load()\nlog(count)\nsave(count)\n"
	f := newSuggestionFixture(t, text, func(input string) string {
		return rewriteOf(input, func(region string) string { return strings.ReplaceAll(region, "(count)", "(total)") })
	})
	f.edit(t, 0, len("count"), "total")
	caret := len("total")
	first, err := f.request(t, caret)
	if err != nil {
		t.Fatal(err)
	}
	current := "total := load()\nlog(count)\nsave(count)\n"
	logUse := strings.Index(current, "count")
	if first.Kind != types.PredictionEdit || first.Start != logUse || first.End != logUse+len("count") || first.Text != "total" {
		t.Fatalf("first = %+v", first)
	}
	if !strings.Contains(f.server.inputs[0], "-count\n+total") || !strings.Contains(f.server.inputs[0], "by the programmer") {
		t.Fatalf("recent edit was not in the request:\n%s", f.server.inputs[0])
	}
	f.edit(t, first.Start, first.End, first.Text)
	next, err := f.request(t, first.Start+len(first.Text))
	if err != nil {
		t.Fatal(err)
	}
	after := "total := load()\nlog(total)\nsave(count)\n"
	saveUse := strings.LastIndex(after, "count")
	if !next.CacheHit || next.Start != saveUse || next.Text != "total" || f.server.calls.Load() != 1 {
		t.Fatalf("chained = %+v calls=%d", next, f.server.calls.Load())
	}
}

// The model must not undo what the programmer just typed, repeat the code
// after the caret, or answer with an unchanged region.
func TestSuggestionChecksDropUnwantedChanges(t *testing.T) {
	var mode atomic.Int32
	text := "a := 1\nreturn a\n"
	f := newSuggestionFixture(t, text, func(input string) string {
		return rewriteOf(input, func(region string) string {
			switch mode.Load() {
			case 0:
				return region
			case 1:
				return strings.Replace(region, "b := 1", "a := 1", 1)
			default:
				return strings.Replace(region, "b := 1\n", "b := 1\nreturn a\n", 1)
			}
		})
	})
	if got, err := f.request(t, 6); err != nil || !got.Empty() {
		t.Fatalf("unchanged region = %+v, %v", got, err)
	}
	f.edit(t, 0, 1, "b")
	mode.Store(1)
	if got, err := f.request(t, 1); err != nil || !got.Empty() {
		t.Fatalf("reverting the user's edit was shown: %+v, %v", got, err)
	}
	mode.Store(2)
	if got, err := f.request(t, len("b := 1\n")); err != nil || !got.Empty() {
		t.Fatalf("repeating the next line was shown: %+v, %v", got, err)
	}
}

func TestSuggestionRefusesOffCappedAndSensitiveWithoutEgress(t *testing.T) {
	f := newSuggestionFixture(t, "x\n", func(input string) string { return rewriteOf(input, func(r string) string { return r + "y\n" }) })
	policy := f.policy
	policy.settings.HourlyTokenLimit = 10
	f.service.Configure(policy)
	if _, err := f.request(t, 1); err == nil || !strings.Contains(err.Error(), "hourly limit") {
		t.Fatalf("capped: %v", err)
	}
	policy.settings.Mode = types.PredictionAsk
	policy.settings.HourlyTokenLimit = 100000
	f.service.Configure(policy)
	f.docs.mu.Lock()
	source := f.docs.analysisIdentityLocked(f.docs.docs[f.id])
	f.docs.mu.Unlock()
	f.seq++
	if _, err := f.service.Request(t.Context(), f.owner, types.EditorPredictionRequest{Source: source, ViewID: "editor", Sequence: f.seq, Caret: 1, Automatic: true}); err == nil {
		t.Fatal("When I ask answered an automatic request")
	}
	policy.consented = "another connection"
	f.service.Configure(policy)
	if _, err := f.request(t, 1); err == nil || !strings.Contains(err.Error(), "connection changed") {
		t.Fatalf("changed connection: %v", err)
	}
	if f.server.calls.Load() != 0 {
		t.Fatalf("refused requests reached the model: %d", f.server.calls.Load())
	}
	if !predictionPathAllowed(f.docs.root, f.docs.root+"/src/a.go") {
		t.Fatal("ordinary source refused")
	}
	for _, path := range []string{"/.env", "/config/.env.local", "/secrets/x.go", "/id_rsa", "/certs/a.pem", "/.git/config"} {
		if predictionPathAllowed(f.docs.root, f.docs.root+path) {
			t.Errorf("sensitive path allowed: %s", path)
		}
	}
}

// A newer request for the same view replaces an older one.
func TestSuggestionOlderSequenceIsRefused(t *testing.T) {
	f := newSuggestionFixture(t, "x\n", func(input string) string { return rewriteOf(input, func(r string) string { return r }) })
	f.seq = 10
	if _, err := f.request(t, 1); err != nil {
		t.Fatal(err)
	}
	f.docs.mu.Lock()
	source := f.docs.analysisIdentityLocked(f.docs.docs[f.id])
	f.docs.mu.Unlock()
	if _, err := f.service.Request(t.Context(), f.owner, types.EditorPredictionRequest{Source: source, ViewID: "editor", Sequence: 3, Caret: 1}); err == nil {
		t.Fatal("an older sequence was answered")
	}
}

func TestSuggestionRewriteParsingAndHunks(t *testing.T) {
	region := "a\nb\nc\n"
	if _, err := parseSuggestionRewrite(prompt.SuggestionRegionStart+"\na\n", region); err == nil {
		t.Fatal("an answer without the end marker was read")
	}
	got, err := parseSuggestionRewrite("```\n"+prompt.SuggestionRegionStart+"\na\nB\nc"+prompt.SuggestionRegionEnd+"\n```", region)
	if err != nil || got != "a\nB\nc\n" {
		t.Fatalf("parse = %q, %v", got, err)
	}
	for _, tc := range []struct {
		name, rewrite string
		caret         int
		want          []suggestionHunk
	}{
		{"insert at caret", "a\nbXY\nc\n", 3, []suggestionHunk{{3, 3, "XY"}}},
		{"replace a word", "a\nB\nc\n", 0, []suggestionHunk{{2, 3, "B"}}},
		{"delete a line", "a\nc\n", 0, []suggestionHunk{{2, 4, ""}}},
		{"two places", "A\nb\nC\n", 0, []suggestionHunk{{0, 1, "A"}, {4, 5, "C"}}},
		{"utf8", "a\nسلام\nc\n", 0, []suggestionHunk{{2, 3, "سلام"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := predictionContextFor(region, tc.caret)
			got := suggestionHunks(c, tc.rewrite)
			if fmt.Sprint(got) != fmt.Sprint(tc.want) {
				t.Fatalf("hunks = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func predictionContextFor(region string, caret int) predictioncontext.Context {
	return predictioncontext.Context{Region: region, RegionStart: 0, RegionEnd: len(region), Caret: caret}
}

// A source change invalidates even a response whose provider finished while
// the document notification was in flight.
func TestSuggestionPublicationRefusesChangedSource(t *testing.T) {
	f := newSuggestionFixture(t, "old\n", func(input string) string { return rewriteOf(input, func(r string) string { return r }) })
	f.docs.mu.Lock()
	source := f.docs.analysisIdentityLocked(f.docs.docs[f.id])
	f.docs.mu.Unlock()
	request := types.EditorPredictionRequest{Source: source, ViewID: "editor", Sequence: 1, Caret: 1}
	ctx, job, policy, err := f.service.begin(t.Context(), f.owner, request)
	if err != nil {
		t.Fatal(err)
	}
	defer f.service.finish(f.owner, request, job)
	f.edit(t, 0, 3, "new")
	got, err := f.service.publish(ctx, f.owner, request, job, policy, types.EditorPredictionResult{Text: "obsolete"}, time.Now(), false)
	if err == nil || !got.Empty() {
		t.Fatalf("changed source published: %+v %v", got, err)
	}
}
