package workspace

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"crude/insight/catalog"
	"crude/internal/fileuri"
	"crude/lsp/client"
	model "crude/types"
)

func TestWindowsLaunchErrorUsesRuntimeMetadata(t *testing.T) {
	message := lspStartError("fixture-ls", &lspclient.RuntimeUnavailableError{Runtime: "FixtureVM"})
	if !strings.Contains(message, "needs FixtureVM") || !strings.Contains(message, "Install it") ||
		strings.Contains(message, "fixture-ls is not installed") {
		t.Fatalf("missing runtime lost its identity or became server-install advice: %q", message)
	}
	message = lspStartError("fixture-ls", lspclient.ErrServerScript)
	if !strings.Contains(message, "installed as a script") || !strings.Contains(message, "first argument") ||
		strings.Contains(message, "Install") {
		t.Fatalf("unsupported script has misleading advice: %q", message)
	}
}

func TestWindowsAssemblyResolvesUnknownPackageFromMetadata(t *testing.T) {
	var doc catalog.Document
	if err := json.Unmarshal([]byte(fixturePack), &doc); err != nil {
		t.Fatal(err)
	}
	doc.Servers[0].Package = "fixture-package"
	doc.Packages = []catalog.PackageRecord{{
		Name: "fixture-package", Source: catalog.PackageSourceRecord{ID: "pkg:npm/%40unknown/fixture@1.0.0"},
		Bin: map[string]string{"fixture-ls": "npm:fixture-ls"},
	}}
	pack, prefix, root := t.TempDir(), t.TempDir(), t.TempDir()
	createFile(t, filepath.Join(pack, "fixture.json"), string(mustJSON(t, doc)))
	if problems := catalog.UsePacks(pack); len(problems) != 0 {
		t.Fatal(problems)
	}
	t.Cleanup(func() { catalog.UsePacks(filepath.Join(pack, "none")) })
	writeTree(t, filepath.Join(prefix, "node_modules", "@unknown", "fixture", "package.json"),
		`{"name":"@unknown/fixture","bin":{"fixture-ls":"bin/server"}}`)
	script := filepath.Join(prefix, "node_modules", "@unknown", "fixture", "bin", "server")
	writeTree(t, script, "#!/usr/bin/env node\n")
	node := filepath.Join(prefix, "node.exe")
	createFile(t, node, "native lookup fixture")
	createFile(t, filepath.Join(prefix, "fixture-ls.cmd"), "@echo MUST NOT RUN")
	createFile(t, filepath.Join(root, "main.fxl"), "fixture")
	t.Setenv("PATH", prefix)
	t.Setenv("PATHEXT", ".CMD;.EXE")
	settings := model.DefaultLSPSettings()
	discovery := discoverProjectLanguages(t.Context(), root, settings)
	resolve := func() model.AssemblyPlan {
		return resolveAssembly(root, settings, discovery, func(spec model.LSPServerSpec, download *catalog.Download) (string, string, error) {
			return locateTool(t.Context(), spec, root, nil, download)
		})
	}
	plan := resolve()
	language, _ := assemblyLanguage(plan, "fixturelang")
	if language.Status != model.ToolReady || language.Executable != script {
		t.Fatalf("unknown package did not resolve to its own tool identity: %+v", language)
	}
	if err := os.Remove(node); err != nil {
		t.Fatal(err)
	}
	plan = resolve()
	language, _ = assemblyLanguage(plan, "fixturelang")
	if language.Status != model.ToolUnavailable || !strings.Contains(language.Reason, "Node.js") ||
		strings.Contains(plan.Message, "Install the missing servers") {
		t.Fatalf("missing runtime was reported as a missing server: %+v", plan)
	}
	issues := lspCommandIssues(plan)
	if len(issues) != 1 || strings.Contains(issues[0].Summary, "not installed") || issues[0].Detail != language.Reason {
		t.Fatalf("launch issue lost its real cause: %+v", issues)
	}
}

func TestWindowsAssemblyDoesNotOfferToReinstallAnUnsupportedScript(t *testing.T) {
	useFixturePack(t)
	root, prefix := t.TempDir(), t.TempDir()
	createFile(t, filepath.Join(root, "main.fxl"), "fixture")
	createFile(t, filepath.Join(prefix, "fixture-ls.cmd"), "@echo MUST NOT RUN")
	t.Setenv("PATH", prefix)
	t.Setenv("PATHEXT", ".CMD;.EXE")
	settings := model.DefaultLSPSettings()
	plan := resolveAssembly(root, settings, discoverProjectLanguages(t.Context(), root, settings),
		func(spec model.LSPServerSpec, download *catalog.Download) (string, string, error) {
			return locateTool(t.Context(), spec, root, nil, download)
		})
	language, _ := assemblyLanguage(plan, "fixturelang")
	if language.Status != model.ToolUnavailable || language.Executable != "" ||
		!strings.Contains(language.Reason, "interpreter") || strings.Contains(language.Reason, "Install") {
		t.Fatalf("unsupported script was reported as ready or absent: %+v", language)
	}
}

// Opt-in acceptance against the user's installed tools, never an installer.
func TestWindowsInstalledNPMServers(t *testing.T) {
	if os.Getenv("CRUDE_TEST_INSTALLED_NPM_LSP") != "1" {
		t.Skip("set CRUDE_TEST_INSTALLED_NPM_LSP=1 to check installed npm servers")
	}
	for _, extension := range []string{".css", ".html", ".json"} {
		t.Run(extension, func(t *testing.T) {
			root := t.TempDir()
			spec, ok := model.DefaultLSPSettings().ResolveServer(filepath.Join(root, "sample"+extension))
			if !ok {
				t.Fatal("catalog did not resolve a server")
			}
			path, source, err := locateTool(t.Context(), spec, root, nil, nil)
			if err != nil || path == "" || source != model.ToolSourcePath {
				t.Fatalf("installed tool not found: %q, %q: %v", path, source, err)
			}
			client := NewLSPRuntime().NewClient(spec, fileuri.FromPath(root), nil)
			t.Cleanup(func() {
				if err := client.Stop(); err != nil {
					t.Error(err)
				}
			})
			if err := client.Start(t.Context()); err != nil {
				t.Fatalf("inventory found %s, but actual startup failed: %v", path, err)
			}
		})
	}
}

func TestWindowsInstalledNPMJSONProbe(t *testing.T) {
	if os.Getenv("CRUDE_TEST_INSTALLED_NPM_LSP") != "1" {
		t.Skip("set CRUDE_TEST_INSTALLED_NPM_LSP=1 to check installed npm servers")
	}
	root := t.TempDir()
	path := filepath.Join(root, "sample.json")
	createFile(t, path, `{"answer": }`)
	m := NewLSPManager(nil)
	t.Cleanup(m.Close)
	probeSources(t, m, root)
	m.SetWorkspaceRoot(root)
	trustAnalysis(t, m, root)
	m.SetSettings(editorDisabledSettings())
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	result, settled, _, err := m.ProbeDiagnostics(ctx, path)
	if err != nil || !settled || len(result.Diagnostics) == 0 {
		t.Fatalf("broken JSON: %+v settled=%v err=%v", result, settled, err)
	}
	createFile(t, path, `{"answer": 42}`)
	result, settled, _, err = m.ProbeDiagnostics(ctx, path)
	if err != nil || !settled || len(result.Diagnostics) != 0 {
		t.Fatalf("fixed JSON: %+v settled=%v err=%v", result, settled, err)
	}
}
