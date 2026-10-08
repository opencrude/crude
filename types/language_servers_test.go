package types

import (
	"reflect"
	"testing"
)

func TestLanguageCatalogWireAndRoots(t *testing.T) {
	s := DefaultLSPSettings()
	for _, tc := range []struct{ ext, command, wire, marker string }{
		{".go", "gopls", "go", "go.work"}, {".rs", "rust-analyzer", "rust", "Cargo.toml"},
		{".py", "pyright-langserver", "python", "pyproject.toml"}, {".zig", "zls", "zig", "build.zig"},
		{".cs", "csharp-ls", "csharp", "*.slnx"}, {".c", "clangd", "c", "compile_commands.json"},
		{".mjs", "typescript-language-server", "javascript", "package.json"}, {".cjs", "typescript-language-server", "javascript", "jsconfig.json"},
		{".mts", "typescript-language-server", "typescript", "tsconfig.json"}, {".cts", "typescript-language-server", "typescript", "tsconfig.json"},
		{".jsx", "typescript-language-server", "javascriptreact", "package.json"}, {".tsx", "typescript-language-server", "typescriptreact", "package.json"},
		{".html", "vscode-html-language-server", "html", ""}, {".scss", "vscode-css-language-server", "scss", ""},
		{".less", "vscode-css-language-server", "less", ""}, {".json", "vscode-json-language-server", "json", ""},
		{".lua", "lua-language-server", "lua", ""}, {".odin", "ols", "odin", ""},
	} {
		spec, ok := s.ResolveServer("file" + tc.ext)
		if !ok || spec.Command != tc.command || spec.LanguageID != tc.wire {
			t.Fatalf("%s: %+v %v", tc.ext, spec, ok)
		}
		if tc.marker != "" {
			found := false
			for _, m := range spec.RootMarkers {
				found = found || m == tc.marker
			}
			if !found {
				t.Fatalf("%s missing %s", tc.ext, tc.marker)
			}
		}
	}
}

func TestExternalLanguagesAndAutoDetect(t *testing.T) {
	s := DefaultLSPSettings()
	s.AutoDetect = false
	if _, ok := s.ResolveServer("a.go"); ok {
		t.Fatal("auto-detect off started built-in")
	}
	s.Languages["ruby"] = LSPLanguageSettings{Enabled: true, Command: "ruby-lsp", Args: []string{"a spaced argument"}, Extensions: []string{".rb"}, LanguageID: "ruby", RootMarkers: []string{"Gemfile"}}
	spec, ok := s.ResolveServer("a.rb")
	if !ok || spec.LanguageID != "ruby" || spec.Args[0] != "a spaced argument" {
		t.Fatalf("custom: %+v %v", spec, ok)
	}
	lang := s.Languages["ruby"]
	lang.Enabled = false
	s.Languages["ruby"] = lang
	if _, ok := s.ResolveServer("a.rb"); ok {
		t.Fatal("disabled custom started")
	}
	s.Servers = map[string]string{".go": "external-go"}
	if _, ok := s.ResolveServer("a.go"); !ok {
		t.Fatal("explicit external disabled by auto-detect")
	}
	s.Enabled = false
	if _, ok := s.ResolveServer("a.go"); ok {
		t.Fatal("master switch ignored")
	}
}

func TestLSPHintsAndDetachedSettings(t *testing.T) {
	s := DefaultLSPSettings()
	before := CloneLSPSettings(s)
	goSpec, _ := s.ResolveServer("a.go")
	if goSpec.Environment["GOMAXPROCS"] != "2" || goSpec.Environment["GOMEMLIMIT"] != "1024MiB" {
		t.Fatal(goSpec)
	}
	if goSpec.Settings["gopls"].(map[string]any)["staticcheck"] != false {
		t.Fatal("default unexpectedly starts heavyweight dependency analysis")
	}
	goCfg := s.Languages["go"]
	goCfg.Settings = map[string]any{"gopls": map[string]any{"staticcheck": true}}
	s.Languages["go"] = goCfg
	explicit, _ := s.ResolveServer("a.go")
	if explicit.Settings["gopls"].(map[string]any)["staticcheck"] != true {
		t.Fatal("explicit deep analysis disabled")
	}
	s.Languages["go"] = before.Languages["go"]
	ts, _ := s.ResolveServer("a.ts")
	if ts.InitializationOptions["maxTsServerMemory"] != 1024 || ts.InitializationOptions["tsserver"].(map[string]any)["useSyntaxServer"] != "never" {
		t.Fatal(ts)
	}
	rust := s.Languages["rust"]
	rust.Settings = map[string]any{"rust-analyzer": map[string]any{"numThreads": 7, "cargo": map[string]any{"jobs": 8}, "nested": []any{map[string]any{"keep": true}}}}
	rust.InitializationOptions = map[string]any{"cachePriming": map[string]any{"numThreads": 9}}
	s.Languages["rust"] = rust
	r, _ := s.ResolveServer("a.rs")
	if r.InitializationOptions["numThreads"] != 7 || r.InitializationOptions["cachePriming"].(map[string]any)["numThreads"] != 9 {
		t.Fatal(r)
	}
	r.Settings["rust-analyzer"].(map[string]any)["nested"].([]any)[0].(map[string]any)["keep"] = false
	if !s.Languages["rust"].Settings["rust-analyzer"].(map[string]any)["nested"].([]any)[0].(map[string]any)["keep"].(bool) {
		t.Fatal("resolve aliased nested settings")
	}
	clone := NormalizeLSP(s)
	clone.Languages["rust"].InitializationOptions["cachePriming"].(map[string]any)["numThreads"] = 20
	if s.Languages["rust"].InitializationOptions["cachePriming"].(map[string]any)["numThreads"] != 9 {
		t.Fatal("normalize aliased input")
	}
	s.Languages["rust"] = before.Languages["rust"]
	if !reflect.DeepEqual(s, before) {
		t.Fatal("resolve mutated source")
	}
	for _, args := range [][]string{{"-j", "6"}, {"-j=6"}, {"-j6"}} {
		cfg := s.Languages["cpp"]
		cfg.Args = args
		s.Languages["cpp"] = cfg
		c, _ := s.ResolveServer("a.cpp")
		if !reflect.DeepEqual(c.Args, args) {
			t.Fatalf("jobs overridden: %v", c.Args)
		}
	}
}

func TestLSPValidationRejectsBoundsCollisionsAndNonJSON(t *testing.T) {
	for _, change := range []func(*LSPSettings){
		func(s *LSPSettings) { s.MaxServers = -1 }, func(s *LSPSettings) { s.ServerThreads = 65 }, func(s *LSPSettings) { s.ServerMemoryMB = 65537 },
		func(s *LSPSettings) { s.MaxDocumentBytes = 64*1024*1024 + 1 }, func(s *LSPSettings) { s.IdleTimeoutSeconds = 86401 },
		func(s *LSPSettings) {
			s.Languages["ruby"] = LSPLanguageSettings{Command: "ruby-lsp", LanguageID: "ruby", Extensions: []string{".go"}}
		},
		func(s *LSPSettings) {
			cfg := s.Languages["go"]
			cfg.Settings = map[string]any{"bad": func() {}}
			s.Languages["go"] = cfg
		},
	} {
		s := DefaultLSPSettings()
		change(&s)
		if s.Validate() == nil {
			t.Fatal("invalid config accepted")
		}
		if _, ok := s.ResolveServer("a.go"); ok {
			t.Fatal("invalid config launched")
		}
	}
}
