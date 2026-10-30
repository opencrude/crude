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
)

func TestAnalyzerEnvironmentUsesPositiveInheritanceRatherThanSecretNameGuessing(t *testing.T) {
	root := t.TempDir()
	tools := filepath.Join(t.TempDir(), "tools")
	ambient := []string{
		"PATH=" + tools, "HOME=" + filepath.Dir(tools), "GOPATH=" + tools,
		"JAVA_HOME=" + tools, "LANG=C.UTF-8",
		"OPENAI_API_KEY=fixture-provider-secret",
		"WHATEVER_A_FUTURE_PROVIDER_CALLS_IT=fixture-unknown-secret",
		"CRUDE_HOME=fixture-private-core", "SSH_AUTH_SOCK=fixture-agent",
		"DBUS_SESSION_BUS_ADDRESS=fixture-session-authority",
		"HTTP_PROXY=http://fixture-user:fixture-password@example.invalid",
		"NODE_OPTIONS=--require=fixture-loader",
		"LD_PRELOAD=fixture-preload", "GIT_CONFIG_COUNT=1",
		"RUSTC_WRAPPER=fixture-executable", "GOFLAGS=fixture-flags",
	}
	explicit := map[string]string{"GOMAXPROCS": "2", "LANG": "C", "CONFIGURED_ONLY": "value"}
	got, err := analyzerEnvironment(ambient, explicit, string(protocol.URIFromPath(root)))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"PATH": tools, "HOME": filepath.Dir(tools), "GOPATH": tools,
		"JAVA_HOME": tools, "LANG": "C", "GOMAXPROCS": "2", "CONFIGURED_ONLY": "value",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ambient execution authority or unknown data crossed positive inheritance: keys=%d", len(got))
	}
	explicit["LANG"] = "mutated"
	ambient[0] = "PATH=mutated"
	if got["LANG"] != "C" || got["PATH"] != tools {
		t.Fatal("caller mutation changed prepared environment")
	}
}

func TestAnalyzerEnvironmentDoesNotSearchWorkspaceOrRelativeAmbientPaths(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	tools := filepath.Join(t.TempDir(), "tools")
	sibling := root + "-other"
	path := strings.Join([]string{"", ".", "relative", root, filepath.Join(root, "node_modules", ".bin"), tools, tools, sibling}, string(filepath.ListSeparator))
	got, err := analyzerEnvironment([]string{"PATH=" + path}, nil, string(protocol.URIFromPath(root)))
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{tools, sibling}, string(filepath.ListSeparator))
	if got["PATH"] != want {
		t.Fatal("ambient PATH retained workspace/relative entries, dropped a sibling, or changed search order")
	}
	// Explicit launch choices remain explicit; validation and execution Trust
	// belong to their real owners rather than an implicit inheritance heuristic.
	got, err = analyzerEnvironment([]string{"PATH=" + path}, map[string]string{"PATH": tools}, string(protocol.URIFromPath(root)))
	if err != nil || got["PATH"] != tools {
		t.Fatal("explicit PATH was not a complete detached override")
	}
	got, err = analyzerEnvironment([]string{"PATH=."}, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, present := got["PATH"]; present {
		t.Fatal("fully rejected ambient search path became an empty current-directory entry")
	}
}

func TestAnalyzerEnvironmentRefusesAmbiguousInputWithoutEchoingValues(t *testing.T) {
	const fixture = "sensitive-fixture-that-must-not-appear"
	for _, input := range []struct {
		ambient []string
		root    string
	}{
		{[]string{"PATH=" + fixture, "PATH=" + fixture}, ""},
		{nil, "untitled:" + fixture},
	} {
		got, err := analyzerEnvironment(input.ambient, nil, input.root)
		if !errors.Is(err, errAnalyzerEnvironment) || got != nil || strings.Contains(err.Error(), fixture) {
			t.Fatal("ambiguous environment exposed a partial table or raw sensitive data")
		}
	}
}

func TestAnalyzerEnvironmentBoundsPathBeforeSplittingAndExplicitTables(t *testing.T) {
	for _, ambient := range [][]string{
		make([]string, maxAnalyzerAmbientEntries+1),
		{"PATH=" + strings.Repeat("x", maxAnalyzerPathBytes+1)},
		{"PATH=" + strings.Repeat(string(filepath.ListSeparator), maxAnalyzerPathEntries)},
	} {
		got, err := analyzerEnvironment(ambient, nil, "")
		if !errors.Is(err, errAnalyzerEnvironment) || got != nil {
			t.Fatal("oversized input exposed a partial environment")
		}
	}
	overrides := make(map[string]string)
	for i := 0; i <= maxAnalyzerEnvironmentEntries; i++ {
		overrides[strings.Repeat("x", i+1)] = "test"
	}
	if got, err := analyzerEnvironment(nil, overrides, ""); !errors.Is(err, errAnalyzerEnvironment) || got != nil {
		t.Fatal("unbounded explicit table accepted")
	}
}

func TestAnalyzerEnvironmentActualFramedLaunchUsesConfiguredPathAndWithholdsUnknownAmbientData(t *testing.T) {
	t.Setenv("CRUDE_ANALYZER_UNKNOWN_FIXTURE", "unknown-provider-fixture-not-a-real-secret")
	t.Setenv("PATH", t.TempDir())
	program, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	explicit := map[string]string{
		"PATH": filepath.Dir(program), "CRUDE_PROTOCOL_HELPER": "environment",
		"CRUDE_ANALYZER_EXPLICIT_FIXTURE": "detached-explicit",
	}
	args := []string{"-test.run=^TestProtocolServerHelper$"}
	integration := NewLSPIntegration(filepath.Base(program), args, string(protocol.URIFromPath(root)), WithServerEnv(explicit))
	explicit["PATH"] = t.TempDir()
	explicit["CRUDE_PROTOCOL_HELPER"] = "mutated"
	explicit["CRUDE_ANALYZER_EXPLICIT_FIXTURE"] = "mutated"
	args[0] = "-test.run=^MustNotRun$"
	t.Cleanup(func() {
		if err := integration.Stop(); err != nil {
			t.Error(err)
		}
	})
	if err := integration.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "fixture.go")
	openTestFile(t, integration, t.Context(), file, "go", "package main\n")
	result, err := integration.RequestHover(t.Context(), file, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		UnknownPresent bool   `json:"unknownPresent"`
		Explicit       string `json:"explicit"`
	}
	if err := json.Unmarshal([]byte(result), &got); err != nil {
		t.Fatal(err)
	}
	if got.UnknownPresent || got.Explicit != "detached-explicit" {
		t.Fatal("framed analyzer saw ambient provider data or caller mutation")
	}
	if err := integration.Stop(); err != nil {
		t.Fatal(err)
	}
}

func TestInvalidAnalyzerEnvironmentNeverAttemptsTrustAdmissionOrExecutable(t *testing.T) {
	for _, values := range []map[string]string{
		{"PATH": "."}, {"UNSAFE=KEY": "fixture"}, {"KEY": "invalid\x00value"},
	} {
		admissions := 0
		integration := NewLSPIntegration("must-not-be-resolved", nil, "",
			WithServerEnv(values), WithStartAdmission(func() error { admissions++; return nil }))
		if err := integration.Start(t.Context()); !errors.Is(err, lsp.ErrProcessEnvironment) {
			t.Fatalf("invalid environment reached launch: %v", err)
		}
		if admissions != 0 || integration.Done() != nil {
			t.Fatal("invalid environment gained a producer lifetime or consumed admission")
		}
		if err := integration.Stop(); err != nil {
			t.Fatal(err)
		}
	}
}
