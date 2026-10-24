package viewmodel

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"crude/coreprotocol"
	"crude/types/control"

	"github.com/charmbracelet/x/ansi"
)

func TestConnectedShellControlTreeRevealFilterAndPaint(t *testing.T) {
	runtime := remoteJourneyRuntime(t, "")
	root := t.TempDir()
	path := filepath.Join(root, "nested", "deeper", "needle.txt")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("content"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "unrelated.txt"), []byte("other"), 0600); err != nil {
		t.Fatal(err)
	}
	one, two := newRemoteJourneyShell(t, runtime, root), newRemoteJourneyShell(t, runtime, root)
	shells := []remoteJourneyShell{one, two}
	result := requestShellForTest(t, runtime, shells, one, "tree.reveal", map[string]any{"paths": []string{path}}, nil)
	if result.State != control.Applied {
		t.Fatal(result)
	}
	if node := one.app.Tree.Model.SelectedNode(); node == nil || node.Path != path {
		t.Fatal("requested node not selected", node)
	}
	if !strings.Contains(ansi.Strip(one.app.View().RenderContent()), "needle.txt") {
		t.Fatal("revealed leaf not painted")
	}
	if node := two.app.Tree.Model.SelectedNode(); node != nil && node.Path == path {
		t.Fatal("peer tree selection stolen")
	}
	result = requestShellForTest(t, runtime, shells, one, "tree.filter", map[string]any{"query": "needle"}, nil)
	if result.State != control.Applied {
		t.Fatal(result)
	}
	paint := ansi.Strip(one.app.View().RenderContent())
	if !strings.Contains(paint, "needle.txt") || strings.Contains(paint, "unrelated.txt") {
		t.Fatal("filter projection not painted", paint)
	}
	result = requestShellForTest(t, runtime, shells, one, "tree.filter", map[string]any{"query": ""}, nil)
	if result.State != control.Applied || !strings.Contains(ansi.Strip(one.app.View().RenderContent()), "unrelated.txt") {
		t.Fatal("clear filter failed", result)
	}
	selected := one.app.Tree.Model.SelectedNode().Path
	for _, bad := range []string{root, filepath.Join(root, "missing.txt"), filepath.Join(root, "..", "outside.txt")} {
		result = requestShellForTest(t, runtime, shells, one, "tree.reveal", map[string]any{"paths": []string{bad}}, nil)
		if result.State != control.Failed {
			t.Fatal("invalid reveal falsely applied", bad, result)
		}
		if node := one.app.Tree.Model.SelectedNode(); node == nil || node.Path != selected {
			t.Fatal("invalid reveal moved selection")
		}
	}
}

func TestConnectedShellControlRejectCatalogSchemaForgery(t *testing.T) {
	runtime := remoteJourneyRuntime(t, "")
	one := newRemoteJourneyShell(t, runtime, t.TempDir())
	result := requestShellForTest(t, runtime, []remoteJourneyShell{one}, one, "tree.set_visible", map[string]any{"visible": false}, func(e *coreprotocol.ControlEnvelope) { e.Descriptor.Input.Document = []byte(`{"type":"object"}`) })
	if result.State != control.Failed || result.Fault == nil || result.Fault.Code != control.Stale || !one.app.Tree.Visible {
		t.Fatal("catalog forgery accepted", result)
	}
}
