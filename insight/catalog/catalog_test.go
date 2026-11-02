package catalog

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

func builtin(t testing.TB) *Catalog {
	t.Helper()
	c, err := Builtin()
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// legacy holds the launch and file rules that Crude hard-coded before the
// catalog existed. The values are copied here as literals so the test checks
// the data, not a second copy of the same logic.
var legacy = []struct {
	language       string
	command        string
	args           []string
	extensions     map[string]string // extension -> LSP languageId sent before
	roots          []string
	workspaceRoots []string
}{
	{"go", "gopls", nil, map[string]string{".go": "go"}, []string{"go.work", "go.mod"}, []string{"go.work"}},
	{"rust", "rust-analyzer", nil, map[string]string{".rs": "rust"}, []string{"Cargo.toml"}, nil},
	{"python", "pyright-langserver", []string{"--stdio"}, map[string]string{".py": "python"}, []string{"pyproject.toml", "pyrightconfig.json", "setup.py", "setup.cfg"}, nil},
	{"typescript", "typescript-language-server", []string{"--stdio"}, map[string]string{".ts": "typescript", ".mts": "typescript", ".cts": "typescript", ".tsx": "typescriptreact", ".js": "javascript", ".mjs": "javascript", ".cjs": "javascript", ".jsx": "javascriptreact"}, []string{"tsconfig.json", "jsconfig.json", "package.json"}, nil},
	{"html", "vscode-html-language-server", []string{"--stdio"}, map[string]string{".html": "html", ".htm": "html"}, nil, nil},
	{"css", "vscode-css-language-server", []string{"--stdio"}, map[string]string{".css": "css", ".scss": "scss", ".less": "less"}, nil, nil},
	{"json", "vscode-json-language-server", []string{"--stdio"}, map[string]string{".json": "json"}, nil, nil},
	{"cpp", "clangd", nil, map[string]string{".c": "c", ".h": "c", ".cpp": "cpp", ".hpp": "cpp", ".cc": "cpp", ".hh": "cpp", ".cxx": "cpp", ".hxx": "cpp", ".c++": "cpp", ".h++": "cpp"}, []string{"compile_commands.json", ".clangd", "CMakeLists.txt"}, nil},
	{"csharp", "csharp-ls", nil, map[string]string{".cs": "csharp"}, []string{"*.sln", "*.slnx", "*.csproj"}, []string{"*.sln", "*.slnx"}},
	{"zig", "zls", nil, map[string]string{".zig": "zig"}, []string{"build.zig", "zls.json"}, nil},
	{"odin", "ols", nil, map[string]string{".odin": "odin"}, nil, nil},
	{"lua", "lua-language-server", nil, map[string]string{".lua": "lua"}, nil, nil},
}

func TestCatalogReproducesLegacyLanguageRules(t *testing.T) {
	c := builtin(t)
	for _, want := range legacy {
		for ext, wireID := range want.extensions {
			language, ok := c.LanguageForPath("/src/file"+ext, nil)
			if !ok {
				t.Fatalf("%s: %s has no language", want.language, ext)
			}
			if language.LSPLanguageID != wireID {
				t.Errorf("%s: %s sends %q, want %q", want.language, ext, language.LSPLanguageID, wireID)
			}
			if len(language.Servers) == 0 {
				t.Fatalf("%s: %s has no server", want.language, ext)
			}
			server, ok := c.Server(language.Servers[0])
			if !ok {
				t.Fatalf("%s: unknown server %q", want.language, language.Servers[0])
			}
			if server.Command != want.command || !slices.Equal(server.Args, want.args) {
				t.Errorf("%s: %s launches %q %v, want %q %v", want.language, ext, server.Command, server.Args, want.command, want.args)
			}
			if !slices.Equal(language.Roots, want.roots) {
				t.Errorf("%s: %s roots %v, want %v", want.language, ext, language.Roots, want.roots)
			}
			if !slices.Equal(language.WorkspaceRoots, want.workspaceRoots) {
				t.Errorf("%s: %s workspace roots %v, want %v", want.language, ext, language.WorkspaceRoots, want.workspaceRoots)
			}
			if len(server.Settings) != 0 || len(server.InitializationOptions) != 0 || len(server.Environment) != 0 {
				t.Errorf("%s: %s launch adds configuration that Crude did not send before", want.language, ext)
			}
		}
	}
}

func jsonEqual(t *testing.T, label string, got, want any) {
	t.Helper()
	a, _ := json.Marshal(got)
	b, _ := json.Marshal(want)
	if string(a) != string(b) {
		t.Errorf("%s:\n got %s\nwant %s", label, a, b)
	}
}

func TestWithHintsMatchesLegacyResourceHints(t *testing.T) {
	c := builtin(t)
	server := func(id string) Server {
		s, ok := c.Server(id)
		if !ok {
			t.Fatalf("no server %s", id)
		}
		return s
	}
	gopls := server("gopls").WithHints(0, 0)
	jsonEqual(t, "gopls env", gopls.Environment, map[string]string{"GOMAXPROCS": "2", "GOMEMLIMIT": "1024MiB"})
	jsonEqual(t, "gopls settings", gopls.Settings, map[string]any{"gopls": map[string]any{"staticcheck": false, "diagnosticsDelay": "1s"}})

	explicitEnv := server("gopls")
	explicitEnv.Environment = map[string]string{"GOMAXPROCS": "7"}
	jsonEqual(t, "gopls explicit env", explicitEnv.WithHints(3, 512).Environment, map[string]string{"GOMAXPROCS": "7", "GOMEMLIMIT": "512MiB"})

	clangd := server("clangd")
	jsonEqual(t, "clangd args", clangd.WithHints(2, 0).Args, []string{"-j=2"})
	clangd.Args = []string{"--log=error"}
	jsonEqual(t, "clangd other args", clangd.WithHints(4, 0).Args, []string{"--log=error", "-j=4"})
	for _, explicit := range []string{"-j", "-j=8", "-j8"} {
		clangd.Args = []string{explicit}
		jsonEqual(t, "clangd explicit "+explicit, clangd.WithHints(4, 0).Args, []string{explicit})
	}

	ra := server("rust-analyzer").WithHints(2, 0)
	defaults := map[string]any{"numThreads": 2, "cachePriming": map[string]any{"numThreads": 2}}
	jsonEqual(t, "rust-analyzer env", ra.Environment, map[string]string{"CARGO_BUILD_JOBS": "2"})
	jsonEqual(t, "rust-analyzer init", ra.InitializationOptions, defaults)
	jsonEqual(t, "rust-analyzer settings", ra.Settings, map[string]any{"rust-analyzer": defaults})
	explicitInit := server("rust-analyzer")
	explicitInit.InitializationOptions = map[string]any{"numThreads": 4}
	got := explicitInit.WithHints(2, 0)
	want := map[string]any{"numThreads": 4, "cachePriming": map[string]any{"numThreads": 2}}
	jsonEqual(t, "rust-analyzer explicit init", got.InitializationOptions, want)
	jsonEqual(t, "rust-analyzer explicit init settings", got.Settings, map[string]any{"rust-analyzer": want})
	explicitSettings := server("rust-analyzer")
	explicitSettings.Settings = map[string]any{"rust-analyzer": map[string]any{"numThreads": 6}}
	got = explicitSettings.WithHints(2, 0)
	want = map[string]any{"numThreads": 6, "cachePriming": map[string]any{"numThreads": 2}}
	jsonEqual(t, "rust-analyzer explicit settings init", got.InitializationOptions, want)
	jsonEqual(t, "rust-analyzer explicit settings", got.Settings, map[string]any{"rust-analyzer": want})

	jsonEqual(t, "pyright settings", server("pyright").WithHints(0, 0).Settings,
		map[string]any{"python": map[string]any{"analysis": map[string]any{"diagnosticMode": "openFilesOnly"}}})
	jsonEqual(t, "typescript init", server("typescript-language-server").WithHints(0, 2048).InitializationOptions,
		map[string]any{"maxTsServerMemory": 2048, "tsserver": map[string]any{"useSyntaxServer": "never"}})

	// The source entry is not changed by applying hints.
	if s := server("gopls"); len(s.Environment) != 0 || len(s.Settings) != 0 {
		t.Fatalf("WithHints changed the catalog entry: %+v", s)
	}
}

func TestLanguageForPath(t *testing.T) {
	c := builtin(t)
	cases := []struct {
		path string
		head string
		want string // catalog language ID, "" for none
	}{
		{"main.go", "", "go"},
		{"/a/b/MAIN.GO", "", "go"},
		{"lib.h", "", "c"},
		{"lib.hpp", "", "cpp"},
		{"Main.C", "", "cpp"},
		{"proj/CMakeLists.txt", "", "cmake"},
		{"notes.txt", "", ""},
		{"Dockerfile", "", "dockerfile"},
		{"script", "#!/usr/bin/env python3\nprint(1)\n", "python"},
		{"run", "#!/bin/bash\necho hi\n", "bash"},
		{"notes.unknownext", "", ""},
		{"no-extension", "", ""},
		{"", "", ""},
	}
	for _, tc := range cases {
		var head []byte
		if tc.head != "" {
			head = []byte(tc.head)
		}
		got, ok := c.LanguageForPath(tc.path, head)
		if tc.want == "" {
			if ok {
				t.Errorf("%q: got %s, want none", tc.path, got.ID)
			}
			continue
		}
		if !ok || got.ID != tc.want {
			t.Errorf("%q: got %q (%v), want %q", tc.path, got.ID, ok, tc.want)
		}
	}
}

func TestPackLanguageUnknownToLinguist(t *testing.T) {
	base := builtin(t)
	pack := `{"format":1,
	  "sources":[{"id":"fixture-pack","license":"MIT"}],
	  "languages":[{"id":"crudefixture","name":"Fixture","extensions":[".cfx"],"lsp_language_id":"crudefixture","roots":["fixture.project"],"servers":["fixture-server"],"sources":["fixture-pack"]}],
	  "servers":[{"id":"fixture-server","command":"fixture-lsp","args":["--stdio"],"diagnostic_scope":"project","sources":["fixture-pack"]}]}`
	c, err := Load(base, []byte(pack))
	if err != nil {
		t.Fatal(err)
	}
	language, ok := c.LanguageForPath("src/a.cfx", nil)
	if !ok || language.ID != "crudefixture" || language.Provenance[0].Source != "fixture-pack" {
		t.Fatalf("pack language not detected: %+v %v", language, ok)
	}
	if _, ok := base.LanguageForPath("src/a.cfx", nil); ok {
		t.Fatal("Load changed the base catalog")
	}
	if !slices.Contains(c.Names(), "fixture-lsp") || !slices.Contains(c.Names(), ".cfx") {
		t.Fatal("Names misses pack entries")
	}
}

func TestLoadRejectsInvalidPacks(t *testing.T) {
	base := builtin(t)
	cases := map[string]string{
		"format":          `{"format":2}`,
		"unknown field":   `{"format":1,"extra":true}`,
		"trailing":        `{"format":1} {}`,
		"bad extension":   `{"format":1,"languages":[{"id":"x","extensions":["X"],"lsp_language_id":"x"}]}`,
		"upper extension": `{"format":1,"languages":[{"id":"x","extensions":[".X"],"lsp_language_id":"x"}]}`,
		"root escape":     `{"format":1,"languages":[{"id":"x","extensions":[".x"],"lsp_language_id":"x","roots":["../up"]}]}`,
		"root absolute":   `{"format":1,"languages":[{"id":"x","extensions":[".x"],"lsp_language_id":"x","roots":["/etc/passwd"]}]}`,
		"unknown server":  `{"format":1,"languages":[{"id":"x","extensions":[".x"],"lsp_language_id":"x","servers":["missing-server"]}]}`,
		"no wire id":      `{"format":1,"languages":[{"id":"x","extensions":[".x"]}]}`,
		"duplicate":       `{"format":1,"servers":[{"id":"s","command":"a"},{"id":"s","command":"b"}]}`,
		"no command":      `{"format":1,"servers":[{"id":"s","command":" "}]}`,
		"bad scope":       `{"format":1,"servers":[{"id":"s","command":"a","diagnostic_scope":"everything"}]}`,
		"bad env":         `{"format":1,"servers":[{"id":"s","command":"a","environment":{"A=B":"c"}}]}`,
		"unknown package": `{"format":1,"servers":[{"id":"s","command":"a","package":"missing"}]}`,
		"bad purl":        `{"format":1,"packages":[{"name":"p","source":{"id":"https://x"}}]}`,
		"unknown source":  `{"format":1,"servers":[{"id":"s","command":"a","sources":["nowhere"]}]}`,
		"bad section":     `{"format":1,"servers":[{"id":"s","command":"a","hints":{"initialization_from_section":"x"}}]}`,
	}
	for name, pack := range cases {
		if _, err := Load(base, []byte(pack)); err == nil {
			t.Errorf("%s: pack was accepted", name)
		}
	}
	if _, err := Load(base, []byte(strings.Repeat(" ", MaxDocumentBytes+1))); err == nil {
		t.Error("oversized pack was accepted")
	}
}

func TestReturnedValuesAreDetached(t *testing.T) {
	c := builtin(t)
	server, _ := c.Server("gopls")
	server.Hints.Settings["gopls"].(map[string]any)["staticcheck"] = true
	again, _ := c.Server("gopls")
	if again.Hints.Settings["gopls"].(map[string]any)["staticcheck"] != false {
		t.Fatal("a caller changed catalog data through a returned map")
	}
	language, _ := c.Language("go")
	language.Extensions[0] = ".changed"
	if again, _ := c.Language("go"); again.Extensions[0] != ".go" {
		t.Fatal("a caller changed catalog data through a returned slice")
	}
}

func TestAssetForExpandsTemplatesAndPrefersTargets(t *testing.T) {
	pkg := Package{Source: Source{ID: "pkg:github/owner/tool@v1.2.3", Assets: []Asset{
		{Targets: []string{"linux_x64"}, File: "tool-{{version}}-linux.tar.gz", Bin: "tool-{{ version | strip_prefix \"v\" }}/tool"},
		{Targets: []string{"linux_x64_gnu"}, File: "tool-gnu.zip", Bin: "tool"},
		{Targets: []string{"darwin_arm64"}, File: "tool-{{unsupported}}.zip"},
	}}}
	if got := pkg.Source.Type(); got != "github" {
		t.Fatalf("type %q", got)
	}
	if got := pkg.Source.Name(); got != "owner/tool" {
		t.Fatalf("name %q", got)
	}
	asset, ok := pkg.AssetFor([]string{"linux_x64_gnu", "linux_x64"})
	if !ok || asset.File != "tool-gnu.zip" {
		t.Fatalf("preferred target: %+v %v", asset, ok)
	}
	asset, ok = pkg.AssetFor([]string{"linux_x64"})
	if !ok || asset.File != "tool-v1.2.3-linux.tar.gz" || asset.Bin != "tool-1.2.3/tool" {
		t.Fatalf("templates: %+v %v", asset, ok)
	}
	if _, ok := pkg.AssetFor([]string{"darwin_arm64"}); ok {
		t.Fatal("unsupported template was expanded")
	}
	if _, ok := pkg.AssetFor([]string{"win_x64"}); ok {
		t.Fatal("missing target matched")
	}
	if len(targetsFor("linux", "amd64")) == 0 || targetsFor("plan9", "amd64") != nil {
		t.Fatal("target names")
	}
}

func TestBuiltinPackagesHaveCurrentTargetAssets(t *testing.T) {
	c := builtin(t)
	server, _ := c.Server("clangd")
	pkg, ok := c.Package(server.Package)
	if !ok {
		t.Fatal("clangd has no package")
	}
	if pkg.Source.Type() != "github" || len(pkg.Licenses) == 0 || len(pkg.Provenance) == 0 {
		t.Fatalf("package metadata: %+v", pkg)
	}
	if _, ok := pkg.AssetFor([]string{"linux_x64_gnu"}); !ok {
		t.Fatal("clangd has no Linux asset")
	}
}

func BenchmarkBuiltinParse(b *testing.B) {
	doc, err := Decode(builtinData)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.SetBytes(int64(len(builtinData)))
	for b.Loop() {
		d, _ := Decode(builtinData)
		if _, err := build(nil, d); err != nil {
			b.Fatal(err)
		}
	}
	_ = doc
}

func BenchmarkLanguageForPath(b *testing.B) {
	c := builtin(b)
	paths := []string{"main.go", "lib/thing.ts", "README.md", "Makefile", "src/x.unknown", "a/b/c/d.h"}
	b.ReportAllocs()
	i := 0
	for b.Loop() {
		c.LanguageForPath(paths[i%len(paths)], nil)
		i++
	}
}

func FuzzLoad(f *testing.F) {
	f.Add([]byte(`{"format":1}`))
	f.Add([]byte(`{"format":1,"languages":[{"id":"x","extensions":[".x"],"lsp_language_id":"x","servers":["s"]}],"servers":[{"id":"s","command":"c"}]}`))
	f.Add([]byte(`{"format":1,"servers":[{"id":"s","command":"a","hints":{"settings":{"x":{}},"initialization_from_section":"x"}}]}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		c, err := Load(nil, data)
		if err != nil {
			return
		}
		// A loaded catalog is internally consistent and safe to query.
		for _, language := range c.Languages() {
			for _, id := range language.Servers {
				if _, ok := c.Server(id); !ok {
					t.Fatalf("language %s names missing server %s", language.ID, id)
				}
			}
			for _, ext := range language.Extensions {
				c.LanguageForPath("f"+ext, []byte("#!/usr/bin/env x"))
			}
		}
		for _, name := range c.Names() {
			if server, ok := c.Server(name); ok {
				server.WithHints(1, 1)
			}
		}
	})
}

func TestShebangInterpreter(t *testing.T) {
	cases := map[string]string{
		"#!/bin/sh\n":                        "sh",
		"#!/usr/bin/env python3\n":           "python3",
		"#!/usr/bin/env -S VAR=1 node --x\n": "node",
		"#! /usr/local/bin/ruby -w\r\n":      "ruby",
		"#!/usr/bin/env\n":                   "",
		"no shebang":                         "",
		"#!":                                 "",
	}
	for head, want := range cases {
		if got := shebangInterpreter([]byte(head)); got != want {
			t.Errorf("%q: got %q, want %q", head, got, want)
		}
	}
}
