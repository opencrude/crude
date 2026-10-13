package session

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"crude/collaboration"
	"crude/provider"
	"crude/sessiondb"
)

func TestFaultyImageDoesNotStrandHumanInput(t *testing.T) {
	for _, route := range []string{"submit", "steering"} {
		for _, corruption := range []string{"encoded-text", "missing", "empty"} {
			t.Run(route+"/"+corruption, func(t *testing.T) {
				requests := make(chan string, 4)
				var calls atomic.Int32
				release := make(chan struct{})
				defer func() {
					select {
					case <-release:
					default:
						close(release)
					}
				}()
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var body json.RawMessage
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
						return
					}
					attempt := calls.Add(1)
					requests <- string(body)
					if route == "steering" && attempt == 1 {
						select {
						case <-release:
						case <-r.Context().Done():
							return
						}
					}
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"continued safely\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
				}))
				t.Cleanup(server.Close)
				home, dir, project := t.TempDir(), t.TempDir(), t.TempDir()
				cfg := mediaAttributionConfig(server.URL, "attachment-recovery", "openai", provider.ModalityImage)
				s, err := NewWithSessionDir("attachment-recovery", cfg, home, dir)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(s.Close)
				s.SetWorkDir(project)
				store, err := collaboration.Attach(s.db, project, filepath.Join(home, "locks"))
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = store.Close() })
				s.SetCollaboration(store, "attachment-fixture")
				badPath, goodPath := filepath.Join(project, "faulty.png"), filepath.Join(project, "good.png")
				bad := []byte(base64.StdEncoding.EncodeToString(mediaPNG.content))
				if corruption == "empty" {
					bad = nil
				}
				if corruption != "missing" {
					if err := os.WriteFile(badPath, bad, 0600); err != nil {
						t.Fatal(err)
					}
				}
				if err := os.WriteFile(goodPath, mediaPNG.content, 0600); err != nil {
					t.Fatal(err)
				}
				for id, path := range map[string]string{"bad": badPath, "good": goodPath} {
					if err := sessiondb.InsertPromptReference(s.db, sessiondb.Reference{ID: id, SessionID: s.sessionID, Kind: sessiondb.KindPastedImage, Label: "[" + id + "]", FilePath: path, Mime: "image/png"}); err != nil {
						t.Fatal(err)
					}
				}
				sender := newMockEventSender()
				o, err := s.NewOrchestrator(sender)
				if err != nil {
					t.Fatal(err)
				}
				const text = "keep working with [bad] and [good]"
				if route == "submit" {
					if !o.SubmitInput(provider.InputWithRefs(text, []string{"bad", "good"})) {
						t.Fatal("submit rejected")
					}
				} else {
					if !o.SubmitInput(provider.PlainInput("start work")) {
						t.Fatal("initial submit rejected")
					}
					select {
					case <-requests:
					case <-time.After(5 * time.Second):
						t.Fatal("initial request missing")
					}
					participant, err := store.Agent(t.Context(), o.ID)
					if err != nil {
						t.Fatal(err)
					}
					_, err = store.Publish(t.Context(), collaboration.PublishRequest{SenderID: "human", Origin: collaboration.OriginHuman, Kind: collaboration.EventHumanSteering, IdempotencyKey: "bad-clipboard", RecipientIDs: []string{o.ID}, RunID: participant.CurrentRunID, Body: text, References: []collaboration.Reference{{Kind: "history_message", ID: "bad-clipboard-input"}, {Kind: "prompt_reference", ID: "bad"}, {Kind: "prompt_reference", ID: "good"}}})
					if err != nil {
						t.Fatal(err)
					}
					close(release)
				}
				var body string
				select {
				case body = <-requests:
				case <-time.After(5 * time.Second):
					t.Fatal("faulty attachment stranded the run")
				}
				if !strings.Contains(body, text) || !strings.Contains(body, "unavailable_attachment") || strings.Count(body, "data:image/png;base64,") != 1 || !strings.Contains(body, base64.StdEncoding.EncodeToString(mediaPNG.content)) {
					t.Fatalf("lost original/good media or missing exclusion evidence: %s", body)
				}
				waitTurnDone(t, sender, o.ID)
				path := s.dbPath
				_ = store.Close()
				s.Close()
				before := calls.Load()
				resumed, err := ResumeSessionWithDir(path, cfg, home, dir)
				if err != nil {
					t.Fatal(err)
				}
				defer resumed.Close()
				state, found, err := sessiondb.LoadContextState(resumed.db, resumed.sessionID, resumed.sessionID)
				if err != nil || !found {
					t.Fatalf("load recovered context: %v", err)
				}
				original, excluded := 0, 0
				for _, message := range state.Messages {
					if message.IsGenuineUserInput() && strings.Contains(message.ContentString(), text) {
						original++
					}
					if strings.Contains(message.ContentString(), "<unavailable_attachment") {
						excluded++
					}
				}
				if original != 1 || excluded != 1 || calls.Load() != before {
					t.Fatalf("resume lost/replayed evidence: original=%d excluded=%d requests=%d", original, excluded, calls.Load())
				}
				if corruption != "missing" {
					current, err := os.ReadFile(badPath)
					if err != nil || string(current) != string(bad) {
						t.Fatal("faulty original was overwritten")
					}
				}
			})
		}
	}
}
