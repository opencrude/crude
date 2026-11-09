package session

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"crude/agent"
	"crude/collaboration"
	"crude/safety"
	"crude/tools"
	"crude/types/toolresult"
)

func TestChildPolicyRefusalsKeepHintsPrivateAndCannotPerformEffects(t *testing.T) {
	s, err := NewWithSessionDir("result-policy", testConfig(), t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	root := t.TempDir()
	s.SetWorkDir(root)
	path := filepath.Join(root, "victim")
	if err := os.WriteFile(path, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := NewSubAgentRunner(s, 1)
	defer runner.SessionCancel()
	called := false
	runner.SetParentToolFn(func(context.Context, string, map[string]any) (tools.ToolResult, error) {
		called = true
		return tools.ToolResult{}, nil
	})
	policy := safety.ReadOnlyPolicy()
	live := agent.NewAgent(nil, "policy-test", s.logger)
	live.Policy = policy
	handle := &agent.SubAgentHandle{Agent: live}
	fn := runner.buildProxiedToolFn(agent.SubAgentSpec{ID: "child", PolicyName: "read-only", Policy: policy}, nil, handle)
	for _, test := range []struct {
		tool   string
		params map[string]any
	}{
		{"write_file", map[string]any{"path": path, "content": "changed"}},
		{"write_stdin", map[string]any{"session_id": 1, "chars": "rm victim\n"}},
		{"execute_command", map[string]any{"command": "rm victim"}},
		{"apply_patch", map[string]any{"input": "*** Begin Patch\n*** Delete File: victim\n*** End Patch\n"}},
	} {
		t.Run(test.tool, func(t *testing.T) {
			result, err := fn(t.Context(), test.tool, test.params)
			if err == nil || result.Outcome.State != toolresult.Denied || result.AgentHint == "" {
				t.Fatalf("refusal lost enforcement or private repair guidance: %+v %v", result, err)
			}
			if strings.Contains(result.Content+err.Error(), "[POLICY BLOCK]") ||
				strings.Contains(result.Content+err.Error(), "Only read-only commands like") ||
				strings.Contains(result.Content+err.Error(), result.AgentHint) || called {
				t.Fatal("policy advice entered display evidence or a denied effect ran")
			}
		})
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != "original" {
		t.Fatalf("a denied tool changed the file: %q %v", got, err)
	}
}

func TestObservedRunFailureDoesNotTurnSuccessfulWaitIntoToolFailure(t *testing.T) {
	observation := collaborationWaitObservation{Status: "ready",
		Run:      &collaboration.Run{ID: "failed-run", State: collaboration.RunFailed, Outcome: "provider failed"},
		Guidance: "Private wait advice"}
	result := collaborationResult(observation, nil)
	result = tools.SettleResult("agent_wait", result, false)
	if result.Outcome.State != toolresult.Succeeded || result.Outcome.Code != "observed_run_failed" ||
		result.Outcome.Summary != "provider failed" || strings.Contains(result.Content, observation.Guidance) {
		t.Fatalf("query and observed run were conflated: %+v", result)
	}
	parts := tools.ResultContentParts(result)
	var decoded collaborationWaitObservation
	if len(parts) != 1 || json.Unmarshal([]byte(parts[0].Text), &decoded) != nil ||
		decoded.Run == nil || decoded.Run.ID != "failed-run" || !strings.Contains(parts[0].Text, observation.Guidance) {
		t.Fatal("model lost the exact machine-readable failed run or wait advice")
	}
}

func TestWaitFreshnessFactsStayInDisplayEvidenceNotOnlyInPrivateAdvice(t *testing.T) {
	for _, observed := range []bool{false, true} {
		last := collaborationWaitObservation{}
		if observed {
			last.ObservedAt = time.Unix(123, 0)
		}
		result := collaborationWaitTimeoutSnapshot(t.Context(), func(context.Context) (collaborationWaitObservation, bool, error) {
			return collaborationWaitObservation{}, false, errors.New("snapshot unavailable")
		}, last, time.Now())
		out := collaborationResult(result, nil)
		want := "No observation completed"
		if observed {
			want = "The final refresh did not complete"
		}
		if !strings.Contains(out.Content, want) || out.AgentHint == "" || strings.Contains(out.Content, out.AgentHint) {
			t.Fatalf("freshness facts were hidden with wait advice: %+v", out)
		}
	}
}
