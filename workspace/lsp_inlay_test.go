package workspace

import (
	"bytes"
	"context"
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

// Like the navigation fixture, this client really acknowledges didOpen/change.
// It lies about provenance and deliberately ignores cancellation at RPC gates.
type inlayAcceptanceClient struct {
	lifecycleClient
	inlayMu          sync.Mutex
	raw, resolved    string
	unsupported      bool
	resolveSupported bool
	wireDelta        int
	err              error
	calls            []inlayAcceptanceCall
	entered          chan<- inlayAcceptanceCall
	release          <-chan struct{}
}

type inlayAcceptanceCall struct {
	ctx      context.Context
	path     string
	span     types.LSPInlayRange
	resolve  bool
	wire     int
	original string
}

func (c *inlayAcceptanceClient) answer(call inlayAcceptanceCall) (types.LSPInlayResponse, error) {
	c.mu.Lock()
	wire := c.versions[call.path]
	c.mu.Unlock()
	c.inlayMu.Lock()
	c.calls = append(c.calls, call)
	raw := c.raw
	if call.resolve {
		raw = c.resolved
	}
	response := types.LSPInlayResponse{
		Boundary: types.InsightBoundary{
			Source: types.InsightSource{Path: "forged-adapter-path", Kind: "disk"},
		},
		WireVersion: wire + c.wireDelta, JSON: []byte(raw),
		Unsupported: c.unsupported, ResolveSupported: c.resolveSupported,
	}
	err, entered, release := c.err, c.entered, c.release
	c.inlayMu.Unlock()
	if entered != nil {
		entered <- call
	}
	if release != nil {
		<-release
	}
	return response, err
}

func (c *inlayAcceptanceClient) RequestInlayHints(ctx context.Context, path string, span types.LSPInlayRange) (types.LSPInlayResponse, error) {
	return c.answer(inlayAcceptanceCall{ctx: ctx, path: path, span: span})
}

func (c *inlayAcceptanceClient) ResolveInlayHint(ctx context.Context, path string, wire int, original []byte) (types.LSPInlayResponse, error) {
	return c.answer(inlayAcceptanceCall{ctx: ctx, path: path, wire: wire, original: string(original), resolve: true})
}

type inlayAcceptanceRuntime struct{ client *inlayAcceptanceClient }

func (r inlayAcceptanceRuntime) NewClient(_ types.LSPServerSpec, _ string, admission func() error) LSPClient {
	r.client.startAdmission = admission
	return r.client
}

type inlayAcceptanceFixture struct {
	w      *Workspace
	client *inlayAcceptanceClient
	owner  document.Owner
	source types.InsightSource
	path   string
	disk   string
}

func newInlayAcceptanceFixture(t *testing.T, text string) *inlayAcceptanceFixture {
	t.Helper()
	root := t.TempDir()
	c := &inlayAcceptanceClient{lifecycleClient: lifecycleClient{
		runtime: &lifecycleRuntime{}, reapableClient: reapableClient{done: make(chan struct{})},
	}, raw: "[]"}
	m := NewLSPManager(nil, inlayAcceptanceRuntime{c})
	trustAnalysis(t, m, root)
	settings := m.Settings()
	settings.InlayHints = true // Default-off is not permission to infer support.
	m.SetSettings(settings)
	t.Cleanup(m.Close)
	db, err := openWorkspaceDBAt(filepath.Join(t.TempDir(), "workspace.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	docs := NewDocumentService(root, nil, m, db.DB)
	t.Cleanup(docs.Close)
	path := filepath.Join(root, "main.go")
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	owner := document.Owner{ClientID: "inlay-owner", Epoch: 1}
	snap, err := docs.Open(owner, path)
	if err != nil {
		t.Fatal(err)
	}
	waitDiagnosticSend(t, m, &c.lifecycleClient, snap)
	docs.mu.Lock()
	source := docs.analysisIdentityLocked(docs.docs[snap.ID])
	docs.mu.Unlock()
	// No save service or Local History authority is needed for observation.
	return &inlayAcceptanceFixture{&Workspace{LiveDocuments: docs, LSPMgr: m}, c, owner, source, path, text}
}

func inlayAcceptanceRange(startLine, startCol, endLine, endCol int) types.LSPInlayRange {
	return types.LSPInlayRange{
		Start: types.LSPInlayPosition{Line: startLine, Character: startCol},
		End:   types.LSPInlayPosition{Line: endLine, Character: endCol},
	}
}

func (f *inlayAcceptanceFixture) request(t *testing.T, raw, view string, sequence uint64, span types.LSPInlayRange) types.LSPInlaySet {
	t.Helper()
	f.client.inlayMu.Lock()
	f.client.raw = raw
	f.client.inlayMu.Unlock()
	set, err := f.w.RequestLSPInlayHints(t.Context(), f.owner, f.source, view, sequence, span)
	if err != nil {
		t.Fatal(err)
	}
	return set
}

func (f *inlayAcceptanceFixture) snapshot(t *testing.T) document.Snapshot {
	t.Helper()
	snap, err := f.w.LiveDocuments.Snapshot(f.source.DocumentID)
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

func (f *inlayAcceptanceFixture) history() string {
	s := f.w.LiveDocuments
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.docs[f.source.DocumentID]
	return fmt.Sprintf("%#v / %d", d.journal, d.historyBytes)
}

func (f *inlayAcceptanceFixture) unchanged(t *testing.T, before document.Snapshot, history string) {
	t.Helper()
	disk, err := os.ReadFile(f.path)
	if err != nil || string(disk) != f.disk {
		t.Fatalf("inlay observation changed literal disk bytes: %q %v", disk, err)
	}
	if after := f.snapshot(t); !reflect.DeepEqual(after, before) {
		t.Fatalf("observation changed text/revision/savepoint: before=%+v after=%+v", before, after)
	}
	if after := f.history(); after != history {
		t.Fatalf("observation changed canonical undo history: before=%s after=%s", history, after)
	}
	for _, call := range f.client.snapshot() {
		if strings.HasPrefix(call, "willSave:") || strings.HasPrefix(call, "didSave:") {
			t.Fatal("read-only observation issued a save", call)
		}
	}
}

func (f *inlayAcceptanceFixture) inspect(t *testing.T, set types.LSPInlaySet, index int, resolve bool) types.LSPInlayInspection {
	t.Helper()
	out, err := f.w.InspectLSPInlayHint(t.Context(), f.owner, set.ID, set.Digest, index, resolve)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestInlayCanonicalUnsavedUTF16BytesOrderAndClosedEndAreReadOnly(t *testing.T) {
	f := newInlayAcceptanceFixture(t, "disk\r\n")
	documentEdit(t, f.w.LiveDocuments, f.owner, f.source.DocumentID, "unsaved", f.source.Revision,
		0, 6, "a😀e\u0301\r\nz\r\n")
	before := f.snapshot(t)
	if !before.Dirty || string(before.Saved) != "disk\r\n" || string(before.Text) != "a😀e\u0301\r\nz\r\n" {
		t.Fatal("fixture did not establish acknowledged unsaved source", before)
	}
	waitDiagnosticSend(t, f.w.LSPMgr, &f.client.lifecycleClient, before)
	f.w.LiveDocuments.mu.Lock()
	f.source = f.w.LiveDocuments.analysisIdentityLocked(f.w.LiveDocuments.docs[before.ID])
	f.w.LiveDocuments.mu.Unlock()
	history := f.history()
	span := inlayAcceptanceRange(0, 0, 2, 0)
	// Character 4 is between e and its combining accent. That is an exact UTF-8
	// boundary; grapheme-safe decoration placement belongs to the renderer.
	raw := `[
{"position":{"line":0,"character":3},"label":"first"},
{"position":{"line":0,"character":3},"label":"second"},
{"position":{"line":0,"character":4},"label":"inside-grapheme"},
{"position":{"line":1,"character":0},"label":"next"},
{"position":{"line":2,"character":0},"label":"EOF"}]`
	set := f.request(t, raw, "editor-a", 1, span)
	if set.Answer != "hints" || set.ID == "" || len(set.Digest) != 64 || set.ViewID != "editor-a" ||
		set.Sequence != 1 || set.Range != span || set.Boundary.Source != f.source ||
		set.Boundary.Producer.InstanceID == "" || set.Boundary.Context.Generation == 0 ||
		len(set.Boundary.Context.UnknownInputs) == 0 || set.WireVersion != 2 {
		t.Fatalf("lost canonical source/provider/context/wire qualification: %+v", set)
	}
	if len(set.Hints) != 5 {
		t.Fatalf("want five hints in original provider order: %+v", set.Hints)
	}
	for i, want := range []struct {
		label string
		off   int
	}{{"first", 5}, {"second", 5}, {"inside-grapheme", 6}, {"next", 10}, {"EOF", 13}} {
		if hint := set.Hints[i]; hint.Index != i || hint.Label != want.label || hint.SourceByte != want.off {
			t.Fatalf("hint %d changed exact byte anchor/order: %+v, want %+v", i, hint, want)
		}
	}
	f.client.inlayMu.Lock()
	call := f.client.calls[0]
	f.client.inlayMu.Unlock()
	if call.path != f.path || call.span != span || call.resolve {
		t.Fatal("wire query changed caller's literal UTF-16 interval", call)
	}
	eof := f.request(t, `[{"position":{"line":2,"character":0},"label":"end"}]`,
		"eof-only", 1, inlayAcceptanceRange(2, 0, 2, 0))
	if len(eof.Hints) != 1 || eof.Hints[0].SourceByte != 13 {
		t.Fatal("closed anchor interval refused zero-length EOF", eof)
	}
	f.unchanged(t, before, history)
}

func TestInlayUnsupportedNullAndEmptyAreRetainedPartialEvidence(t *testing.T) {
	for _, tc := range []struct{ answer, raw string }{{"unsupported", ""}, {"null", "null"}, {"empty", "[]"}} {
		t.Run(tc.answer, func(t *testing.T) {
			f := newInlayAcceptanceFixture(t, "a\n")
			before, history := f.snapshot(t), f.history()
			f.client.unsupported = tc.answer == "unsupported"
			set := f.request(t, tc.raw, "view", 1, inlayAcceptanceRange(0, 0, 1, 0))
			if set.Answer != tc.answer || len(set.Hints) != 0 || set.ID == "" || len(set.Digest) != 64 ||
				set.Boundary.Source != f.source || set.Boundary.Producer.InstanceID == "" ||
				set.Boundary.Context.Generation == 0 || len(set.Boundary.Context.UnknownInputs) == 0 ||
				set.ExpiresAt.IsZero() || set.Detail == "" {
				t.Fatal("negative response lost bounded, partial canonical evidence", set)
			}
			f.w.LiveDocuments.mu.Lock()
			retained := f.w.LiveDocuments.inlayViews[inlayViewKey{f.owner, "view"}]
			retainedID := retained.view.ID
			f.w.LiveDocuments.mu.Unlock()
			if retainedID != set.ID {
				t.Fatal("negative evidence was not retained")
			}
			if strings.Contains(strings.ToLower(set.Detail), "project is clean") {
				t.Fatal("range absence invented project cleanliness", set.Detail)
			}
			if out, err := f.w.InspectLSPInlayHint(t.Context(), f.owner, set.ID, set.Digest, 0, true); err == nil ||
				!reflect.DeepEqual(out, types.LSPInlayInspection{}) {
				t.Fatal("negative answer invented an inspectable hint", out, err)
			}
			f.unchanged(t, before, history)
		})
	}
}

func TestInlayMalformedProviderAnswersFailAtomically(t *testing.T) {
	for _, tc := range []struct{ name, raw string }{
		{"duplicate", `[{"position":{"line":0,"character":0},"label":"a","label":"b"}]`},
		{"nested-duplicate", `[{"position":{"line":0,"character":0},"label":"a","data":{"n":1,"n":2}}]`},
		{"surrogate", `[{"position":{"line":0,"character":0},"label":"\ud800"}]`},
		{"float", `[{"position":{"line":0.0,"character":0},"label":"a"}]`},
		{"missing-position", `[{"label":"a"}]`},
		{"missing-label", `[{"position":{"line":0,"character":0}}]`},
		{"missing-coordinate", `[{"position":{"line":0},"label":"a"}]`},
		{"outside-range", `[{"position":{"line":0,"character":0},"label":"a"}]`},
		{"split-surrogate", `[{"position":{"line":0,"character":2},"label":"a"}]`},
		{"outside-source", `[{"position":{"line":7,"character":0},"label":"a"}]`},
		{"partial-list", `[{"position":{"line":0,"character":3},"label":"ok"},{"label":"bad"}]`},
		{"trailing", `[{"position":{"line":0,"character":3},"label":"a"}] true`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newInlayAcceptanceFixture(t, "a😀e\u0301\r\nz\r\n")
			before, history := f.snapshot(t), f.history()
			f.client.raw = tc.raw
			span := inlayAcceptanceRange(0, 0, 2, 0)
			if tc.name == "outside-range" {
				span.Start.Character = 3
			}
			set, err := f.w.RequestLSPInlayHints(t.Context(), f.owner, f.source, "view", 1, span)
			if err == nil || !reflect.DeepEqual(set, types.LSPInlaySet{}) {
				t.Fatal("malformed response leaked qualified or partial evidence", set, err)
			}
			f.w.LiveDocuments.mu.Lock()
			entry := f.w.LiveDocuments.inlayViews[inlayViewKey{f.owner, "view"}]
			tombstone := entry != nil && entry.view.ID == "" && entry.rawBytes == 0 && entry.ctx.Err() != nil
			f.w.LiveDocuments.mu.Unlock()
			if !tombstone {
				t.Fatal("failed provider response retained active or raw evidence")
			}
			f.unchanged(t, before, history)
		})
	}
}

func TestInlaySourceRangeSettingsTrustAndWireAdmission(t *testing.T) {
	if types.DefaultLSPSettings().InlayHints {
		t.Fatal("inlay hints must be explicitly enabled")
	}
	for _, invalid := range []string{"owner", "revision", "lifetime", "path", "noncanonical", "conflict",
		"negative", "split-surrogate", "reversed", "outside-source", "view", "sequence", "cancel",
		"settings", "trust", "unsynchronized", "wrong-wire", "unsupported-data"} {
		t.Run(invalid, func(t *testing.T) {
			f := newInlayAcceptanceFixture(t, "a😀z\n")
			owner, source, view, sequence := f.owner, f.source, "view", uint64(1)
			span := inlayAcceptanceRange(0, 0, 1, 0)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			switch invalid {
			case "owner":
				owner.ClientID = "foreign"
			case "revision":
				source.Revision++
			case "lifetime":
				source.Lifetime += "-forged"
			case "path":
				source.Path += "-forged"
			case "noncanonical":
				source.Kind = "disk"
			case "conflict":
				f.w.LiveDocuments.mu.Lock()
				f.w.LiveDocuments.docs[source.DocumentID].snapshot.Conflict = true
				f.w.LiveDocuments.mu.Unlock()
			case "negative":
				span.Start.Line = -1
			case "split-surrogate":
				span.Start.Character = 2
			case "reversed":
				span = inlayAcceptanceRange(1, 0, 0, 0)
			case "outside-source":
				span.End.Line = 9
			case "view":
				view = "bad\nview"
			case "sequence":
				sequence = 0
			case "cancel":
				cancel()
			case "settings":
				settings := f.w.LSPMgr.Settings()
				settings.InlayHints = false
				f.w.LSPMgr.SetSettings(settings)
			case "trust":
				f.w.LSPMgr.SetAnalysisPolicy(types.DefaultWorkspaceAnalysisSettings(), "")
			case "unsynchronized":
				f.w.LSPMgr.mu.Lock()
				f.w.LSPMgr.documents[f.path].syncPending = true
				f.w.LSPMgr.mu.Unlock()
			case "wrong-wire":
				f.client.wireDelta = 1
			case "unsupported-data":
				f.client.unsupported = true // A fabricated [] is not unsupported.
			}
			set, err := f.w.RequestLSPInlayHints(ctx, owner, source, view, sequence, span)
			if err == nil || !reflect.DeepEqual(set, types.LSPInlaySet{}) {
				t.Fatal("invalid admission returned qualified evidence", set, err)
			}
			f.client.inlayMu.Lock()
			calls := len(f.client.calls)
			f.client.inlayMu.Unlock()
			wantCalls := 0
			if invalid == "wrong-wire" || invalid == "unsupported-data" {
				wantCalls = 1
			}
			if calls != wantCalls {
				t.Fatalf("inadmissible query probed provider: calls=%d want=%d", calls, wantCalls)
			}
		})
	}
}

func TestInlayInspectRetainsOriginalJSONAndDetachedNestedData(t *testing.T) {
	f := newInlayAcceptanceFixture(t, "abc\n")
	before, history := f.snapshot(t), f.history()
	original := `{ "position" : {"line":0,"character":1}, "label":[{"value":"x","tooltip":"old","location":{"uri":"file:///not-opened.go","range":{"start":{"line":9,"character":0},"end":{"line":9,"character":1}}},"command":{"title":"never run","command":"danger.run","arguments":[1e0]}}], "data":{"n":9007199254740993,"spelling":1e0}, "textEdits":[{"range":{"start":{"line":0,"character":0},"end":{"line":0,"character":0}},"newText":"NEVER"}], "extra":{"n":1.00} }`
	set := f.request(t, "["+original+"]", "view", 1, inlayAcceptanceRange(0, 0, 1, 0))
	if len(set.Hints) != 1 || !set.Hints[0].HasData || !set.Hints[0].HasTextEdits ||
		!set.Hints[0].Parts[0].HasCommand {
		t.Fatal("inert original metadata was lost", set)
	}
	set.Hints[0].Label = "forged"
	set.Hints[0].Parts[0].Value = "forged"
	set.Hints[0].Parts[0].Location.URI = "file:///forged"
	set.Boundary.Context.UnknownInputs[0] = "forged"
	first := f.inspect(t, set, 0, false)
	if string(first.OriginalJSON) != original || first.Hint.Label != "x" || first.Hint.SourceByte != 1 ||
		first.Hint.Parts[0].Location.URI != "file:///not-opened.go" || first.ResolveState != "not-requested" ||
		first.Boundary.Context.UnknownInputs[0] == "forged" {
		t.Fatal("caller presentation replaced original provider evidence", first)
	}
	first.OriginalJSON[0] = '!'
	first.Hint.Parts[0].Location.Range.Start.Line = 77
	first.Boundary.Context.UnknownInputs[0] = "mutated-inspection"
	second := f.inspect(t, set, 0, false)
	if string(second.OriginalJSON) != original || second.Hint.Parts[0].Location.Range.Start.Line != 9 ||
		second.Boundary.Context.UnknownInputs[0] == "mutated-inspection" {
		t.Fatal("inspection leaked nested retained storage", second)
	}
	for _, forged := range []string{"owner", "id", "digest", "index"} {
		owner, id, digest, index := f.owner, set.ID, set.Digest, 0
		switch forged {
		case "owner":
			owner.ClientID = "foreign"
		case "id":
			id += "-forged"
		case "digest":
			digest = strings.Repeat("0", 64)
		case "index":
			index = 1
		}
		out, err := f.w.InspectLSPInlayHint(t.Context(), owner, id, digest, index, true)
		if err == nil || !reflect.DeepEqual(out, types.LSPInlayInspection{}) {
			t.Fatal("forged identity acquired retained evidence", forged, out, err)
		}
	}
	f.client.inlayMu.Lock()
	calls := len(f.client.calls)
	f.client.inlayMu.Unlock()
	if calls != 1 {
		t.Fatal("local or forged inspection dispatched provider resolve", calls)
	}
	f.unchanged(t, before, history)
}

func TestInlayResolveOnlyTooltipsFromExactOriginalProviderObject(t *testing.T) {
	f := newInlayAcceptanceFixture(t, "abc\n")
	before, history := f.snapshot(t), f.history()
	original := `{ "position":{"line":0,"character":1}, "label":[{"value":"x","command":{"title":"inert","command":"never.run"}}], "data":{"n":9007199254740993,"spelling":1e0}, "extra":{"n":1.00} }`
	resolved := `{"position":{"line":0,"character":1},"label":[{"value":"x","tooltip":"part docs","command":{"title":"inert","command":"never.run"}}],"data":{"n":9007199254740993,"spelling":1e0},"extra":{"n":1.00},"tooltip":{"kind":"markdown","value":"**top docs**"}}`
	f.client.resolveSupported, f.client.resolved = true, resolved
	set := f.request(t, "["+original+"]", "view", 1, inlayAcceptanceRange(0, 0, 1, 0))
	out := f.inspect(t, set, 0, true)
	if out.ResolveState != "resolved-tooltips" || string(out.OriginalJSON) != original ||
		string(out.ResolvedJSON) != resolved || out.Hint.Label != "x" || out.Hint.SourceByte != 1 ||
		out.Hint.Tooltip.Value != "**top docs**" || out.Hint.Tooltip.Kind != "markdown" ||
		out.Hint.Parts[0].Tooltip.Value != "part docs" {
		t.Fatal("tooltip-only resolution lost original or literal documentation", out)
	}
	f.client.inlayMu.Lock()
	calls := append([]inlayAcceptanceCall(nil), f.client.calls...)
	f.client.inlayMu.Unlock()
	if len(calls) != 2 || !calls[1].resolve || calls[1].path != f.path ||
		calls[1].original != original || calls[1].wire != 1 {
		t.Fatal("resolve did not send exact retained original and synchronized wire", calls)
	}
	out.ResolvedJSON[0] = '!'
	// Resolution does not rewrite the original retained summary.
	if again := f.inspect(t, set, 0, false); again.Hint.Tooltip.Present || again.Hint.Parts[0].Tooltip.Present {
		t.Fatal("resolved observations mutated original retained object", again)
	}
	f.unchanged(t, before, history)
}

func TestInlayResolveRejectsNonTooltipChangesAndUnadvertisedDispatch(t *testing.T) {
	original := `{"position":{"line":0,"character":1},"label":[{"value":"x","command":{"title":"inert","command":"never.run"}}],"data":{"n":1e0},"extra":{"n":1.00},"textEdits":[]}`
	for _, change := range []string{"label", "position", "command", "edit", "data", "unknown", "unsupported"} {
		t.Run(change, func(t *testing.T) {
			f := newInlayAcceptanceFixture(t, "abc\n")
			f.client.resolveSupported = change != "unsupported"
			resolved := strings.TrimSuffix(original, "}") + `,"tooltip":"docs"}`
			switch change {
			case "label":
				resolved = strings.Replace(resolved, `"value":"x"`, `"value":"other"`, 1)
			case "position":
				resolved = strings.Replace(resolved, `"character":1`, `"character":2`, 1)
			case "command":
				resolved = strings.Replace(resolved, `"never.run"`, `"changed.run"`, 1)
			case "edit":
				resolved = strings.Replace(resolved, `"textEdits":[]`, `"textEdits":[{"range":{"start":{"line":0,"character":0},"end":{"line":0,"character":0}},"newText":"BAD"}]`, 1)
			case "data":
				resolved = strings.Replace(resolved, `"n":1e0`, `"n":1.0`, 1)
			case "unknown":
				resolved = strings.Replace(resolved, `"n":1.00`, `"n":1`, 1)
			}
			f.client.resolved = resolved
			before, history := f.snapshot(t), f.history()
			set := f.request(t, "["+original+"]", "view", 1, inlayAcceptanceRange(0, 0, 1, 0))
			out, err := f.w.InspectLSPInlayHint(t.Context(), f.owner, set.ID, set.Digest, 0, true)
			if change == "unsupported" {
				if err != nil || out.ResolveState != "unsupported" || string(out.OriginalJSON) != original ||
					len(out.ResolvedJSON) != 0 {
					t.Fatal("unadvertised resolve lost explicit unsupported evidence", out, err)
				}
			} else if err == nil || !reflect.DeepEqual(out, types.LSPInlayInspection{}) {
				t.Fatal("resolve changed non-tooltip authority", change, out, err)
			}
			f.client.inlayMu.Lock()
			calls := len(f.client.calls)
			f.client.inlayMu.Unlock()
			if change == "unsupported" && calls != 1 {
				t.Fatal("unadvertised resolve dispatched RPC")
			}
			f.unchanged(t, before, history)
		})
	}
}

func (f *inlayAcceptanceFixture) invalidate(t *testing.T, change string) {
	t.Helper()
	s := f.w.LiveDocuments
	switch change {
	case "edit", "equal-text-undo":
		snap := f.snapshot(t)
		receipt := documentEdit(t, s, f.owner, snap.ID, "invalidation", snap.Revision, 0, 0, "X")
		if change == "equal-text-undo" {
			if _, err := s.Undo(f.owner, snap.ID, "invalidation-undo", receipt.Revision); err != nil {
				t.Fatal(err)
			}
			if after := f.snapshot(t); string(after.Text) != "abc\n" || after.Revision <= f.source.Revision {
				t.Fatal("fixture failed to produce newer equal-text revision", after)
			}
		}
	case "context":
		f.w.LSPMgr.NotifyWatchedFilesChanged(t.Context(), filepath.Join(filepath.Dir(f.path), "go.mod"), 2)
	case "settings":
		settings := f.w.LSPMgr.Settings()
		settings.InlayHints = false
		f.w.LSPMgr.SetSettings(settings)
	case "trust":
		f.w.LSPMgr.SetAnalysisPolicy(types.DefaultWorkspaceAnalysisSettings(), "")
	case "close-reopen":
		if err := s.CloseView(f.owner, f.source.DocumentID); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Open(f.owner, f.path); err != nil {
			t.Fatal(err)
		}
	case "detach":
		s.Detach(f.owner)
	case "provider":
		f.w.LSPMgr.SetRuntime(&lifecycleRuntime{})
	case "expiry":
		s.mu.Lock()
		for _, entry := range s.inlayViews {
			entry.expires = time.Now().Add(-time.Second)
		}
		s.mu.Unlock()
	case "shutdown":
		s.Close()
	default:
		t.Fatal("unknown invalidation", change)
	}
}

func TestInlayCompletedEvidenceIsRevokedByLifecycle(t *testing.T) {
	for _, change := range []string{"edit", "equal-text-undo", "context", "settings", "trust",
		"close-reopen", "detach", "provider", "expiry", "shutdown"} {
		t.Run(change, func(t *testing.T) {
			f := newInlayAcceptanceFixture(t, "abc\n")
			set := f.request(t, `[{"position":{"line":0,"character":1},"label":"x"}]`, "view", 1, inlayAcceptanceRange(0, 0, 1, 0))
			f.invalidate(t, change)
			out, err := f.w.InspectLSPInlayHint(t.Context(), f.owner, set.ID, set.Digest, 0, false)
			if err == nil || !reflect.DeepEqual(out, types.LSPInlayInspection{}) {
				t.Fatal("completed evidence survived its lifecycle boundary", change, out, err)
			}
		})
	}
}

func holdInlayAcceptanceRPC(t *testing.T, c *inlayAcceptanceClient) (<-chan inlayAcceptanceCall, func()) {
	t.Helper()
	entered, release := make(chan inlayAcceptanceCall, 1), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	c.inlayMu.Lock()
	c.entered, c.release = entered, release
	c.inlayMu.Unlock()
	return entered, unblock
}

func awaitInlayAcceptanceCall(t *testing.T, entered <-chan inlayAcceptanceCall) inlayAcceptanceCall {
	t.Helper()
	select {
	case call := <-entered:
		return call
	case <-time.After(3 * time.Second):
		t.Fatal("query never reached synchronized provider")
		return inlayAcceptanceCall{}
	}
}

type inlayAcceptanceOutcome struct {
	set types.LSPInlaySet
	err error
}

func awaitInlayAcceptanceOutcome(t *testing.T, done <-chan inlayAcceptanceOutcome) inlayAcceptanceOutcome {
	t.Helper()
	select {
	case out := <-done:
		return out
	case <-time.After(3 * time.Second):
		t.Fatal("released provider call did not retire")
		return inlayAcceptanceOutcome{}
	}
}

func TestInlayLatestSequenceWinsAndExactReleaseCannotRetireNewer(t *testing.T) {
	f := newInlayAcceptanceFixture(t, "abc\n")
	span := inlayAcceptanceRange(0, 0, 1, 0)
	f.client.raw = `[{"position":{"line":0,"character":1},"label":"first"}]`
	entered, unblock := holdInlayAcceptanceRPC(t, f.client)
	done := make(chan inlayAcceptanceOutcome, 1)
	go func() {
		set, err := f.w.RequestLSPInlayHints(t.Context(), f.owner, f.source, "view", 1, span)
		done <- inlayAcceptanceOutcome{set, err}
	}()
	first := awaitInlayAcceptanceCall(t, entered)
	f.client.inlayMu.Lock()
	f.client.entered, f.client.release = nil, nil
	f.client.inlayMu.Unlock()
	second := f.request(t, `[{"position":{"line":0,"character":1},"label":"second"}]`, "view", 2, span)
	select {
	case <-first.ctx.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("new view sequence did not cancel prior provider request")
	}
	if err := f.w.LiveDocuments.ReleaseLSPInlayHints(f.owner, "view", 1); err != nil {
		t.Fatal(err)
	}
	if out := f.inspect(t, second, 0, false); out.Hint.Label != "second" {
		t.Fatal("late release retired newer sequence", out)
	}
	unblock()
	if out := awaitInlayAcceptanceOutcome(t, done); out.err == nil || !reflect.DeepEqual(out.set, types.LSPInlaySet{}) {
		t.Fatal("late first answer replaced second", out)
	}
	for _, sequence := range []uint64{1, 2} {
		if set, err := f.w.RequestLSPInlayHints(t.Context(), f.owner, f.source, "view", sequence, span); err == nil ||
			!reflect.DeepEqual(set, types.LSPInlaySet{}) {
			t.Fatal("non-monotonic sequence was admitted", sequence, set, err)
		}
	}
	if err := f.w.LiveDocuments.ReleaseLSPInlayHints(f.owner, "view", 2); err != nil {
		t.Fatal(err)
	}
	if _, err := f.w.InspectLSPInlayHint(t.Context(), f.owner, second.ID, second.Digest, 0, false); err == nil {
		t.Fatal("exact release retained inspect authority")
	}
	if _, err := f.w.RequestLSPInlayHints(t.Context(), f.owner, f.source, "view", 2, span); err == nil {
		t.Fatal("released sequence tombstone allowed replay")
	}
	third := f.request(t, "[]", "view", 3, span)
	if third.Sequence != 3 || third.Answer != "empty" {
		t.Fatal("newer request could not replace released tombstone", third)
	}
}

func TestInlayLateNegativeAndPositiveAnswersStayFenced(t *testing.T) {
	for _, answer := range []string{"null", "[]", "unsupported", "hints"} {
		for _, change := range []string{"cancel", "release", "release-before-relay", "context", "edit", "settings", "trust", "provider"} {
			t.Run(answer+"/"+change, func(t *testing.T) {
				f := newInlayAcceptanceFixture(t, "abc\n")
				f.client.raw = answer
				switch answer {
				case "hints":
					f.client.raw = `[{"position":{"line":0,"character":1},"label":"x"}]`
				case "unsupported":
					f.client.raw, f.client.unsupported = "", true
				}
				entered, unblock := holdInlayAcceptanceRPC(t, f.client)
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				done := make(chan inlayAcceptanceOutcome, 1)
				go func() {
					set, err := f.w.RequestLSPInlayHints(ctx, f.owner, f.source, "view", 1, inlayAcceptanceRange(0, 0, 1, 0))
					done <- inlayAcceptanceOutcome{set, err}
				}()
				call := awaitInlayAcceptanceCall(t, entered)
				switch change {
				case "cancel":
					cancel()
				case "release-before-relay":
					// Release is authoritative before its asynchronous RPC cancellation relay.
					if err := f.w.LiveDocuments.ReleaseLSPInlayHints(f.owner, "view", 1); err != nil {
						t.Fatal(err)
					}
				case "release":
					if err := f.w.LiveDocuments.ReleaseLSPInlayHints(f.owner, "view", 1); err != nil {
						t.Fatal(err)
					}
					select {
					case <-call.ctx.Done():
					case <-time.After(3 * time.Second):
						t.Fatal("exact release did not cancel pending provider request")
					}
				default:
					f.invalidate(t, change)
				}
				unblock()
				out := awaitInlayAcceptanceOutcome(t, done)
				if out.err == nil || !reflect.DeepEqual(out.set, types.LSPInlaySet{}) {
					t.Fatal("late invalidated answer escaped canonical fence", answer, change, out)
				}
			})
		}
	}
}

func TestInlayReleaseCancelsPendingTooltipResolve(t *testing.T) {
	for _, waitForRelay := range []bool{false, true} {
		t.Run(fmt.Sprintf("wait-for-relay=%t", waitForRelay), func(t *testing.T) {
			f := newInlayAcceptanceFixture(t, "abc\n")
			original := `{"position":{"line":0,"character":1},"label":"x","data":{"n":1e0}}`
			f.client.resolveSupported = true
			set := f.request(t, "["+original+"]", "view", 1, inlayAcceptanceRange(0, 0, 1, 0))
			f.client.resolved = `{"position":{"line":0,"character":1},"label":"x","data":{"n":1e0},"tooltip":"late"}`
			entered, unblock := holdInlayAcceptanceRPC(t, f.client)
			type result struct {
				out types.LSPInlayInspection
				err error
			}
			done := make(chan result, 1)
			go func() {
				out, err := f.w.InspectLSPInlayHint(t.Context(), f.owner, set.ID, set.Digest, 0, true)
				done <- result{out, err}
			}()
			call := awaitInlayAcceptanceCall(t, entered)
			if !call.resolve || call.original != original {
				t.Fatal("pending resolve lost original", call)
			}
			if err := f.w.LiveDocuments.ReleaseLSPInlayHints(f.owner, "view", 1); err != nil {
				t.Fatal(err)
			}
			if waitForRelay {
				select {
				case <-call.ctx.Done():
				case <-time.After(3 * time.Second):
					t.Fatal("exact release did not cancel pending resolve")
				}
			}
			unblock()
			select {
			case got := <-done:
				if got.err == nil || !reflect.DeepEqual(got.out, types.LSPInlayInspection{}) {
					t.Fatal("released resolve returned late evidence", got)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("released resolve did not retire")
			}
			select {
			case <-call.ctx.Done():
			case <-time.After(3 * time.Second):
				t.Fatal("retired resolve did not cancel its original provider context")
			}
		})
	}
}

func TestInlayActiveViewsAndSequenceTombstonesHaveSeparateBounds(t *testing.T) {
	f := newInlayAcceptanceFixture(t, "abc\n")
	span := inlayAcceptanceRange(0, 0, 1, 0)
	for i := 0; i < 16; i++ {
		f.request(t, "[]", fmt.Sprintf("active-%d", i), 1, span)
	}
	if set, err := f.w.RequestLSPInlayHints(t.Context(), f.owner, f.source, "seventeenth", 1, span); err == nil ||
		!reflect.DeepEqual(set, types.LSPInlaySet{}) {
		t.Fatal("seventeenth active view exceeded cap16", set, err)
	}
	for i := 0; i < 16; i++ {
		if err := f.w.LiveDocuments.ReleaseLSPInlayHints(f.owner, fmt.Sprintf("active-%d", i), 1); err != nil {
			t.Fatal(err)
		}
	}
	// Failed/released views consume bounded replay tracking, not active slots.
	for i := 16; i < 128; i++ {
		view := fmt.Sprintf("tombstone-%d", i)
		if i%2 == 0 {
			f.client.inlayMu.Lock()
			f.client.err = errors.New("literal provider failure")
			f.client.inlayMu.Unlock()
			set, err := f.w.RequestLSPInlayHints(t.Context(), f.owner, f.source, view, 1, span)
			if err == nil || set.ID != "" {
				t.Fatal("provider failure acquired retained evidence", set, err)
			}
			f.client.inlayMu.Lock()
			f.client.err = nil
			f.client.inlayMu.Unlock()
		} else {
			f.request(t, "[]", view, 1, span)
			if err := f.w.LiveDocuments.ReleaseLSPInlayHints(f.owner, view, 1); err != nil {
				t.Fatal(err)
			}
		}
	}
	if set, err := f.w.RequestLSPInlayHints(t.Context(), f.owner, f.source, "tracking-129", 1, span); err == nil ||
		set.ID != "" {
		t.Fatal("unbounded sequence map admitted 129th view", set, err)
	}
	f.w.LiveDocuments.mu.Lock()
	tracked := len(f.w.LiveDocuments.inlayViews)
	f.w.LiveDocuments.mu.Unlock()
	if tracked != 128 {
		t.Fatal("sequence tombstones did not reach exact bounded map", tracked)
	}
	// Existing tracked views can still advance even when replay tracking is full.
	if set := f.request(t, "null", "active-0", 2, span); set.Answer != "null" {
		t.Fatal("tombstones falsely consumed active capacity", set)
	}
	if err := f.w.LiveDocuments.CloseView(f.owner, f.source.DocumentID); err != nil {
		t.Fatal(err)
	}
	s := f.w.LiveDocuments
	s.mu.Lock()
	remaining := len(s.inlayViews)
	for _, view := range s.inlayViews {
		if view.ctx.Err() == nil || view.view.ID != "" || len(view.items) != 0 || view.rawBytes != 0 || view.check != nil {
			s.mu.Unlock()
			t.Fatal("file close retained active inlay evidence")
		}
	}
	s.mu.Unlock()
	if remaining != tracked {
		t.Fatal("file close discarded bounded sequence fences", remaining, tracked)
	}
	s.Detach(f.owner)
	s.ReleaseDetached(f.owner)
	s.mu.Lock()
	remaining = len(s.inlayViews)
	s.mu.Unlock()
	if remaining != 0 {
		t.Fatal("owner detach did not reclaim inlay sequence fences", remaining)
	}
}

func TestInlayRawEvidenceBoundAndReleaseCloseShutdownReclamation(t *testing.T) {
	f := newInlayAcceptanceFixture(t, "abc\n")
	span := inlayAcceptanceRange(0, 0, 1, 0)
	// Bounded 64 KiB item, well below a transport's 1 MiB list ceiling.
	original := `{"position":{"line":0,"character":1},"label":"x","data":"` + strings.Repeat("a", 60<<10) + `"}`
	set := f.request(t, "["+original+"]", "first", 1, span)
	f.request(t, "["+original+"]", "second", 1, span)
	rawBytes := func() (int, int) {
		s := f.w.LiveDocuments
		s.mu.Lock()
		defer s.mu.Unlock()
		total := 0
		for _, view := range s.inlayViews {
			total += view.rawBytes
		}
		return total, len(s.inlayViews)
	}
	if total, _ := rawBytes(); total != 2*len(original) || total > 16<<20 {
		t.Fatal("raw-object accounting lost exact retained original bytes or 16MiB bound", total)
	}
	if err := f.w.LiveDocuments.ReleaseLSPInlayHints(f.owner, "first", 1); err != nil {
		t.Fatal(err)
	}
	if total, _ := rawBytes(); total != len(original) {
		t.Fatal("release did not reclaim raw original bytes", total)
	}
	if _, err := f.w.InspectLSPInlayHint(t.Context(), f.owner, set.ID, set.Digest, 0, false); err == nil {
		t.Fatal("reclaimed raw item remained inspectable")
	}
	// No giant source, parser-depth bomb, or >16MiB allocation is needed.
	f.client.inlayMu.Lock()
	f.client.raw = "[" + original + strings.Repeat(" ", 1<<20) + "]"
	f.client.inlayMu.Unlock()
	if out, err := f.w.RequestLSPInlayHints(t.Context(), f.owner, f.source, "oversize", 1, span); err == nil ||
		!reflect.DeepEqual(out, types.LSPInlaySet{}) {
		t.Fatal("over-budget original JSON acquired evidence", out, err)
	}
	if total, _ := rawBytes(); total != len(original) {
		t.Fatal("failed oversized response retained bytes", total)
	}
	_, beforeClose := rawBytes()
	if err := f.w.LiveDocuments.CloseView(f.owner, f.source.DocumentID); err != nil {
		t.Fatal(err)
	}
	if total, count := rawBytes(); total != 0 || count != beforeClose {
		t.Fatal("close did not discard raw data while retaining sequence fences", total, count, beforeClose)
	}
	snap, err := f.w.LiveDocuments.Open(f.owner, f.path)
	if err != nil {
		t.Fatal(err)
	}
	waitDiagnosticSend(t, f.w.LSPMgr, &f.client.lifecycleClient, snap)
	f.w.LiveDocuments.mu.Lock()
	f.source = f.w.LiveDocuments.analysisIdentityLocked(f.w.LiveDocuments.docs[snap.ID])
	f.w.LiveDocuments.mu.Unlock()
	newSet := f.request(t, "["+original+"]", "reopened", 1, span)
	if got := f.inspect(t, newSet, 0, false); !bytes.Equal(got.OriginalJSON, []byte(original)) {
		t.Fatal("reopen did not retain fresh original")
	}
	f.w.LiveDocuments.Close()
	if total, count := rawBytes(); total != 0 || count != 0 {
		t.Fatal("shutdown did not reclaim retained evidence", total, count)
	}
}
