package workspace

import (
	"context"
	"crude/internal/fileuri"
	"crypto/sha256"
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
	"crude/localhistory"
	"crude/types"
)

// This fake uses the real manager's open/change synchronization. RPC gates
// deliberately ignore cancellation so the coordinator, not the fake, must
// reject a late provider answer.
type codeActionClient struct {
	lifecycleClient
	actionMu         sync.Mutex
	list, resolved   []byte
	resolveSupported bool
	wireDelta        int
	requests         [][4]int
	resolveInputs    [][]byte
	resolveWires     []int
	commandSupported bool
	commandResponse  types.LSPCommandResponse
	commandErr       error
	commands         []struct {
		name string
		args string
	}
	entered chan<- struct{}
	release <-chan struct{}
}

func (c *codeActionClient) answer(path string, resolving bool, wire int, original []byte, position [4]int) (types.LSPCodeActionResponse, error) {
	c.mu.Lock()
	currentWire := c.versions[path]
	c.mu.Unlock()
	c.actionMu.Lock()
	raw := c.list
	if resolving {
		raw = c.resolved
		c.resolveInputs = append(c.resolveInputs, append([]byte(nil), original...))
		c.resolveWires = append(c.resolveWires, wire)
	} else {
		c.requests = append(c.requests, position)
	}
	response := types.LSPCodeActionResponse{
		JSON: append([]byte(nil), raw...), WireVersion: currentWire + c.wireDelta,
		ResolveSupported: c.resolveSupported, CommandSupported: c.commandSupported,
		// Adapter provenance is deliberately false. The synchronized manager
		// must stamp its own source even for null and empty responses.
		Boundary: types.InsightBoundary{Source: types.InsightSource{Path: "forged-adapter-path"}},
	}
	entered, release := c.entered, c.release
	c.actionMu.Unlock()
	if entered != nil {
		entered <- struct{}{}
	}
	if release != nil {
		<-release
	}
	return response, nil
}

func (c *codeActionClient) RequestCodeActions(_ context.Context, path string, line, col, endLine, endCol int) (types.LSPCodeActionResponse, error) {
	return c.answer(path, false, 0, nil, [4]int{line, col, endLine, endCol})
}

func (c *codeActionClient) ResolveCodeAction(_ context.Context, path string, wire int, raw []byte) (types.LSPCodeActionResponse, error) {
	return c.answer(path, true, wire, raw, [4]int{})
}

func (c *codeActionClient) ExecuteCommand(_ context.Context, _ string, _ int, name string, arguments json.RawMessage) (types.LSPCommandResponse, error) {
	c.actionMu.Lock()
	c.commands = append(c.commands, struct{ name, args string }{name, string(arguments)})
	response, err := c.commandResponse, c.commandErr
	c.actionMu.Unlock()
	return response, err
}

type codeActionRuntime struct{ client *codeActionClient }

func (r codeActionRuntime) NewClient(_ types.LSPServerSpec, _ string, admission func() error) LSPClient {
	r.client.startAdmission = admission
	return r.client
}

type codeActionFixture struct {
	workspace *Workspace
	client    *codeActionClient
	owner     document.Owner
	source    types.InsightSource
	path      string
	before    string
}

