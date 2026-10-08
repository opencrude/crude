package session

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"crude/collaboration"
	"crude/provider"
	"crude/sessiondb"
	recoverytypes "crude/types/recovery"
)

func TestStoppedRootControlSurvivesFollowingTurnsAndResume(t *testing.T) {
	for _, timing := range []string{"before-stop", "after-stop", "during-next-turn"} {
		t.Run(timing, func(t *testing.T) {
			var calls atomic.Int32
			requests := make(chan map[string]any, 8)
			releaseFirst, releaseNext := make(chan struct{}), make(chan struct{})
			var firstOnce, nextOnce sync.Once
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Errorf("decode request: %v", err)
					return
				}
				attempt := calls.Add(1)
				requests <- body
				if attempt == 1 || attempt == 2 {
					release := releaseFirst
					if attempt == 2 {
						release = releaseNext
					}
					select {
					case <-release:
					case <-r.Context().Done():
						return
					}
				}
				w.Header().Set("Content-Type", "text/event-stream")
				if attempt == 2 {
					_, _ = fmt.Fprint(w, `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"following-turn-read","type":"function","function":{"name":"list_directory","arguments":"{\"path\":\".\"}"}}]},"finish_reason":"tool_calls"}]}`+"\n\ndata: [DONE]\n\n")
					return
				}
				_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"The first message was hello how are you.\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
			}))
			t.Cleanup(server.Close)
			t.Cleanup(func() {
				firstOnce.Do(func() { close(releaseFirst) })
				nextOnce.Do(func() { close(releaseNext) })
			})
			home, directory, project := t.TempDir(), t.TempDir(), t.TempDir()
			cfg := modelAttributionConfig(server.URL, "stopped-control-"+timing)
			s, err := NewWithSessionDir("stop-control", cfg, home, directory)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(s.Close)
			s.SetWorkDir(project)
			s.SetYolo(true)
			store, err := collaboration.Attach(s.db, project, filepath.Join(home, "locks"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			s.SetCollaboration(store, "stop-control-owner")
			sender := newMockEventSender()
			o, err := s.NewOrchestrator(sender)
			if err != nil {
				t.Fatal(err)
			}
			submit := func(input provider.UserInput) {
				t.Helper()
				intent, err := s.PrepareInputAdmission(o.ID, input, 1)
				if err != nil {
					t.Fatal(err)
				}
				if result := s.CommitInputAdmission(t.Context(), intent); result.Status != InputAdmissionAccepted {
					t.Fatalf("input was not accepted: %+v", result)
				}
			}
			request := func() map[string]any {
				t.Helper()
				select {
				case body := <-requests:
					return body
				case <-time.After(5 * time.Second):
					var events []agentEvent
					for len(sender.ch) > 0 {
						events = append(events, <-sender.ch)
					}
					t.Fatalf("provider request did not arrive; events=%+v", events)
					return nil
				}
			}
			first := provider.UserInput{InternalID: "first-original", Text: "hello how are you", DisplayText: "authored original display"}
			submit(first)
			request()
			runID, version := o.collaborationRunIdentity()
			if runID == "" || version == 0 {
				t.Fatal("first turn has no exact collaboration run")
			}
			originalRows, err := sessiondb.LoadMessagesWithMeta(s.db)
			if err != nil || len(originalRows) != 1 || originalRows[0].Msg.InternalID != first.InternalID {
				t.Fatalf("original input was not preserved before Stop: rows=%+v err=%v", originalRows, err)
			}
			publishStop := func() collaboration.Event {
				t.Helper()
				published, err := store.Publish(t.Context(), collaboration.PublishRequest{
					SenderID: "human", SenderName: "Human", Origin: collaboration.OriginHuman,
					Kind: collaboration.EventLifecycleControl, IdempotencyKey: "first-run-stop", RecipientIDs: []string{o.ID},
					RunID: runID, Body: "Cancel requested by the user.",
					References: []collaboration.Reference{{Kind: "control", ID: "cancel", Revision: int(version)}},
				})
				if err != nil {
					t.Fatal(err)
				}
				return published.Event
			}
			var control collaboration.Event
			if timing == "before-stop" {
				control = publishStop()
			} else if cancelled, _ := s.CancelOrchestrator(o.ID); !cancelled {
				t.Fatal("immediate Stop was rejected")
			}
			waitControlJourneyTurn(t, sender, o, "cancelled")
			firstOnce.Do(func() { close(releaseFirst) })
			if timing == "after-stop" {
				control = publishStop()
				waitControlJourneyAdmission(t, sender, o.ID, control.ID)
			}
			submit(provider.UserInput{InternalID: "second-original", Text: "what was the first message ?"})
			secondRequest := request()
			if timing == "during-next-turn" {
				control = publishStop()
				waitControlJourneyAdmission(t, sender, o.ID, control.ID)
			}
			admitted, found, err := sessiondb.LoadContextState(s.db, s.sessionID, s.sessionID)
			if err != nil || !found {
				t.Fatalf("load admitted control: found=%v err=%v", found, err)
			}
			var originalControl provider.ChatMessage
			for _, message := range admitted.Messages {
				if message.InternalID == control.ID {
					originalControl = message
				}
			}
			if originalControl.Collaboration == nil || originalControl.Collaboration.EventID != control.ID || originalControl.Collaboration.RunID != runID {
				t.Fatal("Stop audit lost its exact event/run attribution")
			}
			assertRequest := func(body map[string]any, wantControl int) {
				t.Helper()
				messages := body["messages"].([]any)
				firstCount, controlCount := 0, 0
				for _, raw := range messages {
					message := raw.(map[string]any)
					content, _ := message["content"].(string)
					if content == first.Text {
						firstCount++
					}
					controlCount += strings.Count(content, `event_id="`+control.ID+`"`)
				}
				if firstCount != 1 || controlCount != wantControl {
					t.Fatalf("wire lost or duplicated original/control: original=%d control=%d wantControl=%d", firstCount, controlCount, wantControl)
				}
			}
			wantControl := 1
			if timing == "during-next-turn" {
				wantControl = 0
			}
			assertRequest(secondRequest, wantControl)
			nextOnce.Do(func() { close(releaseNext) })
			assertRequest(request(), wantControl)
			waitControlJourneyTurn(t, sender, o, "completed")
			assertDurable := func() []sessiondb.MessageWithMeta {
				t.Helper()
				state, found, err := sessiondb.LoadContextState(s.db, s.sessionID, s.sessionID)
				if err != nil || !found {
					t.Fatalf("current context unavailable: found=%v err=%v", found, err)
				}
				seen := make(map[string]bool)
				controlCount := 0
				for _, message := range state.Messages {
					if message.InternalID == "" || seen[message.InternalID] {
						t.Fatalf("current context has missing/duplicate identity %q", message.InternalID)
					}
					seen[message.InternalID] = true
					if message.InternalID == control.ID {
						controlCount++
						if !reflect.DeepEqual(message, originalControl) {
							t.Fatal("following turn rewrote immutable Stop bytes")
						}
					}
				}
				if controlCount != 1 {
					t.Fatalf("current context contains %d Stop messages", controlCount)
				}
				rows, err := sessiondb.LoadMessagesWithMeta(s.db)
				if err != nil || len(rows) < 4 || !reflect.DeepEqual(rows[0], originalRows[0]) {
					t.Fatalf("following turn erased/restamped original input: rows=%+v err=%v", rows, err)
				}
				if _, open, err := sessiondb.LoadRecoveryCheckpoint(s.db, s.sessionID, o.ID); err != nil || open {
					t.Fatalf("successful following turn left unfinished recovery: open=%v err=%v", open, err)
				}
				return rows
			}
			assertDurable()
			submit(provider.UserInput{InternalID: "third-original", Text: "continue without repeating old work"})
			assertRequest(request(), 1)
			waitControlJourneyTurn(t, sender, o, "completed")
			beforeResume := assertDurable()
			if calls.Load() != 4 {
				t.Fatalf("Stop/follow-up replayed provider work: requests=%d want=4", calls.Load())
			}
			path := s.dbPath
			s.Close()
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			s, err = ResumeSessionWithDir(path, cfg, home, directory)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(s.Close)
			s.SetWorkDir(project)
			store, err = collaboration.Attach(s.db, project, filepath.Join(home, "locks"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			s.SetCollaboration(store, "resumed-stop-control-owner")
			sender = newMockEventSender()
			o, err = s.NewOrchestrator(sender)
			if err != nil {
				t.Fatal(err)
			}
			if afterResume := assertDurable(); !reflect.DeepEqual(afterResume, beforeResume) || calls.Load() != 4 {
				t.Fatal("passive resume rewrote history or replayed execution")
			}
			submit(provider.UserInput{InternalID: "resumed-original", Text: "new explicit turn after resume"})
			assertRequest(request(), 1)
			waitControlJourneyTurn(t, sender, o, "completed")
			assertDurable()
			if calls.Load() != 5 {
				t.Fatalf("resumed turn replayed historical requests: %d", calls.Load())
			}
		})
	}
}

