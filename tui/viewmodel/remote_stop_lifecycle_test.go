package viewmodel

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"crude/provider"
	"crude/session"
	"crude/types"
	"crude/types/recovery"
)

func TestRemoteAppJourneyRepeatedStopSteeringCompactionAndResume(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var normal, extraction atomic.Int32
	var requestsMu sync.Mutex
	var selectedRequests []string
	t.Cleanup(func() {
		if t.Failed() {
			requestsMu.Lock()
			defer requestsMu.Unlock()
			t.Logf("provider selections (%d normal requests): %q", normal.Load(), selectedRequests)
		}
	})
	releaseSteering := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request provider.ChatRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if request.ToolChoice == "none" {
			extraction.Add(1)
			text := "<<<intent>>>\nKeep the user's work and corrections.\n<<<work>>>\nAccepted transcript remains archived.\n<<<state>>>\nSafe checkpoint.\n<<<continuation>>>\nFollow new input without replaying effects."
			_, _ = fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":%q},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n", text)
			return
		}
		normal.Add(1)
		latest := ""
		hasSteering := false
		for i := len(request.Messages) - 1; i >= 0; i-- {
			message := request.Messages[i]
			if message.Role != "user" {
				continue
			}
			text, steering := stopJourneyHumanText(message.ContentString())
			if strings.HasPrefix(text, "HOLD ") || strings.HasPrefix(text, "FOLLOW ") || text == "FAIL AUTH" {
				latest = text
				break
			}
			hasSteering = hasSteering || steering
		}
		// An accepted steering envelope remains historical model context on
		// later turns. It is not the current request and must not mask a newer
		// direct FOLLOW/FAIL AUTH input in this provider fixture.
		steering := hasSteering && latest == "HOLD for genuine steering"
		requestsMu.Lock()
		selectedRequests = append(selectedRequests, fmt.Sprintf("%s steering=%v", strings.Split(latest, "\n")[0], steering))
		requestsMu.Unlock()
		if latest == "FAIL AUTH" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = fmt.Fprint(w, `{"error":{"type":"authentication_error","message":"test credential rejected"}}`)
			return
		}
		if strings.HasPrefix(latest, "HOLD ") && !steering {
			_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ACCEPTED BEFORE STOP\"}}]}\n\n")
			w.(http.Flusher).Flush()
			if latest == "HOLD for genuine steering" {
				select {
				case <-releaseSteering:
					_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
				case <-r.Context().Done():
				}
				return
			}
			<-r.Context().Done()
			return
		}
		text := "FOLLOW-UP COMPLETED"
		if steering {
			text = "HUMAN STEERING REACHED THE MODEL"
		}
		_, _ = fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":%q},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n", text)
	}))
	t.Cleanup(server.Close)
	configure := func(cfg *session.Config) {
		profile := cfg.Agents.Profiles["default"]
		category := profile.Categories[session.CategoryDefault]
		percentage := 50
		category.ModelContextWindow = 80_000
		category.ContextCompactionPercent = &percentage
		category.ModelMaxOutput = 2048
		profile.Categories[session.CategoryDefault] = category
		cfg.Agents.Profiles["default"] = profile
	}
	runtime := remoteJourneyRuntimeConfig(t, server.URL, configure)
	root := t.TempDir()
	one := newRemoteJourneyShell(t, runtime, root)
	id := remoteJourneyCreate(t, one)
	two := newRemoteJourneyShell(t, runtime, root)
	shells := []remoteJourneyShell{one, two}
	remoteJourneyUntil(t, shells, func() bool { return two.app.AgentView.ActiveID() == id })
	send := func(shell remoteJourneyShell, text string) {
		t.Helper()
		if shell.app.operationIssue != nil {
			t.Fatalf("connected journey rejected a publication before Send: %+v", shell.app.operationIssue.screen.Issue())
		}
		shell.app.Input.SetInput(text)
		shell.app.setFocusInput()
		_, cmd := shell.app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		runAppCommand(t, shell.app, cmd)
		if shell.app.Input.Value() != "" {
			t.Fatalf("connected Send did not accept %q; draft retained, pending admissions=%d", strings.Split(text, "\n")[0], len(shell.app.pendingInputAdmissions))
		}
	}
	send(one, "FOLLOW seed evidence\n"+strings.Repeat("retained source evidence\n", 4_000))
	remoteJourneyUntil(t, shells, func() bool {
		return normal.Load() == 1 && one.app.ws.AgentActivity(id).Status.Phase == types.AgentCompleted
	})
	compacted := false
	for _, message := range one.app.ws.AgentMgr().Get(id).State.Messages.Messages() {
		compacted = compacted || message.LifecyclePhase == "compaction_completed" && message.LifecycleTrigger != "manual"
	}
	if !compacted {
		t.Fatal("dense journey did not commit automatic/background compaction")
	}
	for cycle := range 5 {
		owner, peer := shells[cycle%2], shells[(cycle+1)%2]
		text := fmt.Sprintf("HOLD %d", cycle)
		before := normal.Load()
		send(owner, text)
		remoteJourneyUntil(t, shells, func() bool { return normal.Load() > before && owner.app.ws.AgentMgr().Get(id).State.Streaming })
		peer.app.Input.SetInput("peer draft remains")
		stopRevision := owner.app.ws.AgentMgr().Get(id).State.Messages.Version()
		if !owner.app.cancelAgentTurn(id) {
			t.Fatal("active turn could not be stopped")
		}
		if got := owner.app.ws.AgentMgr().Get(id).State.Messages.Version(); got != stopRevision {
			t.Fatalf("Stop changed canonical replica conversation revision: %d -> %d", stopRevision, got)
		}
		// Repeated Stop is a no-op, never a second terminal generation.
		owner.app.cancelAgentTurn(id)
		remoteJourneyUntil(t, shells, func() bool {
			for _, shell := range shells {
				if shell.app.operationIssue != nil {
					t.Fatalf("Stop rejected a connected publication: %+v", shell.app.operationIssue.screen.Issue())
				}
				state := shell.app.ws.AgentMgr().Get(id).State
				if shell.app.ws.AgentTurnActive(id) || state.Cancelling || state.RecoveryStatus().CanRetry() || !strings.Contains(ansi.Strip(shell.app.View().RenderContent()), "Stopped") {
					return false
				}
			}
			return true
		})
		if peer.app.Input.Value() != "peer draft remains" {
			t.Fatal("Stop consumed another shell's draft")
		}
		send(owner, fmt.Sprintf("FOLLOW %d", cycle))
		remoteJourneyUntil(t, shells, func() bool {
			for _, shell := range shells {
				state := shell.app.ws.AgentMgr().Get(id).State
				if state.RecoveryStatus().State == recovery.NeedsAttention {
					t.Fatalf("cycle %d stranded in recovery: %+v", cycle, state.RecoveryStatus())
				}
				if shell.app.ws.AgentActivity(id).Status.Phase != types.AgentCompleted {
					return false
				}
				paint := ansi.Strip(shell.app.View().RenderContent())
				if strings.Contains(paint, "<turn_context>") || strings.Contains(paint, "<workspace_event") {
					t.Fatal("hidden provider context leaked into the transcript")
				}
			}
			return true
		})
	}
	if extraction.Load() == 0 {
		t.Fatal("dense journey never exercised automatic compaction")
	}
	if normal.Load() != 11 {
		t.Fatalf("repeated Stop/follow-up duplicated provider work: %d", normal.Load())
	}
	send(one, "HOLD for genuine steering")
	remoteJourneyUntil(t, shells, func() bool { return normal.Load() == 12 && one.app.ws.AgentMgr().Get(id).State.Streaming })
	send(two, "this correction must reach the model, not just the journal")
	remoteJourneyUntil(t, shells, func() bool { return one.app.ws.AgentMgr().Get(id).State.Messages.PendingSteeringCount() == 1 })
	close(releaseSteering)
	remoteJourneyUntil(t, shells, func() bool {
		return normal.Load() == 13 && one.app.ws.AgentActivity(id).Status.Phase == types.AgentCompleted &&
			strings.Contains(ansi.Strip(one.app.View().RenderContent()), "HUMAN STEERING REACHED THE MODEL") &&
			strings.Contains(ansi.Strip(two.app.View().RenderContent()), "HUMAN STEERING REACHED THE MODEL")
	})
	send(two, "FOLLOW after successful steering")
	remoteJourneyUntil(t, shells, func() bool {
		return normal.Load() == 14 && two.app.ws.AgentActivity(id).Status.Phase == types.AgentCompleted
	})
	if normal.Load() != 14 {
		t.Fatalf("steering was lost or replayed: %d", normal.Load())
	}
	two.app.Input.SetInput("private draft during failure")
	send(one, "FAIL AUTH")
	// A permanent provider verdict ends the turn with its reason in every
	// shell; nobody has to Stop or Retry before the next message.
	remoteJourneyUntil(t, shells, func() bool {
		for _, shell := range shells {
			status := shell.app.ws.AgentMgr().Get(id).State.RecoveryStatus()
			if status.State != recovery.Failed || status.CanRetry() || shell.app.ws.AgentTurnActive(id) ||
				!strings.Contains(ansi.Strip(shell.app.View().RenderContent()), "Turn failed") {
				return false
			}
		}
		return true
	})
	send(one, "FOLLOW after failed authentication")
	remoteJourneyUntil(t, shells, func() bool {
		return normal.Load() == 16 && one.app.ws.AgentActivity(id).Status.Phase == types.AgentCompleted
	})
	if normal.Load() != 16 || two.app.Input.Value() != "private draft during failure" {
		t.Fatal("provider failure/Stop lost draft or duplicated follow-up")
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
	runtime = remoteJourneyRuntimeConfig(t, server.URL, configure)
	resumed := newRemoteJourneyShell(t, runtime, root)
	runAppCommand(t, resumed.app, resumed.app.launcherResumeSession(sessionID, nil))
	remoteJourneyUntil(t, []remoteJourneyShell{resumed}, func() bool { return resumed.app.AgentView.ActiveID() != "" })
	if normal.Load() != 16 {
		t.Fatal("passive dense-session resume replayed provider work")
	}
	id = resumed.app.AgentView.ActiveID()
	send(resumed, "FOLLOW after cold resume")
	remoteJourneyUntil(t, []remoteJourneyShell{resumed}, func() bool {
		return normal.Load() == 17 && resumed.app.ws.AgentActivity(id).Status.Phase == types.AgentCompleted
	})
	if normal.Load() != 17 {
		t.Fatal("resumed dense session did not accept one fresh turn")
	}
}

