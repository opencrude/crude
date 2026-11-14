package types

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestWindowsServerPathsRetainCatalogDefaults(t *testing.T) {
	for _, tc := range []struct{ language, extension string }{
		{"go", ".go"}, {"rust", ".rs"}, {"python", ".py"}, {"typescript", ".ts"},
	} {
		for _, suffix := range []string{".exe", ".EXE", ".com", ".cmd", ".bat"} {
			t.Run(tc.language+suffix, func(t *testing.T) {
				settings := DefaultLSPSettings()
				want, ok := settings.ResolveServer("file" + tc.extension)
				if !ok {
					t.Fatal("catalog fixture has no server")
				}
				path := filepath.Join(t.TempDir(), strings.ToUpper(want.Command)+suffix)
				settings.Languages[tc.language] = LSPLanguageSettings{Enabled: true, Command: path}
				got, ok := settings.ResolveServer("file" + tc.extension)
				want.Command = path
				if !ok || !reflect.DeepEqual(got, want) {
					t.Fatalf("explicit native path lost catalog defaults: got=%+v want=%+v", got, want)
				}
			})
		}
	}
}

func TestWindowsCustomServerDoesNotAcquireAnotherProgramsHints(t *testing.T) {
	for _, command := range []string{"gopls-debug.exe", "gopls.exe.backup", "gopls.exe:stream"} {
		settings := DefaultLSPSettings()
		settings.Languages["go"] = LSPLanguageSettings{Enabled: true, Command: command}
		got, ok := settings.ResolveServer("file.go")
		if !ok || got.Command != command || len(got.InitializationOptions) != 0 || len(got.Settings) != 0 {
			t.Fatalf("unrelated command acquired catalog startup settings: %+v", got)
		}
	}
}

func TestWindowsServerPathStillHonorsExplicitStartupChoices(t *testing.T) {
	settings := DefaultLSPSettings()
	settings.Languages["go"] = LSPLanguageSettings{
		Enabled: true, Command: filepath.Join(t.TempDir(), "GOPLS.EXE"),
		InitializationOptions: map[string]any{"semanticTokens": false},
		Settings:              map[string]any{"gopls": map[string]any{"semanticTokens": false}},
	}
	got, ok := settings.ResolveServer("file.go")
	if !ok || got.ServerID != "gopls" || got.InitializationOptions["semanticTokens"] != false ||
		got.Settings["gopls"].(map[string]any)["semanticTokens"] != false {
		t.Fatalf("native basename matching overwrote an explicit startup choice: %+v", got)
	}
}
