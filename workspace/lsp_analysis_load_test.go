package workspace

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"crude/types/insight"
	"crude/types/setting"
	"crude/workspace/document"
)

// TestFallbackCrudeCorpus runs the production discovery, transport and scheduler
// on a disposable copy of the tracked working tree. Every installed stand-in is
// a real stdio process with only document sync and push diagnostics. In
// particular the stand-in for the catalog's project reporter will never report
// a file unless Crude opens it. No tool or document in the real project changes.
func TestFallbackCrudeCorpus(t *testing.T) {
	if os.Getenv("CRUDE_TEST_LSP_CORPUS") != "1" {
		t.Skip("opt-in full-source load test")
	}
	t.Setenv("CRUDE_TEST_LSP_FORCE_FALLBACK", "1")
	repo, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	bin := t.TempDir()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	list, err := exec.Command("git", "-C", repo, "ls-files", "-z").Output()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range bytes.Split(bytes.TrimSuffix(list, []byte{0}), []byte{0}) {
		path := string(name)
		info, err := os.Lstat(filepath.Join(repo, path))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if !info.Mode().IsRegular() {
			continue
		}
		b, err := os.ReadFile(filepath.Join(repo, path))
		if err != nil {
			t.Fatal(err)
		}
		dest := filepath.Join(root, path)
		if err = os.MkdirAll(filepath.Dir(dest), 0700); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(dest, b, 0600); err != nil {
			t.Fatal(err)
		}
	}
	m := NewLSPManager(nil)
	probeSources(t, m, root)
	m.SetWorkspaceRoot(root)
	trustAnalysis(t, m, root)
	t.Cleanup(m.Close)
	s := m.Settings()
	s.Enabled = true
	s.StartOnOpen = true
	s.ProjectCheck = setting.ProjectCheckAlways
	s.MaxServers = 2
	s.Languages = map[string]setting.LSPLanguage{}
	discovery := discoverProjectLanguages(t.Context(), root, s)
	want := 0
	for id, l := range discovery.languages {
		spec, ok := s.ResolveServer(l.sample)
		if !ok {
			continue
		}
		name := filepath.Base(spec.Command)
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		path := filepath.Join(bin, name)
		if err := os.Link(exe, path); err != nil && !os.IsExist(err) {
			t.Fatal(err)
		}
		s.Languages[id] = setting.LSPLanguage{Enabled: true, Command: path, Args: []string{"-test.run=^TestFixtureLanguageServerHelper$", "--"}, Environment: map[string]string{"CRUDE_FIXTURE_LS": "1", "CRUDE_FIXTURE_MODE": "document-push"}}
		for _, path := range l.paths {
			rel, err := filepath.Rel(root, path)
			if err != nil {
				t.Fatal(err)
			}
			if !vendoredPath.MatchString(filepath.ToSlash(rel)) {
				want++
			}
		}
	}
	if want < 3000 {
		t.Fatalf("expected full repository, got %d eligible sources", want)
	}
	m.SetSettings(s)
	start := time.Now()
	m.RefreshAssembly()
	awaitAssembly(t, m, func(p insight.AssemblyPlan) bool { return p.State == insight.AssemblyReady })
	deadline := time.After(15 * time.Minute)
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	var latency []time.Duration
	peak := 0
	edited := false
	for {
		select {
		case <-deadline:
			t.Fatalf("did not settle: %+v", m.ProjectInsights().Analysis)
		case <-tick.C:
		}
		m.mu.Lock()
		views := len(m.checkPaths)
		m.mu.Unlock()
		peak = max(peak, views)
		if views > projectCheckBatch {
			t.Fatalf("%d concurrent background views", views)
		}
		before := time.Now()
		summary := m.ProjectInsights()
		latency = append(latency, time.Since(before))
		if summary.ProjectCheckUnchecked != 0 {
			files := m.UncheckedFiles()
			t.Fatalf("unchecked: %+v", files[:min(3, len(files))])
		}
		if !edited && views > 0 && summary.DiagnosticsObserved > 10 {
			path := filepath.Join(root, "fallback-edit.txt")
			createFile(t, path, "x")
			owner := document.Owner{ClientID: "load-editor", Epoch: 1}
			snap, err := m.sources.Open(owner, path)
			if err != nil {
				t.Fatal(err)
			}
			before = time.Now()
			documentEdit(t, m.sources, owner, snap.ID, "type", snap.Revision, 0, 1, "y")
			t.Logf("canonical edit during fallback: %s", time.Since(before))
			edited = true
		}
		if summary.DiagnosticsObserved == want && len(summary.Analysis) == 0 {
			break
		}
	}
	if !edited || peak < 2 {
		t.Fatalf("no overlapping workload: edit=%v peak=%d", edited, peak)
	}
	slices.Sort(latency)
	t.Logf("%d/%d current; peak background views %d; project status read p95 %s; elapsed %s", want, want, peak, latency[len(latency)*95/100], time.Since(start))
	for _, h := range m.ServerHealthSnapshots() {
		if strings.Contains(h.Coverage, "whole project") {
			t.Fatal("force override did not take effect")
		}
	}
}
