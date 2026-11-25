package viewmodel

import (
	"strings"
	"sync/atomic"
	"testing"

	"crude/core/shellport"
	"crude/internal/providersim"
)

// Exercise the complete product path: composer admission binds its shell, the
// actual root model calls a native control tool, core policy routes that exact
// shell, Update applies the effect and the following model request sees outcome.
func TestConnectedRootAgentControlsOnlySubmittingShell(t *testing.T) {
	var calls atomic.Int32
	requests := make(chan providersim.Request, 4)
	endpoint := providersim.New(t)
	endpoint.Respond(func(request providersim.Request) providersim.Turn {
		requests <- request
		if calls.Add(1) == 1 {
			return simulatedCalls(providersim.ToolCall{ID: "hide-tree", Name: "crude_control", Arguments: `{"command":"tree.set_visible","arguments":{"visible":false}}`})
		}
		return simulatedAnswer("Tree hidden in this shell.")
	})
	runtime := remoteJourneyRuntime(t, endpoint.BaseURL(providersim.Chat))
	root := t.TempDir()
	one := newRemoteJourneyShell(t, runtime, root)
	id := remoteJourneyCreate(t, one)
	two := newRemoteJourneyShell(t, runtime, root)
	shells := []remoteJourneyShell{one, two}
	remoteJourneyUntil(t, shells, func() bool { return two.app.ws.AgentMgr().Get(id) != nil })
	one.app.ws.SetYoloForAgent(id, true)
	remoteJourneyUntil(t, shells, func() bool { return one.app.ws.YoloForAgent(id) })
	one.app.ws.(shellport.ShellControl).StartShellControl()
	two.app.ws.(shellport.ShellControl).StartShellControl()
	one.app.Input.SetInput("hide the tree in this shell")
	command := one.app.submitInput(id, one.app.Input.Value(), nil)
	if command == nil {
		t.Fatal("composer did not submit input")
	}
	runAppCommand(t, one.app, command)
	remoteJourneyUntil(t, shells, func() bool { return calls.Load() == 2 && !one.app.Tree.Visible })
	if !two.app.Tree.Visible {
		t.Fatal("agent changed the neighboring shell")
	}
	first := <-requests
	advertised := false
	for _, definition := range first.Tools {
		if definition.Name == "crude_control" {
			advertised = true
		}
	}
	if !advertised {
		t.Fatal("root tool schema not offered through real model request")
	}
	second := <-requests
	evidence := false
	for _, message := range second.Messages {
		if message.Role == "tool" && strings.Contains(simulatedText(message.Parts), `"state":"applied"`) {
			evidence = true
		}
	}
	if !evidence {
		t.Fatal("model did not receive an applied exact-shell result")
	}
	if one.app.computeLayout().SidebarW >= two.app.computeLayout().SidebarW {
		t.Fatal("tree visibility did not reach presented geometry")
	}
}
