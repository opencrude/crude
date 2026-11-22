package turnloop

import (
	"context"
	"testing"

	"crude/agent/provider"
	"crude/agent/safety"
	"crude/agent/tools"
	"crude/types/command"
	"crude/types/conversation"
)

// runCommand executes one command call and answers its prompt, if any, with
// answer. It reports whether the user was asked and whether the command ran.
func runCommand(t *testing.T, a *Agent, cmd, answer string) (asked, ran bool, tag string) {
	t.Helper()
	a.ToolFn = func(context.Context, string, map[string]any) (tools.ToolResult, error) {
		ran = true
		return tools.ToolResult{Content: "ok"}, nil
	}
	responses := make(chan conversation.BlockResponse, 1)
	call := commandPermissionCall(cmd)
	a.executeOneToolCall(t.Context(), call, func(evt AgentEvent) {
		switch evt.Type {
		case "blocking":
			asked = true
			responses <- conversation.BlockResponse{RequestID: call.InternalID, Outcome: conversation.BlockAnswered, Value: answer}
		case "permission_resolved":
			tag, _ = evt.Data.(map[string]any)["decision"].(string)
		}
	}, responses, new(string))
	return asked, ran, tag
}

// "Always allow" covered nothing below "destructive": an approved unknown
// command was asked about again eight seconds later in Errand's session.
func TestAlwaysAllowCoversTheSameCommandsForTheSession(t *testing.T) {
	a := newPermissionLifecycleAgent(t)
	a.CommandApprovals = safety.NewPermissionStore()
	approved := `cd /repo && pyright --outputjson > "$CRUDE_TEMP_DIR/py.json" 2>&1`
	if asked, ran, _ := runCommand(t, a, approved, "always"); !asked || !ran {
		t.Fatalf("first run: asked=%v ran=%v", asked, ran)
	}
	for _, cmd := range []string{
		approved,
		`cd /repo  &&  pyright --outputjson > "$CRUDE_TEMP_DIR/py.json" 2>&1`,
		`cd /repo && pyright --outputjson > "$CRUDE_TEMP_DIR/py.json" 2>&1; tail -5 "$CRUDE_TEMP_DIR/py.json"`,
	} {
		asked, ran, tag := runCommand(t, a, cmd, "deny")
		if asked || !ran || tag != "always" {
			t.Fatalf("%q: asked=%v ran=%v tag=%q, want it covered by Always allow", cmd, asked, ran, tag)
		}
	}
	for _, cmd := range []string{
		`cd /other && pyright --outputjson > "$CRUDE_TEMP_DIR/py.json" 2>&1`,
		`cd /repo && pyright --outputjson > "$CRUDE_TEMP_DIR/other.json" 2>&1`,
		`cd /repo && pyright --verbose > "$CRUDE_TEMP_DIR/py.json" 2>&1`,
		`cd /repo && pyright --outputjson > "$CRUDE_TEMP_DIR/py.json" 2>&1 && rm -rf build`,
		`pyright --outputjson > "$CRUDE_TEMP_DIR/py.json" 2>&1`,
	} {
		if asked, ran, _ := runCommand(t, a, cmd, "deny"); !asked || ran {
			t.Fatalf("%q: asked=%v ran=%v, want a new approval", cmd, asked, ran)
		}
	}
}

// An interpreter runs its input, so approving one script never approves
// another script for the same interpreter.
func TestAlwaysAllowForAnInterpreterCoversOnlyThatScript(t *testing.T) {
	a := newPermissionLifecycleAgent(t)
	a.CommandApprovals = safety.NewPermissionStore()
	script := "python - <<'PY'\nprint('hello')\nPY"
	if asked, ran, _ := runCommand(t, a, script, "always"); !asked || !ran {
		t.Fatalf("first run: asked=%v ran=%v", asked, ran)
	}
	if asked, ran, _ := runCommand(t, a, script, "deny"); asked || !ran {
		t.Fatalf("same script: asked=%v ran=%v", asked, ran)
	}
	if asked, ran, _ := runCommand(t, a, "python - <<'PY'\nimport shutil; shutil.rmtree('src')\nPY", "deny"); !asked || ran {
		t.Fatalf("other script: asked=%v ran=%v, want a new approval", asked, ran)
	}
}

// The command shell can change between calls in Settings. The same text in
// another shell is another command, so an approval never crosses shells.
func TestAlwaysAllowDoesNotCrossCommandShells(t *testing.T) {
	a := newPermissionLifecycleAgent(t)
	a.CommandApprovals = safety.NewPermissionStore()
	shell := command.Shell{Syntax: command.SyntaxPOSIX}
	a.CommandShell = func() command.Shell { return shell }
	if asked, ran, _ := runCommand(t, a, "make check", "always"); !asked || !ran {
		t.Fatal("first run was not asked")
	}
	shell = command.Shell{Syntax: command.SyntaxPowerShell}
	if asked, ran, _ := runCommand(t, a, "make check", "deny"); !asked || ran {
		t.Fatalf("PowerShell reused a Git Bash approval: asked=%v ran=%v", asked, ran)
	}
	if asked, ran, _ := runCommand(t, a, "make check", "always"); !asked || !ran {
		t.Fatal("PowerShell approval was not asked")
	}
	if asked, _, _ := runCommand(t, a, "make check", "deny"); asked {
		t.Fatal("PowerShell approval was not kept")
	}
	// Two POSIX shells are still two programs.
	shell = command.Shell{Path: "C:/Program Files/Git/bin/bash.exe", Syntax: command.SyntaxPOSIX}
	if asked, _, _ := runCommand(t, a, "make lint", "always"); !asked {
		t.Fatal("Git Bash approval was not asked")
	}
	shell = command.Shell{Path: "C:/Windows/System32/wsl.exe", Syntax: command.SyntaxPOSIX}
	if asked, ran, _ := runCommand(t, a, "make lint", "deny"); !asked || ran {
		t.Fatalf("another POSIX shell reused a Git Bash approval: asked=%v ran=%v", asked, ran)
	}
}

