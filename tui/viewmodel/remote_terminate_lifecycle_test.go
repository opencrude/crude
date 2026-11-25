package viewmodel

import (
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/internal/providersim"
)

// Terminating a running agent removes it from every connected shell through
// ordinary publications. No shell may reject a publication and fall back to a
// resynchronization error dialog.
func TestRemoteAppJourneyTerminateRunningAgentWithoutResync(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var requests atomic.Int32
	gate := providersim.NewGate()
	server := providersim.New(t)
	server.Respond(func(providersim.Request) providersim.Turn {
		requests.Add(1)
		return simulatedAnswer("WORKING").WithGateAfter(chatFirstTextEvents, gate)
	})
	runtime := remoteJourneyRuntimeConfig(t, server.BaseURL(providersim.Chat), nil)
	root := t.TempDir()
	one := newRemoteJourneyShell(t, runtime, root)
	id := remoteJourneyCreate(t, one)
	two := newRemoteJourneyShell(t, runtime, root)
	shells := []remoteJourneyShell{one, two}
	remoteJourneyUntil(t, shells, func() bool { return two.app.AgentView.ActiveID() == id })

	one.app.Input.SetInput("HOLD until terminated")
	one.app.setFocusInput()
	_, cmd := one.app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	runAppCommand(t, one.app, cmd)
	remoteJourneyUntil(t, shells, func() bool {
		node := one.app.ws.AgentMgr().Get(id)
		return requests.Load() == 1 && node != nil && node.State.Streaming
	})

	started := time.Now()
	runAppCommand(t, one.app, one.app.terminateAgent(id))
	noIssue := func() {
		for index, shell := range shells {
			if shell.app.operationIssue != nil {
				t.Fatalf("shell %d showed an error after Terminate: %+v", index, shell.app.operationIssue.screen.Issue())
			}
			for _, issue := range shell.app.pendingOperationIssues {
				t.Fatalf("shell %d queued an error after Terminate: %+v", index, issue)
			}
		}
	}
	remoteJourneyUntil(t, shells, func() bool {
		noIssue()
		for _, shell := range shells {
			if shell.app.ws.AgentMgr().Get(id) != nil {
				return false
			}
		}
		return true
	})
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("Terminate took %v to reach every shell", elapsed)
	}
	// Late cancellation events from the stopped run must not resurrect the
	// agent or desynchronize either replica.
	settle := time.Now().Add(500 * time.Millisecond)
	remoteJourneyUntil(t, shells, func() bool {
		noIssue()
		for index, shell := range shells {
			if shell.app.ws.AgentMgr().Get(id) != nil {
				t.Fatalf("shell %d shows the terminated agent again", index)
			}
			if strings.Contains(shell.app.View().RenderContent(), "resynchron") {
				t.Fatalf("shell %d mentions resynchronization", index)
			}
		}
		return time.Now().After(settle)
	})
}
