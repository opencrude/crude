package viewmodel

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"crude/provider"
	"crude/tui/screens/statusoverview"
	"crude/tui/screens/termdock"
	"crude/tui/ui"
	"crude/types"
	"crude/types/control"
	"crude/workspace"
)

func TestConnectedAnalysisStaticOnlyKeepsEditingAndRejectsSessionProbe(t *testing.T) {
	probeResult := make(chan string, 1)
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request provider.ChatRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			http.Error(w, "invalid fixture request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, message := range request.Messages {
			if message.Role == "tool" && message.ToolCallID == "static-only-probe" {
				probeResult <- message.ContentString()
				_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Static only probe refused.\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
				return
			}
		}
		advertised := false
		for _, definition := range request.Tools {
			advertised = advertised || definition.Function.Name == "lsp"
		}
		if !advertised {
			t.Error("explicit session probe mode did not offer the lsp tool")
		}
		wire, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{
			"delta": map[string]any{"tool_calls": []any{map[string]any{
				"index": 0, "id": "static-only-probe", "type": "function",
				"function": map[string]any{"name": "lsp", "arguments": `{"operation":"diagnostics","path":"main.go"}`},
			}}}, "finish_reason": "tool_calls",
		}}})
		_, _ = fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", wire)
	}))
	defer endpoint.Close()
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	if err := os.WriteFile(path, []byte("package main\nvar Answer = 42\n"), 0600); err != nil {
		t.Fatal(err)
	}
	server := &immediateTreeDiagRuntime{filePath: path, diags: []workspace.LSPDiagnostic{{Severity: workspace.DiagError, Message: "must never be published"}}}
	authority := make(chan *workspace.LSPManager, 1)
	runtime := remoteJourneyRuntime(t, endpoint.URL, func(w *workspace.Workspace) {
		w.LSPMgr.SetRuntime(server)
		authority <- w.LSPMgr
		// Deliberately no grantSemanticFixture: a configured server, enabled
		// editor features and the retained core governor confer no Trust.
	})
	one := newRemoteJourneyShell(t, runtime, root)
	manager := <-authority
	id := remoteJourneyCreate(t, one)
	shells := []remoteJourneyShell{one}
	one.app.Input.SetInput("preserve private composer")
	requestShellForTest(t, runtime, shells, one, "settings.open", map[string]any{"scope": "session"}, nil)
	gateway := analysisControlGateway(t, runtime, one, root, []control.Effect{control.Read, control.Write})
	for _, step := range []struct {
		id string
		op ui.ControlOperation
	}{
		{"session-settings/form/code-intelligence", ui.ControlOperation{Verb: "set_value", Value: json.RawMessage(`"off"`)}},
		{"session-settings/form/code-intelligence", ui.ControlOperation{Verb: "set_value", Value: json.RawMessage(`"on"`)}},
		{"session-settings/actions/save", ui.ControlOperation{Verb: "activate"}},
	} {
		_, desc, node := invokeDeckControl(t, one, step.id, step.op)
		if desc.HumanOnly || desc.Effect != control.Write {
			t.Fatalf("session feature preference is not an ordinary choice: %+v", desc)
		}
		if result := gateway(node, step.op, desc.ID); result.State != control.Applied {
			t.Fatalf("session code intelligence choice: %+v", result)
		}
	}
	one.app.openSessionSettings()
	settings := one.app.sessionSettings().ToSettings()
	if settings.SessionCodeIntelligence == nil || !*settings.SessionCodeIntelligence {
		t.Fatal("session Code Intelligence was not explicitly enabled")
	}
	// Close the clean native deck without touching the persisted session choice.
	_, cmd := one.app.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	runAppCommand(t, one.app, cmd)
	ed := liveTestOpen(t, one.app, path)
	remoteJourneyUntil(t, shells, func() bool {
		state, _ := ed.InsightStatus()
		return state == types.InsightCurrent
	})
	ed.SetCursorPosition(1, len("var Answer"))
	one.app.setFocusEditor()
	_, cmd = one.app.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	runAppCommand(t, one.app, cmd)
	liveTestSync(t, one.app)
	remoteJourneyUntil(t, shells, func() bool {
		state, _ := ed.InsightStatus()
		return state == types.InsightCurrent && ed.Value() == "package main\nvar Answerx = 42\n"
	})
	if paint := ansi.Strip(one.app.View().RenderContent()); !strings.Contains(paint, "Answerx = 42") {
		t.Fatalf("static-only source editing/syntax did not render:\n%s", paint)
	}
	_, cmd = one.app.Update(statusoverview.NavigateMsg{Action: "lsp:start-all"})
	runAppCommand(t, one.app, cmd)
	if manager.EditorEnabled() || manager.ServerReady() || manager.AnalysisPolicy().Trust != types.AnalysisStaticOnly {
		t.Fatal("opening source or explicit Start supplied semantic execution authority")
	}
	one.app.toggleWorkspacePanel(termdock.PanelProblems)
	one.app.paintShellFrame()
	one.app.rootHost.Focus("status-overview/list")
	_, cmd = one.app.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	runAppCommand(t, one.app, cmd)
	if paint := ansi.Strip(one.app.View().RenderContent()); !strings.Contains(paint, "Static only") || !strings.Contains(paint, "Workspace settings") {
		t.Fatalf("rendered static state hides current reason or human repair:\n%s", paint)
	}
	one.app.hideWorkspacePanel()
	one.app.switchToAgentView(id)
	if one.app.Input.Value() != "preserve private composer" {
		t.Fatal("static-only navigation or settings consumed the composer")
	}
	one.app.Input.SetInput("ask for source diagnostics")
	runAppCommand(t, one.app, one.app.submitInput(id, one.app.Input.Value(), nil))
	var refusal string
	remoteJourneyUntil(t, shells, func() bool {
		select {
		case refusal = <-probeResult:
		default:
		}
		return refusal != "" && strings.Contains(ansi.Strip(one.app.View().RenderContent()), "Static only probe refused.")
	})
	if !strings.Contains(refusal, "no workspace execution grant") || !strings.Contains(refusal, "Workspace settings") {
		t.Fatalf("session probe did not report the current Trust reason: %s", refusal)
	}
	// Join the exact owner; no sleeps or empty-row proxy stand in for checking
	// provider construction after open, manual Start and a real session lsp call.
	manager.Close()
	server.mu.Lock()
	created := len(server.rootURIs)
	server.mu.Unlock()
	if created != 0 || len(manager.AllDiagnostics()) != 0 || manager.AnalysisPolicy().Trust != types.AnalysisStaticOnly {
		t.Fatalf("Static only constructed %d providers or acquired semantic authority", created)
	}
	if ed.Value() != "package main\nvar Answerx = 42\n" {
		t.Fatal("session probe consumed private source work")
	}
}
