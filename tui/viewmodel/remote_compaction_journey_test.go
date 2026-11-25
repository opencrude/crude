package viewmodel

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"crude/agent/provider"
	"crude/agent/session"
	"crude/core"
	"crude/internal/providersim"
	"crude/types/statuscenter"
	"crude/workspace"
)

// Exercise the actual completed-turn -> /compact -> Enter path. A fresh reducer
// has no terminal fence, so injecting compaction into an unused agent misses the
// failure that leaves real connected shells unable to queue their next prompt.
func TestRemoteAppJourneyCompactionQueuesFromBothShells(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	var normal, extraction atomic.Int32
	var mu sync.Mutex
	var prompts []string
	gate := providersim.NewGate()
	server := providersim.New(t)
	server.Respond(func(request providersim.Request) providersim.Turn {
		text := "INITIAL TURN COMPLETED"
		if string(request.ToolChoice) == `"none"` {
			extraction.Add(1)
			return simulatedAnswer("<<<intent>>>\nPreserve the accepted request.\n<<<work>>>\nEvidence retained.\n<<<state>>>\nClean.\n<<<continuation>>>\nContinue with the next human input.").WithGate(gate)
		} else {
			normal.Add(1)
			for i := len(request.Messages) - 1; i >= 0; i-- {
				message := request.Messages[i]
				if message.Role == "user" && !strings.Contains(simulatedText(message.Parts), "<") {
					mu.Lock()
					prompts = append(prompts, simulatedText(message.Parts))
					mu.Unlock()
					if strings.HasPrefix(simulatedText(message.Parts), "queued prompt") {
						text = "ANSWER TO " + simulatedText(message.Parts)
					}
					break
				}
			}
		}
		return simulatedAnswer(text)
	})
	t.Cleanup(gate.Release)
	runtime := remoteJourneyRuntime(t, server.BaseURL(providersim.Chat))
	root := t.TempDir()
	one := newRemoteJourneyShell(t, runtime, root)
	id := remoteJourneyCreate(t, one)
	two := newRemoteJourneyShell(t, runtime, root)
	// Release provider work before Runtime.Close joins admitted core jobs, even
	// when an assertion fails while extraction is deliberately held open.
	t.Cleanup(gate.Release)
	shells := []remoteJourneyShell{one, two}
	remoteJourneyUntil(t, shells, func() bool { return two.app.AgentView.ActiveID() == id })
	send := func(shell remoteJourneyShell, text string) {
		t.Helper()
		shell.app.Input.SetInput(text)
		shell.app.setFocusInput()
		_, cmd := shell.app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		runAppCommand(t, shell.app, cmd)
	}
	send(one, "Keep the complete evidence.\n"+strings.Repeat("source evidence\n", 4_000))
	remoteJourneyUntil(t, shells, func() bool {
		return one.app.ws.AgentActivity(id).Status.Phase == statuscenter.AgentCompleted &&
			two.app.ws.AgentActivity(id).Status.Phase == statuscenter.AgentCompleted
	})
	one.app.Input.SetInput("/compact")
	_, compactCmd := one.app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if compactCmd == nil {
		t.Fatal("/compact did not schedule a command after the completed turn")
	}
	compactDone := make(chan tea.Msg, 1)
	go func() { compactDone <- compactCmd() }()
	remoteJourneyUntil(t, shells, func() bool { return extraction.Load() > 0 })
	remoteJourneyUntil(t, shells, func() bool {
		for _, shell := range shells {
			node := shell.app.ws.AgentMgr().Get(id)
			if node.State.ActiveCompaction.Phase == "" ||
				!strings.Contains(ansi.Strip(shell.app.View().RenderContent()), "Compacting context") {
				return false
			}
		}
		return true
	})
	for _, shell := range shells {
		requirePaintedCompactionTitle(t, shell.app, "Compacting context (Manual) ")
	}
	two.app.Input.SetInput("peer private draft")
	send(one, "queued prompt one")
	remoteJourneyUntil(t, shells, func() bool {
		return pendingJourneyPrompt(one.app, id, "queued prompt one") && pendingJourneyPrompt(two.app, id, "queued prompt one")
	})
	if one.app.Input.Value() != "" || two.app.Input.Value() != "peer private draft" {
		t.Fatal("queued acknowledgement did not consume only its originating draft")
	}
	send(two, "queued prompt two")
	remoteJourneyUntil(t, shells, func() bool { return pendingJourneyPrompt(one.app, id, "queued prompt two") })
	if two.app.Input.Value() != "" || normal.Load() != 1 {
		t.Fatalf("input ran before extraction finished: draft=%q requests=%d", two.app.Input.Value(), normal.Load())
	}
	one.app.Input.SetInput("new private draft one")
	two.app.Input.SetInput("new private draft two")
	gate.Release()
	select {
	case result := <-compactDone:
		_, cmd := one.app.Update(result)
		runAppCommand(t, one.app, cmd)
	case <-time.After(8 * time.Second):
		t.Fatal("compaction did not finish")
	}
	remoteJourneyUntil(t, shells, func() bool {
		for _, shell := range shells {
			if !strings.Contains(ansi.Strip(shell.app.View().RenderContent()), "ANSWER TO queued prompt two") ||
				shell.app.ws.AgentActivity(id).Status.Phase != statuscenter.AgentCompleted {
				return false
			}
		}
		return true
	})
	if extraction.Load() != 1 || normal.Load() != 3 {
		t.Fatalf("provider calls: extraction=%d normal=%d", extraction.Load(), normal.Load())
	}
	mu.Lock()
	defer mu.Unlock()
	if len(prompts) != 3 || prompts[1] != "queued prompt one" || prompts[2] != "queued prompt two" {
		t.Fatalf("queued execution order=%q", prompts)
	}
	for i, shell := range shells {
		requirePaintedCompactionTitle(t, shell.app, "Compacted context (Manual) ")
		if shell.app.Input.Value() != fmt.Sprintf("new private draft %s", []string{"one", "two"}[i]) {
			t.Fatal("completion replaced a newer private draft")
		}
		for _, text := range []string{"queued prompt one", "queued prompt two"} {
			count := 0
			answers := 0
			for _, message := range shell.app.ws.AgentMgr().Get(id).State.Messages.Messages() {
				if message.Role == workspace.MessageRoleAssistant && message.Content == "ANSWER TO "+text {
					answers++
				}
				if message.Role == workspace.MessageRoleUser && message.Content == text {
					count++
					if message.PendingSteering {
						t.Fatal("executed prompt is still pending")
					}
				}
			}
			if count != 1 || answers != 1 {
				t.Fatalf("prompt %q appears %d times with %d answers", text, count, answers)
			}
		}
	}
}

