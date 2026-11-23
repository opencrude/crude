package workspace

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"crude/types/setting"
)

// The VS Code JSON server stops pushing once a client announces pull
// diagnostics, so every answer here comes through textDocument/diagnostic.
// Closed files checked through disk views get an exact answer, clean or not,
// and a fix on disk is seen on the next check.
func TestPullOnlyServerAgainstRealJSONServer(t *testing.T) {
	if testing.Short() {
		t.Skip("starts a real language server")
	}
	server, err := exec.LookPath("vscode-json-language-server")
	if err != nil {
		t.Skip("vscode-json-language-server is not installed")
	}
	root := t.TempDir()
	broken, clean := filepath.Join(root, "broken.json"), filepath.Join(root, "clean.json")
	for path, text := range map[string]string{broken: "{ \"a\": 1,, }\n", clean: "{ \"a\": 1 }\n"} {
		if err := os.WriteFile(path, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	m := NewLSPManager(nil)
	t.Cleanup(m.Close)
	probeSources(t, m, root)
	m.SetWorkspaceRoot(root)
	trustAnalysis(t, m, root)
	settings := editorDisabledSettings()
	settings.Languages = map[string]setting.LSPLanguage{"json": {Enabled: true, Command: server, Args: []string{"--stdio"}}}
	m.SetSettings(settings)
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	check := func(want map[string]bool) {
		t.Helper()
		paths := []string{broken, clean}
		for i, result := range m.ProbeDiagnosticsBatch(ctx, paths) {
			if result.Error != "" || !result.Settled || result.Diagnostics.ID == "" {
				t.Fatalf("%s got no exact answer: %+v", filepath.Base(paths[i]), result)
			}
			if problems := len(result.Diagnostics.Diagnostics) > 0; problems != want[paths[i]] {
				t.Fatalf("%s: problems=%v, want %v: %+v", filepath.Base(paths[i]), problems, want[paths[i]], result.Diagnostics.Diagnostics)
			}
		}
	}
	check(map[string]bool{broken: true, clean: false})
	if err := os.WriteFile(broken, []byte("{ \"a\": 1 }\n"), 0600); err != nil {
		t.Fatal(err)
	}
	check(map[string]bool{broken: false, clean: false})
}
