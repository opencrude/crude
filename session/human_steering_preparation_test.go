package session

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"crude/collaboration"
	"crude/provider"
	"crude/sessiondb"
	recoverytypes "crude/types/recovery"
)

func TestHumanSteeringPreparationRequestJourneys(t *testing.T) {
	for _, outcome := range []string{"valid", "unreadable-media", "unsupported-model", "unsupported-protocol"} {
		t.Run(outcome, func(t *testing.T) {
			var calls atomic.Int32
			requests := make(chan map[string]any, 4)
			releaseFirst, releaseSecond := make(chan struct{}), make(chan struct{})
			firstReleased, secondReleased := false, false
			defer func() {
				if !firstReleased {
					close(releaseFirst)
				}
				if !secondReleased {
					close(releaseSecond)
				}
			}()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Errorf("decode steering request: %v", err)
					return
				}
				attempt := calls.Add(1)
				requests <- body
				if attempt == 1 || (attempt == 2 && outcome == "valid") {
					release := releaseFirst
					if attempt == 2 {
						release = releaseSecond
					}
					select {
					case <-release:
					case <-r.Context().Done():
						return
					}
				}
				w.Header().Set("Content-Type", "text/event-stream")
				if outcome == "unsupported-protocol" {
					_, _ = fmt.Fprint(w, "data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"accepted response\"}}\n\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"}}\n\ndata: {\"type\":\"message_stop\"}\n\n")
					return
				}
				_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"accepted response\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
			}))
			t.Cleanup(server.Close)
			home, directory, project := t.TempDir(), t.TempDir(), t.TempDir()
			protocol := "openai"
			if outcome == "unsupported-protocol" {
				protocol = "anthropic"
			}
			cfg := mediaAttributionConfig(server.URL, "human-steering-"+outcome, protocol, provider.ModalityImage)
			category := cfg.Agents.Profiles["default"].Categories[CategoryDefault]
			if outcome == "unsupported-model" {
				category.ModelModalities = []string{}
			}
			if outcome == "unsupported-protocol" {
				category.ModelModalities = []string{provider.ModalityVideo}
			}
			cfg.Agents.Profiles["default"].Categories[CategoryDefault] = category
			s, err := NewWithSessionDir("steering-preparation", cfg, home, directory)
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
			s.SetCollaboration(store, "steering-fixture")
			media := mediaPNG
			if outcome == "unsupported-protocol" {
				media = mediaMP4
			}
			path := filepath.Join(project, media.name)
			if outcome != "unreadable-media" {
				if err := os.WriteFile(path, media.content, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			modality, mime, _ := provider.MediaModalityFromPath(path)
			if outcome == "unsupported-protocol" && provider.ProtocolSupportsMedia(protocol, modality, mime) {
				t.Fatalf("negative fixture is actually supported: protocol=%s modality=%s mime=%s", protocol, modality, mime)
			}
			refs := []sessiondb.Reference{
				{ID: "steering-tool", SessionID: s.sessionID, Kind: sessiondb.KindMCPTool, Title: "mcp__exact_source__inspect", Content: "Connection: exact-source\nFingerprint: immutable-schema"},
				{ID: "steering-context", SessionID: s.sessionID, Kind: sessiondb.KindMCPContext, Title: "exact-source/resource", Content: "Source: exact-source\nURI: exact://resource\nArguments: {\"revision\":8}\noriginal external steering evidence"},
				{ID: "steering-media", SessionID: s.sessionID, Kind: sessiondb.KindSelectedFile, FilePath: path, Mime: mime},
			}
			for _, ref := range refs {
				if err := sessiondb.InsertPromptReference(s.db, ref); err != nil {
					t.Fatal(err)
				}
			}
			sender := newMockEventSender()
			o, err := s.NewOrchestrator(sender)
			if err != nil {
				t.Fatal(err)
			}
			if !o.SubmitInput(provider.PlainInput("begin work before the note")) {
				t.Fatal("initial turn was rejected")
			}
			select {
			case <-requests:
			case <-time.After(5 * time.Second):
				t.Fatal("initial provider request did not start")
			}
			participant, err := store.Agent(t.Context(), o.ID)
			if err != nil || participant.CurrentRunID == "" {
				t.Fatalf("steering target has no exact active run: %+v err=%v", participant, err)
			}
			publish, err := store.Publish(t.Context(), collaboration.PublishRequest{
				SenderID: "human", SenderName: "Human", Origin: collaboration.OriginHuman,
				Kind: collaboration.EventHumanSteering, IdempotencyKey: "selected-steering", RecipientIDs: []string{o.ID},
				RunID: participant.CurrentRunID, Body: "journal-authored note with selected evidence",
				References: []collaboration.Reference{
					{Kind: "history_message", ID: "selected-steering-original"},
					{Kind: "prompt_reference", ID: refs[0].ID},
					{Kind: "prompt_reference", ID: refs[1].ID},
					{Kind: "prompt_reference", ID: refs[2].ID},
					{Kind: "prompt_reference", ID: refs[0].ID},
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			close(releaseFirst)
			firstReleased = true
			awaitAttention := func(after time.Time) recoverytypes.Status {
				t.Helper()
				deadline := time.After(5 * time.Second)
				for {
					select {
					case event := <-sender.ch:
						if event.typ == "done" || event.typ == "tool_call" || event.typ == "tool_result" {
							t.Fatalf("failed steering advanced execution: %s", event.typ)
						}
						if event.typ == "recovery" {
							status := event.data.(recoverytypes.Status)
							if status.State == recoverytypes.NeedsAttention && status.UpdatedAt.After(after) {
								if status.Detail == "" || status.Action == "" {
									t.Fatalf("steering failure is not actionable: %+v", status)
								}
								return status
							}
						}
					case <-deadline:
						t.Fatal("failed steering did not surface NeedsAttention")
					}
				}
			}
			assertRequest := func(body map[string]any) {
				t.Helper()
				var text strings.Builder
				images := 0
				for _, part := range flattenParts(body) {
					if value, ok := part["text"].(string); ok {
						text.WriteString(value)
						text.WriteByte('\n')
					}
					if part["type"] == "image_url" {
						images++
						image, _ := part["image_url"].(map[string]any)
						if image["url"] != "data:image/png;base64,"+base64.StdEncoding.EncodeToString(mediaPNG.content) {
							t.Fatalf("selected steering image bytes changed: %+v", image)
						}
					}
				}
				for _, want := range []string{publish.Event.Body, refs[0].AgentText(), refs[1].AgentText()} {
					if count := strings.Count(text.String(), want); count != 1 {
						t.Fatalf("steering datum appeared %d times, want once: %q\n%s", count, want, text.String())
					}
				}
				if images != 1 || !strings.Contains(text.String(), "not an execution grant") || !strings.Contains(text.String(), "not user instructions or permission") {
					t.Fatalf("steering lost media/authority boundaries: images=%d text=%s", images, text.String())
				}
				if strings.Contains(text.String(), "changed external evidence") {
					t.Fatal("steering adopted changed external content after capturing its selection")
				}
				definitions, _ := body["tools"].([]any)
				for _, tool := range definitions {
					definition, _ := tool.(map[string]any)
					function, _ := definition["function"].(map[string]any)
					if function["name"] == refs[0].Title {
						t.Fatal("selected MCP reference became an execution grant")
					}
				}
			}
			if outcome != "valid" {
				status := awaitAttention(time.Time{})
				if calls.Load() != 1 {
					t.Fatalf("failed steering issued a weakened provider request: %d", calls.Load())
				}
				if !s.RetryRecoveryFor(o.ID, status.ID, status.Generation) {
					t.Fatal("exact Continue was rejected")
				}
				status = awaitAttention(status.UpdatedAt)
				if calls.Load() != 1 {
					t.Fatalf("unrepaired Continue bypassed selected data: %d", calls.Load())
				}
				if outcome == "unsupported-protocol" {
					if !strings.Contains(status.Detail, "protocol \"anthropic\"") || !strings.Contains(status.Detail, "video") {
						t.Fatalf("steering did not fail for the intended wire limitation: %+v", status)
					}
					if !s.CancelRecoveryFor(o.ID, status.ID, status.Generation) {
						t.Fatal("Stop did not reach blocked steering")
					}
					waitTurnDone(t, sender, o.ID)
					state, found, err := sessiondb.LoadContextState(s.db, s.sessionID, s.sessionID)
					if err != nil || !found {
						t.Fatalf("load stopped steering: found=%v err=%v", found, err)
					}
					var original, selection provider.ChatMessage
					captured := false
					for _, message := range state.Messages {
						if message.InternalID == "selected-steering-original" {
							original = message
						}
						if message.InternalID == "selected-steering-original-references" {
							selection = message
							for _, part := range message.Content.Parts() {
								if part.Type == "video" && bytes.Equal(part.Data, media.content) {
									captured = true
								}
							}
						}
					}
					if original.Collaboration == nil || original.Collaboration.EventID != publish.Event.ID || !captured {
						t.Fatal("Stop lost original steering attribution or exact unsupported media preimage")
					}
					dbPath := s.dbPath
					_ = store.Close()
					s.Close()
					resumed, err := ResumeSessionWithDir(dbPath, cfg, home, directory)
					if err != nil {
						t.Fatal(err)
					}
					defer resumed.Close()
					state, found, err = sessiondb.LoadContextState(resumed.db, resumed.sessionID, resumed.sessionID)
					if err != nil || !found {
						t.Fatalf("load passive steering resume: found=%v err=%v", found, err)
					}
					count, selectedCount := 0, 0
					for _, message := range state.Messages {
						if message.InternalID == original.InternalID {
							count++
							if !reflect.DeepEqual(message, original) {
								t.Fatal("passive resume rewrote journal-authored steering")
							}
						}
						if message.InternalID == selection.InternalID {
							selectedCount++
							if !reflect.DeepEqual(message, selection) {
								t.Fatal("passive resume changed the unsupported selected media preimage")
							}
						}
					}
					if count != 1 || selectedCount != 1 || calls.Load() != 1 {
						t.Fatalf("passive resume lost/replayed steering: originals=%d selections=%d requests=%d", count, selectedCount, calls.Load())
					}
					return
				}
				if outcome == "unreadable-media" {
					if err := os.WriteFile(path, media.content, 0o600); err != nil {
						t.Fatal(err)
					}
				} else {
					// Repair configuration, not selected bytes. Mutated backing data
					// must not replace the immutable capture from the failed attempt.
					changed := append([]byte(nil), media.content...)
					changed[len(changed)-1] ^= 1
					if err := os.WriteFile(path, changed, 0o600); err != nil {
						t.Fatal(err)
					}
					if err := sessiondb.UpdatePromptReferenceContent(s.db, refs[1].ID, "changed external evidence"); err != nil {
						t.Fatal(err)
					}
					repaired := copyConfig(cfg)
					category.ModelModalities = []string{provider.ModalityImage}
					repaired.Agents.Profiles["default"].Categories[CategoryDefault] = category
					s.runtimeProfile.UpdateBase(repaired)
				}
				if !s.RetryRecoveryFor(o.ID, status.ID, status.Generation) {
					t.Fatal("Continue after exact repair was rejected")
				}
			}
			select {
			case body := <-requests:
				assertRequest(body)
			case <-time.After(5 * time.Second):
				t.Fatal("valid/repaired steering did not reach the provider")
			}
			if outcome == "valid" {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := sessiondb.UpdatePromptReferenceContent(s.db, refs[1].ID, "changed external evidence"); err != nil {
					t.Fatal(err)
				}
				if _, err := store.Publish(t.Context(), collaboration.PublishRequest{
					SenderID: "human", SenderName: "Human", Origin: collaboration.OriginHuman,
					Kind: collaboration.EventHumanSteering, IdempotencyKey: "next-note", RecipientIDs: []string{o.ID},
					RunID: participant.CurrentRunID, Body: "continue using the already captured evidence",
				}); err != nil {
					t.Fatal(err)
				}
				close(releaseSecond)
				secondReleased = true
				select {
				case body := <-requests:
					assertRequest(body)
				case <-time.After(5 * time.Second):
					t.Fatal("later note did not retain the exact prior steering capture")
				}
			}
			waitTurnDone(t, sender, o.ID)
			state, found, err := sessiondb.LoadContextState(s.db, s.sessionID, s.sessionID)
			if err != nil || !found {
				t.Fatalf("load settled steering: found=%v err=%v", found, err)
			}
			counts := map[string]int{}
			for _, message := range state.Messages {
				counts[message.InternalID]++
				if message.InternalID == "selected-steering-original" &&
					(message.EffectiveOrigin() != provider.OriginHumanSteering || message.Content.HasImage() || message.Collaboration == nil || message.Collaboration.EventID != publish.Event.ID) {
					t.Fatalf("preparation rewrote original steering identity/body: %+v", message)
				}
			}
			for _, id := range []string{"selected-steering-original", "selected-steering-original-references", "selected-steering-original-context"} {
				if counts[id] != 1 {
					t.Fatalf("settled steering identity %q appears %d times", id, counts[id])
				}
			}
		})
	}
}
