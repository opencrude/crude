package session

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"crude/provider"
)

type planModeRequest struct {
	Messages []struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	} `json:"messages"`
	Tools []struct {
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	} `json:"tools"`
}

func (r planModeRequest) text(index int) string {
	var text string
	if json.Unmarshal(r.Messages[index].Content, &text) != nil {
		text = string(r.Messages[index].Content)
	}
	return text
}

func (r planModeRequest) offers(name string) bool {
	for _, tool := range r.Tools {
		if tool.Function.Name == name {
			return true
		}
	}
	return false
}

// Plan mode is enforced by the runtime, not only described: a Plan mode turn
// carries the contract and offers no tool that writes or tracks progress,
// the mode persists across resume, and the first turn after the user turns
// it off regains its tools and is told the mode ended.
func TestPlanModeTurnsAreReadOnlyAndEndExplicitly(t *testing.T) {
	requests := make(chan planModeRequest, 8)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body planModeRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		requests <- body
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	t.Cleanup(server.Close)
	home, dir := t.TempDir(), t.TempDir()
	cfg := mediaAttributionConfig(server.URL, "plan-model", "openai")
	s, err := NewWithSessionDir("", cfg, home, dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	if err := s.SetPlanMode(true); err != nil {
		t.Fatal(err)
	}
	resumed, err := ResumeSessionWithDir(s.dbPath, cfg, home, dir)
	if err != nil {
		t.Fatal(err)
	}
	if !resumed.PlanMode() {
		t.Fatal("Plan mode did not survive resume")
	}
	resumed.Close()

	sender := newMockEventSender()
	o, err := s.NewOrchestrator(sender)
	if err != nil {
		t.Fatal(err)
	}
	if !o.SubmitInput(provider.PlainInput("plan the refactor")) {
		t.Fatal("submit rejected")
	}
	planned := <-requests
	waitTurnDone(t, sender, o.ID)
	if !strings.Contains(planned.text(0), "<plan_mode>") {
		t.Fatal("Plan mode turn lacks the Plan mode contract")
	}
	if !planned.offers("read_file") || !planned.offers("question") {
		t.Fatal("Plan mode turn lost its read-only tools")
	}
	for _, tool := range []string{"write_file", "edit_file", "apply_patch", "delete_file", "update_plan", "agent_start", "crude_control"} {
		if planned.offers(tool) {
			t.Fatalf("Plan mode turn offers %s", tool)
		}
	}

	if err := s.SetPlanMode(false); err != nil {
		t.Fatal(err)
	}
	if !o.SubmitInput(provider.PlainInput("implement it")) {
		t.Fatal("submit rejected")
	}
	normal := <-requests
	waitTurnDone(t, sender, o.ID)
	if strings.Contains(normal.text(0), "<plan_mode>") || !normal.offers("write_file") {
		t.Fatal("normal turn after Plan mode is still restricted")
	}
	ended := false
	for index := range normal.Messages {
		ended = ended || strings.Contains(normal.text(index), "turned Plan mode off")
	}
	if !ended {
		t.Fatal("first turn after Plan mode was not told the mode ended")
	}
}

func TestPlanModeRefusesMutationsAtExecution(t *testing.T) {
	for _, test := range []struct {
		tool    string
		params  map[string]any
		refused bool
	}{
		{"read_file", map[string]any{"path": "a.go"}, false},
		{"exec_command", map[string]any{"cmd": "git status"}, false},
		{"exec_command", map[string]any{"cmd": "rm -rf build"}, true},
		{"execute_command", map[string]any{"command": "sed -i s/a/b/ a.go"}, true},
		{"write_stdin", map[string]any{"chars": "yes\n"}, true},
		{"apply_patch", map[string]any{"input": "*** Begin Patch"}, true},
		{"write_file", map[string]any{"path": "a.go"}, true},
		{"update_plan", map[string]any{}, true},
		{"crude_control", map[string]any{}, true},
	} {
		err := planModeRefusal(test.tool, test.params, nil, nil)
		if (err != nil) != test.refused {
			t.Errorf("%s %v: refusal = %v, want refused %v", test.tool, test.params, err, test.refused)
		}
		if err != nil && !strings.Contains(err.Error(), "[PLAN MODE]") {
			t.Errorf("%s refusal does not name Plan mode: %v", test.tool, err)
		}
	}
}
