package session

import (
	"context"
	"fmt"
	"strings"
	"sync"
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

func TestCancelAndBtwConsumptionHaveSingleOwner(t *testing.T) {
	orchestrator := NewOrchestrator("agent", nil)
	for i := 0; i < 250; i++ {
		if !orchestrator.SubmitInput(provider.PlainInput("turn")) {
			t.Fatalf("turn %d was not admitted", i)
		}
		id := fmt.Sprintf("btw-%d", i)
		input := provider.PlainInput(id)
		input.InternalID = id
		if !orchestrator.SubmitBtw(input) {
			t.Fatalf("BTW %d was not admitted", i)
		}

		start := make(chan struct{})
		var wg sync.WaitGroup
		var consumed bool
		var cleared []string
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			consumed = orchestrator.consumeBtw(id)
		}()
		go func() {
			defer wg.Done()
			<-start
			_, cleared = orchestrator.CancelTurn()
		}()
		close(start)
		wg.Wait()

		wasCleared := len(cleared) == 1 && cleared[0] == id
		if consumed == wasCleared {
			t.Fatalf("BTW %d ownership consumed=%v cleared=%v", i, consumed, cleared)
		}
		request := <-orchestrator.inputCh
		orchestrator.endTurn(request.generation)
	}
}

func TestCancelReturnsOnlyUnconsumedBtwIDs(t *testing.T) {
	orchestrator := NewOrchestrator("agent", nil)
	if !orchestrator.SubmitInput(provider.PlainInput("turn")) {
		t.Fatal("turn was not admitted")
	}
	for _, id := range []string{"consumed", "pending"} {
		input := provider.PlainInput(id)
		input.InternalID = id
		if !orchestrator.SubmitBtw(input) {
			t.Fatalf("BTW %q was not admitted", id)
		}
	}
	if !orchestrator.consumeBtw("consumed") {
		t.Fatal("consumed BTW was not owned by the turn")
	}
	cancelled, cleared := orchestrator.CancelTurn()
	if !cancelled || len(cleared) != 1 || cleared[0] != "pending" {
		t.Fatalf("cancelled=%v cleared=%v", cancelled, cleared)
	}
}

func TestTurnFinalizationAndExplicitCancellationHaveDistinctChildOwnership(t *testing.T) {
	newFixture := func(t *testing.T) (*Orchestrator, context.Context) {
		t.Helper()
		s := &Session{consumedReports: make(map[string]bool)}
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
		orchestrator.subAgentSup = runner
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
	s := &Session{consumedReports: make(map[string]bool)}
	runner := NewSubAgentRunner(s, 2)
	t.Cleanup(runner.SessionCancel)
	orchestrator := NewOrchestrator("root", s)
	orchestrator.subAgentSup = runner

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

func TestSubAgentWakeBatchPreservesTerminalStatus(t *testing.T) {
	text := formatSubAgentWakeBatch([]WakeEvent{{
		Type: WakeSubAgentDone,
		Data: agent.SubAgentResult{
			AgentID: "child", Name: "investigator", Status: agent.SubAgentCancelled,
			SummaryGist: "partial work", ResultPath: "/tmp/report.md",
		},
		SourceID: "child",
	}})
	if !strings.Contains(text, "CANCELLED") || strings.Contains(text, "'child' completed") {
		t.Fatalf("wake text misreported terminal status: %q", text)
	}
}
