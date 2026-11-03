package workspace

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"crude/document"
	"crude/insight/catalog"
	"crude/internal/fileuri"
	"crude/toolstore"
	model "crude/types"
)

// fixturePack defines two languages that no Crude Go code and no built-in
// catalog entry knows. "fixturelang" has a server that checks the whole
// project; "fixturemissing" names a server that is not installed.
const fixturePack = `{
  "format": 1,
  "languages": [
    {"id": "fixturelang", "name": "Fixture", "extensions": [".fxl"], "lsp_language_id": "fixturelang",
      "roots": ["fixture.project"], "servers": ["fixture-ls"], "comment_tokens": [";;"]},
    {"id": "fixturemissing", "name": "Fixture Missing", "extensions": [".fxm"], "lsp_language_id": "fixturemissing",
     "servers": ["fixture-absent"]}
  ],
  "servers": [
      {"id": "fixture-ls", "command": "fixture-ls", "diagnostic_scope": "project",
       "args": ["-test.run=^TestFixtureLanguageServerHelper$"], "environment": {"CRUDE_FIXTURE_LS":"1"}},
    {"id": "fixture-absent", "command": "crude-fixture-server-that-is-not-installed"}
  ]
}`

// useFixturePack loads the fixture pack for one test and restores the
// embedded catalog afterwards.
func useFixturePack(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "fixture.json"), []byte(fixturePack), 0o644); err != nil {
		t.Fatal(err)
	}
	if problems := catalog.UsePacks(dir); len(problems) != 0 {
		t.Fatalf("fixture pack rejected: %+v", problems)
	}
	t.Cleanup(func() { catalog.UsePacks(filepath.Join(dir, "none")) })
}

// TestFixtureLanguageServerHelper is the fixture language's server when
// CRUDE_FIXTURE_LS is set. It reports an error for every line that contains
// ERROR in any .fxl file under the root, for files nobody opened, and checks
// again when files change.
func TestFixtureLanguageServerHelper(t *testing.T) {
	if os.Getenv("CRUDE_FIXTURE_LS") == "" {
		return
	}
	in := bufio.NewReader(os.Stdin)
	write := func(message any) {
		data, _ := json.Marshal(message)
		fmt.Fprintf(os.Stdout, "Content-Length: %d\r\n\r\n%s", len(data), data)
	}
	root := ""
	check := func() {
		_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || filepath.Ext(path) != ".fxl" {
				return nil
			}
			data, _ := os.ReadFile(path)
			diagnostics := []any{}
			for line, text := range strings.Split(string(data), "\n") {
				if col := strings.Index(text, "ERROR"); col >= 0 {
					diagnostics = append(diagnostics, map[string]any{"severity": 1, "message": "fixture error",
						"range": map[string]any{"start": map[string]any{"line": line, "character": col}, "end": map[string]any{"line": line, "character": col + 5}}})
				}
			}
			uri := fileuri.FromPath(path)
			write(map[string]any{"jsonrpc": "2.0", "method": "textDocument/publishDiagnostics", "params": map[string]any{"uri": uri, "diagnostics": diagnostics}})
			return nil
		})
	}
	for {
		length := 0
		for {
			line, err := in.ReadString('\n')
			if err != nil {
				os.Exit(0)
			}
			if line == "\r\n" {
				break
			}
			if strings.HasPrefix(line, "Content-Length:") {
				length, _ = strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "Content-Length:")))
			}
		}
		data := make([]byte, length)
		if _, err := io.ReadFull(in, data); err != nil {
			os.Exit(0)
		}
		var msg struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params struct {
				RootURI string `json:"rootUri"`
			} `json:"params"`
		}
		if json.Unmarshal(data, &msg) != nil {
			os.Exit(2)
		}
		switch msg.Method {
		case "initialize":
			if path, err := fileuri.ToPath(msg.Params.RootURI); err == nil {
				root = path
			}
			if trace := os.Getenv("CRUDE_FIXTURE_TRACE"); trace != "" {
				f, err := os.OpenFile(trace, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
				if err != nil {
					os.Exit(3)
				}
				_, _ = fmt.Fprintln(f, root)
				_ = f.Close()
			}
			switch os.Getenv("CRUDE_FIXTURE_MODE") {
			case "fail_initialize":
				write(map[string]any{"jsonrpc": "2.0", "id": msg.ID, "error": map[string]any{"code": -32603, "message": "fixture initialization refused"}})
				continue
			case "stall_initialize":
				// Keep reading so shutdown can still join this exact process.
				continue
			}
			write(map[string]any{"jsonrpc": "2.0", "id": msg.ID, "result": map[string]any{"capabilities": map[string]any{}}})
		case "initialized", "workspace/didChangeWatchedFiles":
			check()
		case "exit":
			os.Exit(0)
		default:
			if len(msg.ID) > 0 {
				write(map[string]any{"jsonrpc": "2.0", "id": msg.ID, "result": nil})
			}
		}
	}
}

func writeTree(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	createFile(t, path, text)
}

