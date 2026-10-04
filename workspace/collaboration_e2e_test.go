package workspace

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"crude/collaboration"
	"crude/provider"
	ttypes "crude/types"
)

func TestWorkspacePeerMessageWakesExactIdleAgentWithoutPolling(t *testing.T) {
	peerRequest := make(chan []provider.ChatMessage, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Messages []provider.ChatMessage `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode provider request: %v", err)
			return
		}
		peerRequest <- request.Messages
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"peer request handled\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()

	home, project := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	workspace := NewWorkspace(project, "model", "user", ttypes.Settings{}, &testLogger{})
	defer workspace.Close()
	workspace.SetAppDefaults(ttypes.AppSettings{LocalHistory: ttypes.LocalHistorySettings{Enabled: "off"}})
	cfg := historyE2EConfig(server.URL)

	first, err := workspace.CreateSession(cfg, home)
	if err != nil {
		t.Fatal(err)
	}
	firstSender := &historyE2ESender{events: make(chan historyE2EEvent, 1024)}
	firstRoot, err := workspace.StartOrchestrator(firstSender)
	if err != nil {
		t.Fatal(err)
	}
	first.AgentID = firstRoot.ID
	workspace.SessionsByAgent[firstRoot.ID] = first

	second, err := workspace.CreateSession(cfg, home)
	if err != nil {
		t.Fatal(err)
	}
	secondSender := &historyE2ESender{events: make(chan historyE2EEvent, 1024)}
	secondRoot, err := workspace.StartOrchestrator(secondSender)
	if err != nil {
		t.Fatal(err)
	}
	second.AgentID = secondRoot.ID
	workspace.SessionsByAgent[secondRoot.ID] = second

	published, err := workspace.Collaboration.Publish(t.Context(), collaboration.PublishRequest{
		SenderID: firstRoot.ID, IdempotencyKey: "peer-request-1",
		RecipientIDs: []string{secondRoot.ID}, Origin: collaboration.OriginPeer,
		Kind: collaboration.EventWorkRequest, Body: "Review the workspace parser.",
		RequestID: "request-parser", RequestRevision: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	waitHistoryAgentDone(t, secondSender, secondRoot.ID)

	select {
	case messages := <-peerRequest:
		found := false
		for _, message := range messages {
			if strings.Contains(message.ContentString(), `origin="peer"`) &&
				strings.Contains(message.ContentString(), published.Event.ID) &&
				strings.Contains(message.ContentString(), "Review the workspace parser.") {
				found = true
			}
		}
		if !found {
			t.Fatalf("provider request did not contain attributed peer event: %+v", messages)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("provider did not receive the automatic peer turn")
	}

	var peerPersisted bool
	for _, message := range second.backend.MessagesWithMeta() {
		if message.Msg.EffectiveOrigin() == provider.OriginPeer &&
			message.Msg.Collaboration != nil &&
			message.Msg.Collaboration.EventID == published.Event.ID {
			peerPersisted = true
		}
	}
	if !peerPersisted {
		t.Fatal("accepted peer event was not persisted with its attribution")
	}
	page, err := workspace.Collaboration.Page(t.Context(), collaboration.PageQuery{
		RecipientID: secondRoot.ID, Limit: 10,
	})
	if err != nil || len(page.Events) != 1 || page.Events[0].ID != published.Event.ID {
		t.Fatalf("workspace history = %+v, %v", page, err)
	}
}

func TestWorkspaceHumanSteeringTargetsExactActiveRun(t *testing.T) {
	var requests atomic.Int32
	firstStarted := make(chan struct{}, 1)
	releaseFirst := make(chan struct{})
	secondRequest := make(chan []provider.ChatMessage, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Messages []provider.ChatMessage `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode provider request: %v", err)
			return
		}
		if requests.Add(1) == 1 {
			firstStarted <- struct{}{}
			<-releaseFirst
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"initial response\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
			return
		}
		secondRequest <- request.Messages
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"steering applied\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()

	home, project := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	workspace := NewWorkspace(project, "model", "user", ttypes.Settings{}, &testLogger{})
	defer workspace.Close()
	workspace.SetAppDefaults(ttypes.AppSettings{LocalHistory: ttypes.LocalHistorySettings{Enabled: "off"}})
	frontend, err := workspace.CreateSession(historyE2EConfig(server.URL), home)
	if err != nil {
		t.Fatal(err)
	}
	sender := &historyE2ESender{events: make(chan historyE2EEvent, 1024)}
	root, err := workspace.StartOrchestrator(sender)
	if err != nil {
		t.Fatal(err)
	}
	frontend.AgentID = root.ID
	workspace.SessionsByAgent[root.ID] = frontend
	if !frontend.backend.SubmitInput(root.ID, provider.PlainInput("begin work")) {
		t.Fatal("initial turn was not admitted")
	}
	select {
	case <-firstStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("initial provider request did not start")
	}

	steering := provider.InputWithRefs("prioritize the parser edge case", nil)
	steering.InternalID = "history-steering-1"
	if !workspace.SteerAgent(root.ID, steering) {
		t.Fatal("human steering was not durably published to the active run")
	}
	page, err := workspace.Collaboration.Page(t.Context(), collaboration.PageQuery{
		RecipientID: root.ID, Kind: collaboration.EventHumanSteering, Limit: 10,
	})
	if err != nil || len(page.Events) != 1 || page.Events[0].RunID == "" {
		t.Fatalf("human steering journal = %+v, %v", page, err)
	}
	steeringEvent := page.Events[0]
	close(releaseFirst)
	waitHistoryAgentDone(t, sender, root.ID)

	var requestMessages []provider.ChatMessage
	select {
	case requestMessages = <-secondRequest:
	case <-time.After(2 * time.Second):
		t.Fatal("human steering did not trigger a safe-boundary continuation")
	}
	found := false
	for _, message := range requestMessages {
		content := message.ContentString()
		if strings.Contains(content, `origin="human"`) &&
			strings.Contains(content, `kind="human_steering"`) &&
			strings.Contains(content, steeringEvent.ID) &&
			strings.Contains(content, steeringEvent.RunID) &&
			strings.Contains(content, "prioritize the parser edge case") {
			found = true
		}
	}
	if !found {
		t.Fatalf("second request lacked attributed human steering: %+v", requestMessages)
	}

	persisted := false
	for _, message := range frontend.backend.MessagesWithMeta() {
		if message.Msg.InternalID == steering.InternalID &&
			message.Msg.EffectiveOrigin() == provider.OriginHumanSteering &&
			message.Msg.Collaboration != nil &&
			message.Msg.Collaboration.HistoryMessageID == steering.InternalID {
			persisted = true
		}
	}
	if !persisted {
		t.Fatal("admitted human steering was not retained in canonical session history")
	}
}
