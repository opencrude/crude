package session

import (
	"context"
	"fmt"
	"testing"

	"crude/agent"
	"crude/provider"
	ttypes "crude/types"
)

func TestImmediateCancellationCannotMissAdmittedTurn(t *testing.T) {
	orchestrator := NewOrchestrator("agent", nil)
	for i := 0; i < 250; i++ {
		if !orchestrator.SubmitInput(provider.PlainInput(fmt.Sprintf("turn-%d", i))) {
			t.Fatalf("turn %d was not admitted", i)
		}
		cancelled, _ := orchestrator.CancelTurn()
		if !cancelled {
			t.Fatalf("turn %d cancellation was missed", i)
		}
		request := <-orchestrator.inputCh
		select {
		case <-request.ctx.Done():
		default:
			t.Fatalf("turn %d context remained active", i)
		}
		orchestrator.endTurn(request.generation)
	}
}

func TestTurnFinalizationAndExplicitCancellationHaveDistinctChildOwnership(t *testing.T) {
	newFixture := func(t *testing.T) (*Orchestrator, context.Context) {
		t.Helper()
		s := &Session{}
		runner := NewSubAgentRunner(s, 1)
		t.Cleanup(runner.SessionCancel)
		childCtx, childCancel := context.WithCancel(context.Background())
		handle := &agent.SubAgentHandle{
			Spec:   agent.SubAgentSpec{ID: "child", Name: "child", Category: agent.CatResearcher, ParentTurn: 1},
			Agent:  &agent.Agent{Ctx: childCtx},
			Cancel: childCancel,
		}
		if err := runner.Manager().Register(handle); err != nil {
			t.Fatal(err)
		}
		orchestrator := NewOrchestrator("root", s)
		s.subAgentSup = runner
		return orchestrator, childCtx
	}

	t.Run("normal turn end keeps child alive", func(t *testing.T) {
		orchestrator, childCtx := newFixture(t)
		if !orchestrator.SubmitInput(provider.PlainInput("work")) {
			t.Fatal("turn was not admitted")
		}
		request := <-orchestrator.inputCh
		orchestrator.endTurn(request.generation)
		if err := childCtx.Err(); err != nil {
			t.Fatalf("normal turn finalization cancelled child: %v", err)
		}
	})

	t.Run("explicit cancel owns child cancellation", func(t *testing.T) {
		orchestrator, childCtx := newFixture(t)
		if !orchestrator.SubmitInput(provider.PlainInput("work")) {
			t.Fatal("turn was not admitted")
		}
		if cancelled, _ := orchestrator.CancelTurn(); !cancelled {
			t.Fatal("explicit turn cancellation was rejected")
		}
		if err := childCtx.Err(); err != context.Canceled {
			t.Fatalf("explicit cancellation did not cancel child: %v", err)
		}
	})
}

func TestCancellingLaterTurnDoesNotKillEarlierAsyncChild(t *testing.T) {
	s := &Session{}
	runner := NewSubAgentRunner(s, 2)
	t.Cleanup(runner.SessionCancel)
	orchestrator := NewOrchestrator("root", s)
	s.subAgentSup = runner

	earlierCtx, cancelEarlier := context.WithCancel(context.Background())
	t.Cleanup(cancelEarlier)
	earlier := &agent.SubAgentHandle{
		Spec:   agent.SubAgentSpec{ID: "earlier", Name: "earlier", Category: agent.CatResearcher, ParentTurn: 1},
		Agent:  &agent.Agent{Ctx: earlierCtx},
		Cancel: cancelEarlier,
	}
	if err := runner.Manager().Register(earlier); err != nil {
		t.Fatal(err)
	}
	if !orchestrator.SubmitInput(provider.PlainInput("turn one")) {
		t.Fatal("turn one was not admitted")
	}
	first := <-orchestrator.inputCh
	orchestrator.endTurn(first.generation)

	currentCtx, cancelCurrent := context.WithCancel(context.Background())
	current := &agent.SubAgentHandle{
		Spec:   agent.SubAgentSpec{ID: "current", Name: "current", Category: agent.CatResearcher, ParentTurn: 2},
		Agent:  &agent.Agent{Ctx: currentCtx},
		Cancel: cancelCurrent,
	}
	if err := runner.Manager().Register(current); err != nil {
		t.Fatal(err)
	}
	if !orchestrator.SubmitInput(provider.PlainInput("turn two")) {
		t.Fatal("turn two was not admitted")
	}
	if cancelled, _ := orchestrator.CancelTurn(); !cancelled {
		t.Fatal("turn two cancellation was rejected")
	}
	if earlierCtx.Err() != nil {
		t.Fatalf("turn two cancellation killed turn one child: %v", earlierCtx.Err())
	}
	if currentCtx.Err() != context.Canceled {
		t.Fatalf("turn two child was not cancelled: %v", currentCtx.Err())
	}
}

func TestOrchestratorBlockResponsesAreRequestCorrelated(t *testing.T) {
	orchestrator := NewOrchestrator("root", nil)
	response := ttypes.BlockResponse{RequestID: "block-2", Outcome: ttypes.BlockAnswered, Value: "allow"}
	if !orchestrator.ResolveBlock(response) {
		t.Fatal("owned block response was rejected")
	}
	if got := <-orchestrator.BlockResolveCh; got != response {
		t.Fatalf("block response = %#v, want %#v", got, response)
	}
	if orchestrator.ResolveBlock(ttypes.BlockResponse{Outcome: ttypes.BlockAnswered, Value: "allow"}) {
		t.Fatal("response without request ID was accepted")
	}
}

func TestSessionRoutesBlockingResponseToOwningSubAgent(t *testing.T) {
	s := &Session{}
	runner := NewSubAgentRunner(s, 1)
	t.Cleanup(runner.SessionCancel)
	handle := &agent.SubAgentHandle{
		Spec:    agent.SubAgentSpec{ID: "child", Name: "child", Category: agent.CatResearcher},
		BlockCh: make(chan ttypes.BlockResponse, 1),
		Cancel:  func() {},
	}
	if err := runner.Manager().Register(handle); err != nil {
		t.Fatal(err)
	}
	s.subAgentSup = runner

	response := ttypes.BlockResponse{RequestID: "child-block", Outcome: ttypes.BlockAnswered, Value: "answer"}
	if !s.ResolveBlock("child", response) {
		t.Fatal("child blocking response was not routed")
	}
	if got := <-handle.BlockCh; got != response {
		t.Fatalf("child response = %#v, want %#v", got, response)
	}
}
