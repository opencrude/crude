package viewmodel

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"crude/provider"
	"crude/types"
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
	release := make(chan struct{})
	var releaseOnce sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request provider.ChatRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		text := "INITIAL TURN COMPLETED"
		if request.ToolChoice == "none" {
			extraction.Add(1)
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
			text = "Preserve the accepted request and evidence; continue with the next human input."
		} else {
			normal.Add(1)
			for i := len(request.Messages) - 1; i >= 0; i-- {
				message := request.Messages[i]
				if message.Role == "user" && !strings.Contains(message.ContentString(), "<") {
					mu.Lock()
					prompts = append(prompts, message.ContentString())
					mu.Unlock()
					if strings.HasPrefix(message.ContentString(), "queued prompt") {
						text = "ANSWER TO " + message.ContentString()
					}
					break
				}
			}
		}
		encoded, _ := json.Marshal(text)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":%s},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n", encoded)
	}))
	t.Cleanup(server.Close)
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	runtime := remoteJourneyRuntime(t, server.URL)
	root := t.TempDir()
	one := newRemoteJourneyShell(t, runtime, root)
	id := remoteJourneyCreate(t, one)
	two := newRemoteJourneyShell(t, runtime, root)
	// Release provider work before Runtime.Close joins admitted core jobs, even
	// when an assertion fails while extraction is deliberately held open.
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
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
		return one.app.ws.AgentActivity(id).Status.Phase == types.AgentCompleted &&
			two.app.ws.AgentActivity(id).Status.Phase == types.AgentCompleted
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
				!strings.Contains(ansi.Strip(shell.app.View().Content), "Compacting context") {
				return false
			}
		}
		return true
	})
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
	releaseOnce.Do(func() { close(release) })
	select {
	case result := <-compactDone:
		_, cmd := one.app.Update(result)
		runAppCommand(t, one.app, cmd)
	case <-time.After(8 * time.Second):
		t.Fatal("compaction did not finish")
	}
	remoteJourneyUntil(t, shells, func() bool {
		for _, shell := range shells {
			if !strings.Contains(ansi.Strip(shell.app.View().Content), "ANSWER TO queued prompt two") ||
				shell.app.ws.AgentActivity(id).Status.Phase != types.AgentCompleted {
				return false
			}
		}
		return true
	})
	if extraction.Load() != 4 || normal.Load() != 3 {
		t.Fatalf("provider calls: extraction=%d normal=%d", extraction.Load(), normal.Load())
	}
	mu.Lock()
	defer mu.Unlock()
	if len(prompts) != 3 || prompts[1] != "queued prompt one" || prompts[2] != "queued prompt two" {
		t.Fatalf("queued execution order=%q", prompts)
	}
	for i, shell := range shells {
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
	release := make(chan struct{})
	var releaseOnce sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request provider.ChatRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		text := "TURN COMPLETED"
		if request.ToolChoice == "none" {
			extraction.Add(1)
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
			text = "Complete retained source."
		} else {
			normal.Add(1)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":%q},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n", text)
	}))
	t.Cleanup(server.Close)
	runtime := remoteJourneyRuntime(t, server.URL)
	root := t.TempDir()
	one := newRemoteJourneyShell(t, runtime, root)
	id := remoteJourneyCreate(t, one)
	two := newRemoteJourneyShell(t, runtime, root)
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	shells := []remoteJourneyShell{one, two}
	remoteJourneyUntil(t, shells, func() bool { return two.app.AgentView.ActiveID() == id })
	one.app.Input.SetInput("Keep complete context.\n" + strings.Repeat("evidence\n", 7_000))
	_, cmd := one.app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	runAppCommand(t, one.app, cmd)
	remoteJourneyUntil(t, shells, func() bool { return one.app.ws.AgentActivity(id).Status.Phase == types.AgentCompleted })
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
	sessionID := one.app.ActiveSession.SessionID
	for _, shell := range shells {
		shell.app.StopAsyncIO()
		if err := shell.app.CloseWorkspaceAttachment(); err != nil {
			t.Fatal(err)
		}
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	runtime = remoteJourneyRuntime(t, server.URL)
	resumed := newRemoteJourneyShell(t, runtime, root)
	runAppCommand(t, resumed.app, resumed.app.launcherResumeSession(sessionID, nil))
	remoteJourneyUntil(t, []remoteJourneyShell{resumed}, func() bool { return resumed.app.AgentView.ActiveID() != "" })
	resumedID := resumed.app.AgentView.ActiveID()
	if !pendingJourneyPrompt(resumed.app, resumedID, "queued prompt survives Stop") || normal.Load() != 1 || extraction.Load() != 1 {
		t.Fatal("cold resume lost queued input or replayed provider work")
	}
}

func pendingJourneyPrompt(app *App, id, text string) bool {
	for _, message := range app.ws.AgentMgr().Get(id).State.Messages.Messages() {
		if message.Role == workspace.MessageRoleUser && message.Content == text && message.PendingSteering {
			return strings.Contains(ansi.Strip(app.View().Content), text)
		}
	}
	return false
}
