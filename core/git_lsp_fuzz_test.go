package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"crude/coreprotocol"
	"crude/document"
	"crude/fileio"
	"crude/insight/governor"
	"crude/localhistory"
	"crude/types"
	"crude/workspace"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// These targets exercise typed core command preparation and real domain owners,
// not a runtime's receipt journal or a second encoding of the request. Inputs
// select at most eight actions and sixteen bytes; all writes stay in fixtures.
const gitLSPFuzzActions = 8

func gitLSPCommand(t *testing.T, a *actor, method string, input, output any) coreprotocol.Response {
	t.Helper()
	request := coreprotocol.Request{ID: "semantic-command", Method: method, Payload: mustTestPayload(input)}
	run, ok := a.prepareService(request)
	if !ok {
		t.Fatalf("missing service method %s", method)
	}
	out := run(t.Context())
	if out.ID != request.ID {
		t.Fatal("command lost request identity")
	}
	if output != nil {
		if out.Error != nil {
			t.Fatalf("%s: %+v", method, out.Error)
		}
		if err := testDecode(out.Payload, output); err != nil {
			t.Fatal(err)
		}
	}
	return out
}

func FuzzCoreGitReviewedMutations(f *testing.F) {
	f.Add([]byte{1, 2, 2, 3, 0, 4, 1, 5, 3, 6, 4, 7, 5, 8, 6, 9})
	f.Add([]byte{7, 0, 5, 1, 6, 2, 3, 3, 2, 4, 1, 5})
	f.Add([]byte("10%0")) // stage, then discard already-identical bytes
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, sequence []byte) {
		root := t.TempDir()
		repo, err := gogit.PlainInit(root, false)
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if closer, ok := repo.Storer.(io.Closer); ok {
				if err := closer.Close(); err != nil {
					t.Error(err)
				}
			}
		}()
		cfg, err := repo.Config()
		if err != nil {
			t.Fatal(err)
		}
		cfg.Raw.Section("core").SetOption("hooksPath", filepath.Join(t.TempDir(), "no-hooks"))
		cfg.Raw.Section("core").SetOption("autocrlf", "false")
		if err := repo.SetConfig(cfg); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(root, "tracked.txt")
		write := func(text string) {
			t.Helper()
			if err := os.WriteFile(path, []byte(text), 0600); err != nil {
				t.Fatal(err)
			}
		}
		write("HEAD\n")
		wt, err := repo.Worktree()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := wt.Add("tracked.txt"); err != nil {
			t.Fatal(err)
		}
		head, err := wt.Commit("fixture", &gogit.CommitOptions{Author: &object.Signature{Name: "Fuzz", Email: "fuzz@example.test", When: time.Unix(100, 0)}})
		if err != nil {
			t.Fatal(err)
		}
		// Begin with independently different HEAD, index and working-tree bytes.
		write("index\n")
		if _, err := wt.Add("tracked.txt"); err != nil {
			t.Fatal(err)
		}
		write("working\n")
		indexText, diskText := "index\n", "working\n"
		store, err := localhistory.Open(filepath.Join(t.TempDir(), "history"), root)
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := store.Close(); err != nil {
				t.Error(err)
			}
		}()
		a := &actor{ws: &workspace.Workspace{RootPath: root, Git: workspace.NewGitService(root), LocalHistory: store}}
		compare := func(area workspace.GitChangeArea) workspace.GitComparison {
			t.Helper()
			var out coreprotocol.GitCompareResult
			gitLSPCommand(t, a, coreprotocol.GitCompare, coreprotocol.GitCompareRequest{Change: workspace.GitChange{Path: "tracked.txt", Area: area}}, &out)
			before, after := indexText, diskText
			if area == workspace.GitAreaStaged {
				before, after = "HEAD\n", indexText
			}
			c := out.Comparison
			if c.Before != before || c.After != after || c.Path != "tracked.txt" || c.AbsolutePath != path || c.Area != area || !c.BeforePresent || !c.AfterPresent || c.Fingerprint == "" {
				t.Fatalf("comparison disagrees with byte model: %+v", c)
			}
			return c
		}
		check := func() {
			t.Helper()
			// Read the actual index/blob through go-git, never GitService.Compare.
			idx, err := repo.Storer.Index()
			if err != nil || len(idx.Entries) != 1 {
				t.Fatalf("index entries: %+v %v", idx, err)
			}
			entry := idx.Entries[0]
			blob, err := repo.BlobObject(entry.Hash)
			if err != nil {
				t.Fatal(err)
			}
			reader, err := blob.Reader()
			if err != nil {
				t.Fatal(err)
			}
			data, err := io.ReadAll(reader)
			closeErr := reader.Close()
			if err != nil || closeErr != nil || entry.Name != "tracked.txt" || string(data) != indexText {
				t.Fatalf("index changed unexpectedly: %q, %v, %v", data, err, closeErr)
			}
			data, err = os.ReadFile(path)
			if err != nil || string(data) != diskText {
				t.Fatalf("worktree changed unexpectedly: %q %v", data, err)
			}
			ref, err := repo.Head()
			if err != nil || ref.Hash() != head {
				t.Fatal("review mutation changed HEAD", err)
			}
		}
		check()
		for i := 0; i < min(len(sequence)/2, gitLSPFuzzActions); i++ {
			action, value := sequence[2*i]%8, sequence[2*i+1]
			switch action {
			case 0:
				diskText = fmt.Sprintf("content-%c\n", 'a'+value%26)
				write(diskText)
			case 1, 2, 3, 4, 7:
				area, method := workspace.GitAreaWorking, coreprotocol.GitStageReviewed
				if action == 2 {
					area, method = workspace.GitAreaStaged, coreprotocol.GitUnstageReviewed
				}
				c := compare(area)
				switch action {
				case 3:
					diskText += "later\n"
					write(diskText)
				case 4:
					method = coreprotocol.GitUnstageReviewed // wrong review area
				case 7:
					c.After += "forged\n" // fingerprint alone is not authority
				}
				var input any = coreprotocol.GitStageReviewedRequest{Comparison: c}
				if method == coreprotocol.GitUnstageReviewed {
					input = coreprotocol.GitUnstageReviewedRequest{Comparison: c}
				}
				out := gitLSPCommand(t, a, method, input, nil)
				if action == 1 || action == 2 {
					if out.Error != nil {
						t.Fatal(out.Error)
					}
					if action == 1 {
						indexText = diskText
					} else {
						indexText = "HEAD\n"
					}
				} else if out.Error == nil {
					t.Fatal("stale, forged or mismatched review accepted")
				}
			case 5, 6:
				var preview coreprotocol.PreviewGitDiscardResult
				gitLSPCommand(t, a, coreprotocol.PreviewGitDiscard, coreprotocol.PreviewGitDiscardRequest{Change: workspace.GitChange{Path: "tracked.txt", Area: workspace.GitAreaWorking}}, &preview)
				check() // merely opening a preview has no effect
				if action == 6 {
					diskText += "external\n"
					write(diskText)
				}
				out := gitLSPCommand(t, a, coreprotocol.DiscardGitReviewed, coreprotocol.DiscardGitReviewedRequest(preview), nil)
				if action == 6 {
					if out.Error == nil {
						t.Fatal("stale discard accepted")
					}
					break
				}
				var discarded coreprotocol.DiscardGitReviewedResult
				if out.Error != nil || testDecode(out.Payload, &discarded) != nil || discarded.SafetyID == "" {
					t.Fatalf("discard lacks mandatory recovery: %+v", out.Error)
				}
				before := diskText
				diskText = indexText
				check()
				manifest, err := store.LoadManifest(discarded.SafetyID)
				digest := sha256.Sum256([]byte(before))
				if err != nil || manifest.Kind != "safety" || manifest.Entries["tracked.txt"].Hash != hex.EncodeToString(digest[:]) {
					t.Fatal("safety did not retain exact preimage", err)
				}
				plan, err := store.PlanRestorePaths(t.Context(), discarded.SafetyID, localhistory.SnapshotOptions{}, []string{"tracked.txt"})
				if err != nil {
					t.Fatal(err)
				}
				if before == indexText {
					if len(plan.Changes) != 0 {
						t.Fatal("identical bytes produced a restore change")
					}
				} else {
					if len(plan.Changes) != 1 || plan.Changes[0].Path != "tracked.txt" {
						t.Fatal("safety restore omitted the discarded preimage")
					}
					if _, err := store.ApplyRestore(t.Context(), plan.ID, []string{"tracked.txt"}); err != nil {
						t.Fatal(err)
					}
				}
				diskText = before
			}
			check()
		}
	})
}

