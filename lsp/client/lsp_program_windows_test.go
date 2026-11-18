package lspclient

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/charmbracelet/x/powernap/pkg/lsp"
	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol"

	"crude/insight/catalog"
)

func npmProgramFixture(t *testing.T, name string, bin any, header string) (string, catalog.PackageManagerRecipe) {
	t.Helper()
	prefix := filepath.Join(t.TempDir(), "tools & packages")
	pkg := filepath.Join(prefix, "node_modules", filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Join(pkg, "bin"), 0700); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(map[string]any{"name": name, "bin": bin})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkg, "package.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkg, "bin", "server"), []byte(header+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return prefix, catalog.PackageManagerRecipe{Kind: "npm", Name: name, ProgramKind: "npm", Program: "fixture-ls", Manifest: "package.json"}
}

func programEnvironment(t *testing.T, dirs ...string) lsp.ProcessEnvironment {
	t.Helper()
	env, err := lsp.NewProcessEnvironment(map[string]string{
		"PATH": strings.Join(dirs, string(os.PathListSeparator)), "PATHEXT": ".CMD;.EXE;.BAT;.COM",
	})
	if err != nil {
		t.Fatal(err)
	}
	return env
}

func TestWindowsPackageProgramUsesMetadataNotCMD(t *testing.T) {
	prefix, program := npmProgramFixture(t, "@unheard-of/fixture", map[string]string{"fixture-ls": "bin/server"}, "#!/usr/bin/env node")
	node := filepath.Join(prefix, "node.exe")
	if err := os.WriteFile(node, []byte("native lookup fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	// This is deliberately not an npm shim. It must never be parsed or run.
	if err := os.WriteFile(filepath.Join(prefix, "fixture-ls.cmd"), []byte("@echo DO NOT RUN THIS"), 0600); err != nil {
		t.Fatal(err)
	}
	args := []string{"--stdio", "literal & | %PATH% \"value\""}
	executable, actual, err := resolveServerProgram(t.Context(), "fixture-ls", args, programEnvironment(t, prefix), program)
	want := append([]string{filepath.Join(prefix, "node_modules", "@unheard-of", "fixture", "bin", "server")}, args...)
	if err != nil || executable != node || !reflect.DeepEqual(actual, want) {
		t.Fatalf("launch = %q, %q, %v; want %q, %q", executable, actual, err, node, want)
	}
	actual[1] = "mutated"
	if args[0] != "--stdio" {
		t.Fatal("resolved arguments alias the caller")
	}
	explicit := map[string]string{"PATH": prefix, "PATHEXT": ".EXE;.CMD"}
	found, err := LocateServerProgram(t.Context(), "fixture-ls", string(protocol.URIFromPath(t.TempDir())), explicit, program)
	if err != nil || found != want[0] {
		t.Fatalf("inventory and launch disagree: %q, %v", found, err)
	}
}

func TestWindowsPackageProgramBinFormsAndLocalLayout(t *testing.T) {
	for _, form := range []string{"string", "node", "local"} {
		t.Run(form, func(t *testing.T) {
			prefix, program := npmProgramFixture(t, "fixture-ls", "bin/server", "#!/usr/bin/env node\r")
			if err := os.WriteFile(filepath.Join(prefix, "node.exe"), []byte("native lookup fixture"), 0600); err != nil {
				t.Fatal(err)
			}
			search := prefix
			if form == "node" {
				program.ProgramKind, program.Program = "node", "node_modules/fixture-ls/bin/server"
			}
			if form == "local" {
				search = filepath.Join(prefix, "node_modules", ".bin")
				if err := os.MkdirAll(search, 0700); err != nil {
					t.Fatal(err)
				}
			}
			_, args, err := resolveServerProgram(t.Context(), "fixture-ls", nil, programEnvironment(t, search, prefix), program)
			want := filepath.Join(prefix, "node_modules", "fixture-ls", "bin", "server")
			if err != nil || len(args) != 1 || args[0] != want {
				t.Fatalf("entry point = %q, %v", args, err)
			}
		})
	}
}

func TestWindowsPackageProgramMissingRuntimeAndBadEntryPoint(t *testing.T) {
	for _, tc := range []struct {
		name, entry, header string
		want                error
	}{
		{"missing-runtime", "bin/server", "#!/usr/bin/env node", ErrServerRuntime},
		{"escape", "../outside", "#!/usr/bin/env node", ErrServerPackage},
		{"missing-script", "bin/absent", "#!/usr/bin/env node", ErrServerPackage},
		{"other-interpreter", "bin/server", "#!/bin/sh", ErrServerScript},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prefix, program := npmProgramFixture(t, "fixture", map[string]string{"fixture-ls": tc.entry}, tc.header)
			if _, _, err := resolveServerProgram(t.Context(), "fixture-ls", nil, programEnvironment(t, prefix), program); !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestWindowsScriptCannotMasqueradeAsNativeExecutable(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "fixture-ls.cmd")
	if err := os.WriteFile(script, []byte("@echo DO NOT RUN THIS"), 0600); err != nil {
		t.Fatal(err)
	}
	env := programEnvironment(t, dir)
	for _, command := range []string{"fixture-ls", script} {
		if executable, _, err := resolveServerProgram(t.Context(), command, nil, env, catalog.PackageManagerRecipe{}); executable != "" || !errors.Is(err, ErrServerScript) {
			t.Fatalf("script = %q, %v", executable, err)
		}
	}
	native := filepath.Join(dir, "fixture-ls.exe")
	if err := os.WriteFile(native, []byte("native lookup fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	executable, args, err := resolveServerProgram(t.Context(), "fixture-ls", []string{"--stdio"}, env, catalog.PackageManagerRecipe{})
	if err != nil || executable != native || !reflect.DeepEqual(args, []string{"--stdio"}) {
		t.Fatalf("native executable lost precedence: %q, %q, %v", executable, args, err)
	}
}

func TestWindowsPackageProgramDoesNotUseAmbientRuntimeOrProjectPATH(t *testing.T) {
	prefix, program := npmProgramFixture(t, "fixture", map[string]string{"fixture-ls": "bin/server"}, "#!/usr/bin/env node")
	t.Setenv("PATH", prefix)
	if err := os.WriteFile(filepath.Join(prefix, "node.exe"), []byte("native lookup fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	empty, err := lsp.NewProcessEnvironment(nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := resolveServerProgram(t.Context(), "fixture-ls", nil, empty, program); err == nil {
		t.Fatal("empty explicit environment used ambient PATH")
	}
	if executable, err := LocateServerProgram(t.Context(), "fixture-ls", string(protocol.URIFromPath(prefix)), nil, program); executable != "" || err == nil {
		t.Fatal("ambient project PATH supplied a server")
	}
}
