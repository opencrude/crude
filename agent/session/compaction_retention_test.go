package session

import (
	"bytes"
	"encoding/xml"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"crude/agent/provider"
	"crude/agent/sessiondb"
	"crude/types/setting"
)

func TestAnsweredRequestsSurviveTwoCheckpointsAndResume(t *testing.T) {
	first := "Keep <one>&\"only\".\n" + strings.Repeat("first rule ", 210)
	second := "Compare <two> & retain the evidence.\n" + strings.Repeat("second rule ", 210)
	for _, tc := range []struct {
		name   string
		budget int
		want   []string
	}{
		{"both", 4000, []string{first, second}}, {"newest", 1000, []string{second}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			j := newEpochJourney(t)
			settings := j.SettingsSnapshot()
			settings.SessionOverrides = map[string]setting.Category{CategoryDefault: {ContextRetentionTokens: &tc.budget}}
			if err := j.HandleSettingsSave(settings); err != nil {
				t.Fatal(err)
			}
			j.finish(t, j.start(t, first), strings.Repeat("first answer evidence ", 500))
			checkpoint := func() {
				t.Helper()
				before := adaptiveMessageBytes(t, j.activeContext)
				gen := j.contextGeneration
				rows, err := sessiondb.LoadMessagesWithMeta(j.db)
				if err != nil {
					t.Fatal(err)
				}
				if r := j.HandleCompact(); r.Error != "" || r.Cancelled || r.After == 0 {
					t.Fatalf("compact: %+v", r)
				}
				var archived []byte
				if err := j.db.QueryRow(`SELECT messages_json FROM agent_context_generations WHERE session_id=? AND agent_id=? AND generation=?`, j.sessionID, j.sessionID, gen).Scan(&archived); err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(archived, before) {
					t.Fatal("source generation changed in archive")
				}
				after, err := sessiondb.LoadMessagesWithMeta(j.db)
				if err != nil || !bytes.Equal(adaptiveRowsBytes(t, rows), adaptiveRowsBytes(t, after)) {
					t.Fatalf("compaction changed transcript: %v", err)
				}
			}
			checkpoint()
			before := adaptiveMessageBytes(t, j.activeContext)
			cfg, dir, path, home := j.Config(), filepath.Dir(j.dbPath), j.dbPath, j.nameHome
			j.Close()
			resumed, err := ResumeSessionWithDir(path, cfg, home, dir)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(resumed.Close)
			if !bytes.Equal(before, adaptiveMessageBytes(t, resumed.activeContext)) {
				t.Fatal("resume changed the accepted checkpoint")
			}
			j.Session, j.sender = resumed, newMockEventSender()
			j.owner, err = j.NewOrchestrator(j.sender)
			if err != nil {
				t.Fatal(err)
			}
			next := j.start(t, second)
			assertRetentionWire(t, next.Messages, []string{first}, first)
			j.finish(t, next, strings.Repeat("second answer evidence ", 500))
			checkpoint()
			next = j.start(t, "Inspect the third result.")
			assertRetentionWire(t, next.Messages, tc.want, first, second)
			j.finish(t, next, "Done.")
		})
	}
}

func assertRetentionWire(t *testing.T, messages []provider.ChatMessage, want []string, historical ...string) {
	t.Helper()
	blocks := 0
	for _, m := range messages {
		if len(m.AnsweredRequests) != 0 {
			t.Fatal("local source records reached the provider wire")
		}
		text := m.ContentString()
		if strings.HasPrefix(text, "<answered_human_requests>") {
			blocks++
			var block struct {
				Requests []string `xml:"request"`
			}
			if err := xml.Unmarshal([]byte(text), &block); err != nil {
				t.Fatalf("malformed history: %v", err)
			}
			for i := range block.Requests {
				block.Requests[i] = strings.TrimSuffix(strings.TrimPrefix(block.Requests[i], "\n"), "\n")
			}
			if !slices.Equal(block.Requests, want) {
				t.Errorf("answered requests differ: got %d, want %d (or exact text/order changed)", len(block.Requests), len(want))
			}
		} else {
			for _, old := range historical {
				if strings.Contains(text, old) {
					t.Error("answered prompt outside history block")
				}
			}
			if strings.Contains(text, "answer evidence ") {
				t.Error("raw assistant archive in continuation")
			}
		}
	}
	if blocks != 1 {
		t.Errorf("got %d answered blocks, want one", blocks)
	}
}
