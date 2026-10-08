package workspace

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"crude/collaboration"
	"crude/provider"
	"crude/session"
	"crude/sessiondb"
	ttypes "crude/types"
	recoverytypes "crude/types/recovery"
)

func TestWorkspaceStopRetainsOriginalAndCompletesFollowingTurns(t *testing.T) {
	var calls atomic.Int32
	requests := make(chan []provider.ChatMessage, 4)
	releaseFirst := make(chan struct{})
	var releaseOnce sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []provider.ChatMessage `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		requests <- body.Messages
		if calls.Add(1) == 1 {
			select {
			case <-releaseFirst:
			case <-r.Context().Done():
				return
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Your first message was hello how are you.\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	t.Cleanup(server.Close)
	t.Cleanup(func() { releaseOnce.Do(func() { close(releaseFirst) }) })
	home, project := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	ws := NewWorkspace(project, "history-model", "tester", ttypes.Settings{}, &testLogger{})
	t.Cleanup(ws.Close)
	ws.SetAppDefaults(ttypes.AppSettings{LocalHistory: ttypes.LocalHistorySettings{Enabled: "off"}, Memory: ttypes.MemorySettings{AutoExtract: "off", Consolidation: "off"}})
	cfg := historyE2EConfig(server.URL)
	cfg.Memory.AutoExtract, cfg.Memory.Consolidation = "off", "off"
	frontend, err := ws.CreateSession(cfg, home)
	if err != nil {
		t.Fatal(err)
	}
	sender := &historyE2ESender{events: make(chan historyE2EEvent, 1024)}
	root, err := ws.StartOrchestrator(sender)
	if err != nil {
		t.Fatal(err)
	}
	frontend.AgentID = root.ID
	ws.SessionsByAgent[root.ID] = frontend
	submit := func(id, text string) {
		t.Helper()
		intent, err := ws.PrepareAgentInputAdmission(root.ID, provider.UserInput{InternalID: id, Text: text}, 1)
		if err != nil {
			t.Fatal(err)
		}
		if result := ws.CommitAgentInputAdmission(t.Context(), intent); result.Status != InputAdmissionAccepted {
			t.Fatalf("composer admission failed: %+v", result)
		}
	}
	request := func() []provider.ChatMessage {
		t.Helper()
		select {
		case messages := <-requests:
			return messages
		case <-time.After(5 * time.Second):
			t.Fatal("provider request did not arrive")
			return nil
		}
	}
	settle := func(reason string) {
		t.Helper()
		deadline := time.After(5 * time.Second)
		for {
			select {
			case event := <-sender.events:
				if event.agentID != root.ID {
					continue
				}
				if event.typ == "error" {
					t.Fatalf("real workspace turn failed: %v", event.data)
				}
				if event.typ == "recovery" && event.data.(recoverytypes.Status).State == recoverytypes.NeedsAttention {
					t.Fatalf("Stop/follow-up stranded in recovery: %+v", event.data)
				}
				if event.typ == "done" {
					if event.data.(map[string]any)["reason"] != reason {
						t.Fatalf("terminal outcome=%v want=%s", event.data, reason)
					}
					for root.TurnActive() {
						select {
						case <-deadline:
							t.Fatal("turn did not release admission")
						case <-time.After(time.Millisecond):
						}
					}
					return
				}
			case <-deadline:
				t.Fatal("real workspace turn did not settle")
			}
		}
	}
	submit("workspace-first", "hello how are you")
	request()
	original, err := sessiondb.LoadMessagesWithMeta(frontend.backend.DB())
	if err != nil || len(original) != 1 {
		t.Fatalf("original submission was not committed: rows=%+v err=%v", original, err)
	}
	ws.CancelAgent(root.ID)
	settle("cancelled")
	releaseOnce.Do(func() { close(releaseFirst) })
	deadline := time.Now().Add(5 * time.Second)
	var control collaboration.Event
	for time.Now().Before(deadline) {
		page, err := ws.Collaboration.Page(t.Context(), collaboration.PageQuery{RecipientID: root.ID, Kind: collaboration.EventLifecycleControl})
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Events) == 1 {
			control = page.Events[0]
		}
		var count int
		if err := frontend.backend.DB().QueryRow(`SELECT COUNT(*) FROM collaboration_admissions WHERE event_id=?`, control.ID).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if control.ID != "" && count == 1 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if control.ID == "" {
		t.Fatal("workspace Stop failed to publish its immutable audit")
	}
	for turn, text := range []string{"what was the first message ?", "continue normally"} {
		submit(fmt.Sprintf("workspace-next-%d", turn), text)
		messages := request()
		originalCount, controlCount := 0, 0
		for _, message := range messages {
			if message.ContentString() == "hello how are you" {
				originalCount++
			}
			controlCount += strings.Count(message.ContentString(), `event_id="`+control.ID+`"`)
		}
		if originalCount != 1 || controlCount != 1 {
			t.Fatalf("workspace follow-up lost/duplicated first input or control: original=%d control=%d", originalCount, controlCount)
		}
		settle("completed")
		rows, err := sessiondb.LoadMessagesWithMeta(frontend.backend.DB())
		if err != nil || len(rows) != 4+2*turn || !reflect.DeepEqual(rows[0], original[0]) || rows[len(rows)-1].Msg.Role != "assistant" {
			t.Fatalf("workspace follow-up was not fully mirrored: rows=%+v err=%v", rows, err)
		}
		participant, err := ws.Collaboration.Agent(t.Context(), root.ID)
		if err != nil || participant.CurrentRunID != "" {
			t.Fatalf("follow-up did not settle its collaboration run: participant=%+v err=%v", participant, err)
		}
	}
	if calls.Load() != 3 {
		t.Fatalf("workspace Stop/follow-up replayed historical requests: %d", calls.Load())
	}
}

func TestWorkspacePeerReferencesPreserveThreeActiveRoots(t *testing.T) {
	home, project := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if err := os.WriteFile(filepath.Join(project, "evidence.txt"), []byte("retained evidence"), 0600); err != nil {
		t.Fatal(err)
	}
	started := make(chan int, 3)
	release := make(chan struct{})
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	var calls [3]atomic.Int32
	requests := make(chan struct {
		root     int
		messages []provider.ChatMessage
	}, 3)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []provider.ChatMessage `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		index := -1
		for i := 0; i < 3; i++ {
			for _, message := range body.Messages {
				if message.ContentString() == fmt.Sprintf("original-%d", i) {
					index = i
				}
			}
		}
		if index < 0 {
			t.Error("request lost its owning input")
			return
		}
		attempt := calls[index].Add(1)
		if attempt == 1 {
			started <- index
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if attempt == 1 {
			_, _ = fmt.Fprint(w, `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"read-evidence","type":"function","function":{"name":"read_file","arguments":"{\"path\":\"evidence.txt\"}"}}]},"finish_reason":"tool_calls"}]}`+"\n\ndata: [DONE]\n\n")
			return
		}
		requests <- struct {
			root     int
			messages []provider.ChatMessage
		}{index, body.Messages}
		_, _ = fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":%q},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n", fmt.Sprintf("completed root %d", index))
	}))
	t.Cleanup(server.Close)
	ws := NewWorkspace(project, "history-model", "tester", ttypes.Settings{}, &testLogger{})
	t.Cleanup(ws.Close)
	ws.SetAppDefaults(ttypes.AppSettings{LocalHistory: ttypes.LocalHistorySettings{Enabled: "off"}})
	var roots [3]*session.Orchestrator
	var backends [3]*session.Session
	sender := &historyE2ESender{events: make(chan historyE2EEvent, 2048)}
	for i := range roots {
		cfg := historyE2EConfig(server.URL)
		cfg.Memory.AutoExtract, cfg.Memory.Consolidation = "off", "off"
		frontend, err := ws.CreateSession(cfg, home)
		if err != nil {
			t.Fatal(err)
		}
		root, err := ws.StartOrchestrator(sender)
		if err != nil {
			t.Fatal(err)
		}
		roots[i], backends[i] = root, frontend.backend
		frontend.AgentID = root.ID
		ws.SessionsByAgent[root.ID] = frontend
		frontend.backend.SetYolo(true)
		intent, err := ws.PrepareAgentInputAdmission(root.ID, provider.UserInput{InternalID: fmt.Sprintf("input-%d", i), Text: fmt.Sprintf("original-%d", i)}, 1)
		if err != nil {
			t.Fatal(err)
		}
		if result := ws.CommitAgentInputAdmission(t.Context(), intent); result.Status != InputAdmissionAccepted {
			t.Fatalf("root %d admission: %+v", i, result)
		}
	}
	for range roots {
		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Fatal("three roots did not begin")
		}
	}
	var eventIDs [3]string
	for i, root := range roots {
		published, err := ws.Collaboration.Publish(t.Context(), collaboration.PublishRequest{
			SenderID: roots[(i+1)%3].ID, RecipientIDs: []string{root.ID}, Origin: collaboration.OriginPeer,
			Kind: collaboration.EventPeerNote, Body: fmt.Sprintf("peer evidence for root %d", i), IdempotencyKey: fmt.Sprintf("evidence-%d", i),
			References: []collaboration.Reference{{Kind: "history_message", ID: fmt.Sprintf("input-%d", i)}},
		})
		if err != nil {
			t.Fatal(err)
		}
		eventIDs[i] = published.Event.ID
	}
	once.Do(func() { close(release) })
	for range roots {
		select {
		case request := <-requests:
			count := 0
			for _, message := range request.messages {
				count += strings.Count(message.ContentString(), `event_id="`+eventIDs[request.root]+`"`)
			}
			if count != 1 {
				t.Fatalf("root %d peer evidence count=%d", request.root, count)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("a referenced peer note blocked execution")
		}
	}
	finished := make(map[string]bool)
	deadline := time.After(5 * time.Second)
	for len(finished) != 3 {
		select {
		case event := <-sender.events:
			if event.typ == "recovery" && event.data.(recoverytypes.Status).State == recoverytypes.NeedsAttention {
				t.Fatalf("ordinary peer admission parked: %+v", event.data)
			}
			if event.typ == "done" {
				finished[event.agentID] = true
			}
		case <-deadline:
			t.Fatal("three roots could not settle")
		}
	}
	for i, backend := range backends {
		rows, err := sessiondb.LoadMessagesWithMeta(backend.DB())
		if err != nil || len(rows) < 2 || rows[0].Msg.InternalID != fmt.Sprintf("input-%d", i) || rows[0].Msg.ContentString() != fmt.Sprintf("original-%d", i) || calls[i].Load() != 2 {
			t.Fatalf("root %d lost input or replayed work: rows=%d requests=%d error=%v", i, len(rows), calls[i].Load(), err)
		}
	}
}
