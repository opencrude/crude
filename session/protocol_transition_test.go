package session

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"crude/provider"
	ttypes "crude/types"
)

func TestAllWireTransitionsResumeAndContinueTheToolLoop(t *testing.T) {
	for _, source := range provider.ProtocolList() {
		for _, target := range provider.ProtocolList() {
			t.Run(source.Info().ID+"_to_"+target.Info().ID, func(t *testing.T) {
				root, home, sessionDir := t.TempDir(), t.TempDir(), t.TempDir()
				if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package transition_evidence\n"), 0600); err != nil {
					t.Fatal(err)
				}
				const model = "gpt-protocol-transition-test"
				provider.SetModelContext(model, 1_000_000)
				var mu sync.Mutex
				protocol := source.Info().ID
				var bodies []map[string]any
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					body, err := io.ReadAll(r.Body)
					if err != nil {
						t.Error(err)
						return
					}
					var wire map[string]any
					if err := json.Unmarshal(body, &wire); err != nil {
						t.Error(err)
						return
					}
					mu.Lock()
					defer mu.Unlock()
					bodies = append(bodies, wire)
					requestIndex := len(bodies)
					validateSessionTransitionWire(t, protocol, wire)
					if requestIndex%2 == 0 && !bytes.Contains(body, []byte("transition_evidence")) {
						t.Error("real file-tool result missing from next model request")
					}
					writeTransitionResponse(w, protocol, requestIndex)
				}))
				defer server.Close()
				cfg := modelAttributionConfig(server.URL, model)
				pc := cfg.Accounts["account"].Connection
				pc.APIStandard = protocol
				a := cfg.Accounts["account"]
				a.Connection = pc
				cfg.Accounts["account"] = a
				s, err := NewWithSessionDir("protocol-transition", cfg, home, sessionDir)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(s.Close)
				s.SetWorkDir(root)
				sender := newMockEventSender()
				orch, err := s.NewOrchestrator(sender)
				if err != nil {
					t.Fatal(err)
				}
				if !orch.SubmitInput(provider.PlainInput("Read a.go and answer this historical request.")) {
					t.Fatal("historical input rejected")
				}
				waitTransitionTurn(t, sender, orch)
				waitAssistantCount(t, s, 1)
				s.msgsMu.Lock()
				before, err := json.Marshal(s.activeContext)
				s.msgsMu.Unlock()
				if err != nil {
					t.Fatal(err)
				}
				dbPath := s.dbPath
				s.Close()
				resumed, err := ResumeSessionWithDir(dbPath, cfg, home, sessionDir)
				if err != nil {
					t.Fatal(err)
				}
				defer resumed.Close()
				resumed.SetWorkDir(root)
				resumed.msgsMu.Lock()
				after, err := json.Marshal(resumed.activeContext)
				resumed.msgsMu.Unlock()
				if err != nil || !bytes.Equal(before, after) {
					t.Fatal("passive resume rewrote canonical history")
				}
				mu.Lock()
				count := len(bodies)
				protocol = target.Info().ID
				mu.Unlock()
				if count != 2 {
					t.Fatal("resume made a provider request")
				}
				pc.APIStandard = target.Info().ID
				account := cfg.Accounts["account"]
				account.Connection = pc
				cfg.Accounts["account"] = account
				resumed.UpdateWorkspaceDefaults(ttypes.AppSettings{Accounts: cfg.Accounts})
				sender = newMockEventSender()
				orch, err = resumed.NewOrchestrator(sender)
				if err != nil {
					t.Fatal(err)
				}
				for turn := 2; turn <= 3; turn++ {
					if !orch.SubmitInput(provider.PlainInput(fmt.Sprintf("Read a.go and continue request %d.", turn))) {
						t.Fatal("switched input rejected")
					}
					waitTransitionTurn(t, sender, orch)
					waitAssistantCount(t, resumed, turn)
				}
				// Changing connection, model and generation configuration together
				// is another ordinary request boundary, not a transcript rewrite.
				const changedModel = "configured-model-transition-test"
				provider.SetModelContext(changedModel, 1_000_000)
				account = cfg.Accounts["account"]
				account.ID, account.APIKey = "other-account", "other-test-key"
				accounts := provider.CloneAccounts(cfg.Accounts)
				accounts[account.ID] = account
				resumed.UpdateWorkspaceDefaults(ttypes.AppSettings{Accounts: accounts})
				settings := resumed.SettingsSnapshot()
				settings.SessionOverrides = map[string]ttypes.CategorySettings{CategoryDefault: {AccountID: account.ID, Model: changedModel, ThinkingEffort: "none"}}
				if err := resumed.HandleSettingsSave(settings); err != nil {
					t.Fatal(err)
				}
				for turn := 4; turn <= 5; turn++ {
					if !orch.SubmitInput(provider.PlainInput(fmt.Sprintf("Read a.go after compound configuration change %d.", turn))) {
						t.Fatal("compound configuration input rejected")
					}
					waitTransitionTurn(t, sender, orch)
					waitAssistantCount(t, resumed, turn)
				}
				resumed.msgsMu.Lock()
				final := append([]provider.ChatMessage(nil), resumed.activeContext...)
				resumed.msgsMu.Unlock()
				var old []provider.ChatMessage
				if err := json.Unmarshal(before, &old); err != nil {
					t.Fatal(err)
				}
				prefix, err := json.Marshal(final[:len(old)])
				if err != nil || !bytes.Equal(prefix, before) {
					t.Fatal("wire/config switch changed accepted history")
				}
				mu.Lock()
				defer mu.Unlock()
				if len(bodies) != 10 {
					t.Fatalf("agentic loop did not complete all turns: requests=%d", len(bodies))
				}
				for _, key := range []string{"tools", "system", "instructions"} {
					if !reflect.DeepEqual(bodies[2][key], bodies[4][key]) {
						t.Errorf("stable %s changed after the transition", key)
					}
				}
				key := "messages"
				if target.Info().ID == "openai-responses" || target.Info().ID == "codex" {
					key = "input"
				}
				first := bodies[2][key].([]any)
				steady := bodies[4][key].([]any)
				if len(steady) < len(first) {
					t.Fatal("steady request deleted context")
				}
				if !reflect.DeepEqual(withoutTransitionCacheControls(first), withoutTransitionCacheControls(steady[:len(first)])) {
					t.Fatal("steady request rewrote the reusable wire prefix")
				}
				for _, field := range []string{"tools", "system", "instructions", "model"} {
					if !reflect.DeepEqual(bodies[6][field], bodies[8][field]) {
						t.Errorf("compound configuration did not stabilize %s", field)
					}
				}
				first = bodies[6][key].([]any)
				steady = bodies[8][key].([]any)
				if len(steady) < len(first) || !reflect.DeepEqual(withoutTransitionCacheControls(first), withoutTransitionCacheControls(steady[:len(first)])) {
					t.Fatal("compound configuration did not preserve its subsequent warm prefix")
				}
			})
		}
	}
}

