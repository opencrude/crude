package session

import (
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

	"crude/provider"
	"crude/sessiondb"
)

// Optional acceptance against SQLite backup images, never live databases. This
// exercises a dense real session with an isolated provider and artifact root.
func TestRetainedSessionStopSendLifecycle(t *testing.T) {
	root := os.Getenv("CRUDE_LIFECYCLE_SESSION_FIXTURES")
	if root == "" {
		t.Skip("set CRUDE_LIFECYCLE_SESSION_FIXTURES to a directory of backup images")
	}
	paths, err := filepath.Glob(filepath.Join(root, "*.db"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("backup images: %v, %v", paths, err)
	}
	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request provider.ChatRequest
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
					return
				}
				requests.Add(1)
				text := "Retained session accepts new work."
				if request.ToolChoice == "none" {
					text = "<<<intent>>>\nPreserve the original requirements.\n<<<work>>>\nRetain all accepted evidence.\n<<<state>>>\nPrevious turn stopped.\n<<<continuation>>>\nFollow the new input without replay."
				}
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":%q},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n", text)
			}))
			defer server.Close()
			directory := t.TempDir()
			copyPath := filepath.Join(directory, filepath.Base(path))
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(copyPath, raw, 0600); err != nil {
				t.Fatal(err)
			}
			db, err := sessiondb.OpenAndMigrate(copyPath)
			if err != nil {
				t.Fatal(err)
			}
			meta, err := sessiondb.GetMeta(db)
			if err != nil {
				t.Fatal(err)
			}
			originalRows, err := sessiondb.LoadMessagesWithMeta(db)
			if err != nil {
				t.Fatal(err)
			}
			_ = db.Close()
			cfg := modelAttributionConfig(server.URL, "retained-lifecycle-model")
			account := cfg.Accounts["account"]
			account.ID = meta.AccountID
			cfg.Accounts[meta.AccountID] = account
			category := cfg.Agents.Profiles["default"].Categories[CategoryDefault]
			category.AccountID = meta.AccountID
			category.ModelContextWindow = 2_000_000
			disabled := 0
			category.ContextCompactionPercent = &disabled
			cfg.Agents.Profiles["default"].Categories[CategoryDefault] = category
			cfg.Agents.Profiles["default"].Categories[CategoryAeron] = category
			home := t.TempDir()
			s, err := ResumeSessionWithDir(copyPath, cfg, home, directory)
			if err != nil {
				t.Fatalf("real retained session cannot reopen: %v", err)
			}
			defer func() {
				if s != nil {
					s.Close()
				}
			}()
			s.SetWorkDir(t.TempDir())
			sender := newMockEventSender()
			o, err := s.NewOrchestrator(sender)
			if err != nil {
				t.Fatal(err)
			}
			if requests.Load() != 0 {
				t.Fatal("passive resume executed provider work")
			}
			if pending, exists := s.pendingRecoveries[o.ID]; exists {
				if err := s.CancelRecoveryFor(o.ID, pending.Status.ID, pending.Status.Generation); err != nil {
					t.Fatalf("exact Stop on the actual retained incident failed: %v", err)
				}
				waitControlJourneyTurn(t, sender, o, "cancelled")
			}
			for turn := range 3 {
				input := provider.UserInput{InternalID: fmt.Sprintf("verified-follow-up-%d", turn), Text: fmt.Sprintf("New request %d after a stopped retained session.", turn)}
				intent, err := s.PrepareInputAdmission(o.ID, input, uint64(turn+1))
				if err != nil {
					t.Fatalf("retained session still refuses new input: %v", err)
				}
				if result := s.CommitInputAdmission(t.Context(), intent); result.Status != InputAdmissionAccepted {
					t.Fatalf("retained input not accepted: %+v", result)
				}
				waitControlJourneyTurn(t, sender, o, "completed")
			}
			if requests.Load() != 3 {
				t.Fatalf("new turns replayed or failed: requests=%d", requests.Load())
			}
			s.Close()
			s, err = ResumeSessionWithDir(copyPath, cfg, home, directory)
			if err != nil {
				t.Fatalf("completed follow-ups made session unopenable: %v", err)
			}
			rows, err := sessiondb.LoadMessagesWithMeta(s.db)
			if err != nil {
				t.Fatal(err)
			}
			retained := make(map[string]sessiondb.MessageWithMeta, len(rows))
			for _, row := range rows {
				retained[row.Msg.InternalID] = row
			}
			for _, row := range originalRows {
				if !reflect.DeepEqual(retained[row.Msg.InternalID], row) {
					t.Fatalf("accepted history or provenance changed for %s", row.Msg.InternalID)
				}
			}
			counts := make(map[string]int)
			for _, row := range rows {
				if strings.HasPrefix(row.Msg.InternalID, "verified-follow-up-") {
					counts[row.Msg.InternalID]++
				}
			}
			if len(counts) != 3 || counts["verified-follow-up-0"] != 1 || counts["verified-follow-up-1"] != 1 || counts["verified-follow-up-2"] != 1 {
				t.Fatalf("follow-up transcript not exactly once: %v", counts)
			}
			if len(s.pendingRecoveries) != 0 || requests.Load() != 3 {
				t.Fatal("cold reopen replayed work or restored a settled incident")
			}
			t.Logf("verified %s: %d transcript rows, three fresh turns, passive reopen at %s", meta.ID, len(rows), time.Now().Format(time.RFC3339))
		})
	}
}
