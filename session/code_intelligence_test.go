package session

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"crude/agent"
	"crude/safety"
	"crude/sessiondb"
	"crude/tools"
	ttypes "crude/types"
)

type fakeCodeIntelligence struct {
	mu          sync.Mutex
	editor      bool
	warm        bool
	diagnostics map[string][]ttypes.LSPDiagnostic
	locations   []ttypes.LSPLocation
	positions   [][2]int
}

func (f *fakeCodeIntelligence) EditorEnabled() bool         { return f.editor }
func (f *fakeCodeIntelligence) ServesWarm(path string) bool { return f.warm }
func (f *fakeCodeIntelligence) ProbeDiagnostics(_ context.Context, path string) ([]ttypes.LSPDiagnostic, bool, ttypes.LSPProbeView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.diagnostics[filepath.Base(path)], true, ttypes.LSPProbeView{Server: "gopls"}, nil
}
func (f *fakeCodeIntelligence) ProbeDefinition(_ context.Context, _ string, line, col int) ([]ttypes.LSPLocation, ttypes.LSPProbeView, error) {
	f.mu.Lock()
	f.positions = append(f.positions, [2]int{line, col})
	f.mu.Unlock()
	return f.locations, ttypes.LSPProbeView{}, nil
}
func (f *fakeCodeIntelligence) ProbeReferences(ctx context.Context, path string, line, col int) ([]ttypes.LSPLocation, ttypes.LSPProbeView, error) {
	return f.ProbeDefinition(ctx, path, line, col)
}
func (f *fakeCodeIntelligence) ProbeHover(context.Context, string, int, int) (string, ttypes.LSPProbeView, error) {
	return "func y() int", ttypes.LSPProbeView{}, nil
}
func (f *fakeCodeIntelligence) ProbeSymbols(context.Context, string) ([]ttypes.LSPSymbol, ttypes.LSPProbeView, error) {
	return nil, ttypes.LSPProbeView{}, nil
}

func codeIntelFixture(t *testing.T, editor bool) (*Session, *agent.Agent, *fakeCodeIntelligence, string) {
	t.Helper()
	root := t.TempDir()
	s := &Session{cfg: testConfig()}
	service := &fakeCodeIntelligence{editor: editor, diagnostics: map[string][]ttypes.LSPDiagnostic{}}
	s.SetCodeIntelligence(service)
	live := &agent.Agent{AgentID: "root", WorkDir: root, Policy: safety.DefaultPolicy(), FilteredTools: []tools.ToolDef{{Function: tools.ToolFuncDef{Name: "read_file"}}},
		ToolFn: func(context.Context, string, map[string]any) (tools.ToolResult, error) {
			return tools.ToolResult{Content: "Replaced 1 occurrence"}, nil
		}}
	s.bindCodeIntelligence(live)
	return s, live, service, root
}

func toolNames(defs []tools.ToolDef) []string {
	var names []string
	for _, def := range defs {
		names = append(names, def.Function.Name)
	}
	return names
}

// The session override decides; nil follows the workspace switch.
func TestLSPToolFollowsSessionOverrideOverWorkspace(t *testing.T) {
	s, live, _, _ := codeIntelFixture(t, false)
	if defs, _ := live.PrepareStep(); slices.Contains(toolNames(defs), "lsp") {
		t.Fatal("lsp offered while the workspace switch is off and the session inherits")
	}
	on := true
	s.codeIntelOverride = &on
	if defs, _ := live.PrepareStep(); !slices.Contains(toolNames(defs), "lsp") {
		t.Fatal("session override did not offer the lsp tool")
	}
	off := false
	s.codeIntelOverride = &off
	if _, err := live.ToolFn(t.Context(), "lsp", map[string]any{"operation": "hover"}); err == nil || !strings.Contains(err.Error(), "code intelligence is off") {
		t.Fatalf("disabled lsp call = %v", err)
	}
}

