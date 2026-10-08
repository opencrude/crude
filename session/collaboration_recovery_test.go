package session

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"crude/collaboration"
	"crude/provider"
)

func TestPassiveResumeAdmitsOldEvidenceBeforeNewPeerWake(t *testing.T) {
	for _, blocked := range []bool{false, true} {
		name := "old_history_reference"
		if blocked {
			name = "failed_admission_blocks_wake_until_repaired"
		}
		t.Run(name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Retained evidence handled.\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
			}))
			t.Cleanup(server.Close)
			home, project := t.TempDir(), t.TempDir()
			cfg := modelAttributionConfig(server.URL, "passive-boundary-model")
			before, err := NewWithSessionDir("", cfg, home, home)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(before.Close)
			before.SetWorkDir(project)
			store, err := collaboration.Attach(before.db, project, filepath.Join(home, "locks"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			before.SetCollaboration(store, "before-passive-boundary")
			sender := newMockEventSender()
			root, err := before.NewOrchestrator(sender)
			if err != nil {
				t.Fatal(err)
			}
			input := provider.UserInput{InternalID: "retained-human", Text: "Retain this exact original human input."}
			if !root.SubmitInput(input) {
				t.Fatal("real input admission failed")
			}
			waitTransitionTurn(t, sender, root)
			before.CloseOrchestrator(root.ID)
			waitSessionSupervisedJourney(t, "initial real root shutdown", before.orchWg.Wait)
			peer, err := store.RegisterAgent(t.Context(), collaboration.AgentRegistration{
				ID: "passive-peer", Name: "Peer", SessionID: "peer-session", OwnerInstance: "before-peer",
			})
			if err != nil {
				t.Fatal(err)
			}
			old, err := store.Publish(t.Context(), collaboration.PublishRequest{
				SenderID: peer.ID, RecipientIDs: []string{root.ID}, IdempotencyKey: "old-evidence",
				Origin: collaboration.OriginPeer, Kind: collaboration.EventPeerNote, Body: "Old evidence about the original input.",
				References: []collaboration.Reference{{Kind: "history_message", ID: input.InternalID}},
			})
			if err != nil {
				t.Fatal(err)
			}
			path := before.dbPath
			before.Close()
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			after, err := ResumeSessionWithDir(path, cfg, home, home)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(after.Close)
			after.SetWorkDir(project)
			recovered, err := collaboration.Attach(after.db, project, filepath.Join(home, "locks"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = recovered.Close() })
			after.SetCollaboration(recovered, "after-passive-boundary")
			if _, err := recovered.RegisterAgent(t.Context(), collaboration.AgentRegistration{
				ID: peer.ID, Name: peer.Name, SessionID: peer.SessionID, OwnerInstance: "after-peer",
			}); err != nil {
				t.Fatal(err)
			}
			if blocked {
				if _, err := after.db.Exec(`CREATE TRIGGER reject_passive_admission BEFORE INSERT ON immutable_message_identities
					WHEN NEW.internal_id='` + old.Event.ID + `' BEGIN SELECT RAISE(ABORT, 'passive storage unavailable'); END`); err != nil {
					t.Fatal(err)
				}
			}
			var runsBefore int
			if err := after.db.QueryRow(`SELECT COUNT(*) FROM collaboration_runs WHERE agent_id=?`, root.ID).Scan(&runsBefore); err != nil {
				t.Fatal(err)
			}
			resumedSender := newMockEventSender()
			resumedRoot, err := after.NewOrchestrator(resumedSender)
			if err != nil {
				t.Fatal(err)
			}
			time.Sleep(650 * time.Millisecond)
			assertNoPassiveWork := func() {
				t.Helper()
				var runs int
				if err := after.db.QueryRow(`SELECT COUNT(*) FROM collaboration_runs WHERE agent_id=?`, resumedRoot.ID).Scan(&runs); err != nil {
					t.Fatal(err)
				}
				if calls.Load() != 1 || runs != runsBefore {
					t.Fatalf("old evidence started work: calls=%d want=1 runs=%d want=%d", calls.Load(), runs, runsBefore)
				}
			}
			assertNoPassiveWork()
			if blocked {
				var identities int
				if err := after.db.QueryRow(`SELECT COUNT(*) FROM immutable_message_identities WHERE internal_id=?`, old.Event.ID).Scan(&identities); err != nil || identities != 0 {
					t.Fatalf("fixture did not actually block old evidence publication: identities=%d err=%v", identities, err)
				}
			}
			fresh, err := recovered.Publish(t.Context(), collaboration.PublishRequest{
				SenderID: peer.ID, RecipientIDs: []string{resumedRoot.ID}, IdempotencyKey: "genuinely-new-evidence",
				Origin: collaboration.OriginPeer, Kind: collaboration.EventPeerNote, Body: "Genuinely new peer data after attachment.",
			})
			if err != nil {
				t.Fatal(err)
			}
			if blocked {
				time.Sleep(650 * time.Millisecond)
				assertNoPassiveWork()
				if _, err := after.db.Exec(`DROP TRIGGER reject_passive_admission`); err != nil {
					t.Fatal(err)
				}
			}
			waitTransitionTurn(t, resumedSender, resumedRoot)
			if calls.Load() != 2 {
				t.Fatalf("new peer data invoked the provider %d times, want one new request", calls.Load())
			}
			after.msgsMu.Lock()
			counts := make(map[string]int)
			for _, message := range after.activeContext {
				counts[message.InternalID]++
				if message.InternalID == input.InternalID && message.ContentString() != input.Text {
					t.Error("an audit reference replaced original human input")
				}
			}
			after.msgsMu.Unlock()
			if counts[input.InternalID] != 1 || counts[old.Event.ID] != 1 || counts[fresh.Event.ID] != 1 {
				t.Fatalf("passive/new admission lost or duplicated exact identities: %v", counts)
			}
			after.Close()
		})
	}
}
