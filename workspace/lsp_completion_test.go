package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"crude/document"
	"crude/types"
)

// This fixture acknowledges actual didOpen/didChange versions, but forges adapter
// provenance and can return after cancellation. Neither is canonical authority.
type completionAcceptanceClient struct {
	lifecycleClient
	completionMu                  sync.Mutex
	raw, resolved                 string
	unsupported, resolveSupported bool
	wireDelta                     int
	err                           error
	calls                         []completionAcceptanceCall
	entered                       chan<- completionAcceptanceCall
	release                       <-chan struct{}
}

type completionAcceptanceCall struct {
	ctx       context.Context
	path      string
	line, col int
	resolve   bool
	wire      int
	original  string
}

func (c *completionAcceptanceClient) answer(call completionAcceptanceCall) (types.LSPCompletionResponse, error) {
	c.mu.Lock()
	wire := c.versions[call.path]
	c.mu.Unlock()
	c.completionMu.Lock()
	c.calls = append(c.calls, call)
	raw := c.raw
	if call.resolve {
		raw = c.resolved
	}
	out := types.LSPCompletionResponse{
		Boundary:    types.InsightBoundary{Source: types.InsightSource{Path: "forged-adapter", Kind: "disk"}},
		WireVersion: wire + c.wireDelta, JSON: []byte(raw),
		Unsupported: c.unsupported, ResolveSupported: c.resolveSupported,
	}
	err, entered, release := c.err, c.entered, c.release
	c.completionMu.Unlock()
	if entered != nil {
		entered <- call
	}
	if release != nil {
		<-release // Deliberately ignore call.ctx: the manager must reject late data.
	}
	return out, err
}

func (c *completionAcceptanceClient) RequestCompletions(ctx context.Context, path string, line, col int, trigger types.LSPCompletionContext) (types.LSPCompletionResponse, error) {
	return c.answer(completionAcceptanceCall{ctx: ctx, path: path, line: line, col: col})
}

func (c *completionAcceptanceClient) ResolveCompletion(ctx context.Context, path string, wire int, original []byte) (types.LSPCompletionResponse, error) {
	return c.answer(completionAcceptanceCall{ctx: ctx, path: path, wire: wire, original: string(original), resolve: true})
}

type completionAcceptanceRuntime struct{ client *completionAcceptanceClient }

func (r completionAcceptanceRuntime) NewClient(_ types.LSPServerSpec, _ string, admission func() error) LSPClient {
	r.client.startAdmission = admission
	return r.client
}

type completionAcceptanceFixture struct {
	w      *Workspace
	client *completionAcceptanceClient
	owner  document.Owner
	source types.InsightSource
	path   string
	disk   string
}