// Agents name the identifier; the tool converts it to the protocol's UTF-16
// column and answers with 1-based positions and the quoted source line.
func TestLSPDefinitionUsesSymbolAndQuotesSource(t *testing.T) {
	_, live, service, root := codeIntelFixture(t, true)
	path := filepath.Join(root, "a.go")
	if err := os.WriteFile(path, []byte("package a\n// 😀 x := y()\nfunc y() int { return 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	service.locations = []ttypes.LSPLocation{{Path: path, Line: 2, Col: 5}}
	result, err := live.ToolFn(t.Context(), "lsp", map[string]any{"operation": "definition", "path": "a.go", "line": float64(2), "symbol": "y"})
	if err != nil {
		t.Fatal(err)
	}
	// "// 😀 x := " is 10 runes but 11 UTF-16 units before y.
	if len(service.positions) != 1 || service.positions[0] != [2]int{1, 11} {
		t.Fatalf("protocol position = %v, want line 1 col 11", service.positions)
	}
	if !strings.Contains(result.Content, "a.go:3:6  func y() int { return 1 }") {
		t.Fatalf("definition result = %q", result.Content)
	}
	if _, err := live.ToolFn(t.Context(), "lsp", map[string]any{"operation": "hover", "path": "/etc/hosts", "line": float64(1), "symbol": "x"}); err == nil || !strings.Contains(err.Error(), "outside the workspace") {
		t.Fatalf("outside path = %v", err)
	}
	if _, err := live.ToolFn(t.Context(), "lsp", map[string]any{"operation": "definition", "path": "a.go", "line": float64(2), "symbol": "zz"}); err == nil || !strings.Contains(err.Error(), `symbol "zz" is not on line 2`) {
		t.Fatalf("missing symbol = %v", err)
	}
}

func TestLSPDiagnosticsReportSeverityAndCleanFiles(t *testing.T) {
	_, live, service, root := codeIntelFixture(t, true)
	for name, text := range map[string]string{"a.go": "package a\nvar x = y\n", "b.go": "package a\n"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	service.diagnostics["a.go"] = []ttypes.LSPDiagnostic{
		{Line: 1, Col: 8, Severity: 1, Message: "undefined: y", Source: "compiler", Code: "UndeclaredName"},
		{Line: 0, Col: 0, Severity: 4, Message: "hint only"},
	}
	result, err := live.ToolFn(t.Context(), "lsp", map[string]any{"operation": "diagnostics", "paths": []any{"a.go", "b.go"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"a.go: 1 error (gopls)", "2:9 error undefined: y [compiler UndeclaredName]", "var x = y", "b.go: no problems (gopls)"} {
		if !strings.Contains(result.Content, want) {
			t.Fatalf("diagnostics lack %q:\n%s", want, result.Content)
		}
	}
	if strings.Contains(result.Content, "hint only") {
		t.Fatal("default severity floor reported hints")
	}
}

// After an edit, errors from a warm server reach the next request as a
// notice; the edit result itself (a patch card body) is untouched, and a
// later clean edit withdraws the stale report.
func TestPostEditDiagnosticsArriveAsNotice(t *testing.T) {
	_, live, service, root := codeIntelFixture(t, true)
	service.warm = true
	path := filepath.Join(root, "a.go")
	if err := os.WriteFile(path, []byte("package a\nvar x = y\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	service.diagnostics["a.go"] = []ttypes.LSPDiagnostic{{Line: 1, Col: 8, Severity: 1, Message: "undefined: y"}}
	result, err := live.ToolFn(t.Context(), "edit_file", map[string]any{"path": "a.go"})
	if err != nil || result.Content != "Replaced 1 occurrence" {
		t.Fatalf("edit result changed: %q %v", result.Content, err)
	}
	_, notices := live.PrepareStep()
	if len(notices) != 1 || !strings.Contains(notices[0].ContentString(), "undefined: y") {
		t.Fatalf("notices = %+v", notices)
	}
	if _, again := live.PrepareStep(); len(again) != 0 {
		t.Fatal("a delivered notice repeated")
	}
	_, _ = live.ToolFn(t.Context(), "edit_file", map[string]any{"path": "a.go"})
	service.mu.Lock()
	service.diagnostics["a.go"] = nil
	service.mu.Unlock()
	_, _ = live.ToolFn(t.Context(), "edit_file", map[string]any{"path": "a.go"})
	if _, notices := live.PrepareStep(); len(notices) != 0 {
		t.Fatalf("fixed file still reported: %+v", notices)
	}
	service.warm = false
	service.diagnostics["a.go"] = []ttypes.LSPDiagnostic{{Severity: 1, Message: "cold"}}
	_, _ = live.ToolFn(t.Context(), "edit_file", map[string]any{"path": "a.go"})
	if _, notices := live.PrepareStep(); len(notices) != 0 {
		t.Fatal("an edit cold-started a server check")
	}
}

// Toggling delegation rewrites the flags row; it must keep the
// code-intelligence override.
func TestToggleMultiAgentKeepsCodeIntelligenceOverride(t *testing.T) {
	root := t.TempDir()
	sess, err := NewWithSessionDir("", testConfig(), root, root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(sess.Close)
	settings := sess.SettingsSnapshot()
	on := true
	settings.SessionCodeIntelligence = &on
	if err := sess.HandleSettingsSave(settings); err != nil {
		t.Fatal(err)
	}
	if _, err := sess.ToggleMultiAgent(); err != nil {
		t.Fatal(err)
	}
	flags, err := sessiondb.LoadSessionFlags(sess.DB(), sess.SessionID())
	if err != nil || flags.CodeIntelligence == nil || !*flags.CodeIntelligence {
		t.Fatalf("flags after toggle = %+v err=%v", flags, err)
	}
	if got := sess.SettingsSnapshot().SessionCodeIntelligence; got == nil || !*got {
		t.Fatalf("snapshot override = %v", got)
	}
}

// The sub-agent limit applies to a live session at the next launch, and
// clearing the workspace value restores the default instead of keeping it.
func TestSubAgentLimitAppliesLiveAndAutoRestoresDefault(t *testing.T) {
	root := t.TempDir()
	sess, err := NewWithSessionDir("", testConfig(), root, root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(sess.Close)
	runner := NewSubAgentRunner(sess, sess.effectiveMaxSubAgents())
	sess.mu.Lock()
	sess.subAgentSup = runner
	sess.mu.Unlock()
	settings := sess.SettingsSnapshot()
	one := 1
	settings.SessionMaxSubAgents = &one
	if err := sess.HandleSettingsSave(settings); err != nil {
		t.Fatal(err)
	}
	mgr := sess.subAgentSup.Manager()
	if err := mgr.Register(&agent.SubAgentHandle{Spec: agent.SubAgentSpec{ID: "a"}}); err != nil {
		t.Fatal(err)
	}
	if err := mgr.Register(&agent.SubAgentHandle{Spec: agent.SubAgentSpec{ID: "b"}}); err == nil {
		t.Fatal("the session limit of 1 did not reach the live runner")
	}
	flags, _ := sessiondb.LoadSessionFlags(sess.DB(), sess.SessionID())
	if flags.MaxSubAgents == nil || *flags.MaxSubAgents != 1 {
		t.Fatalf("persisted limit = %v", flags.MaxSubAgents)
	}
	mgr.MarkInterrupted("a")
	cfg := testConfig()
	roles := map[string]ttypes.CategorySettings{"default": {Model: "m"}}
	ApplyWorkspaceAgentDefaults(cfg, ttypes.AgentsSettings{MaxConcurrent: 9, Enabled: true, Categories: roles})
	ApplyWorkspaceAgentDefaults(cfg, ttypes.AgentsSettings{MaxConcurrent: 0, Enabled: true, Categories: roles})
	if cfg.Agents.MaxConcurrent != DefaultAgentsConfig().MaxConcurrent {
		t.Fatalf("Auto kept %d", cfg.Agents.MaxConcurrent)
	}
}