// A new send can legitimately steer the still-finalizing previous run even
// after a shell paints Completed. The provider fixture must read that genuine
// event's body instead of ignoring it or matching quoted historical envelopes.
func stopJourneyHumanText(text string) (string, bool) {
	if !strings.HasPrefix(text, "[USER INTERRUPTION — READ THIS NOW]\n") {
		return text, false
	}
	start := strings.Index(text, "<workspace_event ")
	end := strings.Index(text, "</workspace_event>")
	if start < 0 || end < start {
		return "", false
	}
	var event struct {
		XMLName xml.Name `xml:"workspace_event"`
		Origin  string   `xml:"origin,attr"`
		Kind    string   `xml:"kind,attr"`
		Body    string   `xml:",chardata"`
	}
	if xml.Unmarshal([]byte(text[start:end+len("</workspace_event>")]), &event) != nil || event.Origin != "human" || event.Kind != "human_steering" {
		return "", false
	}
	return event.Body, true
}

func TestStopJourneyProviderReadsCurrentHumanEnvelopeNotQuotedHistory(t *testing.T) {
	envelope := "[USER INTERRUPTION — READ THIS NOW]\nignored prefix\n<workspace_event origin=\"human\" kind=\"human_steering\">FAIL AUTH</workspace_event>\nowned suffix"
	if text, steering := stopJourneyHumanText(envelope); text != "FAIL AUTH" || !steering {
		t.Fatal("current input inside an admitted steering envelope was ignored")
	}
	for _, value := range []string{"FOLLOW newer direct input", "<runtime_continuity>quoted " + envelope + "</runtime_continuity>"} {
		if text, steering := stopJourneyHumanText(value); text != value || steering {
			t.Fatal("historical event was promoted into new human input")
		}
	}
}