// The real manager owns lifecycle/admission; only the language engine is fake.
// This deliberately does not claim stdio framing, parser or real server coverage.
type semanticLSPClient struct {
	mu          sync.Mutex
	calls       []semanticLSPCall
	edit        []byte
	boundary    types.InsightBoundary
	version     int
	diagnostics []types.LSPDiagnostic
	admit       func() error
}

type semanticLSPCall struct {
	Path               string
	Line, Col          int
	IncludeDeclaration bool
}

type semanticLSPRuntime struct{ client *semanticLSPClient }

func (r semanticLSPRuntime) NewClient(_ types.LSPServerSpec, _ string, admit func() error) workspace.LSPClient {
	r.client.mu.Lock()
	r.client.admit = admit
	r.client.mu.Unlock()
	return r.client
}
func (*semanticLSPClient) OnDiagnostics(func(types.InsightDiagnosticPublication)) {}
func (c *semanticLSPClient) Start(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	admit := c.admit
	c.mu.Unlock()
	if admit != nil {
		return admit()
	}
	return nil
}
func (*semanticLSPClient) Stop() error           { return nil }
func (*semanticLSPClient) Done() <-chan struct{} { return nil }
func (c *semanticLSPClient) OpenFile(_ context.Context, _, _, _ string, boundary types.InsightBoundary) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.boundary = boundary
	c.version++
	return c.version, nil
}
func (c *semanticLSPClient) UpdateFile(ctx context.Context, path, text string, boundary types.InsightBoundary) (int, error) {
	return c.OpenFile(ctx, path, "", text, boundary)
}
func (*semanticLSPClient) CloseFile(context.Context, string)                      {}
func (*semanticLSPClient) NotifySave(context.Context, string, string, int, bool)  {}
func (*semanticLSPClient) NotifyWatchedFilesChanged(context.Context, string, int) {}
func (c *semanticLSPClient) AwaitDiagnostics(context.Context, string) (types.InsightDiagnosticPublication, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v := c.version
	return types.InsightDiagnosticPublication{Boundary: c.boundary, Sequence: 1, WireVersion: v, ReportedVersion: &v, Diagnostics: append([]types.LSPDiagnostic{}, c.diagnostics...)}, true
}
func (c *semanticLSPClient) record(path string, line, col int, name string, include bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, semanticLSPCall{Path: path, Line: line, Col: col, IncludeDeclaration: include})
}
func (c *semanticLSPClient) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.calls)
}
func (c *semanticLSPClient) RequestNavigation(_ context.Context, path, kind string, line, col int, include bool) (types.LSPNavigationResponse, error) {
	c.record(path, line, col, "", include)
	c.mu.Lock()
	defer c.mu.Unlock()
	return types.LSPNavigationResponse{Kind: kind, WireVersion: c.version,
		JSON: []byte(`[{"uri":"file:///fuzz/definition.go","range":{"start":{"line":2,"character":3},"end":{"line":4,"character":5}}}]`)}, nil
}
func (c *semanticLSPClient) RequestHover(_ context.Context, path string, line, col int) (string, error) {
	c.record(path, line, col, "", false)
	return "hover **symbol**\nsecond line", nil
}
func (c *semanticLSPClient) RequestSignatureHelp(_ context.Context, path string, line, col int) (types.LSPSignatureHelp, error) {
	c.record(path, line, col, "", false)
	return types.LSPSignatureHelp{Signatures: []types.LSPSignature{{Label: "call(value int)", ActiveParameter: 0,
		Parameters: []types.LSPSignatureParameter{{Label: "value int"}}}}}, nil
}
func (c *semanticLSPClient) RequestRename(_ context.Context, path string, line, col int, name string) (types.LSPRenameEdit, error) {
	c.record(path, line, col, name, false)
	c.mu.Lock()
	defer c.mu.Unlock()
	return types.LSPRenameEdit{JSON: append([]byte(nil), c.edit...), WireVersion: c.version}, nil
}
func (c *semanticLSPClient) RequestCodeActions(context.Context, string, int, int, int, int) (types.LSPCodeActionResponse, error) {
	return types.LSPCodeActionResponse{}, context.Canceled
}
func (c *semanticLSPClient) ResolveCodeAction(context.Context, string, int, []byte) (types.LSPCodeActionResponse, error) {
	return types.LSPCodeActionResponse{}, context.Canceled
}
func (c *semanticLSPClient) RequestInlayHints(context.Context, string, types.LSPInlayRange) (types.LSPInlayResponse, error) {
	return types.LSPInlayResponse{}, context.Canceled
}
func (c *semanticLSPClient) ResolveInlayHint(context.Context, string, int, []byte) (types.LSPInlayResponse, error) {
	return types.LSPInlayResponse{}, context.Canceled
}
func (c *semanticLSPClient) RequestCompletions(_ context.Context, path string, line, col int) (types.LSPCompletionResponse, error) {
	c.record(path, line, col, "", false)
	c.mu.Lock()
	defer c.mu.Unlock()
	return types.LSPCompletionResponse{WireVersion: c.version, JSON: []byte(`[{"label":"complete","detail":"detail","documentation":"docs","kind":3,"insertText":"insert"}]`)}, nil
}
func (c *semanticLSPClient) ResolveCompletion(context.Context, string, int, []byte) (types.LSPCompletionResponse, error) {
	return types.LSPCompletionResponse{}, context.Canceled
}
func (c *semanticLSPClient) RequestDocumentSymbols(_ context.Context, path string) []workspace.LSPSymbol {
	c.record(path, 0, 0, "", false)
	return []workspace.LSPSymbol{{Name: "parent", Detail: "outline", Kind: 12, KindName: "function", Line: 1, Col: 2, EndLine: 9, EndCol: 10, Children: []workspace.LSPSymbol{{Name: "child", Line: 3, Col: 4, EndLine: 5, EndCol: 6}}}}
}

