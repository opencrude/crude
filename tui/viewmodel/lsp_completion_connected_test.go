package viewmodel

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"crude/tui/ui"
	"crude/types"
	"crude/types/control"
	"crude/workspace"
)

const completionConnectedFirst = `{"label":"same","filterText":"pr","kind":3,"insertText":"first","documentation":"FIRST_INITIAL","data":{"n":9007199254740993}}`
const completionConnectedSecond = `{"label":"same","filterText":"pr","kind":3,"insertText":"second","documentation":"SECOND_INITIAL","data":{"n":9007199254740995}}`

type completionConnectedRuntime struct {
	base      renameConnectedRuntime
	requests  int      // guarded by base.mu
	originals []string // guarded by base.mu
}

type completionConnectedClient struct {
	*renameConnectedClient
	owner *completionConnectedRuntime
}

func (r *completionConnectedRuntime) NewClient(spec types.LSPServerSpec, root string, admit func() error) workspace.LSPClient {
	return &completionConnectedClient{r.base.NewClient(spec, root, admit).(*renameConnectedClient), r}
}

func (c *completionConnectedClient) RequestCompletions(_ context.Context, path string, line, col int) (types.LSPCompletionResponse, error) {
	r := c.owner
	r.base.mu.Lock()
	defer r.base.mu.Unlock()
	d := c.docs[path]
	if d.text != "pr" || line != 0 || col != 2 {
		return types.LSPCompletionResponse{}, fmt.Errorf("completion did not query the literal acknowledged prefix: %q %d:%d", d.text, line, col)
	}
	r.requests++
	return types.LSPCompletionResponse{WireVersion: d.version, ResolveSupported: true,
		JSON: []byte("[" + completionConnectedFirst + "," + completionConnectedSecond + "]")}, nil
}

func (c *completionConnectedClient) ResolveCompletion(_ context.Context, path string, wire int, original []byte) (types.LSPCompletionResponse, error) {
	r := c.owner
	r.base.mu.Lock()
	defer r.base.mu.Unlock()
	if c.docs[path].version != wire {
		return types.LSPCompletionResponse{}, fmt.Errorf("selected-row resolve changed its synchronized wire source")
	}
	raw := string(original)
	switch raw {
	case completionConnectedFirst:
		raw = strings.Replace(raw, "FIRST_INITIAL", "FIRST_RESOLVED", 1)
	case completionConnectedSecond:
		raw = strings.Replace(raw, "SECOND_INITIAL", "SECOND_RESOLVED", 1)
	default:
		return types.LSPCompletionResponse{}, fmt.Errorf("resolve reconstructed the exact original row: %s", raw)
	}
	r.originals = append(r.originals, string(original))
	return types.LSPCompletionResponse{WireVersion: wire, ResolveSupported: true, JSON: []byte(raw)}, nil
}