func writeTransitionResponse(w http.ResponseWriter, protocol string, index int) {
	callID := fmt.Sprintf("read-%d", index)
	text := fmt.Sprintf("complete-answer-%d", index)
	tool := index%2 == 1
	w.Header().Set("Content-Type", "application/json")
	switch protocol {
	case "openai":
		message := map[string]any{"role": "assistant", "content": text}
		reason := "stop"
		if tool {
			message["content"] = "tool commentary"
			message["tool_calls"] = []any{map[string]any{"id": callID, "type": "function", "function": map[string]any{"name": "read_file", "arguments": `{"path":"a.go"}`}}}
			reason = "tool_calls"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": message, "finish_reason": reason}}})
	case "openai-responses", "codex":
		output := []any{map[string]any{"type": "message", "id": fmt.Sprintf("message-%d", index), "role": "assistant", "status": "completed", "phase": "final_answer", "content": []any{map[string]any{"type": "output_text", "text": text, "annotations": []any{}}}}}
		if tool {
			output = []any{map[string]any{"type": "function_call", "call_id": callID, "name": "read_file", "arguments": `{"path":"a.go"}`, "status": "completed"}}
		}
		response := map[string]any{"id": fmt.Sprintf("response-%d", index), "status": "completed", "store": false, "output": output}
		w.Header().Set("Content-Type", "text/event-stream")
		encoded, _ := json.Marshal(response)
		_, _ = fmt.Fprintf(w, "data: {\"type\":\"response.completed\",\"response\":%s}\n\n", encoded)
	case "anthropic":
		content := []any{map[string]any{"type": "text", "text": text}}
		reason := "end_turn"
		if tool {
			content = []any{map[string]any{"type": "text", "text": "tool commentary"}, map[string]any{"type": "tool_use", "id": callID, "name": "read_file", "input": map[string]any{"path": "a.go"}}}
			reason = "tool_use"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"type": "message", "id": fmt.Sprintf("message-%d", index), "role": "assistant", "content": content, "stop_reason": reason})
	case "ollama":
		message := map[string]any{"role": "assistant", "content": text}
		if tool {
			message["content"] = "tool commentary"
			message["tool_calls"] = []any{map[string]any{"id": callID, "function": map[string]any{"name": "read_file", "arguments": map[string]any{"path": "a.go"}}}}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"model": "gpt-protocol-transition-test", "done": true, "message": message, "done_reason": "stop"})
	}
}