func FuzzCoreLSPRequests(f *testing.F) {
	f.Add([]byte{0, 0, 1, 1, 0, 2, 1, 3})
	f.Add([]byte{0, 0, 1, 1, 2, 2, 3, 3, 4, 4, 5, 5, 6, 6, 7, 7})
	f.Add([]byte{0, 255, 3, 128, 1, 64, 4, 32, 2, 16, 5, 8})
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, sequence []byte) {
		root := t.TempDir()
		path := filepath.Join(root, "main.go")
		if err := os.WriteFile(path, []byte("package main\n"), 0600); err != nil {
			t.Fatal(err)
		}
		client := &semanticLSPClient{}
		manager := workspace.NewLSPManager(nil, semanticLSPRuntime{client})
		defer manager.Close()
		manager.SetWorkspaceRoot(root)
		// The semantic transport fixture opts in explicitly. This is not a
		// production constructor default or a grant from a session feature flag.
		identity, err := fileio.DirectoryIdentity(root)
		if err != nil {
			t.Fatal(err)
		}
		g, err := governor.New(governor.Limits{MaxProcesses: 2, MaxWaiting: 4})
		if err != nil {
			t.Fatal(err)
		}
		if err := manager.SetAnalysisGovernor(g, "fuzz-workspace", "fuzz-generation"); err != nil {
			t.Fatal(err)
		}
		manager.SetAnalysisPolicy(types.WorkspaceAnalysisSettings{Trust: types.AnalysisProjectExecutionAllowed}, identity)
		docs := workspace.NewDocumentService(root, nil, manager)
		defer docs.Close()
		snapshot, err := docs.Open(document.Owner{ClientID: "fuzz", Epoch: 1}, path)
		if err != nil {
			t.Fatal(err)
		}
		// Probe waits for the manager's exact document synchronization boundary,
		// without sleeping/polling or starting an external executable.
		ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
		defer cancel()
		if _, settled, _, err := manager.ProbeDiagnostics(ctx, path); err != nil || !settled {
			t.Fatal("manager failed to admit fixture", err)
		}
		a := &actor{ws: &workspace.Workspace{RootPath: root, LSPMgr: manager, LiveDocuments: docs}}
		source := types.InsightSource{Kind: "canonical", WorkspaceGeneration: snapshot.AnalysisGeneration, DocumentID: snapshot.ID, Lifetime: snapshot.AnalysisLifetime, Path: snapshot.Path, Revision: snapshot.Revision}
		methods := [...]string{coreprotocol.LSPNavigationMethod, coreprotocol.LSPNavigationMethod, coreprotocol.LSPRequestHover, coreprotocol.LSPRequestSignatureHelp, coreprotocol.LSPCompletionsMethod, coreprotocol.LSPRequestDocumentSymbols}
		for i := 0; i < min(len(sequence)/2, gitLSPFuzzActions); i++ {
			action, value := sequence[2*i]%8, sequence[2*i+1]
			method := methods[int(action)%len(methods)]
			in := coreprotocol.LSPRequest{Path: path, Line: int(int8(value)), Col: int(value % 17)}
			if action < 2 {
				in.Line /= 16
			}
			if action >= 6 {
				method = coreprotocol.LSPRequestHover
			}
			if action == 7 {
				// These are unopened paths, not a claim that core normalizes/fences
				// requests: the manager only admits its exact retained document.
				in.Path = []string{"main.go", filepath.Join(root, "unopened.go"), filepath.Join(filepath.Dir(root), "outside.go"), "../outside.go"}[value%4]
			}
			request := coreprotocol.Request{ID: fmt.Sprintf("lsp-%d", i), Method: method, Owner: coreprotocol.Owner{ClientID: "fuzz", Epoch: 1}, Payload: mustTestPayload(in)}
			if action == 4 {
				request.Payload = mustTestPayload(coreprotocol.CompletionsRequest{Source: source, ViewID: "fuzz-completion", Sequence: uint64(i + 1), Line: in.Line, Col: in.Col})
			}
			kind := []string{"definition", "type-definition", "implementation", "references"}[value%4]
			if action < 2 {
				request.Payload = mustTestPayload(coreprotocol.NavigationRequest{Source: source, Kind: kind, Line: in.Line, Col: in.Col, IncludeDeclaration: kind == "references"})
			}
			run, ok := a.prepareLSP(request)
			if !ok {
				t.Fatal("known LSP method missing")
			}
			before := client.count()
			callCtx := t.Context()
			if action == 6 {
				cancelled, stop := context.WithCancel(callCtx)
				stop()
				callCtx = cancelled
			}
			out := run(callCtx)
			if out.ID != request.ID {
				t.Fatal("LSP response identity lost")
			}
			if action == 6 || action == 7 {
				if client.count() != before {
					t.Fatal("cancelled/unopened request reached language client")
				}
				if action == 6 && (out.Error == nil || out.Error.Code != "cancelled") {
					t.Fatal("pre-cancelled worker did not refuse", out.Error)
				}
				if action == 7 && out.Error == nil {
					var result coreprotocol.LSPResult
					if err := testDecode(out.Payload, &result); err != nil || result.Hover != "" {
						t.Fatal("unopened query returned a result", err)
					}
				}
				continue
			}
			validNavigationPosition := in.Line == 0 && in.Col <= 12 || in.Line == 1 && in.Col == 0
			if (action < 2 || action == 4) && !validNavigationPosition {
				if out.Error == nil || client.count() != before {
					t.Fatal("invalid navigation source position reached provider", out.Error)
				}
				continue
			}
			if out.Error != nil || client.count() != before+1 {
				t.Fatal("query did not reach one real-manager client", out.Error)
			}
			client.mu.Lock()
			gotInput := client.calls[len(client.calls)-1]
			client.mu.Unlock()
			wantInput := semanticLSPCall{Path: path, Line: in.Line, Col: in.Col}
			switch action {
			case 0, 1:
				wantInput.IncludeDeclaration = kind == "references"
			case 5:
				wantInput.Line, wantInput.Col = 0, 0
			}
			// Reader coordinates are currently forwarded, not validated here.
			if gotInput != wantInput {
				t.Fatalf("query arguments changed: got %+v want %+v", gotInput, wantInput)
			}
			if action < 2 {
				var result coreprotocol.NavigationReply
				if err := testDecode(out.Payload, &result); err != nil || result.Result.Answer != "targets" || result.Result.Kind != kind || result.Result.Boundary.Source != source || len(result.Result.Targets) != 1 {
					t.Fatal("qualified navigation reply changed", err, result)
				}
				target := result.Result.Targets[0]
				if target.URI != "file:///fuzz/definition.go" || target.Selection.Start.Line != 2 || target.Selection.Start.Character != 3 || target.Range.End.Line != 4 || target.Range.End.Character != 5 || !target.External {
					t.Fatal("original target changed", target)
				}
				continue
			}
			if action == 4 {
				var result coreprotocol.CompletionReply
				if err := testDecode(out.Payload, &result); err != nil || result.Set.Boundary.Source != source || len(result.Set.Entries) != 1 {
					t.Fatal("qualified completion reply changed", err, result)
				}
				row := result.Set.Entries[0]
				if row.Index != 0 || row.ID != "item-0" || row.Insertion.Label != "complete" || row.Insertion.InsertText != "insert" ||
					row.Documentation.Value != "docs" || row.Documentation.Detail != "detail" {
					t.Fatal("original completion identity/payload lost", row)
				}
				continue
			}
			var result coreprotocol.LSPResult
			if err := testDecode(out.Payload, &result); err != nil || !result.Applied {
				t.Fatal("query result missing", err)
			}
			want := coreprotocol.LSPResult{Applied: true}
			switch action {
			case 2:
				want.Hover = "hover **symbol**\nsecond line"
			case 3:
				want.SignatureHelp = types.LSPSignatureHelp{Signatures: []types.LSPSignature{{Label: "call(value int)", ActiveParameter: 0, Parameters: []types.LSPSignatureParameter{{Label: "value int"}}}}}
				// The manager, not the language fake, qualifies this semantic result.
				want.SignatureHelp.Boundary = result.SignatureHelp.Boundary
			case 5:
				want.Symbols = []workspace.LSPSymbol{{Name: "parent", Detail: "outline", Kind: 12, KindName: "function", Line: 1, Col: 2, EndLine: 9, EndCol: 10, Children: []workspace.LSPSymbol{{Name: "child", Line: 3, Col: 4, EndLine: 5, EndCol: 6}}}}
			}
			if !reflect.DeepEqual(result, want) {
				t.Fatalf("typed query result changed: got %+v want %+v", result, want)
			}

		}
		if run, ok := a.prepareLSP(coreprotocol.Request{Method: "lsp.arbitrary"}); ok || run != nil {
			t.Fatal("LSP dispatcher admitted an undeclared method")
		}
		if data, err := os.ReadFile(path); err != nil || string(data) != "package main\n" {
			t.Fatal("query wrote source file", err)
		}
	})
}