func awaitAssembly(t *testing.T, m *LSPManager, ready func(model.AssemblyPlan) bool) model.AssemblyPlan {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if plan := m.Assembly(); ready(plan) {
			return plan
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("assembly did not settle: %+v", m.Assembly())
	return model.AssemblyPlan{}
}

func assemblyLanguage(plan model.AssemblyPlan, id string) (model.AssemblyLanguage, bool) {
	for _, language := range plan.Languages {
		if language.ID == id {
			return language, true
		}
	}
	return model.AssemblyLanguage{}, false
}

// A language that exists only in a catalog pack runs through every stage
// with no Go change: discovery finds its files, native setup approves catalog
// acquisition, independent evaluation initializes the staged executable, then
// activation starts a whole-project check, and Problems gets an
// error for a file nobody opened. Fixing the file clears the error.
func TestFixtureLanguageRunsThroughEveryStage(t *testing.T) {
	var doc catalog.Document
	if err := json.Unmarshal([]byte(fixturePack), &doc); err != nil {
		t.Fatal(err)
	}
	doc.Servers[0].Package = "fixture-acquired"
	trace := filepath.Join(t.TempDir(), "roots.txt")
	doc.Servers[0].Environment["CRUDE_FIXTURE_TRACE"] = trace
	doc.Packages = append(doc.Packages, catalog.PackageRecord{Name: "fixture-acquired", Licenses: []string{"MIT"},
		Source: catalog.PackageSourceRecord{ID: "pkg:github/fixture/fixture-acquired@v1",
			Assets: []catalog.AssetRecord{{Targets: catalog.CurrentTargets(), File: "server.zip", Bin: setupFixtureBin()}}},
		Bin: map[string]string{"fixture-ls": "{{source.asset.bin}}"}})
	pack := t.TempDir()
	if err := os.WriteFile(filepath.Join(pack, "fixture.json"), mustJSON(t, doc), 0600); err != nil {
		t.Fatal(err)
	}
	if problems := catalog.UsePacks(pack); len(problems) != 0 {
		t.Fatal(problems)
	}
	t.Cleanup(func() { catalog.UsePacks(filepath.Join(pack, "none")) })
	serveCatalogArtifact(t, fixtureExecutableArchive(t, setupFixtureBin()), "/fixture/fixture-acquired/releases/download/v1/server.zip")
	store, err := toolstore.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	w, _ := decisionFixture(t)
	root, m := w.RootPath, w.LSPMgr
	writeTree(t, filepath.Join(root, "fixture.project"), "")
	broken := filepath.Join(root, "src", "broken.fxl")
	writeTree(t, broken, "fine\nan ERROR here\n")
	writeTree(t, filepath.Join(root, "src", "clean.fxl"), "fine\n")
	writeTree(t, filepath.Join(root, "unsupported.fxm"), "still editable\n")

	m.SetToolStore(store)
	probeSources(t, m, root)
	trustAnalysis(t, m, root)
	m.RefreshAssembly()

	awaitAssembly(t, m, func(p model.AssemblyPlan) bool {
		language, ok := assemblyLanguage(p, "fixturelang")
		return p.State == model.AssemblyReady && ok && language.Status == model.ToolMissing
	})
	setup := NewProjectToolSetupOwner(w, store)
	defer setup.Close()
	owner := document.Owner{ClientID: "native-fixture-shell", Epoch: 1}
	review := setupReview(t, setup, owner)
	var selected []string
	for _, item := range review.Review.Items {
		if item.Package == "fixture-acquired" {
			selected = append(selected, item.ID)
		}
	}
	if len(selected) != 1 || !review.NeedsDecision {
		t.Fatalf("native setup needs: %+v", review)
	}
	result, err := setup.Decide(t.Context(), owner, "native-fixture-install", review.ID, selected)
	if err != nil || len(result.Items) != 1 || !result.Items[0].Installed || result.Items[0].Evaluation != toolEvaluationPassed {
		t.Fatalf("acquire/evaluate: %+v %v", result, err)
	}
	plan := awaitAssembly(t, m, func(p model.AssemblyPlan) bool {
		language, ok := assemblyLanguage(p, "fixturelang")
		return p.State == model.AssemblyReady && ok && language.Status == model.ToolReady
	})
	language, ok := assemblyLanguage(plan, "fixturelang")
	if !ok || language.Status != model.ToolReady || language.Scope != model.CoverageProject ||
		language.Files != 2 || language.Root != root || language.Source != model.ToolSourceStore {
		t.Fatalf("fixture language plan = %+v (found %v)", language, ok)
	}

	deadline := time.Now().Add(20 * time.Second)
	for {
		batch, ok := m.FileDiagnostics(broken)
		if ok && batch.FromProjectCheck() && len(batch.Diagnostics) == 1 && batch.Diagnostics[0].Line == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no project error for the unopened file: %+v %v; health %+v", batch, ok, m.ServerHealthSnapshots())
		}
		time.Sleep(20 * time.Millisecond)
	}
	if summary := m.ProjectInsights(); summary.Errors != 1 || summary.FilesAffected != 1 {
		t.Fatalf("status summary = %+v", summary)
	}
	m.mu.Lock()
	openedDocuments := len(m.documents)
	m.mu.Unlock()
	if openedDocuments != 0 {
		t.Fatalf("project diagnostic required opening %d documents", openedDocuments)
	}
	missing, ok := assemblyLanguage(plan, "fixturemissing")
	if !ok || missing.Status != model.ToolMissing || !strings.Contains(plan.Message, "Install the missing servers") {
		t.Fatalf("partial project support was hidden: %+v", plan)
	}
	roots, err := os.ReadFile(trace)
	if err != nil {
		t.Fatal(err)
	}
	checkedRoots := strings.Split(strings.TrimSpace(string(roots)), "\n")
	if len(checkedRoots) != 2 || checkedRoots[0] == root || checkedRoots[1] != root {
		t.Fatalf("independent check must precede project activation: %q", checkedRoots)
	}
	if _, err := os.Stat(checkedRoots[0]); !os.IsNotExist(err) {
		t.Fatalf("joined check retained its scratch root: %v", err)
	}
	var project bool
	for _, health := range m.ServerHealthSnapshots() {
		project = project || health.Scope == model.CoverageProject && strings.Contains(health.Coverage, "whole project")
	}
	if !project {
		t.Fatalf("coverage does not state the whole-project check: %+v", m.ServerHealthSnapshots())
	}

	// The fix clears the error: the server checks again when files change.
	createFile(t, broken, "fine\nnow fixed\n")
	m.NotifyWatchedFilesChanged(context.Background(), broken, 2)
	deadline = time.Now().Add(20 * time.Second)
	for {
		if batch, _ := m.FileDiagnostics(broken); len(batch.Diagnostics) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the fixed file still shows the error")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// A server that is not installed is never launched. The plan and the status
// name the missing program, and editing the file stays usable.
func TestMissingServerIsNamedAndNotLaunched(t *testing.T) {
	useFixturePack(t)
	m, r, root := lifecycleManager(t)
	path := filepath.Join(root, "notes.fxm")
	createFile(t, path, "text\n")
	m.RefreshAssembly()
	plan := awaitAssembly(t, m, func(p model.AssemblyPlan) bool {
		_, ok := assemblyLanguage(p, "fixturemissing")
		return p.State == model.AssemblyReady && ok
	})
	language, _ := assemblyLanguage(plan, "fixturemissing")
	if language.Status != model.ToolMissing || !strings.Contains(language.Reason, "crude-fixture-server-that-is-not-installed") {
		t.Fatalf("missing server plan = %+v", language)
	}
	if !strings.Contains(plan.Message, "Install the missing servers") {
		t.Fatalf("plan message = %q", plan.Message)
	}
	editorSource(t, m, path, "text\nmore text\n")
	if got := m.ProjectInsights().Language; got.State != model.LanguageNoServer || !strings.Contains(got.Message, "not installed") {
		t.Fatalf("status = %+v", got)
	}
	time.Sleep(100 * time.Millisecond)
	if len(r.snapshot()) != 0 {
		t.Fatal("a missing server was launched")
	}
}

// Discovery reads names only, skips hidden folders, and picks the file
// closest to the root as the language's sample.
func TestDiscoveryCountsLanguagesAndPicksShallowSample(t *testing.T) {
	useFixturePack(t)
	root := t.TempDir()
	writeTree(t, filepath.Join(root, "deep", "er", "a.fxl"), "")
	writeTree(t, filepath.Join(root, "top.fxl"), "")
	writeTree(t, filepath.Join(root, ".hidden", "b.fxl"), "")
	writeTree(t, filepath.Join(root, "readme.unknownext"), "")
	found := discoverProjectLanguages(context.Background(), root, model.DefaultLSPSettings())
	language := found.languages["fixturelang"]
	if language == nil || language.files != 2 || language.sample != filepath.Join(root, "top.fxl") {
		t.Fatalf("discovery = %+v", language)
	}
	if found.seen != 3 || found.limited {
		t.Fatalf("seen=%d limited=%v", found.seen, found.limited)
	}
}

// One server for two languages at one root is one process: per-language
// root markers choose the root, they are not part of the server identity.
func TestOneServerForTwoLanguagesSharesOneProcess(t *testing.T) {
	a := model.LSPServerSpec{LanguageID: "first", Command: "shared-ls", RootMarkers: []string{"a.mod", "a.work"}, WorkspaceRootMarkers: []string{"a.work"}}
	b := model.LSPServerSpec{LanguageID: "second", Command: "shared-ls"}
	if lspServerKey("file:///p", a) != lspServerKey("file:///p", b) {
		t.Fatal("root markers split one server into two processes")
	}
	if lspServerKey("file:///p", a) == lspServerKey("file:///q", a) {
		t.Fatal("different roots share a process")
	}
}