func TestRemoteAppJourneyStopCompactionPreservesQueuedPromptAndResumeIsPassive(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	var normal, extraction atomic.Int32
	gate := providersim.NewGate()
	server := providersim.New(t)
	server.Respond(func(request providersim.Request) providersim.Turn {
		text := "TURN COMPLETED"
		if string(request.ToolChoice) == `"none"` {
			extraction.Add(1)
			return simulatedAnswer("Complete retained source.").WithGate(gate)
		} else {
			normal.Add(1)
		}
		return simulatedAnswer(text)
	})
	runtime := remoteJourneyRuntime(t, server.BaseURL(providersim.Chat))
	root := t.TempDir()
	one := newRemoteJourneyShell(t, runtime, root)
	id := remoteJourneyCreate(t, one)
	two := newRemoteJourneyShell(t, runtime, root)
	t.Cleanup(gate.Release)
	shells := []remoteJourneyShell{one, two}
	remoteJourneyUntil(t, shells, func() bool { return two.app.AgentView.ActiveID() == id })
	one.app.Input.SetInput("Keep complete context.\n" + strings.Repeat("evidence\n", 7_000))
	_, cmd := one.app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	runAppCommand(t, one.app, cmd)
	remoteJourneyUntil(t, shells, func() bool { return one.app.ws.AgentActivity(id).Status.Phase == statuscenter.AgentCompleted })
	one.app.Input.SetInput("/compact")
	_, cmd = one.app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	compactDone := make(chan tea.Msg, 1)
	go func() { compactDone <- cmd() }()
	remoteJourneyUntil(t, shells, func() bool {
		return extraction.Load() != 0 && two.app.ws.AgentMgr().Get(id).State.ActiveCompaction.Phase != ""
	})
	two.app.Input.SetInput("queued prompt survives Stop")
	_, cmd = two.app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	runAppCommand(t, two.app, cmd)
	remoteJourneyUntil(t, shells, func() bool { return pendingJourneyPrompt(one.app, id, "queued prompt survives Stop") })
	two.app.Input.SetInput("newer private draft")
	one.app.cancelAgentTurn(id)
	select {
	case msg := <-compactDone:
		_, cmd = one.app.Update(msg)
		runAppCommand(t, one.app, cmd)
	case <-time.After(8 * time.Second):
		t.Fatal("Stop did not cancel the extraction request")
	}
	remoteJourneyUntil(t, shells, func() bool {
		return one.app.ws.AgentMgr().Get(id).State.ActiveCompaction.Phase == "" && two.app.ws.AgentMgr().Get(id).State.ActiveCompaction.Phase == ""
	})
	if normal.Load() != 1 || !pendingJourneyPrompt(two.app, id, "queued prompt survives Stop") || two.app.Input.Value() != "newer private draft" {
		t.Fatal("Stop started queued work or consumed accepted input/private draft")
	}
	liveRow := requirePaintedCompactionTitle(t, two.app, "Compaction cancelled (Manual) ")
	resumed := coldResumeCompactionJourney(t, runtime, shells, root, server.BaseURL(providersim.Chat))
	resumedID := resumed.app.AgentView.ActiveID()
	if !pendingJourneyPrompt(resumed.app, resumedID, "queued prompt survives Stop") || normal.Load() != 1 || extraction.Load() != 1 {
		t.Fatal("cold resume lost queued input or replayed provider work")
	}
	if resumedRow := requirePaintedCompactionTitle(t, resumed.app, "Compaction cancelled (Manual) "); resumedRow != liveRow {
		t.Fatalf("cold resume changed cancelled row: live=%q resumed=%q", liveRow, resumedRow)
	}
}

