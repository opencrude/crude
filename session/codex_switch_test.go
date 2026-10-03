package session

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"crude/codexio"
	"crude/provider"
	ttypes "crude/types"
)

func TestModelSwitchSwapsGPTPromptAndToolsOnRealRequests(t *testing.T) {
	requests := make(chan string, 4)
	var canonicalSystem string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Model    string                 `json:"model"`
			Messages []provider.ChatMessage `json:"messages"`
			Tools    []struct {
				Type     string         `json:"type"`
				Name     string         `json:"name"`
				Function map[string]any `json:"function"`
				Custom   map[string]any `json:"custom"`
			} `json:"tools"`
			Input []struct {
				Role    string `json:"role"`
				Content []struct {
					Text string `json:"text"`
				} `json:"content"`
			} `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			return
		}
		gpt := codexio.IsGPT(req.Model)
		responses := strings.HasSuffix(r.URL.Path, "/responses")
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
			t.Errorf("%s: patch=%t edit=%t write=%t", req.Model, patch, edit, write)
		}
		system := ""
		if responses && len(req.Input) > 0 && len(req.Input[0].Content) > 0 {
			system = req.Input[0].Content[0].Text
		} else if len(req.Messages) > 0 {
			system = req.Messages[0].ContentString()
		}
		if !strings.Contains(system, `<crude_agent version="3">`) || !strings.Contains(system, "stable-project-policy") {
			t.Error("model switch lost canonical Crude prompt")
		}
		if canonicalSystem == "" {
			canonicalSystem = system
		} else if system != canonicalSystem {
			t.Error("model/protocol switch changed unchanged guidance prompt bytes")
		}
		requests <- req.Model
		w.Header().Set("Content-Type", "text/event-stream")
		if responses {
			_, _ = fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"done\"}]}]}}\n\n")
		} else {
			_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"done\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
		}
	}))
	defer server.Close()
	cfg := modelAttributionConfig(server.URL, "gpt-5.5")
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
			settings := s.SettingsSnapshot()
			settings.Model = model
			settings.ProviderID = "openrouter"
			settings.SessionOverrides = map[string]ttypes.CategorySettings{CategoryDefault: {Provider: "openrouter", Model: model, APIStandard: selection.protocol}}
			if err := s.HandleSettingsSave(settings); err != nil {
				t.Fatal(err)
			}
		}
		if !orch.SubmitInput(provider.PlainInput("continue")) {
			t.Fatal("turn rejected")
		}
		if got := waitRequestedModel(t, requests); got != model {
			t.Fatal(got)
		}
		waitTurnDone(t, sender, orch.ID)
		waitAssistantCount(t, s, i+1)
	}
}
