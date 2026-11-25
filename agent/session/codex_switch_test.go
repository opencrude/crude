package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"crude/agent/codexio"
	"crude/agent/provider"
	"crude/internal/providersim"
	"crude/types/setting"
)

func TestModelSwitchSwapsGPTPromptAndToolsOnRealRequests(t *testing.T) {
	requests := make(chan providersim.Request, 4)
	var canonicalSystem string
	server := providersim.New(t)
	endpoint, setWire := simStableEndpoint(t, server, providersim.Chat)
	server.Respond(func(wire providersim.Request) providersim.Turn {
		req := simDecodeRequest[struct {
			Tools []struct {
				Type     string         `json:"type"`
				Name     string         `json:"name"`
				Function map[string]any `json:"function"`
				Custom   map[string]any `json:"custom"`
			} `json:"tools"`
		}](t, wire)
		gpt := codexio.IsGPT(wire.Model)
		responses := wire.Wire == providersim.Responses
		patch, edit, write := false, false, false
		for _, tool := range req.Tools {
			if tool.Custom != nil || (!responses && tool.Type != "function") {
				t.Error("Chat gateway received unsupported custom tool")
			}
			name, _ := tool.Function["name"].(string)
			if responses {
				name = tool.Name
			}
			patch = patch || name == "apply_patch"
			edit = edit || name == "edit_file"
			write = write || name == "write_file"
			if name == "multi_tool_use" || name == "multi_tool_use.parallel" {
				t.Error("provider-reserved namespace exposed after protocol switch")
			}
		}
		if gpt != patch || gpt == edit || gpt == write {
			t.Errorf("%s: patch=%t edit=%t write=%t", wire.Model, patch, edit, write)
		}
		system := simText(wire.System)
		if !strings.Contains(system, `<crude_agent version="3">`) || !strings.Contains(system, "stable-project-policy") {
			t.Error("model switch lost canonical Crude prompt")
		}
		if canonicalSystem == "" {
			canonicalSystem = system
		} else if system != canonicalSystem {
			t.Error("model/protocol switch changed unchanged guidance prompt bytes")
		}
		requests <- wire
		return providersim.Text("done").WithoutUsage()
	})
	cfg := modelAttributionConfig(endpoint, "gpt-5.5")
	account := cfg.Accounts["account"]
	account.Connection.Protocol = &provider.ProtocolSettings{Store: new(false)}
	cfg.Accounts["account"] = account
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("stable-project-policy"), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := NewWithSessionDir("codex-switch", cfg, root, filepath.Join(t.TempDir(), "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.SetWorkDir(root)
	sender := newMockEventSender()
	orch, err := s.NewOrchestrator(sender)
	if err != nil {
		t.Fatal(err)
	}
	for i, selection := range []struct{ model, protocol string }{
		{"gpt-5.5", "openai"}, {"claude-test", "openai-responses"}, {"gateway/GPT-5.5", "openai-responses"}, {"gpt-5.5", "openai"},
	} {
		model := selection.model
		if i > 0 {
			setWire(sessionSimWire(selection.protocol))
			account.Connection.APIStandard = selection.protocol
			cfg.Accounts["account"] = account
			s.UpdateWorkspaceDefaults(setting.App{Accounts: cfg.Accounts})
			settings := s.SettingsSnapshot()
			settings.Model = model
			settings.SessionOverrides = map[string]setting.Category{CategoryDefault: {AccountID: account.ID, Model: model}}
			if err := s.HandleSettingsSave(settings); err != nil {
				t.Fatal(err)
			}
		}
		if !orch.SubmitInput(provider.PlainInput("continue")) {
			t.Fatal("turn rejected")
		}
		select {
		case got := <-requests:
			if got.Model != model || got.Wire != sessionSimWire(selection.protocol) {
				t.Fatalf("request %d used %s/%s, want %s/%s", i+1, got.Model, got.Wire, model, selection.protocol)
			}
		case <-time.After(5 * time.Second):
			var seen []string
			for _, request := range server.Requests() {
				seen = append(seen, string(request.Wire)+" "+request.Path+": "+request.Violation)
			}
			if log, err := os.ReadFile(filepath.Join(s.SessionDir(), "session.log")); err == nil {
				lines := strings.Split(string(log), "\n")
				t.Log(strings.Join(lines[max(0, len(lines)-12):], "\n"))
			}
			t.Fatalf("request %d for %s/%s did not arrive; seen %v", i+1, model, selection.protocol, seen)
		}
		waitTurnDone(t, sender, orch.ID)
		waitAssistantCount(t, s, i+1)
	}
}
