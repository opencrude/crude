package session

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"crude/codexio"
	"crude/provider"
	ttypes "crude/types"
)

func TestModelSwitchSwapsGPTPromptAndToolsOnRealRequests(t *testing.T) {
	requests := make(chan string, 3)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Model    string                 `json:"model"`
			Messages []provider.ChatMessage `json:"messages"`
			Tools    []struct {
				Type     string         `json:"type"`
				Function map[string]any `json:"function"`
				Custom   map[string]any `json:"custom"`
			} `json:"tools"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			return
		}
		gpt := codexio.IsGPT(req.Model)
		patch, edit, write := false, false, false
		for _, tool := range req.Tools {
			if tool.Custom != nil {
				t.Error("Chat gateway received unsupported custom tool")
			}
			patch = patch || (tool.Type == "function" && tool.Function["name"] == "apply_patch")
			edit = edit || tool.Function["name"] == "edit_file"
			write = write || tool.Function["name"] == "write_file"
		}
		if gpt != patch || gpt == edit || gpt == write {
			t.Errorf("%s: patch=%t edit=%t write=%t", req.Model, patch, edit, write)
		}
		if gpt {
			base, _ := codexio.Instructions(req.Model)
			if len(req.Messages) == 0 || !strings.HasPrefix(req.Messages[0].ContentString(), base) {
				t.Error("wrong model prompt")
			}
		}
		requests <- req.Model
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"done\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	cfg := modelAttributionConfig(server.URL, "gpt-5.5")
	s, err := NewWithSessionDir("codex-switch", cfg, t.TempDir(), filepath.Join(t.TempDir(), "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	sender := newMockEventSender()
	orch, err := s.NewOrchestrator(sender)
	if err != nil {
		t.Fatal(err)
	}
	for i, model := range []string{"gpt-5.5", "claude-test", "gateway/GPT-5.5"} {
		if i > 0 {
			settings := s.SettingsSnapshot()
			settings.Model = model
			settings.ProviderID = "openrouter"
			settings.SessionOverrides = map[string]ttypes.CategorySettings{CategoryDefault: {Provider: "openrouter", Model: model, APIStandard: "openai"}}
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