func coldResumeCompactionJourney(t *testing.T, runtime *core.Runtime, shells []remoteJourneyShell, root, endpoint string) remoteJourneyShell {
	t.Helper()
	sessionID := shells[0].app.ActiveSession.SessionID
	for _, shell := range shells {
		shell.app.StopAsyncIO()
		if err := shell.app.CloseWorkspaceAttachment(); err != nil {
			t.Fatal(err)
		}
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	resumed := newRemoteJourneyShell(t, remoteJourneyRuntime(t, endpoint), root)
	runAppCommand(t, resumed.app, resumed.app.launcherResumeSession(sessionID, nil))
	remoteJourneyUntil(t, []remoteJourneyShell{resumed}, func() bool { return resumed.app.AgentView.ActiveID() != "" })
	return resumed
}

// Read the terminal cell buffer, not the activity strip or a serialized
// transcript. Only the ordinary tool title has a rail and one-cell marker.
func paintedCompactionTitle(app *App, title string) string {
	for y := app.chatRect.Y; y < app.chatRect.Y+app.chatRect.H-1; y++ {
		before, after, found := strings.Cut(shellBufferRow(app, y), title)
		if !found {
			continue
		}
		prefix := strings.Fields(before)
		if len(prefix) >= 2 && prefix[len(prefix)-2] == "│" {
			marker := prefix[len(prefix)-1]
			if marker == "●" || marker == "○" || marker == "×" {
				return "│ " + marker + " " + strings.TrimRight(title+after, " │┃")
			}
		}
	}
	return ""
}

func requirePaintedCompactionTitle(t *testing.T, app *App, title string) string {
	t.Helper()
	paint := ansi.Strip(app.View().RenderContent())
	row := paintedCompactionTitle(app, title)
	if row == "" || strings.Contains(row, "requested with /compact") {
		t.Fatalf("want an ordinary tool title for %q, got %q:\n%s", title, row, paint)
	}
	return row
}

func pendingJourneyPrompt(app *App, id, text string) bool {
	for _, message := range app.ws.AgentMgr().Get(id).State.Messages.Messages() {
		if message.Role == workspace.MessageRoleUser && message.Content == text && message.PendingSteering {
			return strings.Contains(ansi.Strip(app.View().RenderContent()), text)
		}
	}
	return false
}

// The reported failure: /compact typed while the agent works was refused with
// a notice that was easy to miss. Now the shell shows that the request was
// accepted, and the compaction runs as soon as the turn allows it.
func TestRemoteAppJourneyCompactDuringActiveTurnIsAccepted(t *testing.T) {
	t.Run("after_final_step", func(t *testing.T) { runRemoteRequestedCompaction(t, false) })
	t.Run("between_steps", func(t *testing.T) { runRemoteRequestedCompaction(t, true) })
}

func runRemoteRequestedCompaction(t *testing.T, betweenSteps bool) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	var normal, extraction atomic.Int32
	held := make(chan struct{}, 1)
	gate := providersim.NewGate()
	compactGate := providersim.NewGate()
	server := providersim.New(t)
	server.Respond(func(request providersim.Request) providersim.Turn {
		if string(request.ToolChoice) == `"none"` {
			extraction.Add(1)
			return simulatedAnswer("<<<intent>>>\nKeep the request.\n<<<work>>>\nEvidence retained.\n<<<state>>>\nClean.\n<<<continuation>>>\nNone.").WithGate(compactGate)
		}
		if normal.Add(1) == 1 {
			held <- struct{}{}
			if betweenSteps {
				return providersim.CallTools(providersim.ToolCall{Name: "list_directory", Arguments: `{"path":"."}`}).WithGate(gate)
			}
			return simulatedAnswer("TURN FINISHED").WithGate(gate)
		}
		return simulatedAnswer("TURN FINISHED")
	})
	t.Cleanup(gate.Release)
	runtime := remoteJourneyRuntime(t, server.BaseURL(providersim.Chat))
	root := t.TempDir()
	shell := newRemoteJourneyShell(t, runtime, root)
	t.Cleanup(gate.Release)
	t.Cleanup(compactGate.Release)
	id := remoteJourneyCreate(t, shell)
	shells := []remoteJourneyShell{shell}
	shell.app.Input.SetInput("Keep the complete evidence.\n" + strings.Repeat("source evidence\n", 4_000))
	shell.app.setFocusInput()
	_, cmd := shell.app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	runAppCommand(t, shell.app, cmd)
	select {
	case <-held:
	case <-time.After(8 * time.Second):
		t.Fatal("the turn did not reach its model request")
	}
	remoteJourneyUntil(t, shells, func() bool { return shell.app.ws.AgentTurnActive(id) })

	shell.app.Input.SetInput("/compact")
	_, compactCmd := shell.app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if compactCmd == nil {
		t.Fatal("/compact during an active turn did not reach the session")
	}
	_, cmd = shell.app.Update(compactCmd())
	runAppCommand(t, shell.app, cmd)
	paint := ansi.Strip(shell.app.View().RenderContent())
	if !strings.Contains(paint, "Compaction will start after the current step.") || strings.Contains(paint, "Stop the active turn") {
		t.Fatalf("accepted /compact was not shown to the user:\n%s", paint)
	}
	if extraction.Load() != 0 {
		t.Fatal("compaction overlapped the model request in flight")
	}
	gate.Release()
	remoteJourneyUntil(t, shells, func() bool {
		return extraction.Load() == 1 && strings.Contains(ansi.Strip(shell.app.View().RenderContent()), "Compacting context")
	})
	requirePaintedCompactionTitle(t, shell.app, "Compacting context (Manual) ")
	compactGate.Release()
	wantPhase, wantTrigger, wantNormal := statuscenter.AgentCompactionResult, "manual", int32(1)
	if betweenSteps {
		wantPhase, wantTrigger, wantNormal = statuscenter.AgentCompleted, "requested", 2
	}
	remoteJourneyUntil(t, shells, func() bool {
		return extraction.Load() == 1 && shell.app.ws.AgentActivity(id).Status.Phase == wantPhase &&
			strings.Contains(ansi.Strip(shell.app.View().RenderContent()), "Compacted context")
	})
	liveRow := requirePaintedCompactionTitle(t, shell.app, "Compacted context (Manual) ")
	for _, message := range shell.app.ws.AgentMgr().Get(id).State.Messages.Messages() {
		if message.LifecyclePhase == "compaction_completed" && message.LifecycleTrigger == wantTrigger {
			wantTrigger = ""
			break
		}
	}
	if wantTrigger != "" {
		t.Fatalf("fixture never completed the %s compaction", wantTrigger)
	}
	for _, notice := range shell.app.ws.Status().Notifications(false) {
		if notice.Severity == statuscenter.SeverityError && strings.Contains(strings.ToLower(notice.Summary), "compact") {
			t.Fatalf("accepted compaction reported an error: %+v", notice)
		}
	}
	if normal.Load() != wantNormal {
		t.Fatalf("model requests=%d, want %d in the original turn", normal.Load(), wantNormal)
	}
	resumed := coldResumeCompactionJourney(t, runtime, shells, root, server.BaseURL(providersim.Chat))
	if resumedRow := requirePaintedCompactionTitle(t, resumed.app, "Compacted context (Manual) "); resumedRow != liveRow {
		t.Fatalf("cold resume changed requested row: live=%q resumed=%q", liveRow, resumedRow)
	}
	if normal.Load() != wantNormal || extraction.Load() != 1 {
		t.Fatal("cold resume replayed provider work")
	}
}