// The prompt says why Auto Review left the request to the user.
func TestPromptCarriesTheAutoReviewNote(t *testing.T) {
	a := newPermissionLifecycleAgent(t)
	a.PermissionReviewer = func(context.Context, provider.ToolCall, map[string]any, safety.CommandRisk, func(AgentEvent)) PermissionReview {
		return PermissionReview{Decision: "ask", Reason: "Auto Review was not sure: it deletes files."}
	}
	a.ToolFn = func(context.Context, string, map[string]any) (tools.ToolResult, error) {
		return tools.ToolResult{}, nil
	}
	responses := make(chan conversation.BlockResponse, 1)
	call := commandPermissionCall("rm -rf build")
	note := ""
	a.executeOneToolCall(t.Context(), call, func(evt AgentEvent) {
		if evt.Type == "blocking" {
			note = evt.Data.(map[string]string)["auto_review"]
			responses <- conversation.BlockResponse{RequestID: call.InternalID, Outcome: conversation.BlockAnswered, Value: "deny"}
		}
	}, responses, new(string))
	if note != "Auto Review was not sure: it deletes files." {
		t.Fatalf("prompt note = %q", note)
	}
}

// Turning Auto Review on while a request waits reviews that request, so the
// user does not answer what the new policy decides.
func TestWaitingRequestIsReviewedWhenThePolicyChanges(t *testing.T) {
	for _, tc := range []struct {
		decision string
		ran      bool
		tag      string
	}{{"allow", true, "reviewer-allowed"}, {"deny", false, "reviewer-denied"}} {
		t.Run(tc.decision, func(t *testing.T) {
			a := newPermissionLifecycleAgent(t)
			reviewOn := false
			a.PermissionReviewer = func(context.Context, provider.ToolCall, map[string]any, safety.CommandRisk, func(AgentEvent)) PermissionReview {
				if !reviewOn {
					return PermissionReview{Decision: "ask"}
				}
				return PermissionReview{Decision: tc.decision, Reason: "reviewed"}
			}
			changed := make(chan struct{})
			a.PermissionPolicyChanged = func() <-chan struct{} { return changed }
			ran, completed := false, false
			tag := ""
			a.ToolFn = func(context.Context, string, map[string]any) (tools.ToolResult, error) {
				ran = true
				return tools.ToolResult{}, nil
			}
			a.executeOneToolCall(t.Context(), commandPermissionCall("rm -rf build"), func(evt AgentEvent) {
				switch evt.Type {
				case "blocking":
					reviewOn = true
					close(changed)
				case "blocking_complete":
					completed = true
				case "permission_resolved":
					tag, _ = evt.Data.(map[string]any)["decision"].(string)
				}
			}, make(chan conversation.BlockResponse, 1), new(string))
			if ran != tc.ran || !completed || tag != tc.tag {
				t.Fatalf("ran=%v completed=%v tag=%q, want ran=%v tag=%q", ran, completed, tag, tc.ran, tc.tag)
			}
		})
	}
}

// A human answer queued while the review runs is the decision.
func TestHumanAnswerWinsOverAReviewAfterAPolicyChange(t *testing.T) {
	a := newPermissionLifecycleAgent(t)
	responses := make(chan conversation.BlockResponse, 1)
	call := commandPermissionCall("rm -rf build")
	reviewOn := false
	a.PermissionReviewer = func(context.Context, provider.ToolCall, map[string]any, safety.CommandRisk, func(AgentEvent)) PermissionReview {
		if !reviewOn {
			return PermissionReview{Decision: "ask"}
		}
		responses <- conversation.BlockResponse{RequestID: call.InternalID, Outcome: conversation.BlockAnswered, Value: "deny"}
		return PermissionReview{Decision: "allow", Reason: "reviewed"}
	}
	changed := make(chan struct{})
	a.PermissionPolicyChanged = func() <-chan struct{} { return changed }
	ran := false
	a.ToolFn = func(context.Context, string, map[string]any) (tools.ToolResult, error) {
		ran = true
		return tools.ToolResult{}, nil
	}
	a.executeOneToolCall(t.Context(), call, func(evt AgentEvent) {
		if evt.Type == "blocking" {
			reviewOn = true
			close(changed)
		}
	}, responses, new(string))
	if ran {
		t.Fatal("the reviewer overrode the user's Deny")
	}
}
