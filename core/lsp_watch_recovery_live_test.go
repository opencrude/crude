package core

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"crude/core/coreprotocol"
	"crude/types/insight"
	"crude/types/setting"
	"crude/workspace"
	"crude/workspace/document"
)

// Exercise native observation, a real server, canonical unsaved edits and the
// same diagnostic facts consumed by connected shells and agent probes.
func TestLiveLSPDiagnosticsRecoverAfterWatchGap(t *testing.T) {
	if os.Getenv("CRUDE_LSP_LIVE") != "1" {
		t.Skip("set CRUDE_LSP_LIVE=1 to run the installed-server journey")
	}
	server, err := exec.LookPath("gopls")
	if err != nil {
		t.Fatal("the live acceptance journey requires installed gopls", err)
	}
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	const good = "package example\n"
	for name, text := range map[string]string{
		"go.mod":  "module example.test/watchrecovery\n\ngo 1.22\n",
		"main.go": good,
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	setCoreTestHome(t, t.TempDir())
	r, err := New(func(root string) (*workspace.Workspace, error) {
		w := workspace.NewWorkspace(root, "", "tester", setting.Settings{}, coreTestLogger{})
		settings := setting.DefaultLSP()
		language := settings.Languages["go"]
		language.Command = server
		settings.Languages["go"] = language
		w.SetAppDefaults(setting.App{LSP: settings, LocalHistory: setting.LocalHistory{Enabled: "off"}})
		return w, nil
	}, filepath.Join(t.TempDir(), "receipts.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := r.Close(); err != nil {
			t.Error(err)
		}
	})
	wire := newResourceWire(t, r, "watch-recovery-editor")
	var attached coreprotocol.AttachResult
	wire.call(t, "", coreprotocol.Attach, &coreprotocol.AttachRequest{Root: root}, &attached)
	a, err := r.workspace(t.Context(), attached.WorkspaceID, root)
	if err != nil {
		t.Fatal(err)
	}
	var granted coreprotocol.WorkspacePreferencesResult
	wire.call(t, attached.WorkspaceID, coreprotocol.WorkspaceOverridesSet, coreprotocol.WorkspacePreferencesRequest{
		Edits: []setting.Edit{{Path: []string{"analysis", "trust"}, After: []byte(`"trusted"`)}},
	}, &granted)
	manager := a.ws.LSPMgr
	watchAwait(t, "assembled project producer", func() bool {
		return manager.Assembly().State == insight.AssemblyReady && len(manager.ProjectProducers()) > 0
	})

	// Replace only this fixture's observer to control a native directory race.
	// The actual manager, server, wire commands and facts remain unchanged.
	a.fileWatch.Close()
	// A build output that arrives with more files than the watch bound is a
	// gap: the producers reindex after observation is restored.
	unpacked := filepath.Join(t.TempDir(), "build-output")
	if err := os.MkdirAll(unpacked, 0700); err != nil {
		t.Fatal(err)
	}
	for i := 0; i <= coreWatchSubtreeLimit; i++ {
		if err := os.WriteFile(filepath.Join(unpacked, fmt.Sprintf("out-%04d.txt", i)), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	gapped := make(chan struct{}, 1)
	recovered := make(chan struct{}, 1)
	var armed, recovering atomic.Bool
	watch, err := startCoreFileWatch(root, nil, manager.NotifyWatchedFilesChanged, func(reliable bool) {
		manager.SetProjectWatchReliable(reliable)
		if !reliable && armed.CompareAndSwap(true, false) {
			recovering.Store(true)
			gapped <- struct{}{}
		} else if reliable && recovering.CompareAndSwap(true, false) {
			recovered <- struct{}{}
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.inspect(t.Context(), func() { a.fileWatch = watch }); err != nil {
		watch.Close()
		t.Fatal(err)
	}
	<-watch.ready
	var source document.Snapshot
	wire.call(t, attached.WorkspaceID, coreprotocol.DocumentOpen, coreprotocol.DocumentOpenRequest{Path: path}, &source)
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	initial, settled, _, err := manager.ProbeDiagnostics(ctx, path)
	if err != nil || !settled || initial.ID == "" || len(initial.Diagnostics) != 0 {
		t.Fatalf("initial source was not checked: %+v settled=%v err=%v", initial, settled, err)
	}
	armed.Store(true)
	if err := os.Rename(unpacked, filepath.Join(root, "build-output")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-gapped:
	case <-ctx.Done():
		t.Fatal("a subtree larger than the watch bound made no gap")
	}
	select {
	case <-recovered:
	case <-ctx.Done():
		t.Fatal("native watcher did not recover")
	}
	var receipt document.Receipt
	const bad = good + "sxsxsx\n"
	wire.call(t, attached.WorkspaceID, coreprotocol.DocumentEdit, coreprotocol.DocumentEditRequest{
		ID: source.ID, Revision: source.Revision,
		Edits: []document.Edit{{Start: len(good), End: len(good), Text: "sxsxsx\n"}},
	}, &receipt)
	answer, settled, view, err := manager.ProbeDiagnostics(ctx, path)
	if err != nil || !settled || answer.ID == "" || !answer.HasExactSource() ||
		view.SourceText != bad || answer.Boundary.Source.Revision != receipt.Revision ||
		len(answer.Diagnostics) == 0 || answer.Diagnostics[0].Line != 1 ||
		!strings.Contains(answer.Diagnostics[0].Message, "sxsxsx") {
		t.Fatalf("typed invalid declaration did not reach diagnostics: %+v settled=%v err=%v", answer, settled, err)
	}
	var snapshot coreprotocol.LSPSnapshot
	wire.call(t, attached.WorkspaceID, coreprotocol.LSPSnapshotMethod, coreprotocol.LSPRequest{}, &snapshot)
	if snapshot.Diagnostics[path].ID != answer.ID || snapshot.Insights.Errors == 0 {
		t.Fatal("the connected shell did not receive the agent's diagnostic result")
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != good {
		t.Fatal("checking the unsaved error changed disk", err)
	}
}
