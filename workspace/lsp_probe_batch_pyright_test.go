package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"crude/types"
)

func TestProbeDiagnosticsBatchAgainstRealPyright(t *testing.T) {
	if testing.Short() {
		t.Skip("starts a real language server")
	}
	server, err := exec.LookPath("pyright-langserver")
	if err != nil {
		t.Skip("pyright-langserver is not installed")
	}
	checker, err := exec.LookPath("pyright")
	if err != nil {
		t.Skip("pyright is not installed")
	}
	root := t.TempDir()
	var paths []string
	for i := range 11 {
		path := filepath.Join(root, fmt.Sprintf("file%d.py", i))
		if err := os.WriteFile(path, []byte("def value() -> int:\n    return 1\n"), 0600); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, path)
	}
	if err := os.WriteFile(filepath.Join(root, "pyrightconfig.json"), []byte(`{"include":["."],"typeCheckingMode":"strict"}`), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, checker, "--outputjson", root)
	command.Dir = root
	output, err := command.Output()
	if err != nil {
		t.Fatalf("independent pyright check failed: %v", err)
	}
	var report struct {
		Summary struct{ ErrorCount, FilesAnalyzed int }
	}
	if err := json.Unmarshal(output, &report); err != nil || report.Summary.ErrorCount != 0 || report.Summary.FilesAnalyzed != 11 {
		t.Fatalf("independent checker did not confirm eleven clean files: %+v %v", report, err)
	}
	m := NewLSPManager(nil)
	t.Cleanup(m.Close)
	probeSources(t, m, root)
	m.SetWorkspaceRoot(root)
	trustAnalysis(t, m, root)
	settings := editorDisabledSettings()
	settings.Languages = map[string]types.LSPLanguageSettings{"python": {
		Enabled: true, Command: server, Args: []string{"--stdio"}, RootMarkers: []string{"pyrightconfig.json"},
	}}
	m.SetSettings(settings)
	for i, result := range m.ProbeDiagnosticsBatch(ctx, paths) {
		if result.Error != "" || !result.Settled || result.Diagnostics.ID == "" ||
			len(result.Diagnostics.Diagnostics) != 0 || result.View.Source.Path != paths[i] {
			t.Fatalf("clean file %s failed in the batch: %+v", paths[i], result)
		}
	}
	if err := os.WriteFile(paths[0], []byte("def value() -> int:\n    return \"wrong\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for i, result := range m.ProbeDiagnosticsBatch(ctx, paths) {
		if result.Error != "" || !result.Settled || result.Diagnostics.ID == "" {
			t.Fatalf("second batch did not finish: %+v", result)
		}
		want := 0
		if i == 0 {
			want = 1
		}
		if len(result.Diagnostics.Diagnostics) != want {
			t.Fatalf("%s: got %+v, want %d problems", paths[i], result.Diagnostics.Diagnostics, want)
		}
	}
}