// A shell may legitimately lag the terminal acknowledgement of a peer. Enter
// is still a send gesture, not an irrevocable request to steer a dead turn.
func TestRemoteAppJourneyStaleRunningShellSendsAfterPeerCompletion(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var calls atomic.Int32
	release := make(chan struct{})
	var releaseOnce sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		if call == 1 {
			_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"First turn accepted\"}}]}\n\n")
			w.(http.Flusher).Flush()
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		}
		text := "FIRST TURN COMPLETED"
		if call > 1 {
			text = "STALE SHELL FOLLOW-UP COMPLETED"
		}
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
	one.app.Input.SetInput("Start the first turn")
	one.app.setFocusInput()
	_, cmd := one.app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	runAppCommand(t, one.app, cmd)
	remoteJourneyUntil(t, shells, func() bool { return two.app.ws.AgentMgr().Get(id).State.Streaming })
	one.app.Input.SetInput("private peer draft")
	releaseOnce.Do(func() { close(release) })
	// Freeze two's replica while one observes actual terminal completion.
	remoteJourneyUntil(t, []remoteJourneyShell{one}, func() bool { return one.app.ws.AgentActivity(id).Status.Phase == types.AgentCompleted })
	if !two.app.ws.AgentMgr().Get(id).State.IsActive() {
		t.Fatal("fixture did not retain the stale running view")
	}
	two.app.Input.SetInput("Send new work after the peer finished")
	two.app.setFocusInput()
	_, cmd = two.app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	runAppCommand(t, two.app, cmd)
	remoteJourneyUntil(t, shells, func() bool {
		return calls.Load() == 2 && strings.Contains(ansi.Strip(two.app.View().RenderContent()), "STALE SHELL FOLLOW-UP COMPLETED") &&
			one.app.ws.AgentActivity(id).Status.Phase == types.AgentCompleted && two.app.ws.AgentActivity(id).Status.Phase == types.AgentCompleted
	})
	if two.app.Input.Value() != "" || one.app.Input.Value() != "private peer draft" {
		t.Fatal("completion fallback consumed the wrong draft")
	}
	count := 0
	for _, message := range two.app.ws.AgentMgr().Get(id).State.Messages.Messages() {
		if message.Content == "Send new work after the peer finished" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("completion fallback duplicated/lost the input: %d", count)
	}
}