func validateSessionTransitionWire(t *testing.T, protocol string, wire map[string]any) {
	t.Helper()
	if protocol == "openai-responses" || protocol == "codex" {
		for _, raw := range wire["input"].([]any) {
			item := raw.(map[string]any)
			if item["role"] != "assistant" {
				continue
			}
			if _, ok := item["content"].(string); ok {
				continue
			}
			for _, part := range item["content"].([]any) {
				kind := part.(map[string]any)["type"]
				if kind != "output_text" && kind != "refusal" {
					t.Errorf("historical assistant uses invalid %v", kind)
				}
			}
		}
	}
	for _, raw := range wire["tools"].([]any) {
		tool := raw.(map[string]any)
		if protocol == "openai-responses" || protocol == "codex" {
			continue
		}
		var schema any
		if protocol == "anthropic" {
			schema = tool["input_schema"]
			if strings.Contains(tool["name"].(string), ".") {
				t.Error("illegal dotted Anthropic tool name")
			}
		} else {
			if tool["type"] != "function" {
				t.Error("unsupported custom tool definition")
			}
			schema = tool["function"].(map[string]any)["parameters"]
		}
		if object, ok := schema.(map[string]any); !ok || object["type"] != "object" {
			t.Error("tool lacks object input schema")
		}
	}
}

func withoutTransitionCacheControls(value any) any {
	switch value := value.(type) {
	case []any:
		copy := make([]any, len(value))
		for i, v := range value {
			copy[i] = withoutTransitionCacheControls(v)
		}
		return copy
	case map[string]any:
		copy := make(map[string]any, len(value))
		for k, v := range value {
			if k != "cache_control" {
				copy[k] = withoutTransitionCacheControls(v)
			}
		}
		return copy
	default:
		return value
	}
}

func waitTransitionTurn(t *testing.T, sender *mockEventSender, orch *Orchestrator) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case event := <-sender.ch:
			if event.agentID != orch.ID {
				continue
			}
			if event.typ == "error" {
				t.Fatalf("agentic loop failed: %v", event.data)
			}
			if event.typ == "done" {
				for {
					orch.turnMu.Lock()
					settled := orch.turnCancel == nil
					orch.turnMu.Unlock()
					if settled {
						return
					}
					select {
					case <-deadline:
						t.Fatal("completed turn did not release admission")
					case <-time.After(time.Millisecond):
					}
				}
			}
		case <-deadline:
			t.Fatal("transition turn did not complete")
		}
	}
}
