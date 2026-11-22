package workspace

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"crude/agent/provider"
	"crude/agent/session"
	"crude/internal/metrics"
	"crude/types/insight"
	"crude/types/setting"
	"crude/workspace/document"
)

func requestSuggestionFollow(t *testing.T, f *suggestionFixture, caret int) insight.EditorPredictionResult {
	t.Helper()
	f.docs.mu.Lock()
	source := f.docs.analysisIdentityLocked(f.docs.docs[f.id])
	f.docs.mu.Unlock()
	f.seq++
	got, err := f.service.Request(t.Context(), f.owner, insight.EditorPredictionRequest{
		Source: source, ViewID: "editor", Sequence: f.seq, Caret: caret, Follow: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestSuggestionFollowWithoutAChainNeverCallsTheModel(t *testing.T) {
	f := newSuggestionFixture(t, "value =\n", func(input string) string {
		return rewriteOf(input, func(region string) string {
			return strings.Replace(region, "value =", "value = 23", 1)
		})
	})
	before := f.server.calls.Load()
	got := requestSuggestionFollow(t, f, len("value ="))
	if calls := f.server.calls.Load(); !got.Empty() || got.Text != "" || calls != before {
		t.Fatalf("follow with no chain = %+v, model calls %d -> %d", got, before, calls)
	}
}

func TestSuggestionFollowNamesItsSourcesAndReleaseDiscardsRemainingHunks(t *testing.T) {
	const original = "count := load()\nlog(count)\nsave(count)\nreport(count)\n"
	const edited = "total := load()\nlog(count)\nsave(count)\nreport(count)\n"
	f := newSuggestionFixture(t, original, func(input string) string {
		return rewriteOf(input, func(region string) string {
			return strings.ReplaceAll(region, "(count)", "(total)")
		})
	})
	f.edit(t, 0, len("count"), "total")
	first, err := f.request(t, len("total"))
	logStart := strings.Index(edited, "log(count)") + len("log(")
	if err != nil || first.Kind != insight.PredictionEdit || first.Start != logStart || first.End != logStart+len("count") ||
		first.Text != "total" || first.CacheHit || f.server.calls.Load() != 1 {
		t.Fatalf("first edit = %+v, err=%v, model calls=%d", first, err, f.server.calls.Load())
	}
	if !slices.Contains(first.Sources, insight.PredictionSourceEdits) {
		t.Errorf("first answer sources = %v; missing %q", first.Sources, insight.PredictionSourceEdits)
	}
	f.edit(t, first.Start, first.End, first.Text)
	next := requestSuggestionFollow(t, f, first.Start+len(first.Text))
	saveStart := strings.Index(edited, "save(count)") + len("save(")
	if next.Kind != insight.PredictionEdit || next.Start != saveStart || next.End != saveStart+len("count") ||
		next.Text != "total" || !next.CacheHit || f.server.calls.Load() != 1 {
		t.Fatalf("second edit = %+v, model calls=%d", next, f.server.calls.Load())
	}
	if !slices.Equal(next.Sources, []string{insight.PredictionSourceChain}) {
		t.Errorf("follow-up sources = %v, want [%s]", next.Sources, insight.PredictionSourceChain)
	}
	f.service.Release(f.owner, "editor", next.Sequence)
	// The remaining third rename now matches this document exactly. It must
	// still stay absent after the view releases its previous answer.
	f.edit(t, next.Start, next.End, next.Text)
	got := requestSuggestionFollow(t, f, next.Start+len(next.Text))
	if !got.Empty() || got.Text != "" || f.server.calls.Load() != 1 {
		t.Fatalf("released view offered a follow-up: %+v, model calls=%d", got, f.server.calls.Load())
	}
}

func TestSuggestionAgentBusyUsesAuthorshipFileAndAge(t *testing.T) {
	const text, path = "one\ntwo\ncount = 1\nfour\nfive\n", "text.txt"
	at := strings.Index(text, "count")
	hunk := suggestionHunk{at, at + len("count"), "total"}
	now := time.Unix(1_000_000, 0)
	for _, tc := range []struct {
		name, author, path string
		line               int
		age                time.Duration
		busy               bool
	}{
		{"other recently changed the hunk line", insight.RecentEditOther, path, 3, time.Second, true},
		{"disk recently changed the hunk line", insight.RecentEditDisk, path, 3, time.Second, true},
		{"old changes by another author", insight.RecentEditOther, path, 3, suggestionAgentWindow + time.Second, false},
		{"old disk changes", insight.RecentEditDisk, path, 3, suggestionAgentWindow + time.Second, false},
		{"my own change", insight.RecentEditYou, path, 3, time.Second, false},
		{"change in another file", insight.RecentEditOther, "another.txt", 3, time.Second, false},
		{"change away from the hunk", insight.RecentEditOther, path, 5, time.Second, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			edit := insight.RecentEdit{Path: tc.path, Author: tc.author, Line: tc.line, Inserted: "1", Unix: now.Add(-tc.age).Unix()}
			if got := agentBusy([]byte(text), hunk, path, []insight.RecentEdit{edit}, now); got != tc.busy {
				t.Fatalf("agentBusy = %t, want %t", got, tc.busy)
			}
		})
	}
}

func TestSuggestionRequestHidesAChangeOnRecentlyEditedPeerOrDiskLines(t *testing.T) {
	const initial = "header\nnotes\nblank\ncount = 0\nfooter\n"
	for _, tc := range []struct {
		name, author string
		old          bool
		hidden       bool
	}{
		{"recent peer edit", insight.RecentEditOther, false, true},
		{"recent disk edit", insight.RecentEditDisk, false, true},
		{"old peer edit", insight.RecentEditOther, true, false},
		{"old disk edit", insight.RecentEditDisk, true, false},
		{"my recent edit", insight.RecentEditYou, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newSuggestionFixture(t, initial, func(input string) string {
				return rewriteOf(input, func(region string) string {
					return strings.Replace(region, "count = 1", "total = 1", 1)
				})
			})
			valueAt := strings.Index(initial, "0")
			switch tc.author {
			case insight.RecentEditYou:
				f.edit(t, valueAt, valueAt+1, "1")
			case insight.RecentEditOther:
				// The existing document fixture opens the same document for b.
				documentEdit(t, f.docs, document.Owner{ClientID: "b", Epoch: 1}, f.id, "peer-edit", 1, valueAt, valueAt+1, "1")
			case insight.RecentEditDisk:
				// Keep a non-overlapping unsaved edit so the on-disk change
				// reaches the document as a real disk merge.
				f.edit(t, 0, len("header"), "HEADER")
				snapshot, err := f.docs.Snapshot(f.id)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(snapshot.Path, []byte(strings.Replace(initial, "count = 0", "count = 1", 1)), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := f.docs.ReconcileDisk(t.Context(), f.id); err != nil {
					t.Fatal(err)
				}
			}
			wantSource := strings.Replace(initial, "count = 0", "count = 1", 1)
			if tc.author == insight.RecentEditDisk {
				wantSource = strings.Replace(wantSource, "header", "HEADER", 1)
			}
			if snapshot := documentText(t, f.docs, f.id, wantSource); snapshot.Conflict {
				t.Fatalf("fixture unexpectedly has a disk conflict: %+v", snapshot)
			}
			edits := f.docs.RecentEdits(f.owner, 16)
			recorded := slices.ContainsFunc(edits, func(e insight.RecentEdit) bool { return e.Author == tc.author && e.Line == 4 })
			if !recorded {
				t.Errorf("committed change on line 4 was not recorded as %s: %+v", tc.author, edits)
			}
			if tc.old {
				// Expire the authoritative recent-edit record without sleeping.
				f.docs.mu.Lock()
				for i := range f.docs.recentEdits {
					if edit := &f.docs.recentEdits[i]; edit.document == f.id && edit.line == 4 {
						edit.unix = time.Now().Add(-suggestionAgentWindow - time.Second).Unix()
					}
				}
				f.docs.mu.Unlock()
			}
			at := strings.Index(initial, "count")
			got, err := f.request(t, at)
			if err != nil || f.server.calls.Load() != 1 {
				t.Fatalf("model answer = %+v, err=%v, model calls=%d", got, err, f.server.calls.Load())
			}
			if tc.hidden {
				if !got.Empty() || got.Text != "" {
					t.Fatalf("suggestion exposed a change on a recently edited %s line: %+v", tc.author, got)
				}
			} else if got.Kind != insight.PredictionEdit || got.Text != "total" || got.Start != at || got.End != at+len("count") {
				t.Fatalf("uncontested suggestion was not offered at the hunk: %+v", got)
			}
		})
	}
}

func TestSuggestionStagesAreReportedToTheCoreMetrics(t *testing.T) {
	f := newSuggestionFixture(t, "value =\n", func(input string) string {
		return rewriteOf(input, func(region string) string {
			return strings.Replace(region, "value =", "value = 23", 1)
		})
	})
	query := metrics.Query{Seconds: metrics.HistorySeconds, Points: 1}
	before := metrics.Take(query)
	got, err := f.request(t, len("value ="))
	if err != nil || got.Kind != insight.PredictionInsert || got.Text != " 23" || got.CacheHit || f.server.calls.Load() != 1 {
		t.Fatalf("model insertion = %+v, err=%v, model calls=%d", got, err, f.server.calls.Load())
	}
	s := got.Stages
	// A short stage may round down to zero. The model includes time
	// to first text, and the full request includes account and model.
	if s.ContextUS < 0 || s.AccountUS < 0 || s.FirstByteUS < 0 || s.ModelUS < 0 || s.CheckUS < 0 || s.TotalUS < 0 ||
		s.TotalUS < s.AccountUS+s.ModelUS || s.ModelUS < s.FirstByteUS {
		t.Fatalf("invalid microsecond stages for a model answer: %+v", s)
	}
	after := metrics.Take(query)
	for _, tc := range []struct {
		name string
		us   int64
	}{{"suggest.total", s.TotalUS}, {"suggest.context", s.ContextUS}} {
		oldSeries := suggestionLatencySummary(before, tc.name)
		newSeries := suggestionLatencySummary(after, tc.name)
		if newSeries.Total != oldSeries.Total+1 {
			t.Errorf("%s after one answer: observations %d -> %d, want one more observation",
				tc.name, oldSeries.Total, newSeries.Total)
		}
		// Check the reported unit against the metric when the same
		// observations still occupy the rolling window.
		if newSeries.Window.Count == oldSeries.Window.Count+1 && newSeries.Window.Sum-oldSeries.Window.Sum != tc.us {
			t.Errorf("%s recorded %dµs, want the stage's %dµs", tc.name, newSeries.Window.Sum-oldSeries.Window.Sum, tc.us)
		}
	}
}

func suggestionLatencySummary(snapshot metrics.Snapshot, name string) metrics.LatencySeries {
	for _, series := range snapshot.Latencies {
		if series.Meta.Name == name {
			return series
		}
	}
	return metrics.LatencySeries{}
}

func TestSuggestionFIMOnlyReportsSourcesSentToTheModel(t *testing.T) {
	f := newSuggestionFixture(t, "value = ()\n", func(input string) string {
		return rewriteOf(input, func(region string) string { return region })
	})
	var fimCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fimCalls.Add(1)
		var req struct {
			Prefix string `json:"prompt"`
			Suffix string `json:"suffix"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
		}
		if r.URL.Path != "/v1/completions" || req.Prefix != "score = (" || req.Suffix != ")\n" {
			t.Errorf("FIM sent path=%q, prefix=%q, suffix=%q; want only code around the caret", r.URL.Path, req.Prefix, req.Suffix)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"text\":\"23\",\"finish_reason\":\"stop\"}]}\n\ndata: {\"choices\":[],\"usage\":{\"prompt_tokens\":12,\"completion_tokens\":1}}\n\ndata: [DONE]\n\n")
	}))
	t.Cleanup(server.Close)
	account := provider.Account{ID: "fim", ProviderID: "openai", APIKey: "fixture-only",
		Connection: provider.Template{Name: "Local fixture", ProviderURL: server.URL, APIStandard: "openai", AuthType: provider.AuthBearer}}
	cfg := &session.Config{Accounts: map[string]provider.Account{account.ID: account},
		Agents: session.AgentsConfig{Suggestions: setting.Suggestion{
			AccountID: account.ID, Model: "fast", Format: setting.SuggestionFormatOpenAI, EndpointPath: "/completions",
		}}}
	completer, err := cfg.SuggestionCompleter()
	if err != nil {
		t.Fatal(err)
	}
	policy := f.policy
	policy.completer, policy.consented = completer, completer.ConnectionIdentity
	f.service.Configure(policy)
	f.edit(t, 0, len("value"), "score")
	caret := len("score = (")
	got, err := f.request(t, caret)
	if err != nil || got.Kind != insight.PredictionInsert || got.Start != caret || got.End != caret || got.Text != "23" ||
		fimCalls.Load() != 1 || f.server.calls.Load() != 0 {
		t.Fatalf("FIM suggestion=%+v err=%v (calls=%d FIM, %d Chat)", got, err, fimCalls.Load(), f.server.calls.Load())
	}
	if got.Sources != nil {
		t.Fatalf("FIM claims context sources it did not send: %v", got.Sources)
	}
}

func TestSuggestionRecentEditsCoalesceDeleteAndBackspaceRenames(t *testing.T) {
	for _, backward := range []bool{false, true} {
		key := "Delete"
		if backward {
			key = "Backspace"
		}
		t.Run(key, func(t *testing.T) {
			const prefix, old, suffix = "prefix ", "count", " suffix\n"
			s, snap, owner, _ := documentFixture(t, prefix+old+suffix)
			revision := snap.Revision
			for i := range len(old) {
				at := len(prefix)
				if backward {
					at += len(old) - 1 - i
				}
				revision = documentEdit(t, s, owner, snap.ID, fmt.Sprintf("remove-%d", i), revision, at, at+1, "").Revision
			}
			documentText(t, s, snap.ID, prefix+suffix)
			edits := s.RecentEdits(owner, 16)
			if len(edits) != 1 || edits[0].Author != insight.RecentEditYou || edits[0].Line != 1 ||
				edits[0].Removed != old || edits[0].Inserted != "" {
				t.Fatalf("%s would show fragmented removals instead of one -%s edit: %+v", key, old, edits)
			}
			const replacement = "total"
			for i := range len(replacement) {
				at := len(prefix) + i
				revision = documentEdit(t, s, owner, snap.ID, fmt.Sprintf("insert-%d", i), revision, at, at, replacement[i:i+1]).Revision
			}
			documentText(t, s, snap.ID, prefix+replacement+suffix)
			edits = s.RecentEdits(owner, 16)
			if len(edits) != 1 || edits[0].Removed != old || edits[0].Inserted != replacement ||
				edits[0].Author != insight.RecentEditYou || edits[0].Line != 1 {
				t.Fatalf("%s plus typing would not show one complete rename -%s/+%s: %+v", key, old, replacement, edits)
			}
		})
	}
}