func newCodeActionFixture(t *testing.T, text string) codeActionFixture {
	t.Helper()
	root := t.TempDir()
	client := &codeActionClient{
		lifecycleClient: lifecycleClient{runtime: &lifecycleRuntime{}, reapableClient: reapableClient{done: make(chan struct{})}},
		list:            []byte("[]"),
	}
	manager := NewLSPManager(nil, codeActionRuntime{client})
	trustAnalysis(t, manager, root)
	t.Cleanup(manager.Close)
	db, err := openWorkspaceDBAt(filepath.Join(t.TempDir(), "workspace.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	docs := NewDocumentService(root, nil, manager, db.DB)
	t.Cleanup(docs.Close)
	store, err := localhistory.Open(filepath.Join(t.TempDir(), "history"), root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	path := filepath.Join(root, "main.go")
	if err := os.WriteFile(path, []byte(text), 0640); err != nil {
		t.Fatal(err)
	}
	owner := document.Owner{ClientID: "code-action-owner", Epoch: 1}
	snapshot, err := docs.Open(owner, path)
	if err != nil {
		t.Fatal(err)
	}
	waitDiagnosticSend(t, manager, &client.lifecycleClient, snapshot)
	docs.mu.Lock()
	source := docs.analysisIdentityLocked(docs.docs[snapshot.ID])
	docs.mu.Unlock()
	w := &Workspace{LiveDocuments: docs, LSPMgr: manager, LocalHistory: store}
	return codeActionFixture{w, client, owner, source, path, text}
}

func (f codeActionFixture) list(t *testing.T, raw string, resolve bool) CodeActionSet {
	t.Helper()
	f.client.actionMu.Lock()
	f.client.list, f.client.resolveSupported = []byte(raw), resolve
	f.client.actionMu.Unlock()
	set, err := f.workspace.RequestLSPCodeActions(t.Context(), f.owner, f.source, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	return set
}

func (f codeActionFixture) prepare(t *testing.T, set CodeActionSet) ReviewedEditPreview {
	t.Helper()
	preview, err := f.workspace.PrepareLSPCodeAction(t.Context(), f.owner, set.ID, set.Digest, 0)
	if err != nil {
		t.Fatal(err)
	}
	return preview
}

func (f codeActionFixture) assertUnchanged(t *testing.T) {
	t.Helper()
	disk, err := os.ReadFile(f.path)
	if err != nil || string(disk) != f.before {
		t.Fatalf("refusal/preparation changed literal disk bytes: %q %v", disk, err)
	}
}

func codeActionEdit(path string) string {
	return fmt.Sprintf(`{"changes":{%q:[{"range":{"start":{"line":0,"character":4},"end":{"line":0,"character":7}},"newText":"new"},{"range":{"start":{"line":1,"character":0},"end":{"line":1,"character":3}},"newText":"new"}]}}`, fileuri.FromPath(path))
}

func codeActionLiteral(path string) string {
	return `{"title":"Replace \"old\"\n\tliteral","kind":"quickfix.custom","isPreferred":false,"edit":` + codeActionEdit(path) + `}`
}

func codeActionHash(text string) string { return fmt.Sprintf("%x", sha256.Sum256([]byte(text))) }

func TestCodeActionLiteralImmutableReviewRecoverableCanonicalApply(t *testing.T) {
	f := newCodeActionFixture(t, "var old = 1\r\nold\r\n")
	raw := codeActionLiteral(f.path)
	set := f.list(t, "["+raw+"]", false)
	if len(set.Items) != 1 || !set.Items[0].CanReview || set.Items[0].NeedsResolve ||
		!set.Items[0].HasPreferred || set.Items[0].Preferred || set.Boundary.Source != f.source {
		t.Fatalf("literal action/source was not preserved: %+v", set)
	}
	// Neither returned presentation nor the fake's original buffer is authority.
	unknownInputs := append([]string(nil), set.Boundary.Context.UnknownInputs...)
	set.Items[0].Title, set.Items[0].Index, set.Items[0].CanReview = "FORGED", 99, false
	if len(set.Boundary.Context.UnknownInputs) != 0 {
		set.Boundary.Context.UnknownInputs[0] = "FORGED"
	}
	f.client.actionMu.Lock()
	f.client.list[0] = '!'
	f.client.actionMu.Unlock()
	preview := f.prepare(t, set)
	want := "main.go  +2  -2\nLine 1\n- var old = 1\\r\n- old\\r\n+ var new = 1\\r\n+ new\\r\n\n"
	if preview.Text != want || len(preview.Files) != 1 || preview.Boundary.Source != f.source ||
		strings.Contains(preview.Evidence, "FORGED") || !reflect.DeepEqual(preview.Boundary.Context.UnknownInputs, unknownInputs) ||
		!strings.Contains(preview.Evidence, "Original action SHA-256: "+codeActionHash(raw)) ||
		preview.Files[0].BeforeSHA256 != codeActionHash("var old = 1\r\nold\r\n") ||
		preview.Files[0].AfterSHA256 != codeActionHash("var new = 1\r\nnew\r\n") {
		t.Fatalf("immutable literal review differs:\ngot: %q\nwant: %q", preview.Text, want)
	}
	f.assertUnchanged(t)
	preview.Text, preview.Files[0].AfterSHA256 = "FORGED", "FORGED"
	result := f.workspace.LiveDocuments.ApplyReviewedEdit(t.Context(), f.owner, preview.ID, preview.Digest,
		f.workspace.LocalHistory, NewFileOpsService(filepath.Dir(f.path), nil, nil))
	if result.State != "committed" || result.Error != "" || result.SafetySnapshotID == "" || result.SafetyReference != preview.ID {
		t.Fatalf("real Apply did not establish recoverable success: %+v", result)
	}
	disk, err := os.ReadFile(f.path)
	if err != nil || string(disk) != "var new = 1\r\nnew\r\n" {
		t.Fatalf("literal disk result = %q, %v", disk, err)
	}
	snapshot, err := f.workspace.LiveDocuments.Snapshot(f.source.DocumentID)
	if err != nil || string(snapshot.Text) != "var new = 1\r\nnew\r\n" || snapshot.Dirty || snapshot.Revision <= f.source.Revision {
		t.Fatalf("Apply bypassed canonical owner: %+v %v", snapshot, err)
	}
	safety, err := f.workspace.LocalHistory.LoadRetainedSnapshot(t.Context(), preview.ID)
	if err != nil || safety.ID != result.SafetySnapshotID || len(safety.Entries) != 1 ||
		safety.Entries["main.go"].Hash != codeActionHash("var old = 1\r\nold\r\n") {
		t.Fatalf("original preimage was not retained: %+v %v", safety, err)
	}
}

func TestCodeActionSourceBytesBecomeLiteralUTF16Selection(t *testing.T) {
	f := newCodeActionFixture(t, "a😀e\u0301\r\nz\r\n")
	// Source bytes: a=0, emoji=1..5, e=5, combining acute=6..8,
	// CR=8, LF=9, z=10. Expectations are authored without Position.
	for _, tc := range []struct {
		name       string
		start, end int
		want       [4]int
	}{
		{"astral", 1, 5, [4]int{0, 1, 0, 3}},
		{"combining-rune", 6, 8, [4]int{0, 4, 0, 5}},
		{"CRLF-across-lines", 8, 10, [4]int{0, 5, 1, 0}},
		{"last-line", 10, 11, [4]int{1, 0, 1, 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			set, err := f.workspace.RequestLSPCodeActions(t.Context(), f.owner, f.source, tc.start, tc.end)
			if err != nil {
				t.Fatal(err)
			}
			f.client.actionMu.Lock()
			got := f.client.requests[len(f.client.requests)-1]
			f.client.actionMu.Unlock()
			if got != tc.want || set.Start != tc.start || set.End != tc.end || set.Boundary.Source != f.source {
				t.Fatalf("source-byte selection lost: wire=%v set=%+v; want %v", got, set, tc.want)
			}
			if err := f.workspace.LiveDocuments.ReleaseLSPCodeActions(f.owner, set.ID, set.Digest); err != nil {
				t.Fatal(err)
			}
		})
	}
	f.client.actionMu.Lock()
	calls := len(f.client.requests)
	f.client.actionMu.Unlock()
	for _, tc := range []struct{ start, end int }{{5, 1}, {2, 5}, {6, 7}, {9, 10}, {-1, 0}, {0, 99}} {
		if _, err := f.workspace.RequestLSPCodeActions(t.Context(), f.owner, f.source, tc.start, tc.end); err == nil {
			t.Errorf("invalid source range %d..%d queried a provider", tc.start, tc.end)
		}
	}
	f.client.actionMu.Lock()
	defer f.client.actionMu.Unlock()
	if len(f.client.requests) != calls {
		t.Fatal("invalid byte selection reached provider")
	}
}

func TestCodeActionVersionedLiteralRequiresExactWireAndRetainsSafety(t *testing.T) {
	for _, version := range []string{"1", "2", "0", "null"} {
		t.Run(version, func(t *testing.T) {
			f := newCodeActionFixture(t, "var old = 1\n")
			// Initial didOpen is version 1. Do not synthesize expected source or
			// output from the edit normalizer being exercised.
			f.client.mu.Lock()
			wire := f.client.versions[f.path]
			f.client.mu.Unlock()
			if wire != 1 {
				t.Fatal("fixture initial wire is not the independently expected first version", wire)
			}
			raw := fmt.Sprintf(`[{"title":"Versioned literal","edit":{"documentChanges":[{"textDocument":{"uri":%q,"version":%s},"edits":[{"range":{"start":{"line":0,"character":4},"end":{"line":0,"character":7}},"newText":"new"}]}]}}]`, fileuri.FromPath(f.path), version)
			set := f.list(t, raw, false)
			if version != "1" {
				if len(set.Items) != 1 || set.Items[0].CanReview || set.Items[0].UnsupportedReason == "" {
					t.Fatal("wrong/unknown wire acquired action approval", set)
				}
				if preview, err := f.workspace.PrepareLSPCodeAction(t.Context(), f.owner, set.ID, set.Digest, 0); err == nil || preview.ID != "" {
					t.Fatal("wrong/unknown wire acquired a pending review")
				}
				f.assertUnchanged(t)
				return
			}
			if len(set.Items) != 1 || !set.Items[0].CanReview {
				t.Fatal("exact versioned ordinary text edit was refused", set)
			}
			p := f.prepare(t, set)
			if !strings.Contains(p.Text, "- var old = 1") || !strings.Contains(p.Text, "+ var new = 1") {
				t.Fatal("versioned action lost literal changed-span review")
			}
			out := f.workspace.LiveDocuments.ApplyReviewedEdit(t.Context(), f.owner, p.ID, p.Digest,
				f.workspace.LocalHistory, NewFileOpsService(filepath.Dir(f.path), nil, nil))
			disk, err := os.ReadFile(f.path)
			if out.State != "committed" || out.SafetySnapshotID == "" || err != nil || string(disk) != "var new = 1\n" {
				t.Fatal("exact versioned literal did not use recoverable canonical mutation", out, string(disk), err)
			}
			safety, err := f.workspace.LocalHistory.LoadRetainedSnapshot(t.Context(), p.ID)
			if err != nil || safety.Entries["main.go"].Hash != codeActionHash("var old = 1\n") {
				t.Fatal("versioned action failed to retain exact original source", safety, err)
			}
		})
	}
}

func TestCodeActionUnsupportedRowsRemainVisibleWithoutPartialReview(t *testing.T) {
	f := newCodeActionFixture(t, "var old = 1\r\nold\r\n")
	other := filepath.Join(filepath.Dir(f.path), "other.go")
	if err := os.WriteFile(other, []byte("var old = 1\r\nold\r\n"), 0600); err != nil {
		t.Fatal(err)
	}
	edit := codeActionEdit(f.path)
	docChanges := fmt.Sprintf(`{"documentChanges":[{"textDocument":{"uri":%q,"version":0},"edits":[{"range":{"start":{"line":0,"character":4},"end":{"line":0,"character":7}},"newText":"new"}]}]}`, fileuri.FromPath(f.path))
	for _, tc := range []struct{ name, raw string }{
		{"disabled", `{"title":"disabled","disabled":{"reason":"not available"},"edit":` + edit + `}`},
		{"command-only", `{"title":"run tool","command":"provider.run","arguments":["literal"]}`},
		{"lazy-no-resolve", `{"title":"unresolvable","data":{"opaque":true}}`},
		{"edit-plus-command", `{"title":"do both","command":{"title":"run","command":"provider.run"},"edit":` + edit + `}`},
		{"invalid-versioned-documentChanges", `{"title":"versioned","edit":` + docChanges + `}`},
		{"remote-uri", `{"title":"remote","edit":` + strings.ReplaceAll(edit, fileuri.FromPath(f.path), "https://example.invalid/source.go") + `}`},
		{"no-op", `{"title":"same bytes","edit":` + strings.ReplaceAll(edit, `"newText":"new"`, `"newText":"old"`) + `}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			set := f.list(t, "["+tc.raw+"]", false)
			if len(set.Items) != 1 || set.Items[0].Title == "" || set.Items[0].CanReview ||
				set.Items[0].UnsupportedReason == "" && set.Items[0].DisabledReason == "" {
				t.Fatalf("unsupported provider row was dropped or advertised reviewable: %+v", set)
			}
			if _, err := f.workspace.PrepareLSPCodeAction(t.Context(), f.owner, set.ID, set.Digest, 0); err == nil {
				t.Fatal("unsupported row acquired a review")
			}
			f.assertUnchanged(t)
			disk, err := os.ReadFile(other)
			if err != nil || string(disk) != "var old = 1\r\nold\r\n" {
				t.Fatalf("unsupported action partially wrote another file: %q %v", disk, err)
			}
			if err := f.workspace.LiveDocuments.ReleaseLSPCodeActions(f.owner, set.ID, set.Digest); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestCodeActionPrivateOwnerDigestIndexAndMandatoryHistory(t *testing.T) {
	f := newCodeActionFixture(t, "var old = 1\r\nold\r\n")
	set := f.list(t, "["+codeActionLiteral(f.path)+"]", false)
	peer := document.Owner{ClientID: "code-action-peer", Epoch: 1}
	if _, err := f.workspace.LiveDocuments.Open(peer, f.path); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		owner      document.Owner
		id, digest string
		index      int
	}{
		{peer, set.ID, set.Digest, 0}, {f.owner, set.ID, "forged", 0},
		{f.owner, "missing", set.Digest, 0}, {f.owner, set.ID, set.Digest, -1},
		{f.owner, set.ID, set.Digest, 1},
	} {
		if _, err := f.workspace.PrepareLSPCodeAction(t.Context(), tc.owner, tc.id, tc.digest, tc.index); err == nil {
			t.Errorf("invalid private capability accepted: %+v", tc)
		}
	}
	if err := f.workspace.LiveDocuments.ReleaseLSPCodeActions(peer, set.ID, set.Digest); err == nil {
		t.Fatal("peer released another owner's list")
	}
	if err := f.workspace.LiveDocuments.ReleaseLSPCodeActions(f.owner, set.ID, "forged"); err == nil {
		t.Fatal("forged digest released list")
	}
	preview := f.prepare(t, set)
	result := f.workspace.LiveDocuments.ApplyReviewedEdit(t.Context(), f.owner, preview.ID, preview.Digest,
		nil, NewFileOpsService(filepath.Dir(f.path), nil, nil))
	if result.State != "refused" || result.Error == "" || result.SafetySnapshotID != "" {
		t.Fatalf("missing mandatory history was accepted: %+v", result)
	}
	f.assertUnchanged(t)
}

func TestCodeActionBoundedListsExpireAndReleaseRevokesPreparedApply(t *testing.T) {
	f := newCodeActionFixture(t, "var old = 1\r\nold\r\n")
	var sets []CodeActionSet
	for range 4 {
		sets = append(sets, f.list(t, "["+codeActionLiteral(f.path)+"]", false))
	}
	f.client.actionMu.Lock()
	calls := len(f.client.requests)
	f.client.actionMu.Unlock()
	if _, err := f.workspace.RequestLSPCodeActions(t.Context(), f.owner, f.source, 0, 0); err == nil {
		t.Fatal("fifth live list exceeded fixed bound")
	}
	f.client.actionMu.Lock()
	if len(f.client.requests) != calls {
		t.Error("known full list capacity still probed provider")
	}
	f.client.actionMu.Unlock()
	preview := f.prepare(t, sets[0])
	if err := f.workspace.LiveDocuments.ReleaseLSPCodeActions(f.owner, sets[0].ID, sets[0].Digest); err != nil {
		t.Fatal(err)
	}
	result := f.workspace.LiveDocuments.ApplyReviewedEdit(t.Context(), f.owner, preview.ID, preview.Digest,
		f.workspace.LocalHistory, NewFileOpsService(filepath.Dir(f.path), nil, nil))
	if result.State != "refused" || result.Error == "" {
		t.Fatalf("release failed to revoke a previously prepared Apply: %+v", result)
	}
	// Advance only retained test timestamps: no wall-clock wait and no relaxed
	// production lifetime or cap.
	f.workspace.LiveDocuments.mu.Lock()
	f.workspace.LiveDocuments.codeActions[sets[1].ID].expires = time.Now().Add(-time.Second)
	f.workspace.LiveDocuments.mu.Unlock()
	if _, err := f.workspace.PrepareLSPCodeAction(t.Context(), f.owner, sets[1].ID, sets[1].Digest, 0); err == nil {
		t.Fatal("expired list acquired a review")
	}
	_ = f.list(t, "null", false)
	_ = f.list(t, "[]", false)
	f.assertUnchanged(t)
}

func TestCodeActionLifecycleRevokesListsAndPreparedReview(t *testing.T) {
	for _, change := range []string{"peer-text", "equal-text-undo", "close", "detach", "context", "provider", "expiry"} {
		t.Run(change, func(t *testing.T) {
			f := newCodeActionFixture(t, "var old = 1\r\nold\r\n")
			set := f.list(t, "["+codeActionLiteral(f.path)+"]", false)
			preview := f.prepare(t, set)
			switch change {
			case "peer-text", "equal-text-undo":
				peer := document.Owner{ClientID: "peer", Epoch: 1}
				snap, err := f.workspace.LiveDocuments.Open(peer, f.path)
				if err != nil {
					t.Fatal(err)
				}
				receipt, err := f.workspace.LiveDocuments.Apply(document.Change{DocumentID: snap.ID, Owner: peer,
					RequestID: "peer-insert", BaseRevision: snap.Revision, Edits: []document.Edit{{Text: "X"}}})
				if err != nil {
					t.Fatal(err)
				}
				if change == "equal-text-undo" {
					if _, err := f.workspace.LiveDocuments.Undo(peer, snap.ID, "peer-undo", receipt.Revision); err != nil {
						t.Fatal(err)
					}
					restored, err := f.workspace.LiveDocuments.Snapshot(snap.ID)
					if err != nil || string(restored.Text) != "var old = 1\r\nold\r\n" || restored.Revision <= f.source.Revision {
						t.Fatalf("test did not establish equal-text newer history: %+v %v", restored, err)
					}
				}
			case "close":
				if err := f.workspace.LiveDocuments.CloseView(f.owner, f.source.DocumentID); err != nil {
					t.Fatal(err)
				}
			case "detach":
				f.workspace.LiveDocuments.Detach(f.owner)
			case "context":
				f.workspace.LSPMgr.NotifyWatchedFilesChanged(t.Context(), filepath.Join(filepath.Dir(f.path), "go.mod"), 2)
			case "provider":
				f.workspace.LSPMgr.SetRuntime(&lifecycleRuntime{})
			case "expiry":
				f.workspace.LiveDocuments.mu.Lock()
				f.workspace.LiveDocuments.codeActions[set.ID].expires = time.Now().Add(-time.Second)
				f.workspace.LiveDocuments.mu.Unlock()
			}
			if _, err := f.workspace.PrepareLSPCodeAction(t.Context(), f.owner, set.ID, set.Digest, 0); err == nil {
				t.Fatal("invalidated list acquired another review")
			}
			result := f.workspace.LiveDocuments.ApplyReviewedEdit(t.Context(), f.owner, preview.ID, preview.Digest,
				f.workspace.LocalHistory, NewFileOpsService(filepath.Dir(f.path), nil, nil))
			if result.State != "refused" || result.Error == "" || result.SafetySnapshotID != "" {
				t.Fatalf("invalidated list retained mutation authority: %+v", result)
			}
			f.assertUnchanged(t)
		})
	}
}

func TestCodeActionNullEmptyRemainScopedToCanonicalBoundary(t *testing.T) {
	for _, raw := range []string{"null", "[]"} {
		t.Run(raw, func(t *testing.T) {
			f := newCodeActionFixture(t, "var old = 1\r\nold\r\n")
			set := f.list(t, raw, false)
			if set.ID == "" || set.Digest == "" || len(set.Items) != 0 || set.Boundary.Source != f.source ||
				set.Boundary.Producer.InstanceID == "" || set.Boundary.Context.Generation == 0 ||
				len(set.Boundary.Context.UnknownInputs) == 0 || set.Start != 0 || set.End != 0 {
				t.Fatalf("empty answer lost partial source/provider/context scope: %+v", set)
			}
			if _, err := f.workspace.PrepareLSPCodeAction(t.Context(), f.owner, set.ID, set.Digest, 0); err == nil {
				t.Fatal("empty answer acquired an Apply review")
			}
			f.assertUnchanged(t)
		})
	}
}

func TestCodeActionCanceledBeforeRPCDoesNotProbeProvider(t *testing.T) {
	f := newCodeActionFixture(t, "var old = 1\r\nold\r\n")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := f.workspace.RequestLSPCodeActions(ctx, f.owner, f.source, 0, 0); err == nil {
		t.Fatal("pre-canceled request succeeded")
	}
	set := f.list(t, "["+codeActionLiteral(f.path)+"]", false)
	if _, err := f.workspace.PrepareLSPCodeAction(ctx, f.owner, set.ID, set.Digest, 0); err == nil {
		t.Fatal("pre-canceled preparation succeeded")
	}
	f.client.actionMu.Lock()
	defer f.client.actionMu.Unlock()
	if len(f.client.requests) != 1 || len(f.client.resolveInputs) != 0 {
		t.Fatal("pre-canceled operation probed provider")
	}
}

func TestCodeActionSourceAndManagerAdmissionRefuseWithoutRPC(t *testing.T) {
	for _, invalid := range []string{"foreign-owner", "revision", "lifetime", "noncanonical", "dirty", "missing-history", "unsynchronized"} {
		t.Run(invalid, func(t *testing.T) {
			f := newCodeActionFixture(t, "var old = 1\r\nold\r\n")
			owner, source := f.owner, f.source
			ctx := t.Context()
			switch invalid {
			case "foreign-owner":
				owner = document.Owner{ClientID: "not-an-editor", Epoch: 1}
			case "revision":
				source.Revision++
			case "lifetime":
				source.Lifetime += "-foreign"
			case "noncanonical":
				source.Kind = "disk"
			case "dirty":
				if _, err := f.workspace.LiveDocuments.Apply(document.Change{DocumentID: source.DocumentID, Owner: owner,
					RequestID: "dirty-source", BaseRevision: source.Revision, Edits: []document.Edit{{Text: "X"}}}); err != nil {
					t.Fatal(err)
				}
				f.workspace.LiveDocuments.mu.Lock()
				source = f.workspace.LiveDocuments.analysisIdentityLocked(f.workspace.LiveDocuments.docs[source.DocumentID])
				f.workspace.LiveDocuments.mu.Unlock()
			case "missing-history":
				f.workspace.LocalHistory = nil
			case "unsynchronized":
				f.workspace.LSPMgr.mu.Lock()
				f.workspace.LSPMgr.documents[f.path].syncPending = true
				f.workspace.LSPMgr.mu.Unlock()
				// A permanently held send must cancel without reaching the RPC.
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, 50*time.Millisecond)
				defer cancel()
			}
			if set, err := f.workspace.RequestLSPCodeActions(ctx, owner, source, 0, 0); err == nil || set.ID != "" {
				t.Fatalf("invalid source acquired list: %+v %v", set, err)
			}
			f.client.actionMu.Lock()
			calls := len(f.client.requests)
			f.client.actionMu.Unlock()
			if calls != 0 {
				t.Fatal("invalid canonical source probed provider")
			}
			f.assertUnchanged(t)
		})
	}
}

// A gate is released by cleanup too, so an assertion failure cannot strand a
// manager-owned in-flight call. No sleep is used to order request and mutation.
func holdCodeActionRPC(t *testing.T, c *codeActionClient) (<-chan struct{}, func()) {
	t.Helper()
	entered, release := make(chan struct{}, 1), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	c.actionMu.Lock()
	c.entered, c.release = entered, release
	c.actionMu.Unlock()
	return entered, unblock
}

func TestCodeActionLateRPCFencesIncludingNullAndEmpty(t *testing.T) {
	for _, raw := range []string{"null", "[]", "literal"} {
		for _, change := range []string{"cancel", "context", "provider", "source", "wire"} {
			t.Run(raw+"/"+change, func(t *testing.T) {
				f := newCodeActionFixture(t, "var old = 1\r\nold\r\n")
				answer := raw
				if raw == "literal" {
					answer = "[" + codeActionLiteral(f.path) + "]"
				}
				f.client.actionMu.Lock()
				f.client.list = []byte(answer)
				if change == "wire" {
					f.client.wireDelta = 1
				}
				f.client.actionMu.Unlock()
				entered, unblock := holdCodeActionRPC(t, f.client)
				ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
				defer cancel()
				type outcome struct {
					set CodeActionSet
					err error
				}
				done := make(chan outcome, 1)
				go func() {
					set, err := f.workspace.RequestLSPCodeActions(ctx, f.owner, f.source, 0, 0)
					done <- outcome{set, err}
				}()
				select {
				case <-entered:
				case <-ctx.Done():
					t.Fatal("request never reached provider")
				}
				switch change {
				case "cancel":
					cancel()
				case "context":
					f.workspace.LSPMgr.NotifyWatchedFilesChanged(t.Context(), filepath.Join(filepath.Dir(f.path), "go.mod"), 2)
				case "provider":
					f.workspace.LSPMgr.SetRuntime(&lifecycleRuntime{})
				case "source":
					_, err := f.workspace.LiveDocuments.Apply(document.Change{DocumentID: f.source.DocumentID, Owner: f.owner,
						RequestID: "during-rpc", BaseRevision: f.source.Revision, Edits: []document.Edit{{Text: "X"}}})
					if err != nil {
						t.Fatal(err)
					}
				}
				unblock()
				got := <-done
				if got.err == nil || got.set.ID != "" || len(got.set.Items) != 0 {
					t.Fatalf("late invalidated %s answer escaped coordinator: %+v", raw, got)
				}
				f.workspace.LiveDocuments.mu.Lock()
				retained := len(f.workspace.LiveDocuments.codeActions)
				f.workspace.LiveDocuments.mu.Unlock()
				if retained != 0 {
					t.Fatal("invalidated late answer retained a capability")
				}
				f.assertUnchanged(t)
			})
		}
	}
}

func TestCodeActionLazyResolvePreservesOriginalBytesAndCurrentWire(t *testing.T) {
	f := newCodeActionFixture(t, "var old = 1\r\nold\r\n")
	// Whitespace and large integer spelling are intentional opaque data.
	original := `{ "title" : "lazy", "kind":"quickfix", "data": {"n":9007199254740993,"spelling":1e0} }`
	set := f.list(t, "["+original+"]", true)
	if len(set.Items) != 1 || !set.Items[0].NeedsResolve || !set.Items[0].CanReview {
		t.Fatalf("resolve-supported lazy action not available: %+v", set)
	}
	f.client.actionMu.Lock()
	if len(f.client.resolveInputs) != 0 {
		t.Fatal("discovery eagerly resolved a lazy row")
	}
	f.client.resolved = []byte(strings.TrimSuffix(original, "}") + `,"edit":` + codeActionEdit(f.path) + `}`)
	f.client.actionMu.Unlock()
	f.client.mu.Lock()
	wire := f.client.versions[f.path]
	f.client.mu.Unlock()
	preview := f.prepare(t, set)
	f.client.actionMu.Lock()
	inputs, wires := f.client.resolveInputs, f.client.resolveWires
	f.client.actionMu.Unlock()
	if len(inputs) != 1 || string(inputs[0]) != original || len(wires) != 1 || wires[0] != wire ||
		!strings.Contains(preview.Evidence, "Original action SHA-256: "+codeActionHash(original)) ||
		!strings.Contains(preview.Text, "- var old = 1") || !strings.Contains(preview.Text, "+ var new = 1") {
		t.Fatalf("resolve rewrote original bytes or lost synchronized wire: inputs=%q wires=%v review=%q", inputs, wires, preview.Text)
	}
	f.assertUnchanged(t)
}

func TestCodeActionLazyResolveFencesAndRejectsChangedOriginal(t *testing.T) {
	for _, change := range []string{"cancel", "release", "context", "provider", "wire", "changed-title", "changed-number", "command"} {
		t.Run(change, func(t *testing.T) {
			f := newCodeActionFixture(t, "var old = 1\r\nold\r\n")
			original := `{"title":"lazy","data":{"n":1e0}}`
			set := f.list(t, "["+original+"]", true)
			resolved := `{"title":"lazy","data":{"n":1e0},"edit":` + codeActionEdit(f.path) + `}`
			switch change {
			case "changed-title":
				resolved = strings.Replace(resolved, `"title":"lazy"`, `"title":"other"`, 1)
			case "changed-number":
				resolved = strings.Replace(resolved, `"n":1e0`, `"n":1.0`, 1)
			case "command":
				resolved = strings.TrimSuffix(resolved, "}") + `,"command":{"title":"run","command":"tool.run"}}`
			}
			f.client.actionMu.Lock()
			f.client.resolved = []byte(resolved)
			if change == "wire" {
				f.client.wireDelta = 1
			}
			f.client.actionMu.Unlock()
			entered, unblock := holdCodeActionRPC(t, f.client)
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			type outcome struct {
				preview ReviewedEditPreview
				err     error
			}
			done := make(chan outcome, 1)
			go func() {
				preview, err := f.workspace.PrepareLSPCodeAction(ctx, f.owner, set.ID, set.Digest, 0)
				done <- outcome{preview, err}
			}()
			select {
			case <-entered:
			case <-ctx.Done():
				t.Fatal("resolve never reached provider")
			}
			switch change {
			case "cancel":
				cancel()
			case "release":
				if err := f.workspace.LiveDocuments.ReleaseLSPCodeActions(f.owner, set.ID, set.Digest); err != nil {
					t.Fatal(err)
				}
			case "context":
				f.workspace.LSPMgr.NotifyWatchedFilesChanged(t.Context(), filepath.Join(filepath.Dir(f.path), "go.mod"), 2)
			case "provider":
				f.workspace.LSPMgr.SetRuntime(&lifecycleRuntime{})
			}
			unblock()
			got := <-done
			if got.err == nil || got.preview.ID != "" {
				t.Fatalf("invalid lazy resolve acquired review: %+v", got)
			}
			f.assertUnchanged(t)
		})
	}
}
func TestCodeActionCreateFileReviewAndApply(t *testing.T) {
	f := newCodeActionFixture(t, "var old = 1\r\nold\r\n")
	target := filepath.Join(filepath.Dir(f.path), "new.go")
	uri := fileuri.FromPath(target)
	raw := fmt.Sprintf(`{"title":"Add file","kind":"refactor.extract.toNewFile","edit":{"documentChanges":[{"kind":"create","uri":%q},{"textDocument":{"uri":%q,"version":0},"edits":[{"range":{"start":{"line":0,"character":0},"end":{"line":0,"character":0}},"newText":"package new\n"}]}]}}`, uri, uri)
	set := f.list(t, "["+raw+"]", false)
	if len(set.Items) != 1 || !set.Items[0].CanReview || set.Items[0].UnsupportedReason != "" {
		t.Fatalf("create action was not offered for review: %+v", set)
	}
	preview := f.prepare(t, set)
	if preview.ID == "" || len(preview.Files) != 1 || !preview.Files[0].Created ||
		preview.Files[0].AfterSHA256 != codeActionHash("package new\n") || preview.Files[0].Path != target {
		t.Fatalf("create preview lost exact postimage: %+v", preview)
	}
	if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("preview created the file early: %v", err)
	}
	result := f.workspace.LiveDocuments.ApplyReviewedEdit(t.Context(), f.owner, preview.ID, preview.Digest,
		f.workspace.LocalHistory, NewFileOpsService(filepath.Dir(f.path), nil, nil))
	if result.State != "committed" || result.Error != "" {
		t.Fatalf("create action did not commit: %+v", result)
	}
	disk, err := os.ReadFile(target)
	if err != nil || string(disk) != "package new\n" {
		t.Fatalf("created source = %q, %v", disk, err)
	}
}
func TestCodeActionCommandEditRunsOnceAndReviewsCreate(t *testing.T) {
	f := newCodeActionFixture(t, "var old = 1\r\nold\r\n")
	f.client.actionMu.Lock()
	f.client.commandSupported = true
	target := filepath.Join(filepath.Dir(f.path), "generated.go")
	uri := fileuri.FromPath(target)
	f.client.commandResponse = types.LSPCommandResponse{WorkspaceEdits: [][]byte{[]byte(fmt.Sprintf(
		`{"documentChanges":[{"kind":"create","uri":%q},{"textDocument":{"uri":%q,"version":0},"edits":[{"range":{"start":{"line":0,"character":0},"end":{"line":0,"character":0}},"newText":"package generated\n"}]}]}`,
		uri, uri))}}
	f.client.actionMu.Unlock()
	raw := `{"title":"Generate file","kind":"refactor.extract.toNewFile","command":{"title":"Generate","command":"provider.generate","arguments":[{"uri":"file:///main.go"}]}}`
	set := f.list(t, "["+raw+"]", false)
	if len(set.Items) != 1 || !set.Items[0].CanReview || set.Items[0].UnsupportedReason != "" {
		t.Fatalf("command action was not offered: %+v", set)
	}
	preview := f.prepare(t, set)
	if preview.ID == "" || len(preview.Files) != 1 || !preview.Files[0].Created ||
		preview.Files[0].AfterSHA256 != codeActionHash("package generated\n") {
		t.Fatalf("command edit lost its exact review: %+v", preview)
	}
	f.client.actionMu.Lock()
	calls := len(f.client.commands)
	f.client.actionMu.Unlock()
	if calls != 1 {
		t.Fatalf("command executions = %d, want exactly one", calls)
	}
	result := f.workspace.LiveDocuments.ApplyReviewedEdit(t.Context(), f.owner, preview.ID, preview.Digest,
		f.workspace.LocalHistory, NewFileOpsService(filepath.Dir(f.path), nil, nil))
	if result.State != "committed" || result.Error != "" {
		t.Fatalf("command create did not commit: %+v", result)
	}
	disk, err := os.ReadFile(target)
	if err != nil || string(disk) != "package generated\n" {
		t.Fatalf("generated source = %q, %v", disk, err)
	}
}

func TestCodeActionCommandBrowserOutcomeHasNoReview(t *testing.T) {
	f := newCodeActionFixture(t, "var old = 1\r\nold\r\n")
	f.client.actionMu.Lock()
	f.client.commandSupported = true
	f.client.commandResponse = types.LSPCommandResponse{Result: []byte(`"https://go.dev/gopls/features"`)}
	f.client.actionMu.Unlock()
	raw := `{"title":"Browse documentation","kind":"source.doc","command":{"title":"Browse","command":"provider.doc"}}`
	set := f.list(t, "["+raw+"]", false)
	preview := f.prepare(t, set)
	if preview.ID != "" || preview.Message != "https://go.dev/gopls/features" ||
		preview.URL != "https://go.dev/gopls/features" {
		t.Fatalf("browser command outcome = %+v", preview)
	}
}
func TestCodeActionMultiFileEditReviewsAndAppliesAllTargets(t *testing.T) {
	f := newCodeActionFixture(t, "var old = 1\r\nold\r\n")
	other := filepath.Join(filepath.Dir(f.path), "other.go")
	if err := os.WriteFile(other, []byte("var old = 1\r\nold\r\n"), 0600); err != nil {
		t.Fatal(err)
	}
	raw := `{"title":"two files","edit":` + strings.TrimSuffix(codeActionEdit(f.path), "}}") + "," + strings.TrimPrefix(codeActionEdit(other), `{"changes":{`) + `}`
	set := f.list(t, "["+raw+"]", false)
	if len(set.Items) != 1 || !set.Items[0].CanReview || set.Items[0].UnsupportedReason != "" {
		t.Fatalf("multi-file action was not offered: %+v", set)
	}
	preview := f.prepare(t, set)
	if preview.ID == "" || len(preview.Files) != 2 {
		t.Fatalf("multi-file preview = %+v", preview)
	}
	result := f.workspace.LiveDocuments.ApplyReviewedEdit(t.Context(), f.owner, preview.ID, preview.Digest,
		f.workspace.LocalHistory, NewFileOpsService(filepath.Dir(f.path), nil, nil))
	if result.State != "committed" || result.Error != "" {
		t.Fatalf("multi-file action did not commit: %+v", result)
	}
	for _, path := range []string{f.path, other} {
		data, err := os.ReadFile(path)
		if err != nil || string(data) != "var new = 1\r\nnew\r\n" {
			t.Fatalf("multi-file result %s = %q, %v", path, data, err)
		}
	}
}
