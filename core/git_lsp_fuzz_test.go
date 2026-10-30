package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
	"crude/localhistory"
	"crude/types"
	"crude/workspace"

	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol"
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
	calls       []coreprotocol.LSPRequest
	edit        *protocol.WorkspaceEdit
	boundary    types.InsightBoundary
	version     int
	diagnostics []types.LSPDiagnostic
}

type semanticLSPRuntime struct{ client *semanticLSPClient }

func (r semanticLSPRuntime) NewClient(types.LSPServerSpec, string) workspace.LSPClient {
	return r.client
}
func (*semanticLSPClient) OnDiagnostics(func(types.InsightDiagnosticPublication)) {}
func (*semanticLSPClient) Start(context.Context) error                            { return nil }
func (*semanticLSPClient) Stop() error                                            { return nil }
func (*semanticLSPClient) Done() <-chan struct{}                                  { return nil }
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
	c.calls = append(c.calls, coreprotocol.LSPRequest{Path: path, Line: line, Col: col, NewName: name, IncludeDeclaration: include})
}
func (c *semanticLSPClient) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.calls)
}
func (c *semanticLSPClient) RequestDefinition(_ context.Context, path string, line, col int) []workspace.LSPLocation {
	c.record(path, line, col, "", false)
	return []workspace.LSPLocation{{URI: "file:///fuzz/definition.go", Path: "/fuzz/definition.go", Line: 2, Col: 3, EndLine: 4, EndCol: 5}}
}
func (c *semanticLSPClient) FindReferences(_ context.Context, path string, line, col int, include bool) []workspace.LSPLocation {
	c.record(path, line, col, "", include)
	return []workspace.LSPLocation{{URI: "file:///fuzz/ref.go", Path: "/fuzz/ref.go", Line: 6, Col: 7, EndLine: 8, EndCol: 9}}
}
func (c *semanticLSPClient) RequestHover(_ context.Context, path string, line, col int) (string, error) {
	c.record(path, line, col, "", false)
	return "hover **symbol**\nsecond line", nil
}
func (c *semanticLSPClient) RequestRename(_ context.Context, path string, line, col int, name string) (*protocol.WorkspaceEdit, error) {
	c.record(path, line, col, name, false)
	return c.edit, nil
}
func (c *semanticLSPClient) RequestCompletions(_ context.Context, path string, line, col int) []workspace.CompletionItem {
	c.record(path, line, col, "", false)
	return []workspace.CompletionItem{{Label: "complete", Detail: "detail", Documentation: "docs", Kind: 3, InsertText: "insert", TextEdit: &types.LSPTextEdit{Line: 1, Col: 2, EndLine: 3, EndCol: 4, NewText: "primary"}, AdditionalTextEdits: []types.LSPTextEdit{{Line: 5, Col: 6, EndLine: 7, EndCol: 8, NewText: "import"}}}}
}
func (c *semanticLSPClient) RequestDocumentSymbols(_ context.Context, path string) []workspace.LSPSymbol {
	c.record(path, 0, 0, "", false)
	return []workspace.LSPSymbol{{Name: "parent", Detail: "outline", Kind: 12, KindName: "function", Line: 1, Col: 2, EndLine: 9, EndCol: 10, Children: []workspace.LSPSymbol{{Name: "child", Line: 3, Col: 4, EndLine: 5, EndCol: 6}}}}
}