func newCompletionAcceptanceFixture(t *testing.T, text string) *completionAcceptanceFixture {
	t.Helper()
	root := t.TempDir()
	client := &completionAcceptanceClient{lifecycleClient: lifecycleClient{
		runtime: &lifecycleRuntime{}, reapableClient: reapableClient{done: make(chan struct{})},
	}, raw: "[]"}
	manager := NewLSPManager(nil, completionAcceptanceRuntime{client})
	trustAnalysis(t, manager, root)
	settings := manager.Settings()
	settings.Completions = true
	manager.SetSettings(settings)
	t.Cleanup(manager.Close)
	db, err := openWorkspaceDBAt(filepath.Join(t.TempDir(), "workspace.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	docs := NewDocumentService(root, nil, manager, db.DB)
	t.Cleanup(docs.Close)
	path := filepath.Join(root, "main.go")
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	owner := document.Owner{ClientID: "completion-owner", Epoch: 1}
	snap, err := docs.Open(owner, path)
	if err != nil {
		t.Fatal(err)
	}
	waitDiagnosticSend(t, manager, &client.lifecycleClient, snap)
	docs.mu.Lock()
	source := docs.analysisIdentityLocked(docs.docs[snap.ID])
	docs.mu.Unlock()
	return &completionAcceptanceFixture{&Workspace{LiveDocuments: docs, LSPMgr: manager}, client, owner, source, path, text}
}

func (f *completionAcceptanceFixture) request(t *testing.T, raw, view string, sequence uint64, line, col int) types.LSPCompletionSet {
	t.Helper()
	f.client.completionMu.Lock()
	f.client.raw = raw
	f.client.completionMu.Unlock()
	set, err := f.w.RequestLSPCompletions(t.Context(), f.owner, f.source, view, sequence, line, col, types.LSPCompletionContext{})
	if err != nil {
		t.Fatal(err)
	}
	return set
}

func (f *completionAcceptanceFixture) inspect(t *testing.T, set types.LSPCompletionSet, row int, resolve bool) types.LSPCompletionInspection {
	t.Helper()
	out, err := f.w.InspectLSPCompletion(t.Context(), f.owner, set.ID, set.Digest, set.Entries[row].ID, resolve)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func (f *completionAcceptanceFixture) snapshot(t *testing.T) document.Snapshot {
	t.Helper()
	snap, err := f.w.LiveDocuments.Snapshot(f.source.DocumentID)
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

func (f *completionAcceptanceFixture) history() string {
	s := f.w.LiveDocuments
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.docs[f.source.DocumentID]
	return fmt.Sprintf("%#v / %d", d.journal, d.historyBytes)
}

func (f *completionAcceptanceFixture) unchanged(t *testing.T, before document.Snapshot, history string) {
	t.Helper()
	disk, err := os.ReadFile(f.path)
	if err != nil || string(disk) != f.disk {
		t.Fatalf("completion changed literal disk bytes: %q %v", disk, err)
	}
	if after := f.snapshot(t); !reflect.DeepEqual(before, after) {
		t.Fatalf("completion changed source/revision/savepoint: before=%+v after=%+v", before, after)
	}
	if after := f.history(); after != history {
		t.Fatalf("completion changed undo history: before=%s after=%s", history, after)
	}
	for _, call := range f.client.snapshot() {
		if strings.HasPrefix(call, "willSave:") || strings.HasPrefix(call, "didSave:") {
			t.Fatal("completion observation issued a save", call)
		}
	}
}

func TestCompletionCanonicalUnsavedSnippetAndLiteralAdjunctsAreReadOnly(t *testing.T) {
	f := newCompletionAcceptanceFixture(t, "disk\r\n")
	documentEdit(t, f.w.LiveDocuments, f.owner, f.source.DocumentID, "unsaved", f.source.Revision,
		0, 6, "a😀 fo\r\n")
	before := f.snapshot(t)
	waitDiagnosticSend(t, f.w.LSPMgr, &f.client.lifecycleClient, before)
	f.w.LiveDocuments.mu.Lock()
	f.source = f.w.LiveDocuments.analysisIdentityLocked(f.w.LiveDocuments.docs[before.ID])
	f.w.LiveDocuments.mu.Unlock()
	if !before.Dirty || string(before.Text) != "a😀 fo\r\n" || string(before.Saved) != "disk\r\n" {
		t.Fatal("fixture did not establish unsaved canonical bytes", before)
	}
	history := f.history()
	original := `{"label":"function","insertText":"fn(${1:x}, $1)$0","insertTextFormat":2,"additionalTextEdits":[{"range":{"start":{"line":0,"character":0},"end":{"line":0,"character":0}},"newText":"$1 literal\r\n"}],"data":{"n":9007199254740993}}`
	f.client.resolveSupported = true
	f.client.resolved = strings.TrimSuffix(original, "}") + `,"detail":"signature","documentation":{"kind":"markdown","value":"**inert**"}}`
	set := f.request(t, "["+original+"]", "editor", 1, 0, 6)
	now := time.Now()
	if !set.ExpiresAt.After(now.Add(4*time.Minute)) || set.ExpiresAt.After(now.Add(5*time.Minute)) {
		t.Fatal("completion invocation does not have a bounded five-minute lifetime", set.ExpiresAt)
	}
	if set.Boundary.Source != f.source || set.Boundary.Producer.InstanceID == "" ||
		set.Boundary.Context.Generation == 0 || len(set.Boundary.Context.UnknownInputs) == 0 ||
		set.WireVersion != 2 || set.Line != 0 || set.Col != 6 || set.ViewID != "editor" ||
		set.Sequence != 1 || set.ID == "" || len(set.Digest) != 64 {
		t.Fatal("adapter provenance replaced synchronized canonical authority", set)
	}
	if len(set.Entries) != 1 {
		t.Fatal("lost initial snippet", set)
	}
	item := set.Entries[0].Insertion
	wantPrimary := types.LSPTextEdit{Line: 0, Col: 4, EndLine: 0, EndCol: 6, NewText: "fn(x, x)"}
	wantAdditional := []types.LSPTextEdit{{Line: 0, Col: 0, EndLine: 0, EndCol: 0, NewText: "$1 literal\r\n"}}
	if item.InsertText != "fn(x, x)" || item.TextEdit == nil || *item.TextEdit != wantPrimary ||
		!reflect.DeepEqual(item.AdditionalTextEdits, wantAdditional) || item.Snippet == nil {
		t.Fatal("initial numeric snippet or literal additional edit changed", item)
	}
	wantOccurrences := []types.CompletionOccurrence{
		{Stop: 1, StartByte: 3, EndByte: 4, Parent: -1},
		{Stop: 1, StartByte: 6, EndByte: 7, Parent: -1},
		{Stop: 0, StartByte: 8, EndByte: 8, Parent: -1},
	}
	if len(item.Snippet.Occurrences) != len(wantOccurrences) {
		t.Fatal("snippet lost literal initial byte anchors", item.Snippet)
	}
	for i, want := range wantOccurrences {
		got := item.Snippet.Occurrences[i]
		if got.Stop != want.Stop || got.StartByte != want.StartByte || got.EndByte != want.EndByte ||
			got.Parent != want.Parent || len(got.Choices) != 0 {
			t.Fatal("snippet lost literal initial byte anchor", i, got, want)
		}
	}
	// Acceptance preflight and documentation resolution both remain observations.
	f.inspect(t, set, 0, false)
	resolved := f.inspect(t, set, 0, true)
	if resolved.ResolveState != "resolved" || resolved.Documentation != (types.LSPCompletionDocumentation{
		HasDetail: true, Present: true, Detail: "signature", Form: "markup", Kind: "markdown", Value: "**inert**",
	}) {
		t.Fatal("lost selected documentation form", resolved)
	}
	f.client.completionMu.Lock()
	calls := append([]completionAcceptanceCall(nil), f.client.calls...)
	f.client.completionMu.Unlock()
	if len(calls) != 2 || calls[0].path != f.path || calls[0].line != 0 || calls[0].col != 6 ||
		!calls[1].resolve || calls[1].wire != 2 || calls[1].original != original {
		t.Fatal("request/resolve lost original wire coordinates or opaque identity", calls)
	}
	f.w.LiveDocuments.mu.Lock()
	retained := f.w.LiveDocuments.completionViews[completionViewKey{f.owner, "editor"}].set
	f.w.LiveDocuments.mu.Unlock()
	if !reflect.DeepEqual(retained.Entries[0].Insertion, item) {
		t.Fatal("resolve replaced original insertion", retained)
	}
	// A shell cannot write through nested detached insertion/snippet evidence.
	set.Entries[0].Insertion.TextEdit.NewText = "forged"
	set.Entries[0].Insertion.AdditionalTextEdits[0].NewText = "forged"
	set.Entries[0].Insertion.Snippet.Occurrences[0].StartByte = 999
	f.w.LiveDocuments.mu.Lock()
	after := types.CloneCompletionItem(f.w.LiveDocuments.completionViews[completionViewKey{f.owner, "editor"}].set.Entries[0].Insertion)
	f.w.LiveDocuments.mu.Unlock()
	if after.TextEdit.NewText != "fn(x, x)" || after.AdditionalTextEdits[0].NewText != "$1 literal\r\n" ||
		after.Snippet.Occurrences[0].StartByte != 3 {
		t.Fatal("returned insertion aliases retained authority", after)
	}
	f.unchanged(t, before, history)
}

func TestCompletionSameLabelsKeepExactIndicesRawIdentityAndDocumentationForms(t *testing.T) {
	f := newCompletionAcceptanceFixture(t, "abc\n")
	first := `{"label":"same","data":{"n":9007199254740993},"documentation":"first"}`
	second := `{"label":"same","data":{"n":9007199254740995},"detail":"","documentation":{"kind":"plaintext","value":""}}`
	f.client.resolveSupported = true
	set := f.request(t, `{"isIncomplete":true,"items":[`+first+`,{"label":"command","command":{"command":"unsafe"}},`+second+`,{"label":"absent"}]}`, "view", 1, 0, 1)
	if set.Answer != "items" || !set.Incomplete || set.UnsupportedItems != 1 || len(set.Entries) != 3 {
		t.Fatal("partial list or omitted unsupported row was disguised as complete", set)
	}
	if set.Entries[0].ID == set.Entries[1].ID || set.Entries[0].Index != 0 || set.Entries[1].Index != 2 ||
		set.Entries[0].Insertion.Label != "same" || set.Entries[1].Insertion.Label != "same" {
		t.Fatal("same-label rows collapsed or lost original indices", set.Entries)
	}
	want := []types.LSPCompletionDocumentation{
		{Present: true, Form: "string", Kind: "plaintext", Value: "first"},
		{HasDetail: true, Present: true, Form: "markup", Kind: "plaintext"},
		{},
	}
	for i := range want {
		if got := f.inspect(t, set, i, false).Documentation; got != want[i] {
			t.Fatalf("row %d lost string/markup/absence evidence: %+v want %+v", i, got, want[i])
		}
	}
	for i, original := range []string{first, second} {
		f.client.completionMu.Lock()
		f.client.resolved = strings.Replace(original, `"documentation":"first"`, `"documentation":"resolved selected"`, 1)
		if i == 1 {
			f.client.resolved = `{"label":"same","data":{"n":9007199254740995},"detail":"","documentation":"resolved selected"}`
		}
		f.client.completionMu.Unlock()
		got := f.inspect(t, set, i, true)
		if got.ItemID != set.Entries[i].ID || got.Documentation.Form != "string" || got.Documentation.Value != "resolved selected" {
			t.Fatal("resolve selected another same-label row", got)
		}
		f.client.completionMu.Lock()
		call := f.client.calls[len(f.client.calls)-1]
		f.client.completionMu.Unlock()
		if !call.resolve || call.original != original {
			t.Fatal("resolve reconstructed or rounded >2^53 opaque identity", call.original, original)
		}
	}
}

func TestCompletionUnsupportedUnknownNullEmptyAndFailuresStayDistinct(t *testing.T) {
	for _, tc := range []struct {
		name, raw, answer string
		unsupported       bool
		unsupportedRows   int
		failure           error
	}{
		{name: "static-unsupported", unsupported: true, answer: "unsupported"},
		{name: "unknown-static-support-null", raw: "null", answer: "null"},
		{name: "unknown-static-support-empty", raw: "[]", answer: "empty"},
		{name: "incomplete-empty", raw: `{"isIncomplete":true,"items":[]}`, answer: "empty"},
		{name: "only-unsupported-rows", raw: `[{"label":"command","command":{"command":"unsafe"}}]`, answer: "items", unsupportedRows: 1},
		{name: "unknown-is-not-unsupported"},
		{name: "transport-error", raw: "null", failure: errors.New("literal transport failure")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newCompletionAcceptanceFixture(t, "abc\n")
			before, history := f.snapshot(t), f.history()
			f.client.raw, f.client.unsupported, f.client.err = tc.raw, tc.unsupported, tc.failure
			set, err := f.w.RequestLSPCompletions(t.Context(), f.owner, f.source, "view", 1, 0, 1, types.LSPCompletionContext{})
			if tc.answer == "" {
				if err == nil || !reflect.DeepEqual(set, types.LSPCompletionSet{}) {
					t.Fatal("missing support/result or provider failure became successful absence", set, err)
				}
			} else {
				if err != nil || set.Answer != tc.answer || len(set.Entries) != 0 ||
					set.Boundary.Source != f.source || set.Boundary.Producer.InstanceID == "" ||
					set.Boundary.Context.Generation == 0 || set.WireVersion != 1 ||
					set.ID == "" || len(set.Digest) != 64 || set.ExpiresAt.IsZero() || set.Detail == "" {
					t.Fatal("negative evidence lost original qualification", set, err)
				}
				if set.Incomplete != (tc.name == "incomplete-empty") {
					t.Fatal("empty partial list lost isIncomplete", set)
				}
				if set.UnsupportedItems != tc.unsupportedRows {
					t.Fatal("unsupported-only answer was disguised as provider absence", set)
				}
				out, err := f.w.InspectLSPCompletion(t.Context(), f.owner, set.ID, set.Digest, "item-0", true)
				if err == nil || !reflect.DeepEqual(out, types.LSPCompletionInspection{}) {
					t.Fatal("negative response invented a selectable item", out, err)
				}
			}
			f.unchanged(t, before, history)
		})
	}
}

func TestCompletionMalformedResponsesAreAtomicAndRetainNoRawObjects(t *testing.T) {
	for _, raw := range []string{
		`[{"label":"ok"},{"label":3}]`,
		`[{"label":"ok","data":{"n":1,"n":2}}]`,
		`[{"label":"ok","documentation":null}]`,
		`[{"label":"ok","data":"\ud800"}]`,
		`{"isIncomplete":false,"items":[{"label":"ok"}],"itemDefaults":{}}`,
		`{"items":[{"label":"ok"}]}`,
		`[{"label":"ok","insertTextFormat":2.0}]`,
		`[{"label":"ok","textEdit":{"range":{"start":{"line":0,"character":2},"end":{"line":0,"character":3}},"newText":"bad"}}]`,
		`[{"label":"ok"}] true`,
	} {
		t.Run(raw, func(t *testing.T) {
			f := newCompletionAcceptanceFixture(t, "a😀z\n")
			before, history := f.snapshot(t), f.history()
			f.client.raw = raw
			out, err := f.w.RequestLSPCompletions(t.Context(), f.owner, f.source, "view", 1, 0, 3, types.LSPCompletionContext{})
			if err == nil || !reflect.DeepEqual(out, types.LSPCompletionSet{}) {
				t.Fatal("invalid list published partial canonical evidence", out, err)
			}
			f.w.LiveDocuments.mu.Lock()
			view := f.w.LiveDocuments.completionViews[completionViewKey{f.owner, "view"}]
			retired := view != nil && view.set.ID == "" && view.originals == nil && view.bytes == 0 && view.ctx.Err() != nil
			f.w.LiveDocuments.mu.Unlock()
			if !retired {
				t.Fatal("failed normalization retained raw or active authority")
			}
			f.unchanged(t, before, history)
		})
	}
}

func TestCompletionAdmissionAndExactInspectionIdentity(t *testing.T) {
	for _, invalid := range []string{"owner", "revision", "lifetime", "path", "kind", "conflict",
		"negative", "surrogate", "outside", "view", "sequence", "cancel", "settings", "wire", "unsupported-payload"} {
		t.Run(invalid, func(t *testing.T) {
			f := newCompletionAcceptanceFixture(t, "a😀z\n")
			owner, source, view, sequence, line, col := f.owner, f.source, "view", uint64(1), 0, 3
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			switch invalid {
			case "owner":
				owner.Epoch++
			case "revision":
				source.Revision++
			case "lifetime":
				source.Lifetime += "-forged"
			case "path":
				source.Path += "-forged"
			case "kind":
				source.Kind = "disk"
			case "conflict":
				f.w.LiveDocuments.mu.Lock()
				f.w.LiveDocuments.docs[source.DocumentID].snapshot.Conflict = true
				f.w.LiveDocuments.mu.Unlock()
			case "negative":
				line = -1
			case "surrogate":
				col = 2
			case "outside":
				line = 9
			case "view":
				view = "bad\nview"
			case "sequence":
				sequence = 0
			case "cancel":
				cancel()
			case "settings":
				settings := f.w.LSPMgr.Settings()
				settings.Completions = false
				f.w.LSPMgr.SetSettings(settings)
			case "wire":
				f.client.wireDelta = 1
			case "unsupported-payload":
				f.client.unsupported = true
			}
			out, err := f.w.RequestLSPCompletions(ctx, owner, source, view, sequence, line, col, types.LSPCompletionContext{})
			if err == nil || !reflect.DeepEqual(out, types.LSPCompletionSet{}) {
				t.Fatal("invalid source/wire/admission acquired evidence", invalid, out, err)
			}
		})
	}
	f := newCompletionAcceptanceFixture(t, "abc\n")
	set := f.request(t, `[{"label":"same"}]`, "view", 1, 0, 1)
	for _, invalid := range []string{"owner", "set", "digest", "row", "label", "cancel"} {
		owner, id, digest, row := f.owner, set.ID, set.Digest, set.Entries[0].ID
		ctx, cancel := context.WithCancel(t.Context())
		switch invalid {
		case "owner":
			owner.ClientID = "foreign"
		case "set":
			id += "-forged"
		case "digest":
			digest = strings.Repeat("0", 64)
		case "row":
			row = "item-1"
		case "label":
			row = "same"
		case "cancel":
			cancel()
		}
		out, err := f.w.InspectLSPCompletion(ctx, owner, id, digest, row, true)
		cancel()
		if err == nil || !reflect.DeepEqual(out, types.LSPCompletionInspection{}) {
			t.Fatal("inspection accepted caller-authored identity", invalid, out, err)
		}
	}
}

func TestCompletionResolveOnlyDocumentationAndDetailOrRejectAtomically(t *testing.T) {
	const original = `{"label":"same","sortText":"01","filterText":"s","insertText":"first","data":{"n":9007199254740993},"documentation":"initial","additionalTextEdits":[]}`
	for _, tc := range []struct{ name, resolved string }{
		{"data", strings.Replace(original, "9007199254740993", "9007199254740992", 1)},
		{"label", strings.Replace(original, `"label":"same"`, `"label":"other"`, 1)},
		{"sort", strings.Replace(original, `"sortText":"01"`, `"sortText":"02"`, 1)},
		{"filter", strings.Replace(original, `"filterText":"s"`, `"filterText":"o"`, 1)},
		{"insertion", strings.Replace(original, `"insertText":"first"`, `"insertText":"second"`, 1)},
		{"new-primary", strings.TrimSuffix(original, "}") + `,"textEdit":{"range":{"start":{"line":0,"character":0},"end":{"line":0,"character":1}},"newText":"second"}}`},
		{"snippet-format", strings.TrimSuffix(original, "}") + `,"insertTextFormat":2}`},
		{"adjunct", strings.Replace(original, `"additionalTextEdits":[]`, `"additionalTextEdits":[{"range":{"start":{"line":0,"character":0},"end":{"line":0,"character":0}},"newText":"X"}]`, 1)},
		{"command", strings.TrimSuffix(original, "}") + `,"command":{"command":"unsafe"}}`},
		{"partial", `{"label":"same","documentation":"must not leak"}`},
		{"null", "null"},
		{"duplicate", strings.TrimSuffix(original, "}") + `,"label":"same"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newCompletionAcceptanceFixture(t, "abc\n")
			before, history := f.snapshot(t), f.history()
			f.client.resolveSupported, f.client.resolved = true, tc.resolved
			set := f.request(t, "["+original+"]", "view", 1, 0, 1)
			f.w.LiveDocuments.mu.Lock()
			initial := cloneCompletionSet(f.w.LiveDocuments.completionViews[completionViewKey{f.owner, "view"}].set)
			f.w.LiveDocuments.mu.Unlock()
			out, err := f.w.InspectLSPCompletion(t.Context(), f.owner, set.ID, set.Digest, set.Entries[0].ID, true)
			if err == nil || !reflect.DeepEqual(out, types.LSPCompletionInspection{}) {
				t.Fatal("resolve accepted non-documentation or partial-object mutation", out, err)
			}
			f.w.LiveDocuments.mu.Lock()
			entry := f.w.LiveDocuments.completionViews[completionViewKey{f.owner, "view"}]
			unchanged, cached := reflect.DeepEqual(entry.set, initial), len(entry.resolved)
			f.w.LiveDocuments.mu.Unlock()
			if !unchanged || cached != 0 || f.inspect(t, set, 0, false).Documentation.Value != "initial" {
				t.Fatal("rejected resolve poisoned original evidence or cache")
			}
			// Failure is not cached; the same exact row can subsequently enrich docs.
			f.client.completionMu.Lock()
			f.client.resolved = strings.Replace(original, `"documentation":"initial"`, `"detail":"","documentation":{"kind":"markdown","value":"safe docs"}`, 1)
			f.client.completionMu.Unlock()
			got := f.inspect(t, set, 0, true)
			if got.Documentation != (types.LSPCompletionDocumentation{
				HasDetail: true, Present: true, Form: "markup", Kind: "markdown", Value: "safe docs",
			}) {
				t.Fatal("valid documentation-only retry was not accepted", got)
			}
			f.inspect(t, set, 0, true)
			f.client.completionMu.Lock()
			calls := len(f.client.calls)
			f.client.completionMu.Unlock()
			if calls != 3 {
				t.Fatal("successful exact-row documentation was not cached", calls)
			}
			f.unchanged(t, before, history)
		})
	}
	f := newCompletionAcceptanceFixture(t, "abc\n")
	set := f.request(t, `[{"label":"no-resolve"}]`, "view", 1, 0, 1)
	if got := f.inspect(t, set, 0, true); got.ResolveState != "unsupported" {
		t.Fatal("unadvertised resolve was not visibly unsupported", got)
	}
	f.client.completionMu.Lock()
	calls := len(f.client.calls)
	f.client.completionMu.Unlock()
	if calls != 1 {
		t.Fatal("unadvertised resolve reached provider", calls)
	}
}

func TestCompletionResolvedDocumentationAbsenceAndEmptyFormsRemainDistinct(t *testing.T) {
	const original = `{"label":"same","data":{"n":9007199254740993},"detail":"initial","documentation":"initial"}`
	for _, tc := range []struct {
		name, resolved string
		want           types.LSPCompletionDocumentation
	}{
		{"absent", `{"label":"same","data":{"n":9007199254740993}}`, types.LSPCompletionDocumentation{}},
		{"empty-string", `{"label":"same","data":{"n":9007199254740993},"detail":"","documentation":""}`,
			types.LSPCompletionDocumentation{HasDetail: true, Present: true, Form: "string", Kind: "plaintext"}},
		{"empty-plaintext-markup", `{"label":"same","data":{"n":9007199254740993},"documentation":{"kind":"plaintext","value":""}}`,
			types.LSPCompletionDocumentation{Present: true, Form: "markup", Kind: "plaintext"}},
		{"empty-markdown", `{"label":"same","data":{"n":9007199254740993},"documentation":{"kind":"markdown","value":""}}`,
			types.LSPCompletionDocumentation{Present: true, Form: "markup", Kind: "markdown"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newCompletionAcceptanceFixture(t, "abc\n")
			f.client.resolveSupported, f.client.resolved = true, tc.resolved
			set := f.request(t, "["+original+"]", "view", 1, 0, 1)
			if got := f.inspect(t, set, 0, true); got.Documentation != tc.want || got.ResolveState != "resolved" {
				t.Fatal("resolve fabricated or flattened documentation presence/kind", got, tc.want)
			}
			if got := f.inspect(t, set, 0, false); got.Documentation.Value != "initial" || got.Documentation.Detail != "initial" {
				t.Fatal("resolved documentation replaced initial evidence", got)
			}
		})
	}
}

func TestCompletionAcceptancePreflightValidatesAllOriginalEditsWithoutApplying(t *testing.T) {
	for _, tc := range []struct {
		name, adjunct string
		valid         bool
	}{
		{"disjoint", `[{"range":{"start":{"line":1,"character":0},"end":{"line":1,"character":0}},"newText":"$1 literal"}]`, true},
		{"primary-overlap", `[{"range":{"start":{"line":0,"character":0},"end":{"line":0,"character":2}},"newText":"X"}]`, false},
		{"adjunct-overlap", `[{"range":{"start":{"line":1,"character":0},"end":{"line":1,"character":2}},"newText":"X"},{"range":{"start":{"line":1,"character":1},"end":{"line":1,"character":3}},"newText":"Y"}]`, false},
		{"outside-source", `[{"range":{"start":{"line":8,"character":0},"end":{"line":8,"character":0}},"newText":"X"}]`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newCompletionAcceptanceFixture(t, "abc\nxyz\n")
			before, history := f.snapshot(t), f.history()
			set := f.request(t, `[{"label":"complete","additionalTextEdits":`+tc.adjunct+`}]`, "view", 1, 0, 2)
			out, err := f.w.InspectLSPCompletion(t.Context(), f.owner, set.ID, set.Digest, set.Entries[0].ID, false)
			if tc.valid {
				if err != nil || out.ResolveState != "initial" {
					t.Fatal("disjoint original vector failed preflight", out, err)
				}
			} else if err == nil || !reflect.DeepEqual(out, types.LSPCompletionInspection{}) {
				t.Fatal("invalid complete edit vector reached acceptance", out, err)
			}
			f.unchanged(t, before, history)
		})
	}
}

func (f *completionAcceptanceFixture) invalidate(t *testing.T, change string) {
	t.Helper()
	docs := f.w.LiveDocuments
	switch change {
	case "edit", "equal-text-undo":
		snap := f.snapshot(t)
		receipt := documentEdit(t, docs, f.owner, snap.ID, "invalidation", snap.Revision, 0, 0, "X")
		if change == "equal-text-undo" {
			if _, err := docs.Undo(f.owner, snap.ID, "invalidation-undo", receipt.Revision); err != nil {
				t.Fatal(err)
			}
			if after := f.snapshot(t); string(after.Text) != f.disk || after.Revision <= f.source.Revision {
				t.Fatal("fixture did not establish newer equal-text revision", after)
			}
		}
	case "close-reopen":
		if err := docs.CloseView(f.owner, f.source.DocumentID); err != nil {
			t.Fatal(err)
		}
		if _, err := docs.Open(f.owner, f.path); err != nil {
			t.Fatal(err)
		}
	case "detach":
		docs.Detach(f.owner)
	case "owner-epoch":
		next := f.owner
		next.Epoch++
		if _, err := docs.Open(next, f.path); err != nil {
			t.Fatal(err)
		}
	case "context":
		f.w.LSPMgr.NotifyWatchedFilesChanged(t.Context(), filepath.Join(filepath.Dir(f.path), "go.mod"), 2)
	case "settings-cycle":
		settings := f.w.LSPMgr.Settings()
		settings.Completions = false
		f.w.LSPMgr.SetSettings(settings)
		settings.Completions = true
		f.w.LSPMgr.SetSettings(settings)
	case "provider":
		f.w.LSPMgr.SetRuntime(&lifecycleRuntime{})
	case "expiry":
		docs.mu.Lock()
		for _, view := range docs.completionViews {
			view.expires = time.Now().Add(-time.Second)
		}
		docs.mu.Unlock()
	case "shutdown":
		docs.Close()
	default:
		t.Fatal("unknown completion invalidation", change)
	}
}

func TestCompletionCompletedEvidenceRetiresAcrossCanonicalLifetimeBoundaries(t *testing.T) {
	for _, change := range []string{"edit", "equal-text-undo", "close-reopen", "detach", "owner-epoch",
		"context", "settings-cycle", "provider", "expiry", "shutdown"} {
		t.Run(change, func(t *testing.T) {
			f := newCompletionAcceptanceFixture(t, "abc\n")
			set := f.request(t, `[{"label":"x"}]`, "view", 1, 0, 1)
			f.invalidate(t, change)
			for _, resolve := range []bool{false, true} {
				out, err := f.w.InspectLSPCompletion(t.Context(), f.owner, set.ID, set.Digest, set.Entries[0].ID, resolve)
				if err == nil || !reflect.DeepEqual(out, types.LSPCompletionInspection{}) {
					t.Fatal("completed insertion/docs survived lifecycle boundary", change, out, err)
				}
			}
		})
	}
}

func holdCompletionAcceptanceRPC(t *testing.T, client *completionAcceptanceClient) (<-chan completionAcceptanceCall, func()) {
	t.Helper()
	entered, release := make(chan completionAcceptanceCall, 1), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	client.completionMu.Lock()
	client.entered, client.release = entered, release
	client.completionMu.Unlock()
	return entered, unblock
}

func awaitCompletionAcceptanceCall(t *testing.T, entered <-chan completionAcceptanceCall) completionAcceptanceCall {
	t.Helper()
	select {
	case call := <-entered:
		return call
	case <-time.After(5 * time.Second):
		t.Fatal("completion never reached synchronized provider")
		return completionAcceptanceCall{}
	}
}

type completionAcceptanceOutcome struct {
	set types.LSPCompletionSet
	err error
}

func awaitCompletionAcceptanceOutcome(t *testing.T, done <-chan completionAcceptanceOutcome) completionAcceptanceOutcome {
	t.Helper()
	select {
	case out := <-done:
		return out
	case <-time.After(5 * time.Second):
		t.Fatal("released completion call did not retire")
		return completionAcceptanceOutcome{}
	}
}

func TestCompletionLatestViewSequenceWinsAndOldReleaseCannotRetireNewer(t *testing.T) {
	f := newCompletionAcceptanceFixture(t, "abc\n")
	f.client.raw = `[{"label":"first"}]`
	entered, unblock := holdCompletionAcceptanceRPC(t, f.client)
	done := make(chan completionAcceptanceOutcome, 1)
	go func() {
		set, err := f.w.RequestLSPCompletions(t.Context(), f.owner, f.source, "view", 1, 0, 1, types.LSPCompletionContext{})
		done <- completionAcceptanceOutcome{set, err}
	}()
	first := awaitCompletionAcceptanceCall(t, entered)
	f.client.completionMu.Lock()
	f.client.entered, f.client.release = nil, nil
	f.client.completionMu.Unlock()
	second := f.request(t, `[{"label":"second","documentation":"new"}]`, "view", 2, 0, 1)
	select {
	case <-first.ctx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("newer view sequence did not cancel old provider request")
	}
	if err := f.w.LiveDocuments.ReleaseLSPCompletions(f.owner, "view", 1); err != nil {
		t.Fatal(err)
	}
	if got := f.inspect(t, second, 0, false); got.Documentation.Value != "new" {
		t.Fatal("delayed release retired newer set", got)
	}
	unblock()
	if got := awaitCompletionAcceptanceOutcome(t, done); got.err == nil || !reflect.DeepEqual(got.set, types.LSPCompletionSet{}) {
		t.Fatal("late old request replaced newer set", got)
	}
	for _, sequence := range []uint64{1, 2} {
		if set, err := f.w.RequestLSPCompletions(t.Context(), f.owner, f.source, "view", sequence, 0, 1, types.LSPCompletionContext{}); err == nil ||
			!reflect.DeepEqual(set, types.LSPCompletionSet{}) {
			t.Fatal("nonmonotonic request replayed provider evidence", sequence, set, err)
		}
	}
	if err := f.w.LiveDocuments.ReleaseLSPCompletions(f.owner, "view", 2); err != nil {
		t.Fatal(err)
	}
	if _, err := f.w.InspectLSPCompletion(t.Context(), f.owner, second.ID, second.Digest, second.Entries[0].ID, false); err == nil {
		t.Fatal("released set remained acceptable")
	}
	if _, err := f.w.RequestLSPCompletions(t.Context(), f.owner, f.source, "view", 2, 0, 1, types.LSPCompletionContext{}); err == nil {
		t.Fatal("exact release lost sequence tombstone")
	}
	if third := f.request(t, "[]", "view", 3, 0, 1); third.Answer != "empty" || third.Sequence != 3 {
		t.Fatal("newer sequence could not replace retired invocation", third)
	}
}

func TestCompletionLatePositiveAndAbsenceAnswersCannotCrossLifetimeFences(t *testing.T) {
	for _, answer := range []string{`[{"label":"late"}]`, "null", "[]", "unsupported"} {
		for _, change := range []string{"cancel", "release", "edit", "equal-text-undo", "close-reopen",
			"detach", "owner-epoch", "context", "settings-cycle", "provider"} {
			t.Run(answer+"/"+change, func(t *testing.T) {
				f := newCompletionAcceptanceFixture(t, "abc\n")
				f.client.raw = answer
				if answer == "unsupported" {
					f.client.raw, f.client.unsupported = "", true
				}
				entered, unblock := holdCompletionAcceptanceRPC(t, f.client)
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				done := make(chan completionAcceptanceOutcome, 1)
				go func() {
					set, err := f.w.RequestLSPCompletions(ctx, f.owner, f.source, "view", 1, 0, 1, types.LSPCompletionContext{})
					done <- completionAcceptanceOutcome{set, err}
				}()
				awaitCompletionAcceptanceCall(t, entered)
				switch change {
				case "cancel":
					cancel()
				case "release":
					if err := f.w.LiveDocuments.ReleaseLSPCompletions(f.owner, "view", 1); err != nil {
						t.Fatal(err)
					}
				default:
					f.invalidate(t, change)
				}
				unblock()
				if got := awaitCompletionAcceptanceOutcome(t, done); got.err == nil || !reflect.DeepEqual(got.set, types.LSPCompletionSet{}) {
					t.Fatal("cancellation-ignoring adapter leaked stale result", answer, change, got)
				}
			})
		}
	}
}

func TestCompletionLateResolveCannotPublishOrCacheAfterRetirement(t *testing.T) {
	for _, change := range []string{"release", "newer-view", "edit", "equal-text-undo", "owner-epoch",
		"context", "settings-cycle", "provider", "expiry"} {
		t.Run(change, func(t *testing.T) {
			f := newCompletionAcceptanceFixture(t, "abc\n")
			const original = `{"label":"same","data":{"n":9007199254740993}}`
			f.client.resolveSupported = true
			set := f.request(t, "["+original+"]", "view", 1, 0, 1)
			f.client.resolved = strings.TrimSuffix(original, "}") + `,"documentation":"late docs"}`
			entered, unblock := holdCompletionAcceptanceRPC(t, f.client)
			type outcome struct {
				out types.LSPCompletionInspection
				err error
			}
			done := make(chan outcome, 1)
			go func() {
				out, err := f.w.InspectLSPCompletion(t.Context(), f.owner, set.ID, set.Digest, set.Entries[0].ID, true)
				done <- outcome{out, err}
			}()
			call := awaitCompletionAcceptanceCall(t, entered)
			if !call.resolve || call.original != original || call.wire != set.WireVersion {
				t.Fatal("resolve gate lost exact original object", call)
			}
			switch change {
			case "release":
				if err := f.w.LiveDocuments.ReleaseLSPCompletions(f.owner, "view", 1); err != nil {
					t.Fatal(err)
				}
			case "newer-view":
				f.client.completionMu.Lock()
				f.client.entered, f.client.release = nil, nil
				f.client.completionMu.Unlock()
				f.request(t, `[{"label":"new"}]`, "view", 2, 0, 1)
			default:
				f.invalidate(t, change)
			}
			unblock()
			select {
			case got := <-done:
				if got.err == nil || !reflect.DeepEqual(got.out, types.LSPCompletionInspection{}) {
					t.Fatal("retired resolve returned documentation", change, got)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("released resolve did not retire")
			}
			f.w.LiveDocuments.mu.Lock()
			cached := 0
			for _, view := range f.w.LiveDocuments.completionViews {
				cached += len(view.resolved)
			}
			f.w.LiveDocuments.mu.Unlock()
			if cached != 0 {
				t.Fatal("late resolve cached stale display evidence", cached)
			}
		})
	}
}

func TestCompletionActiveViewsAndReplayTrackingHaveConstantSeparateCaps(t *testing.T) {
	f := newCompletionAcceptanceFixture(t, "abc\n")
	for i := 0; i < 16; i++ {
		f.request(t, "[]", fmt.Sprintf("view-%d", i), 1, 0, 1)
	}
	if set, err := f.w.RequestLSPCompletions(t.Context(), f.owner, f.source, "active-17", 1, 0, 1, types.LSPCompletionContext{}); err == nil ||
		!reflect.DeepEqual(set, types.LSPCompletionSet{}) {
		t.Fatal("seventeenth active view exceeded cap16", set, err)
	}
	for i := 0; i < 128; i++ {
		view := fmt.Sprintf("view-%d", i)
		if i >= 16 {
			f.request(t, "[]", view, 1, 0, 1)
		}
		if err := f.w.LiveDocuments.ReleaseLSPCompletions(f.owner, view, 1); err != nil {
			t.Fatal(err)
		}
	}
	if set, err := f.w.RequestLSPCompletions(t.Context(), f.owner, f.source, "tracking-129", 1, 0, 1, types.LSPCompletionContext{}); err == nil ||
		!reflect.DeepEqual(set, types.LSPCompletionSet{}) {
		t.Fatal("129th replay-tracked view exceeded cap128", set, err)
	}
	if got := f.request(t, "null", "view-0", 2, 0, 1); got.Answer != "null" {
		t.Fatal("retired sequence consumed active capacity", got)
	}
	f.w.LiveDocuments.mu.Lock()
	count := len(f.w.LiveDocuments.completionViews)
	f.w.LiveDocuments.mu.Unlock()
	if count != 128 {
		t.Fatal("tracking map did not retain exact bounded tombstones", count)
	}
	// Expiry reclaims both active data and replay tracking without sleeping.
	f.invalidate(t, "expiry")
	f.request(t, "[]", "after-expiry", 1, 0, 1)
	f.w.LiveDocuments.mu.Lock()
	count = len(f.w.LiveDocuments.completionViews)
	f.w.LiveDocuments.mu.Unlock()
	if count != 1 {
		t.Fatal("expired sequence tracking was not reclaimed", count)
	}
}

func TestCompletionRawAndRetainedByteBoundsReleaseAndCloseReclaimEvidence(t *testing.T) {
	f := newCompletionAcceptanceFixture(t, "abc\n")
	// Real bounded provider rows fill both normalized display and opaque originals.
	item := `{"label":"x","documentation":"` + strings.Repeat("d", 16<<10) +
		`","data":"` + strings.Repeat("o", 32<<10) + `"}`
	raw := "[" + strings.TrimSuffix(strings.Repeat(item+",", 16), ",") + "]"
	if !json.Valid([]byte(raw)) || len(raw) >= 1<<20 {
		t.Fatal("byte-bound workload is not valid sub-1MiB provider JSON")
	}
	f.client.raw = raw
	retained := func() (int, int) {
		s := f.w.LiveDocuments
		s.mu.Lock()
		defer s.mu.Unlock()
		total := 0
		for _, view := range s.completionViews {
			total += view.bytes
		}
		return total, len(s.completionViews)
	}
	accepted, rejected := 0, false
	for i := 0; i < 16; i++ {
		set, err := f.w.RequestLSPCompletions(t.Context(), f.owner, f.source, fmt.Sprintf("bytes-%d", i), 1, 0, 1, types.LSPCompletionContext{})
		if err != nil {
			if !reflect.DeepEqual(set, types.LSPCompletionSet{}) || !strings.Contains(err.Error(), "16 MiB") {
				t.Fatal("workload failed before aggregate retained-byte cap", set, err)
			}
			rejected = true
			break
		}
		accepted++
	}
	before, _ := retained()
	if !rejected || accepted < 2 || before <= 14<<20 || before > 16<<20 {
		t.Fatal("retained display+opaque evidence is not bounded at 16MiB", accepted, rejected, before)
	}
	if err := f.w.LiveDocuments.ReleaseLSPCompletions(f.owner, "bytes-0", 1); err != nil {
		t.Fatal(err)
	}
	after, _ := retained()
	if after >= before {
		t.Fatal("release retained original/display byte accounting", before, after)
	}
	f.client.raw = `[{"label":"too-large","data":"x"}]` + strings.Repeat(" ", 1<<20)
	if set, err := f.w.RequestLSPCompletions(t.Context(), f.owner, f.source, "oversize", 1, 0, 1, types.LSPCompletionContext{}); err == nil ||
		!reflect.DeepEqual(set, types.LSPCompletionSet{}) {
		t.Fatal("over-1MiB provider JSON acquired retained evidence", set, err)
	}
	if total, _ := retained(); total != after {
		t.Fatal("failed oversize response retained bytes", total, after)
	}
	if err := f.w.LiveDocuments.CloseView(f.owner, f.source.DocumentID); err != nil {
		t.Fatal(err)
	}
	if total, count := retained(); total != 0 || count != 0 {
		t.Fatal("close did not reclaim original/display evidence and tombstones", total, count)
	}
}

func TestCompletionCapabilitiesAndOldServerIntentFence(t *testing.T) {
	fixture := newCompletionAcceptanceFixture(t, "unit")
	manager := fixture.w.LSPMgr
	caps := manager.AssistanceCapabilities(fixture.path)
	if !caps.Completion || caps.Producer.InstanceID == "" {
		t.Fatal("initialized file has no capabilities", caps)
	}
	caps.CompletionTriggers[0] = "corrupt"
	if manager.AssistanceCapabilities(fixture.path).CompletionTriggers[0] != "." {
		t.Fatal("capabilities aliased the manager")
	}
	old := caps.Producer
	old.InstanceID = "retired-instance"
	result, err := fixture.w.RequestLSPCompletions(t.Context(), fixture.owner, fixture.source, "old-server", 1, 0, 4, types.LSPCompletionContext{Producer: old, TriggerKind: 2, TriggerCharacter: "."})
	fixture.client.completionMu.Lock()
	calls := len(fixture.client.calls)
	fixture.client.completionMu.Unlock()
	if err == nil || result.ID != "" || calls != 0 {
		t.Fatal("retired-server trigger queried or published a replacement answer", result, err, calls)
	}
	manager.Stop()
	if manager.AssistanceCapabilities(fixture.path).Completion || len(manager.AssistanceSnapshots()) != 0 {
		t.Fatal("Stop retained capabilities")
	}
}