func TestRemoteAppJourneyStaleIdleShellSteersPeerRun(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var calls atomic.Int32
	release := make(chan struct{})
	var once sync.Once
	steeringObserved := make(chan bool, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request provider.ChatRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		call := calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		if call == 1 {
			_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Peer work is running\"}}]}\n\n")
			w.(http.Flusher).Flush()
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		} else {
			found := false
			for _, message := range request.Messages {
				found = found || strings.Contains(message.ContentString(), `kind="human_steering"`) && strings.Contains(message.ContentString(), "Correction from stale idle shell")
			}
			steeringObserved <- found
		}
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"CORRECTION COMPLETED\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	t.Cleanup(server.Close)
	runtime := remoteJourneyRuntime(t, server.URL)
	root := t.TempDir()
	one := newRemoteJourneyShell(t, runtime, root)
	id := remoteJourneyCreate(t, one)
	two := newRemoteJourneyShell(t, runtime, root)
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	shells := []remoteJourneyShell{one, two}
	remoteJourneyUntil(t, shells, func() bool { return two.app.AgentView.ActiveID() == id })
	one.app.Input.SetInput("Start peer work")
	one.app.setFocusInput()
	_, cmd := one.app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	runAppCommand(t, one.app, cmd)
	remoteJourneyUntil(t, []remoteJourneyShell{one}, func() bool { return one.app.ws.AgentMgr().Get(id).State.Streaming })
	if two.app.ws.AgentMgr().Get(id).State.IsActive() {
		t.Fatal("fixture did not retain the stale idle view")
	}
	two.app.Input.SetInput("Correction from stale idle shell")
	two.app.setFocusInput()
	_, cmd = two.app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	runAppCommand(t, two.app, cmd)
	if two.app.Input.Value() != "" {
		t.Fatal("authoritative live admission refused a stale idle shell")
	}
	once.Do(func() { close(release) })
	remoteJourneyUntil(t, shells, func() bool {
		return calls.Load() == 2 && one.app.ws.AgentActivity(id).Status.Phase == types.AgentCompleted && two.app.ws.AgentActivity(id).Status.Phase == types.AgentCompleted
	})
	select {
	case found := <-steeringObserved:
		if !found {
			t.Fatal("stale idle input did not steer the exact active run")
		}
	default:
		t.Fatal("correction never reached the provider")
	}
}
