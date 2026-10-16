package workspace

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Real gopls: a type error is reported once analysis settles, the fix clears
// it, navigation resolves across files, and closing the manager leaves no
// server behind. Skipped where gopls is not installed.
func TestProbeAgainstRealGopls(t *testing.T) {
	if testing.Short() {
		t.Skip("starts a real language server")
	}
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls is not installed")
	}
	root := t.TempDir()
	write := func(name, text string) string {
		path := filepath.Join(root, name)
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	write("go.mod", "module probe\n\ngo 1.22\n")
	lib := write("lib.go", "package probe\n\nfunc Answer() int { return 42 }\n")
	main := write("main.go", "package probe\n\nvar x int = Answer() + missing\n")
	m := NewLSPManager(nil)
	m.SetWorkspaceRoot(root)
	settings := editorDisabledSettings()
	m.SetSettings(settings)

	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	diags, settled, _, err := m.ProbeDiagnostics(ctx, main)
	if err != nil || !settled {
		t.Fatalf("diagnostics err=%v settled=%v", err, settled)
	}
	if len(diags) != 1 || !strings.Contains(diags[0].Message, "missing") || diags[0].Line != 2 {
		t.Fatalf("diagnostics = %+v", diags)
	}

	write("main.go", "package probe\n\nvar x int = Answer() + 1\n")
	if diags, settled, _, err = m.ProbeDiagnostics(ctx, main); err != nil || !settled || len(diags) != 0 {
		t.Fatalf("after the fix: %+v settled=%v err=%v", diags, settled, err)
	}

	locs, _, err := m.ProbeDefinition(ctx, main, 2, strings.Index("var x int = Answer() + 1", "Answer"))
	if err != nil || len(locs) != 1 || filepath.Clean(locs[0].Path) != lib || locs[0].Line != 2 {
		t.Fatalf("definition = %+v err=%v", locs, err)
	}
	refs, _, err := m.ProbeReferences(ctx, lib, 2, strings.Index("func Answer() int { return 42 }", "Answer"))
	if err != nil || len(refs) != 2 {
		t.Fatalf("references = %+v err=%v", refs, err)
	}

	if runtime.GOOS != "linux" {
		m.Close()
		return
	}
	if !goplsRunningIn(root) {
		t.Fatal("no gopls process found for the probe root; the leak check below would prove nothing")
	}
	m.Close()
	deadline := time.Now().Add(5 * time.Second)
	for goplsRunningIn(root) {
		if time.Now().After(deadline) {
			t.Fatal("gopls survived Close")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// goplsRunningIn reports a live gopls whose working directory is root.
func goplsRunningIn(root string) bool {
	entries, _ := os.ReadDir("/proc")
	for _, entry := range entries {
		dir := filepath.Join("/proc", entry.Name())
		cmdline, err := os.ReadFile(filepath.Join(dir, "cmdline"))
		if err != nil || !strings.Contains(filepath.Base(strings.SplitN(string(cmdline), "\x00", 2)[0]), "gopls") {
			continue
		}
		if cwd, err := os.Readlink(filepath.Join(dir, "cwd")); err == nil && cwd == root {
			return true
		}
	}
	return false
}
