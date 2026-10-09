package workspace

import (
	"errors"
	"reflect"
	"testing"
)

func TestAgentEffectsPermissionPromptRetainsPayloadAndIdentity(t *testing.T) {
	state := NewAgentState("owner", nil, nil)
	want := AgentPrompt{
		BlockID: "permission-1", Question: "Allow this edit?", Operation: "Update",
		Tool: "edit_file", Target: "main.go", Risk: "medium", Reason: "Requested edit",
		Suggestion: "Review the patch",
	}
	effects := state.ApplyEvent(AgentEvent{Type: "blocking", Data: map[string]string{
		"block_id": want.BlockID, "question": want.Question, "operation": want.Operation,
		"tool": want.Tool, "target": want.Target, "risk": want.Risk, "reason": want.Reason,
		"suggestion": want.Suggestion,
	}})
	if effects.Prompt == nil || !reflect.DeepEqual(*effects.Prompt, want) {
		t.Fatalf("permission prompt = %+v, want %+v", effects.Prompt, want)
	}
	if effects.Thinking || effects.Streaming || !effects.Blocking || !state.IsActive() || state.PendingBlockID != want.BlockID {
		t.Fatalf("permission lifecycle: effects=%+v state=%+v", effects, state)
	}
	if len(state.Messages.Messages()) != 0 {
		t.Fatal("permission prompt created a transcript row")
	}
}

func TestAgentEffectsMultiQuestionPromptRetainsPayloadAndIdentity(t *testing.T) {
	const questions = `[{"header":"Scope","question":"Which files?","options":["one","all"]},{"header":"Checks","question":"Run tests?"}]`
	for _, operation := range []string{"question", "questions"} {
		t.Run(operation, func(t *testing.T) {
			state := NewAgentState("owner", nil, nil)
			effects := state.ApplyEvent(AgentEvent{Type: "blocking", Data: map[string]any{
				"block_id": "question-1", "operation": operation, "tool": "question", "questions_json": questions,
			}})
			want := AgentPrompt{IsQuestion: true, BlockID: "question-1", QuestionsJSON: questions}
			if effects.Prompt == nil || !reflect.DeepEqual(*effects.Prompt, want) || state.PendingBlockID != want.BlockID {
				t.Fatalf("question prompt = %+v, pending=%q, want %+v", effects.Prompt, state.PendingBlockID, want)
			}
			if !effects.Blocking || effects.Thinking || effects.Streaming {
				t.Fatalf("question lifecycle effects=%+v", effects)
			}
		})
	}
}

func TestAgentEffectsBlockingCompletionIsExact(t *testing.T) {
	for _, anyMap := range []bool{false, true} {
		state := NewAgentState("owner", nil, nil)
		state.ApplyEvent(AgentEvent{Type: "blocking", Data: map[string]string{
			"block_id": "pending", "operation": "Update", "tool": "edit_file",
		}})
		payload := func(id string) any {
			if anyMap {
				return map[string]any{"block_id": id}
			}
			return map[string]string{"block_id": id}
		}
		before := *state
		effects := state.ApplyEvent(AgentEvent{Type: "blocking_complete", Data: payload("unrelated")})
		if !reflect.DeepEqual(effects, AgentEffects{}) || !reflect.DeepEqual(*state, before) {
			t.Fatalf("unrelated completion changed pending prompt: effects=%+v state=%+v", effects, state)
		}
		effects = state.ApplyEvent(AgentEvent{Type: "blocking_complete", Data: payload("pending")})
		if !effects.DismissPrompt || effects.DismissBlockID != "pending" || effects.Prompt != nil || state.PendingBlockID != "" {
			t.Fatalf("exact completion did not dismiss its prompt: effects=%+v pending=%q", effects, state.PendingBlockID)
		}
		if !effects.ThinkingChanged || !effects.Thinking || effects.Streaming || effects.Blocking || !state.IsActive() {
			t.Fatalf("completed prompt did not resume running lifecycle: %+v", effects)
		}
	}
}

func TestAgentEffectsDoneRetainsTerminalData(t *testing.T) {
	for _, reason := range []string{"completed", "cancelled", "failed"} {
		t.Run(reason, func(t *testing.T) {
			state := NewAgentState("owner", nil, nil)
			state.SubmitInput("work")
			effects := state.ApplyEvent(AgentEvent{Type: "done", AgentID: "owner", Data: map[string]any{
				"content": "accepted answer", "reason": reason,
			}})
			if effects.Done == nil || *effects.Done != (AgentDoneResult{AgentID: "owner", Reason: reason}) || effects.Error != nil {
				t.Fatalf("done effect=%+v", effects)
			}
			if !effects.ThinkingChanged || effects.Thinking || effects.Streaming || effects.Blocking || !effects.StatusChanged || effects.Status != "" || state.IsActive() {
				t.Fatalf("done did not settle lifecycle: effects=%+v state=%+v", effects, state)
			}
		})
	}
}

func TestAgentEffectsErrorRetainsTerminalData(t *testing.T) {
	state := NewAgentState("owner", nil, nil)
	state.SubmitInput("work")
	effects := state.ApplyEvent(AgentEvent{Type: "error", AgentID: "owner", Data: errors.New("connection failed")})
	if effects.Error == nil || *effects.Error != (AgentErrorResult{AgentID: "owner", Err: "connection failed"}) || effects.Done != nil {
		t.Fatalf("error effect=%+v", effects)
	}
	if !effects.ThinkingChanged || effects.Thinking || effects.Streaming || effects.Blocking || state.IsActive() {
		t.Fatalf("error did not settle lifecycle: effects=%+v state=%+v", effects, state)
	}
	messages := state.Messages.Messages()
	if len(messages) != 1 || messages[0].Role != MessageRoleError || messages[0].Content != "connection failed" {
		t.Fatalf("error lost canonical evidence: %+v", messages)
	}
}