func FuzzCoreLSPRequests(f *testing.F) {
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
		// A literal independent LSP fixture exercises both edit representations,
		// ranges, version, multiple URIs and JSON union decoding in LSPEdit.
		const editJSON = `{"changes":{"file:///fuzz/a.go":[{"range":{"start":{"line":1,"character":2},"end":{"line":3,"character":4}},"newText":"alpha"}],"file:///fuzz/b.go":[{"range":{"start":{"line":5,"character":6},"end":{"line":7,"character":8}},"newText":"beta"}]},"documentChanges":[{"textDocument":{"uri":"file:///fuzz/c.go","version":9},"edits":[{"range":{"start":{"line":10,"character":11},"end":{"line":12,"character":13}},"newText":"gamma"}]}]}`
		if err := json.Unmarshal([]byte(editJSON), &client.edit); err != nil {
			t.Fatal(err)
		}
		manager := workspace.NewLSPManager(nil, semanticLSPRuntime{client})
		defer manager.Close()
		manager.SetWorkspaceRoot(root)
		docs := workspace.NewDocumentService(root, nil, manager)
		defer docs.Close()
		if _, err := docs.Open(document.Owner{ClientID: "fuzz", Epoch: 1}, path); err != nil {
			t.Fatal(err)
		}
		// Probe waits for the manager's exact document synchronization boundary,
		// without sleeping/polling or starting an external executable.
		ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
		defer cancel()
		if _, settled, _, err := manager.ProbeDiagnostics(ctx, path); err != nil || !settled {
			t.Fatal("manager failed to admit fixture", err)
		}
		a := &actor{ws: &workspace.Workspace{RootPath: root, LSPMgr: manager}}
		methods := [...]string{coreprotocol.LSPRequestDefinition, coreprotocol.LSPFindReferences, coreprotocol.LSPRequestHover, coreprotocol.LSPRequestRename, coreprotocol.LSPRequestCompletions, coreprotocol.LSPRequestDocumentSymbols}
		for i := 0; i < min(len(sequence)/2, gitLSPFuzzActions); i++ {
			action, value := sequence[2*i]%8, sequence[2*i+1]
			method := methods[int(action)%len(methods)]
			in := coreprotocol.LSPRequest{Path: path, Line: int(int8(value)), Col: int(value % 17), NewName: []string{"renamed", "", "with space", "symbol_2"}[value%4], IncludeDeclaration: value&1 != 0}
			if action == 7 {
				// These are unopened paths, not a claim that core normalizes/fences
				// requests: the manager only admits its exact retained document.
				in.Path = []string{"main.go", filepath.Join(root, "unopened.go"), filepath.Join(filepath.Dir(root), "outside.go"), "../outside.go"}[value%4]
			}
			request := coreprotocol.Request{ID: fmt.Sprintf("lsp-%d", i), Method: method, Payload: mustTestPayload(in)}
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
					if err := testDecode(out.Payload, &result); err != nil || len(result.Locations) != 0 || result.Edit != nil {
						t.Fatal("unopened query returned a result", err)
					}
				}
				continue
			}
			if out.Error != nil || client.count() != before+1 {
				t.Fatal("query did not reach one real-manager client", out.Error)
			}
			client.mu.Lock()
			gotInput := client.calls[len(client.calls)-1]
			client.mu.Unlock()
			wantInput := coreprotocol.LSPRequest{Path: path, Line: in.Line, Col: in.Col}
			switch action {
			case 1:
				wantInput.IncludeDeclaration = in.IncludeDeclaration
			case 3:
				wantInput.NewName = in.NewName
			case 5:
				wantInput.Line, wantInput.Col = 0, 0
			}
			// Coordinates and names are currently forwarded, not validated here.
			if gotInput != wantInput {
				t.Fatalf("query arguments changed: got %+v want %+v", gotInput, wantInput)
			}
			var result coreprotocol.LSPResult
			if err := testDecode(out.Payload, &result); err != nil || !result.Applied {
				t.Fatal("query result missing", err)
			}
			want := coreprotocol.LSPResult{Applied: true}
			switch action {
			case 0:
				want.Locations = []workspace.LSPLocation{{URI: "file:///fuzz/definition.go", Path: "/fuzz/definition.go", Line: 2, Col: 3, EndLine: 4, EndCol: 5}}
			case 1:
				want.Locations = []workspace.LSPLocation{{URI: "file:///fuzz/ref.go", Path: "/fuzz/ref.go", Line: 6, Col: 7, EndLine: 8, EndCol: 9}}
			case 2:
				want.Hover = "hover **symbol**\nsecond line"
			case 3:
				want.Edit = (*coreprotocol.LSPEdit)(client.edit)
			case 4:
				want.Completions = []workspace.CompletionItem{{Label: "complete", Detail: "detail", Documentation: "docs", Kind: 3, InsertText: "insert", TextEdit: &types.LSPTextEdit{Line: 1, Col: 2, EndLine: 3, EndCol: 4, NewText: "primary"}, AdditionalTextEdits: []types.LSPTextEdit{{Line: 5, Col: 6, EndLine: 7, EndCol: 8, NewText: "import"}}}}
			case 5:
				want.Symbols = []workspace.LSPSymbol{{Name: "parent", Detail: "outline", Kind: 12, KindName: "function", Line: 1, Col: 2, EndLine: 9, EndCol: 10, Children: []workspace.LSPSymbol{{Name: "child", Line: 3, Col: 4, EndLine: 5, EndCol: 6}}}}
			}
			if !reflect.DeepEqual(result, want) {
				t.Fatalf("typed query result changed: got %+v want %+v", result, want)
			}
			if action == 3 {
				result.Edit.Changes["file:///fuzz/a.go"][0].NewText = "caller mutation"
				if client.edit.Changes["file:///fuzz/a.go"][0].NewText != "alpha" {
					t.Fatal("decoded edit aliases language client")
				}
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