func TestConnectedCompletionSemanticReadAndAcceptanceRetainOriginalOwners(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	if err := os.WriteFile(path, []byte("pr"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module journey\n\ngo 1.22\n"), 0600); err != nil {
		t.Fatal(err)
	}
	command, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	engine := &completionConnectedRuntime{}
	var owned *workspace.Workspace
	runtime := remoteJourneyRuntime(t, "http://unused.invalid", func(w *workspace.Workspace) {
		owned = w
		w.LSPMgr.SetRuntime(engine)
		settings := w.AppDefaults()
		settings.LSP = types.DefaultLSPSettings()
		settings.LSP.AutoDetect, settings.LSP.AutoComplete = false, false
		settings.LSP.Languages = map[string]types.LSPLanguageSettings{"go": {
			Enabled: true, Command: command, RootMarkers: []string{"go.mod"},
		}}
		w.SetAppDefaults(settings)
		grantSemanticFixture(t, w)
	})
	one, two := newRemoteJourneyShell(t, runtime, root), newRemoteJourneyShell(t, runtime, root)
	shells := []remoteJourneyShell{one, two}
	a, b := liveTestOpen(t, one.app, path), liveTestOpen(t, two.app, path)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	_, view, err := owned.LSPMgr.ProbeHover(ctx, path, types.LSPSourcePosition{Line: 1, Column: 1})
	cancel()
	if err != nil || view.SourceText != "pr" {
		t.Fatal("provider did not synchronize initial canonical bytes", view, err)
	}
	remoteJourneyUntil(t, shells, func() bool {
		_, current := a.CurrentInsightSource()
		return current && one.app.ws.LSPMgr().ServerReady()
	})
	one.app.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	a.SetCursorPosition(0, 2)
	b.SetCursorPosition(0, 0)
	one.app.setFocusEditor()
	one.app.Input.SetInput("private completion one")
	two.app.Input.SetInput("private completion two")
	renameConnectedKey(t, one, tea.KeyPressMsg{Code: ' ', Mod: tea.ModCtrl})
	remoteJourneyUntil(t, shells, func() bool {
		s := one.app.completion
		return s != nil && s.screen != nil && s.pending == nil && s.observed["item-0"]
	})
	s := one.app.completion
	completionRequireText(t, one.app, []string{"FIRST_RESOLVED"}, []string{"SECOND_RESOLVED"})
	target := one.app.ws.(interface{ ControlIdentity() control.Target }).ControlIdentity()
	page, err := one.app.inspectShellControls(target, control.PageRequest{Limit: 128})
	if err != nil {
		t.Fatal(err)
	}
	component := ""
	rows := 0
	for _, node := range page.Nodes {
		if strings.HasSuffix(node.ID, "::completion/list") {
			component = node.ID
		}
		if strings.Contains(node.ID, "::completion/list/item/item-") {
			rows++
		}
	}
	if component == "" || rows != 2 {
		t.Fatal("framed shell inventory lost exact same-label native rows", component, rows)
	}
	result := journeySettingsControl(t, runtime, shells, one, "completion/list", ui.ControlOperation{Verb: "select", ItemID: "item-1"})
	if result.State != control.Applied || len(result.ResourceIDs) != 1 || result.ResourceIDs[0] != s.set.ID {
		t.Fatal("selected documentation did not await its exact mounted observation", result)
	}
	completionRequireText(t, one.app, []string{"SECOND_RESOLVED"}, []string{"FIRST_RESOLVED"})
	if a.Value() != "pr" || b.Value() != "pr" || a.Modified() || two.app.completion != nil {
		t.Fatal("documentation changed shared text or another shell's popup")
	}
	engine.base.mu.Lock()
	originals := append([]string(nil), engine.originals...)
	engine.base.mu.Unlock()
	if len(originals) != 2 || originals[0] != completionConnectedFirst || originals[1] != completionConnectedSecond {
		t.Fatal("semantic row selection reconstructed opaque original identity", originals)
	}
	result = journeySettingsControl(t, runtime, shells, one, "completion/list", ui.ControlOperation{Verb: "activate", ItemID: "item-1"})
	if result.State != control.Admitted {
		t.Fatalf("local completion falsely claimed canonical acceptance before its document receipt: %+v", result)
	}
	remoteJourneyUntil(t, shells, func() bool {
		return a.Value() == "second" && b.Value() == "second" && a.DocumentState().PendingEdits == 0 && !a.DocumentState().InFlight
	})
	if one.app.completion != nil || b.CursorLine() != 0 || b.CursorCol() != 0 ||
		one.app.Input.Value() != "private completion one" || two.app.Input.Value() != "private completion two" {
		t.Fatal("original insertion altered a private peer cursor/draft/chooser")
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "pr" {
		t.Fatalf("unsaved completion wrote disk: %q %v", data, err)
	}
	renameConnectedKey(t, one, tea.KeyPressMsg{Code: 'z', Mod: tea.ModCtrl})
	remoteJourneyUntil(t, shells, func() bool { return a.Value() == "pr" && b.Value() == "pr" && !a.DocumentState().UndoPending })
	a.SetCursorPosition(0, 2)
	renameConnectedKey(t, one, tea.KeyPressMsg{Code: ' ', Mod: tea.ModCtrl})
	remoteJourneyUntil(t, shells, func() bool { return one.app.completion != nil && one.app.completion.screen != nil })
	current := one.app.completion
	for _, shell := range shells {
		result := requestShellForTest(t, runtime, shells, shell, "shell.describe_control",
			shellControlIntent{ComponentID: component, Operation: ui.ControlOperation{Verb: "activate", ItemID: "item-1"}}, nil)
		if result.State == control.Applied || one.app.completion != current || a.Value() != "pr" {
			t.Fatal("retired or foreign shell control acquired a newer private invocation", result)
		}
	}
}
