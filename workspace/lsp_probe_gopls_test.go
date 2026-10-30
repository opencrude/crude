package workspace

import (
	"context"
	"crude/types"
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
	t.Cleanup(m.Close)
	probeSources(t, m, root)
	m.SetWorkspaceRoot(root)
	settings := editorDisabledSettings()
	m.SetSettings(settings)

	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	diags, settled, view, err := m.ProbeDiagnostics(ctx, main)
	if err != nil || !settled {
		t.Fatalf("diagnostics err=%v settled=%v", err, settled)
	}
	if len(diags.Diagnostics) != 1 || !strings.Contains(diags.Diagnostics[0].Message, "missing") || diags.Diagnostics[0].Line != 2 {
		t.Fatalf("diagnostics = %+v", diags)
	}
	if diags.ID == "" || diags.Boundary.Source != view.Source || view.SourceText != "package probe\n\nvar x int = Answer() + missing\n" ||
		diags.State != types.InsightProvisional || len(diags.Boundary.Context.UnknownInputs) == 0 {
		t.Fatalf("real server quiescence lost source or implied complete context: answer=%+v view=%+v", diags, view)
	}

	write("main.go", "package probe\n\nvar x int = Answer() + 1\n")
	if diags, settled, _, err = m.ProbeDiagnostics(ctx, main); err != nil || !settled || len(diags.Diagnostics) != 0 {
		t.Fatalf("after the fix: %+v settled=%v err=%v", diags, settled, err)
	}

	locs, _, err := m.ProbeDefinition(ctx, main, types.LSPSourcePosition{Line: 3, Symbol: "Answer"})
	if err != nil || len(locs) != 1 || filepath.Clean(locs[0].Path) != lib || locs[0].Line != 2 {
		t.Fatalf("definition = %+v err=%v", locs, err)
	}
	refs, _, err := m.ProbeReferences(ctx, lib, types.LSPSourcePosition{Line: 3, Symbol: "Answer"})
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