func waitControlJourneyTurn(t *testing.T, sender *mockEventSender, o *Orchestrator, reason string) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case event := <-sender.ch:
			if event.agentID != o.ID {
				continue
			}
			if event.typ == "error" {
				t.Fatalf("turn failed: %v", event.data)
			}
			if event.typ == "recovery" {
				status := event.data.(recoverytypes.Status)
				if status.State == recoverytypes.NeedsAttention {
					t.Fatalf("ordinary following turn required recovery: %+v", status)
				}
			}
			if event.typ == "done" {
				data := event.data.(map[string]any)
				if data["reason"] != reason {
					t.Fatalf("terminal outcome=%v want=%s", data, reason)
				}
				for o.TurnActive() {
					select {
					case <-deadline:
						t.Fatal("terminal turn did not release admission")
					case <-time.After(time.Millisecond):
					}
				}
				return
			}
		case <-deadline:
			t.Fatal("turn did not settle")
		}
	}
}

func waitControlJourneyAdmission(t *testing.T, sender *mockEventSender, agentID, eventID string) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case event := <-sender.ch:
			if event.agentID == agentID && event.typ == "collaboration_message" && event.data.(collaboration.Event).ID == eventID {
				return
			}
		case <-deadline:
			t.Fatal("late Stop audit was not admitted")
		}
	}
}