func TestRemoteAppJourneyAutomaticCompactionTitleSurvivesColdResume(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	var normal, extraction atomic.Int32
	server := providersim.New(t)
	server.Respond(func(request providersim.Request) providersim.Turn {
		if string(request.ToolChoice) == `"none"` {
			extraction.Add(1)
			return simulatedAnswer("<<<intent>>>\nKeep source evidence.\n<<<work>>>\nEvidence retained.\n<<<state>>>\nCheckpoint ready.\n<<<continuation>>>\nFinish the request.")
		}
		normal.Add(1)
		return simulatedAnswer("Evidence reviewed.")
	})
	endpoint := server.BaseURL(providersim.Chat)
	runtime := remoteJourneyRuntimeConfig(t, endpoint, func(cfg *session.Config) {
		profile := cfg.Agents.Profiles["default"]
		category := profile.Categories[session.CategoryDefault]
		percentage := 50
		category.ModelContextWindow, category.ModelMaxOutput = 80_000, 2_048
		category.ContextCompactionPercent = &percentage
		profile.Categories[session.CategoryDefault] = category
		cfg.Agents.Profiles["default"] = profile
	})
	root := t.TempDir()
	shell := newRemoteJourneyShell(t, runtime, root)
	id := remoteJourneyCreate(t, shell)
	shells := []remoteJourneyShell{shell}
	runAppCommand(t, shell.app, shell.app.submitPreparedInput(id, provider.UserInput{
		Text:        "Review the evidence.\n" + strings.Repeat("retained source evidence\n", 4_000),
		DisplayText: "Review the retained source evidence.",
	}, shell.app.Input.DraftRevision()))
	remoteJourneyUntil(t, shells, func() bool {
		return normal.Load() == 1 && shell.app.ws.AgentActivity(id).Status.Phase == statuscenter.AgentCompleted
	})
	liveRow := requirePaintedCompactionTitle(t, shell.app, "Compacted context (Auto) ")
	if extraction.Load() == 0 {
		t.Fatal("automatic compaction did not reach the provider")
	}
	extractions := extraction.Load()
	resumed := coldResumeCompactionJourney(t, runtime, shells, root, endpoint)
	if resumedRow := requirePaintedCompactionTitle(t, resumed.app, "Compacted context (Auto) "); resumedRow != liveRow {
		t.Fatalf("cold resume changed automatic row: live=%q resumed=%q", liveRow, resumedRow)
	}
	if normal.Load() != 1 || extraction.Load() != extractions {
		t.Fatal("cold resume replayed provider work")
	}
}
