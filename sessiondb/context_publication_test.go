package sessiondb

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"crude/provider"
	"crude/types/recovery"
)

func TestPublishContextCASAppendAndIdempotence(t *testing.T) {
	db := OpenAndMigrateAt(t.TempDir())
	defer db.Close()
	if err := CreateSessionRow(db, "session"); err != nil {
		t.Fatal(err)
	}
	created := time.Unix(0, 1234).UnixNano()
	delta := ContextMessageDelta{Message: provider.ChatMessage{InternalID: "m1", Role: "user", Content: provider.TextContent("hello")}, Turn: 1, CreatedAt: created, AgentID: "agent"}
	state, err := PublishContext(db, ContextPublication{SessionID: "session", AgentID: "agent", Mutation: ContextMutation{Append: []ContextMessageDelta{delta}}})
	if err != nil {
		t.Fatal(err)
	}
	if state.Revision != 1 || len(state.Messages) != 1 {
		t.Fatalf("state=%+v", state)
	}
	replay, err := PublishContext(db, ContextPublication{SessionID: "session", AgentID: "agent", ExpectedRevision: 1, Mutation: ContextMutation{Append: []ContextMessageDelta{delta}}})
	if err != nil {
		t.Fatal(err)
	}
	if replay.Revision != 1 {
		t.Fatalf("idempotent replay churned revision: %d", replay.Revision)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM messages WHERE internal_id='m1'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("count=%d err=%v", count, err)
	}
	_, err = PublishContext(db, ContextPublication{SessionID: "session", AgentID: "agent", ExpectedRevision: 0, Mutation: ContextMutation{Append: []ContextMessageDelta{{Message: provider.ChatMessage{InternalID: "m2", Role: "user", Content: provider.TextContent("new")}, Turn: 1, CreatedAt: created + 1}}}})
	var conflict *ContextRevisionConflictError
	if !errors.As(err, &conflict) || conflict.Actual != 1 {
		t.Fatalf("err=%v", err)
	}
}

func TestRecoveryOnlyPublicationDoesNotRewriteImmutableContext(t *testing.T) {
	db := OpenAndMigrateAt(t.TempDir())
	defer db.Close()
	if err := CreateSessionRow(db, "session"); err != nil {
		t.Fatal(err)
	}
	message := provider.NewMultiPartMessage("user", []provider.ContentPart{{Type: "text", Text: "inspect image"}, {Type: "image_url", ImageURL: &provider.ImageURL{URL: "data:image/png;base64," + strings.Repeat("YWJj", 1024)}}})
	message.InternalID = "image-input"
	state, err := PublishContext(db, ContextPublication{SessionID: "session", AgentID: "root", Mutation: ContextMutation{Replace: &ContextReplacement{Generation: 1, Reason: "input", Messages: []provider.ChatMessage{message}}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TRIGGER reject_context_payload_rewrite BEFORE UPDATE OF messages_json ON agent_context_state BEGIN SELECT RAISE(ABORT,'immutable payload rewritten'); END`); err != nil {
		t.Fatal(err)
	}
	checkpoint := RecoveryCheckpoint{Status: recovery.Status{ID: "incident", AgentID: "root", Generation: 1}, PendingInput: provider.PlainInput("inspect image"), InputAdmitted: true, Archive: state.Messages, ActiveContext: state.Messages}
	next, err := PublishContext(db, ContextPublication{SessionID: "session", AgentID: "root", RuntimeAgentID: "root", ExpectedRevision: state.Revision, Recovery: &checkpoint})
	if err != nil {
		t.Fatalf("status-only recovery rewrote unchanged image context: %v", err)
	}
	loaded, found, err := LoadRecoveryCheckpoint(db, "session", "root")
	if err != nil || !found || loaded.ContextRevision != next.Revision || !reflect.DeepEqual(loaded.ActiveContext, state.Messages) || !reflect.DeepEqual(loaded.Archive, state.Messages) {
		t.Fatalf("recovery lost image bytes or exact context revision: found=%v error=%v", found, err)
	}
	_, err = PublishContext(db, ContextPublication{SessionID: "session", AgentID: "root", RuntimeAgentID: "root", ExpectedRevision: state.Revision, Recovery: &checkpoint})
	var conflict *ContextRevisionConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("status-only publication bypassed revision fence: %v", err)
	}
	if _, err := db.Exec(`CREATE TRIGGER reject_recovery_bookkeeping BEFORE INSERT ON agent_recoveries BEGIN SELECT RAISE(ABORT,'recovery unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	checkpoint.Status.ID = "failed-incident"
	if _, err := PublishContext(db, ContextPublication{SessionID: "session", AgentID: "root", RuntimeAgentID: "root", ExpectedRevision: next.Revision, Recovery: &checkpoint}); err == nil {
		t.Fatal("status-only publication acknowledged a failed recovery commit")
	}
	current, found, err := LoadContextState(db, "session", "root")
	if err != nil || !found || current.Revision != next.Revision || !reflect.DeepEqual(current.Messages, state.Messages) {
		t.Fatalf("failed recovery changed context revision or bytes: found=%v error=%v", found, err)
	}
}

func BenchmarkPublishContextRecoveryImage(b *testing.B) {
	for _, size := range []int{1024, 4 << 20} {
		b.Run(fmt.Sprintf("bytes_%d", size), func(b *testing.B) {
			db := OpenAndMigrateAt(b.TempDir())
			defer db.Close()
			if err := CreateSessionRow(db, "session"); err != nil {
				b.Fatal(err)
			}
			message := provider.NewMultiPartMessage("user", []provider.ContentPart{{Type: "image_url", ImageURL: &provider.ImageURL{URL: "data:image/png;base64," + strings.Repeat("YWJj", size/4)}}})
			message.InternalID = "image-input"
			state, err := PublishContext(db, ContextPublication{SessionID: "session", AgentID: "root", Mutation: ContextMutation{Replace: &ContextReplacement{Generation: 1, Reason: "input", Messages: []provider.ChatMessage{message}}}})
			if err != nil {
				b.Fatal(err)
			}
			checkpoint := RecoveryCheckpoint{Status: recovery.Status{ID: "incident", AgentID: "root", Generation: 1}, PendingInput: provider.PlainInput("inspect image"), InputAdmitted: true, Archive: state.Messages, ActiveContext: state.Messages}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				state, err = PublishContext(db, ContextPublication{SessionID: "session", AgentID: "root", RuntimeAgentID: "root", ExpectedRevision: state.Revision, Recovery: &checkpoint})
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func TestPublishContextCommitsHumanPromptMetadataWithInput(t *testing.T) {
	db := OpenAndMigrateAt(t.TempDir())
	defer db.Close()
	if err := CreateSessionRow(db, "session"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE sessions SET model='model',provider='provider',account_id='account' WHERE id='session'`); err != nil {
		t.Fatal(err)
	}
	first := ContextMessageDelta{Message: provider.ChatMessage{InternalID: "first", Role: "user", Content: provider.TextContent("original request")}, Turn: 1, CreatedAt: 1, AgentID: "root"}
	state, err := PublishContext(db, ContextPublication{SessionID: "session", AgentID: "root", Mutation: ContextMutation{Append: []ContextMessageDelta{first}}})
	if err != nil {
		t.Fatal(err)
	}
	runtime := ContextMessageDelta{Message: provider.ChatMessage{InternalID: "runtime", Role: "user", Origin: provider.OriginRuntimeContext, Visibility: provider.VisibilityContextOnly, Content: provider.TextContent("not a human prompt")}, Meta: DisplayMeta{ContextOnly: true}, Turn: 1, CreatedAt: 2, AgentID: "root"}
	state, err = PublishContext(db, ContextPublication{SessionID: "session", AgentID: "root", ExpectedRevision: state.Revision, Mutation: ContextMutation{Append: []ContextMessageDelta{runtime}}})
	if err != nil {
		t.Fatal(err)
	}
	meta, err := GetMeta(db)
	if err != nil || !meta.FirstPrompt.Valid || !meta.LastPrompt.Valid || meta.FirstPrompt.String != "original request" || meta.LastPrompt.String != "original request" || meta.TurnCount != 1 {
		t.Fatalf("initial input metadata = %+v error=%v", meta, err)
	}
	if _, err := db.Exec(`CREATE TRIGGER reject_input BEFORE INSERT ON messages WHEN NEW.internal_id='second' BEGIN SELECT RAISE(FAIL,'input commit unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	text := strings.Repeat("\u754c", 80)
	second := ContextMessageDelta{Message: provider.ChatMessage{InternalID: "second", Role: "user", Origin: provider.OriginHumanSteering, Content: provider.TextContent("attributed steering envelope")}, Meta: DisplayMeta{DisplayContent: text}, Turn: 2, CreatedAt: 3, AgentID: "root"}
	publication := ContextPublication{SessionID: "session", AgentID: "root", ExpectedRevision: state.Revision, Mutation: ContextMutation{Append: []ContextMessageDelta{second}}}
	if _, err := PublishContext(db, publication); err == nil {
		t.Fatal("failed input was acknowledged")
	}
	failed, err := GetMeta(db)
	if err != nil || !reflect.DeepEqual(meta, failed) {
		t.Fatalf("failed publication changed prompt metadata: before=%+v after=%+v error=%v", meta, failed, err)
	}
	if _, err := db.Exec(`DROP TRIGGER reject_input`); err != nil {
		t.Fatal(err)
	}
	if _, err := PublishContext(db, publication); err != nil {
		t.Fatal(err)
	}
	meta, err = GetMeta(db)
	if err != nil || !meta.FirstPrompt.Valid || !meta.LastPrompt.Valid || meta.FirstPrompt.String != "original request" || meta.LastPrompt.String != strings.Repeat("\u754c", 66) || !utf8.ValidString(meta.LastPrompt.String) || meta.TurnCount != 2 {
		t.Fatalf("committed steering metadata = %+v error=%v", meta, err)
	}
}

func TestPublishContextExactIdentityAndTranscriptOnlyAdoption(t *testing.T) {
	db := OpenAndMigrateAt(t.TempDir())
	defer db.Close()
	if err := CreateSessionRow(db, "session"); err != nil {
		t.Fatal(err)
	}
	meta := DisplayMeta{SnapshotVersion: 1, ContextOnly: false, TurnGeneration: 9, ProviderID: "provider"}
	delta := ContextMessageDelta{Message: provider.ChatMessage{InternalID: "m", Role: "assistant", Content: provider.TextContent("answer")}, Meta: meta, Turn: 3, CreatedAt: 77, AgentID: "agent"}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := appendMessageWithMetaTx(tx, "session", delta); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	state, err := PublishContext(db, ContextPublication{SessionID: "session", AgentID: "agent", Mutation: ContextMutation{Append: []ContextMessageDelta{delta}}})
	if err != nil {
		t.Fatal(err)
	}
	if state.Revision != 1 || len(state.Messages) != 1 {
		t.Fatalf("state=%+v", state)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM messages WHERE internal_id='m'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("count=%d err=%v", count, err)
	}

	mutations := []func(*ContextMessageDelta){
		func(v *ContextMessageDelta) { v.Message.Content = provider.TextContent("different") },
		func(v *ContextMessageDelta) { v.Meta.ProviderID = "other" },
		func(v *ContextMessageDelta) { v.Turn++ },
		func(v *ContextMessageDelta) { v.CreatedAt++ },
		func(v *ContextMessageDelta) { v.AgentID = "other" },
	}
	before := publicationBytes(t, db)
	for i, mutate := range mutations {
		changed := delta
		mutate(&changed)
		_, err := PublishContext(db, ContextPublication{SessionID: "session", AgentID: "agent", ExpectedRevision: 1, Mutation: ContextMutation{Append: []ContextMessageDelta{changed}}})
		var conflict *ContextMessageConflictError
		if !errors.As(err, &conflict) {
			t.Fatalf("case %d err=%v", i, err)
		}
		if after := publicationBytes(t, db); !reflect.DeepEqual(before, after) {
			t.Fatalf("case %d mutated database", i)
		}
	}
}

func TestPublishContextMissingTranscriptAndContextOnlySemantics(t *testing.T) {
	db := OpenAndMigrateAt(t.TempDir())
	defer db.Close()
	if err := CreateSessionRow(db, "session"); err != nil {
		t.Fatal(err)
	}
	message := provider.ChatMessage{InternalID: "only", Role: "system", Content: provider.TextContent("checkpoint")}
	first, err := PublishContext(db, ContextPublication{SessionID: "session", AgentID: "agent", Mutation: ContextMutation{Append: []ContextMessageDelta{{Message: message, Meta: DisplayMeta{ContextOnly: true}, CreatedAt: 1, AgentID: "agent"}}}})
	if err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM messages`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("count=%d err=%v", count, err)
	}
	if _, err := PublishContext(db, ContextPublication{SessionID: "session", AgentID: "agent", ExpectedRevision: first.Revision, Mutation: ContextMutation{Append: []ContextMessageDelta{{Message: message, CreatedAt: 1, AgentID: "agent"}}}}); err == nil {
		t.Fatal("missing transcript accepted")
	}
}

func TestPublishContextConcurrentExactRevisionOneWinner(t *testing.T) {
	db := OpenAndMigrateAt(t.TempDir())
	defer db.Close()
	if err := CreateSessionRow(db, "session"); err != nil {
		t.Fatal(err)
	}
	initial, err := PublishContext(db, ContextPublication{SessionID: "session", AgentID: "agent", Mutation: ContextMutation{Replace: &ContextReplacement{Generation: 4, Reason: "initial"}}})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, reason := range []string{"left", "right"} {
		wg.Add(1)
		go func(reason string) {
			defer wg.Done()
			_, err := PublishContext(db, ContextPublication{SessionID: "session", AgentID: "agent", ExpectedRevision: initial.Revision, Mutation: ContextMutation{Replace: &ContextReplacement{Generation: 5, Reason: reason}}})
			errs <- err
		}(reason)
	}
	wg.Wait()
	close(errs)
	success, conflicts := 0, 0
	for err := range errs {
		if err == nil {
			success++
		} else {
			var conflict *ContextRevisionConflictError
			if errors.As(err, &conflict) {
				conflicts++
			} else {
				t.Fatal(err)
			}
		}
	}
	if success != 1 || conflicts != 1 {
		t.Fatalf("success=%d conflicts=%d", success, conflicts)
	}
	state, ok, err := LoadContextState(db, "session", "agent")
	if err != nil || !ok || state.Revision != 2 || state.Generation != 5 {
		t.Fatalf("state=%+v ok=%v err=%v", state, ok, err)
	}
}

func TestPublishContextRelatedOwnerValidationRollsBack(t *testing.T) {
	tests := []struct {
		name  string
		alter func(*ContextPublication)
	}{
		{"missing runtime agent", func(p *ContextPublication) {
			p.Recovery = &RecoveryCheckpoint{Status: recovery.Status{ID: "i", AgentID: "run", Generation: 1}}
		}},
		{"recovery context owner", func(p *ContextPublication) {
			p.RuntimeAgentID = "run"
			p.Recovery = &RecoveryCheckpoint{Status: recovery.Status{ID: "i", AgentID: "run", Generation: 1}, ContextOwnerID: "foreign"}
		}},
		{"recovery runtime agent", func(p *ContextPublication) {
			p.RuntimeAgentID = "other"
			p.Recovery = &RecoveryCheckpoint{Status: recovery.Status{ID: "i", AgentID: "run", Generation: 1}}
		}},
		{"admission", func(p *ContextPublication) {
			p.RuntimeAgentID = "run"
			p.Admissions = []CollaborationAdmission{{DeliveryID: "d", EventID: "e", SessionID: "foreign", AgentID: "agent", RuntimeEpoch: 1, DeliveryVersion: 1, ContextCommitID: "c"}}
		}},
		{"compaction", func(p *ContextPublication) {
			p.RuntimeAgentID = "run"
			p.Compaction = &CompactionRecord{OperationID: "o", SessionID: "foreign", AgentID: "agent"}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := OpenAndMigrateAt(t.TempDir())
			defer db.Close()
			if err := CreateSessionRow(db, "session"); err != nil {
				t.Fatal(err)
			}
			before := publicationBytes(t, db)
			p := ContextPublication{SessionID: "session", AgentID: "agent", Mutation: ContextMutation{Replace: &ContextReplacement{Generation: 1, Reason: "x"}}}
			test.alter(&p)
			if _, err := PublishContext(db, p); err == nil {
				t.Fatal("expected validation failure")
			}
			if got := publicationBytes(t, db); !reflect.DeepEqual(before, got) {
				t.Fatalf("mutated database: %q", got)
			}
		})
	}
}

func TestPublishContextRootOwnerAndRuntimeAgentCommitRelatedRows(t *testing.T) {
	db := OpenAndMigrateAt(t.TempDir())
	defer db.Close()
	const sessionID = "root-session"
	const runtimeAgentID = "root-orchestrator"
	if err := CreateSessionRow(db, sessionID); err != nil {
		t.Fatal(err)
	}
	now := time.Unix(0, 55)
	delta := ContextMessageDelta{
		Message:   provider.ChatMessage{InternalID: "message", Role: "user", Content: provider.TextContent("accepted")},
		Turn:      1,
		CreatedAt: 1,
		AgentID:   runtimeAgentID,
	}
	checkpoint := RecoveryCheckpoint{
		Status:         recovery.Status{ID: "incident", AgentID: runtimeAgentID, Generation: 3, State: recovery.Interrupted, StartedAt: now, UpdatedAt: now},
		ContextOwnerID: sessionID,
	}
	admission := CollaborationAdmission{
		DeliveryID: "delivery", EventID: "event", SessionID: sessionID, AgentID: runtimeAgentID,
		RuntimeEpoch: 7, DeliveryVersion: 2, ContextCommitID: "commit", RunID: "run",
	}
	compaction := CompactionRecord{
		OperationID: "operation", SessionID: sessionID, AgentID: runtimeAgentID,
		Trigger: "automatic", Role: "root", State: CompactionPreparing,
	}
	state, err := PublishContext(db, ContextPublication{
		SessionID: sessionID, AgentID: sessionID, RuntimeAgentID: runtimeAgentID,
		Mutation: ContextMutation{Append: []ContextMessageDelta{delta}}, TranscriptAppend: []ContextMessageDelta{delta},
		Recovery: &checkpoint, Admissions: []CollaborationAdmission{admission}, Compaction: &compaction,
	})
	if err != nil {
		t.Fatal(err)
	}
	if state.AgentID != sessionID || state.Revision != 1 || len(state.Messages) != 1 {
		t.Fatalf("context state = %+v", state)
	}
	for table, want := range map[string]int{
		"agent_context_state":      1,
		"messages":                 1,
		"agent_recoveries":         1,
		"collaboration_admissions": 1,
		"context_compactions":      1,
	} {
		var got int
		if err := db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&got); err != nil || got != want {
			t.Fatalf("%s count=%d want=%d err=%v", table, got, want, err)
		}
	}
	loadedCheckpoint, ok, err := LoadRecoveryCheckpoint(db, sessionID, runtimeAgentID)
	if err != nil || !ok || loadedCheckpoint.ContextOwnerID != sessionID || loadedCheckpoint.ContextRevision != state.Revision {
		t.Fatalf("checkpoint=%+v ok=%v err=%v", loadedCheckpoint, ok, err)
	}
	pending, err := PendingCollaborationAdmissions(db, runtimeAgentID)
	if err != nil || len(pending) != 1 || pending[0].RunID != admission.RunID || pending[0].RuntimeEpoch != admission.RuntimeEpoch {
		t.Fatalf("admissions=%+v err=%v", pending, err)
	}
	var compactionAgent string
	if err := db.QueryRow(`SELECT agent_id FROM context_compactions WHERE operation_id=?`, compaction.OperationID).Scan(&compactionAgent); err != nil || compactionAgent != runtimeAgentID {
		t.Fatalf("compaction agent=%q err=%v", compactionAgent, err)
	}
}

func TestPublishContextRuntimeIdentityMismatchRollsBack(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ContextPublication)
	}{
		{"declared recipient", func(p *ContextPublication) { p.RuntimeAgentID = "foreign-runtime" }},
		{"recovery producer", func(p *ContextPublication) { p.Recovery.Status.AgentID = "foreign-runtime" }},
		{"admission recipient", func(p *ContextPublication) { p.Admissions[0].AgentID = "foreign-runtime" }},
		{"admission session", func(p *ContextPublication) { p.Admissions[0].SessionID = "foreign-session" }},
		{"compaction producer", func(p *ContextPublication) { p.Compaction.AgentID = "foreign-runtime" }},
		{"compaction session", func(p *ContextPublication) { p.Compaction.SessionID = "foreign-session" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := OpenAndMigrateAt(t.TempDir())
			defer db.Close()
			const sessionID = "root-session"
			const runtimeAgentID = "root-orchestrator"
			if err := CreateSessionRow(db, sessionID); err != nil {
				t.Fatal(err)
			}
			before := publicationBytes(t, db)
			p := ContextPublication{
				SessionID: sessionID, AgentID: sessionID, RuntimeAgentID: runtimeAgentID,
				Mutation: ContextMutation{Append: []ContextMessageDelta{{
					Message: provider.ChatMessage{InternalID: "message", Role: "user", Content: provider.TextContent("accepted")},
					Turn:    1, CreatedAt: 1, AgentID: runtimeAgentID,
				}}},
				Recovery: &RecoveryCheckpoint{Status: recovery.Status{ID: "incident", AgentID: runtimeAgentID, Generation: 1}},
				Admissions: []CollaborationAdmission{{
					DeliveryID: "delivery", EventID: "event", SessionID: sessionID, AgentID: runtimeAgentID,
					RuntimeEpoch: 7, DeliveryVersion: 2, ContextCommitID: "commit", RunID: "run",
				}},
				Compaction: &CompactionRecord{OperationID: "operation", SessionID: sessionID, AgentID: runtimeAgentID, State: CompactionPreparing},
			}
			test.mutate(&p)
			if _, err := PublishContext(db, p); err == nil {
				t.Fatal("expected runtime identity conflict")
			}
			if after := publicationBytes(t, db); !reflect.DeepEqual(before, after) {
				t.Fatal("runtime identity conflict partially published")
			}
		})
	}
}

func publicationBytes(t *testing.T, db *sql.DB) []byte {
	t.Helper()
	tables := []string{"agent_context_state", "messages", "agent_recoveries", "collaboration_admissions", "context_compactions", "agent_context_generations"}
	result := make(map[string][]map[string]any, len(tables))
	for _, table := range tables {
		rows, err := db.Query(`SELECT * FROM ` + table + ` ORDER BY rowid`)
		if err != nil {
			t.Fatal(err)
		}
		cols, _ := rows.Columns()
		for rows.Next() {
			values := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range values {
				ptrs[i] = &values[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				t.Fatal(err)
			}
			row := map[string]any{}
			for i, col := range cols {
				if b, ok := values[i].([]byte); ok {
					row[col] = string(b)
				} else {
					row[col] = values[i]
				}
			}
			result[table] = append(result[table], row)
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestPublishContextRelatedRowsRollbackOnSQLiteAbort(t *testing.T) {
	db := OpenAndMigrateAt(t.TempDir())
	defer db.Close()
	if err := CreateSessionRow(db, "session"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TRIGGER abort_admission BEFORE INSERT ON collaboration_admissions BEGIN SELECT RAISE(ABORT,'fault'); END`); err != nil {
		t.Fatal(err)
	}
	now := time.Unix(0, 55)
	checkpoint := RecoveryCheckpoint{Status: recovery.Status{ID: "incident", AgentID: "agent", Generation: 1, State: recovery.Interrupted, StartedAt: now, UpdatedAt: now}}
	admission := CollaborationAdmission{DeliveryID: "d", EventID: "e", SessionID: "session", AgentID: "agent", RuntimeEpoch: 1, DeliveryVersion: 1, ContextCommitID: "c"}
	_, err := PublishContext(db, ContextPublication{SessionID: "session", AgentID: "agent", RuntimeAgentID: "agent",
		Mutation: ContextMutation{Append: []ContextMessageDelta{{Message: provider.ChatMessage{InternalID: "m", Role: "user", Content: provider.TextContent("x")}, Turn: 1, CreatedAt: 1, AgentID: "agent"}}},
		Recovery: &checkpoint, Admissions: []CollaborationAdmission{admission}})
	if err == nil {
		t.Fatal("expected abort")
	}
	for _, table := range []string{"agent_context_state", "messages", "agent_recoveries", "collaboration_admissions"} {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s count=%d err=%v", table, count, err)
		}
	}
}

func TestPublishContextEverySQLiteAbortRollsBack(t *testing.T) {
	tables := []string{"messages", "agent_context_state", "agent_recoveries", "collaboration_admissions", "context_compactions"}
	for _, table := range tables {
		t.Run(table, func(t *testing.T) {
			db := OpenAndMigrateAt(t.TempDir())
			defer db.Close()
			if err := CreateSessionRow(db, "session"); err != nil {
				t.Fatal(err)
			}
			before := publicationBytes(t, db)
			if _, err := db.Exec(`CREATE TRIGGER abort_publication BEFORE INSERT ON ` + table + ` BEGIN SELECT RAISE(ABORT,'fault'); END`); err != nil {
				t.Fatal(err)
			}
			now := time.Unix(0, 55)
			p := ContextPublication{SessionID: "session", AgentID: "agent", RuntimeAgentID: "agent",
				Mutation:   ContextMutation{Append: []ContextMessageDelta{{Message: provider.ChatMessage{InternalID: "m", Role: "user", Content: provider.TextContent("x")}, Turn: 1, CreatedAt: 1, AgentID: "agent"}}},
				Recovery:   &RecoveryCheckpoint{Status: recovery.Status{ID: "incident", AgentID: "agent", Generation: 1, State: recovery.Interrupted, StartedAt: now, UpdatedAt: now}},
				Admissions: []CollaborationAdmission{{DeliveryID: "d", EventID: "e", SessionID: "session", AgentID: "agent", RuntimeEpoch: 1, DeliveryVersion: 1, ContextCommitID: "c"}},
				Compaction: &CompactionRecord{OperationID: "op", SessionID: "session", AgentID: "agent", State: CompactionPreparing},
			}
			if _, err := PublishContext(db, p); err == nil {
				t.Fatal("expected trigger abort")
			}
			if after := publicationBytes(t, db); !reflect.DeepEqual(before, after) {
				t.Fatalf("partial publication after %s abort", table)
			}
		})
	}
}

func TestPublishContextMultipleMessageFailureRollsBackEarlierAppend(t *testing.T) {
	db := OpenAndMigrateAt(t.TempDir())
	defer db.Close()
	if err := CreateSessionRow(db, "session"); err != nil {
		t.Fatal(err)
	}
	first := ContextMessageDelta{Message: provider.ChatMessage{InternalID: "one", Role: "user", Content: provider.TextContent("one")}, Turn: 1, CreatedAt: 1, AgentID: "agent"}
	conflicting := ContextMessageDelta{Message: provider.ChatMessage{InternalID: "one", Role: "user", Content: provider.TextContent("different")}, Turn: 1, CreatedAt: 2, AgentID: "agent"}
	before := publicationBytes(t, db)
	if _, err := PublishContext(db, ContextPublication{SessionID: "session", AgentID: "agent", Mutation: ContextMutation{Append: []ContextMessageDelta{first, conflicting}}}); err == nil {
		t.Fatal("expected conflict")
	}
	if after := publicationBytes(t, db); !reflect.DeepEqual(before, after) {
		t.Fatal("first append survived later conflict")
	}
}

func TestPublishContextCompactionGenerationIndependentFromRevision(t *testing.T) {
	db := OpenAndMigrateAt(t.TempDir())
	defer db.Close()
	if err := CreateSessionRow(db, "session"); err != nil {
		t.Fatal(err)
	}
	first, err := PublishContext(db, ContextPublication{SessionID: "session", AgentID: "agent", Mutation: ContextMutation{Replace: &ContextReplacement{Generation: 7, Reason: "source", Messages: []provider.ChatMessage{{Role: "user", Content: provider.TextContent("a")}}}}})
	if err != nil {
		t.Fatal(err)
	}
	record := CompactionRecord{OperationID: "op", SessionID: "session", AgentID: "agent", Trigger: "manual", Role: "root", State: CompactionCompleted}
	second, err := PublishContext(db, ContextPublication{SessionID: "session", AgentID: "agent", RuntimeAgentID: "agent", ExpectedRevision: first.Revision, Mutation: ContextMutation{Replace: &ContextReplacement{Generation: 8, Reason: "compacted", Messages: []provider.ChatMessage{{Role: "user", Content: provider.TextContent("b")}}}}, Compaction: &record, ArchivePrevious: true})
	if err != nil {
		t.Fatal(err)
	}
	if second.Revision != 2 || second.Generation != 8 {
		t.Fatalf("state=%+v", second)
	}
	var archived int
	if err := db.QueryRow(`SELECT COUNT(*) FROM agent_context_generations WHERE generation=7`).Scan(&archived); err != nil || archived != 1 {
		t.Fatalf("archive=%d err=%v", archived, err)
	}
}

func TestPublishContextTranscriptAppendWithCompactedReplacement(t *testing.T) {
	db := OpenAndMigrateAt(t.TempDir())
	defer db.Close()
	if err := CreateSessionRow(db, "session"); err != nil {
		t.Fatal(err)
	}
	old := ContextMessageDelta{Message: provider.ChatMessage{InternalID: "old", Role: "user", Content: provider.TextContent("accepted prefix")}, Turn: 1, CreatedAt: 11, AgentID: "agent"}
	source, err := PublishContext(db, ContextPublication{SessionID: "session", AgentID: "agent", Mutation: ContextMutation{Append: []ContextMessageDelta{old}}})
	if err != nil {
		t.Fatal(err)
	}
	checkpoint := provider.ChatMessage{InternalID: "checkpoint", Role: "system", Content: provider.TextContent("compacted prefix")}
	tail := ContextMessageDelta{Message: provider.ChatMessage{InternalID: "tail", Role: "assistant", Content: provider.TextContent("new accepted tail")}, Meta: DisplayMeta{SnapshotVersion: 1, TurnGeneration: 2, ProviderID: "provider"}, Turn: 2, CreatedAt: 22, AgentID: "agent"}
	next, err := PublishContext(db, ContextPublication{
		SessionID:        "session",
		AgentID:          "agent",
		RuntimeAgentID:   "agent",
		ExpectedRevision: source.Revision,
		Mutation: ContextMutation{Replace: &ContextReplacement{
			Generation: 2,
			Reason:     "compacted",
			Messages:   []provider.ChatMessage{checkpoint, tail.Message},
		}},
		TranscriptAppend: []ContextMessageDelta{tail},
		ArchivePrevious:  true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if next.Revision != 2 || next.Generation != 2 || !reflect.DeepEqual(next.Messages, []provider.ChatMessage{checkpoint, tail.Message}) {
		t.Fatalf("active context=%+v", next)
	}
	messages, turns, err := LoadMessages(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 2 || messages[0].InternalID != old.Message.InternalID || messages[0].Content.String() != old.Message.Content.String() || messages[1].InternalID != tail.Message.InternalID || messages[1].Content.String() != tail.Message.Content.String() || !reflect.DeepEqual(turns, []int{1, 2}) {
		t.Fatalf("transcript messages=%+v turns=%v", messages, turns)
	}
	var archivedRaw []byte
	if err := db.QueryRow(`SELECT messages_json FROM agent_context_generations WHERE session_id=? AND agent_id=? AND generation=?`, "session", "agent", source.Generation).Scan(&archivedRaw); err != nil {
		t.Fatal(err)
	}
	var archived []provider.ChatMessage
	if err := json.Unmarshal(archivedRaw, &archived); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(archived, []provider.ChatMessage{old.Message}) {
		t.Fatalf("archived source=%+v", archived)
	}

	replay, err := PublishContext(db, ContextPublication{SessionID: "session", AgentID: "agent", ExpectedRevision: next.Revision, TranscriptAppend: []ContextMessageDelta{tail}})
	if err != nil {
		t.Fatal(err)
	}
	if replay.Revision != next.Revision {
		t.Fatalf("equal transcript replay churned revision: %d", replay.Revision)
	}
}

func TestPublishContextTranscriptConflictRollsBackReplacement(t *testing.T) {
	db := OpenAndMigrateAt(t.TempDir())
	defer db.Close()
	if err := CreateSessionRow(db, "session"); err != nil {
		t.Fatal(err)
	}
	seed := ContextMessageDelta{Message: provider.ChatMessage{InternalID: "tail", Role: "assistant", Content: provider.TextContent("accepted")}, Meta: DisplayMeta{ProviderID: "provider"}, Turn: 2, CreatedAt: 22, AgentID: "agent"}
	state, err := PublishContext(db, ContextPublication{SessionID: "session", AgentID: "agent", Mutation: ContextMutation{Replace: &ContextReplacement{Generation: 1, Reason: "source"}}, TranscriptAppend: []ContextMessageDelta{seed}})
	if err != nil {
		t.Fatal(err)
	}
	before := publicationBytes(t, db)
	changed := seed
	changed.Meta.ProviderID = "changed"
	_, err = PublishContext(db, ContextPublication{SessionID: "session", AgentID: "agent", ExpectedRevision: state.Revision, Mutation: ContextMutation{Replace: &ContextReplacement{Generation: 2, Reason: "compacted", Messages: []provider.ChatMessage{{InternalID: "checkpoint", Role: "system", Content: provider.TextContent("new")}}}}, TranscriptAppend: []ContextMessageDelta{changed}, ArchivePrevious: true})
	var conflict *ContextMessageConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("err=%v", err)
	}
	if after := publicationBytes(t, db); !reflect.DeepEqual(before, after) {
		t.Fatal("transcript identity conflict partially published replacement/archive")
	}
}

func TestPublishContextTranscriptAppendRollsBackAfterLaterSQLiteAbort(t *testing.T) {
	db := OpenAndMigrateAt(t.TempDir())
	defer db.Close()
	if err := CreateSessionRow(db, "session"); err != nil {
		t.Fatal(err)
	}
	initial, err := PublishContext(db, ContextPublication{SessionID: "session", AgentID: "agent", Mutation: ContextMutation{Replace: &ContextReplacement{Generation: 1, Reason: "source"}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TRIGGER abort_checkpoint BEFORE INSERT ON agent_recoveries BEGIN SELECT RAISE(ABORT,'fault'); END`); err != nil {
		t.Fatal(err)
	}
	before := publicationBytes(t, db)
	now := time.Unix(0, 55)
	_, err = PublishContext(db, ContextPublication{
		SessionID:        "session",
		AgentID:          "agent",
		ExpectedRevision: initial.Revision,
		Mutation:         ContextMutation{Replace: &ContextReplacement{Generation: 2, Reason: "compacted"}},
		TranscriptAppend: []ContextMessageDelta{{Message: provider.ChatMessage{InternalID: "tail", Role: "assistant", Content: provider.TextContent("tail")}, Turn: 2, CreatedAt: 22, AgentID: "agent"}},
		Recovery:         &RecoveryCheckpoint{Status: recovery.Status{ID: "incident", AgentID: "agent", Generation: 2, State: recovery.Interrupted, StartedAt: now, UpdatedAt: now}},
	})
	if err == nil {
		t.Fatal("expected checkpoint trigger abort")
	}
	if after := publicationBytes(t, db); !reflect.DeepEqual(before, after) {
		t.Fatal("transcript/context survived later checkpoint abort")
	}
}

func TestPublishContextImmutableIdentityRejectsDifferentOwnerAndNilShape(t *testing.T) {
	db := OpenAndMigrateAt(t.TempDir())
	defer db.Close()
	for _, id := range []string{"one", "two"} {
		if err := CreateSessionRow(db, id); err != nil {
			t.Fatal(err)
		}
	}
	message := provider.ChatMessage{InternalID: "immutable", Role: "user", Content: provider.TextContent("same"), ReferenceLabels: []string{}}
	if _, err := PublishContext(db, ContextPublication{SessionID: "one", AgentID: "root", Mutation: ContextMutation{Replace: &ContextReplacement{Generation: 1, Reason: "source", Messages: []provider.ChatMessage{message}}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := PublishContext(db, ContextPublication{SessionID: "two", AgentID: "root", Mutation: ContextMutation{Replace: &ContextReplacement{Generation: 1, Reason: "source", Messages: []provider.ChatMessage{message}}}}); err == nil {
		t.Fatal("global identity collision with another session owner was accepted")
	}
	changed := message
	changed.ReferenceLabels = nil
	if _, err := PublishContext(db, ContextPublication{SessionID: "one", AgentID: "root", ExpectedRevision: 1, Mutation: ContextMutation{Replace: &ContextReplacement{Generation: 2, Reason: "retry", Messages: []provider.ChatMessage{changed}}}}); err == nil {
		t.Fatal("nil/empty immutable identity change was accepted")
	}
}

func TestPublishContextImmutableIdentityUsesContextOwnerNotProducer(t *testing.T) {
	db := OpenAndMigrateAt(t.TempDir())
	defer db.Close()
	if err := CreateSessionRow(db, "session"); err != nil {
		t.Fatal(err)
	}
	message := provider.ChatMessage{InternalID: "child-produced", Role: "assistant", Content: provider.TextContent("result")}
	first, err := PublishContext(db, ContextPublication{SessionID: "session", AgentID: "root", Mutation: ContextMutation{Append: []ContextMessageDelta{{Message: message, CreatedAt: 1, AgentID: "child-a"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := PublishContext(db, ContextPublication{SessionID: "session", AgentID: "root", ExpectedRevision: first.Revision, Mutation: ContextMutation{Replace: &ContextReplacement{Generation: 2, Reason: "settled", Messages: []provider.ChatMessage{message}}}}); err != nil {
		t.Fatalf("same context owner with a producer attribution must replay: %v", err)
	}
	var owner string
	if err := db.QueryRow(`SELECT agent_id FROM immutable_message_identities WHERE internal_id=?`, message.InternalID).Scan(&owner); err != nil || owner != "root" {
		t.Fatalf("identity owner=%q err=%v", owner, err)
	}
}

func TestHydrateMessageIdentitiesRestoresProviderFactsAndEmptyShapes(t *testing.T) {
	db := OpenAndMigrateAt(t.TempDir())
	defer db.Close()
	if err := CreateSessionRow(db, "session"); err != nil {
		t.Fatal(err)
	}
	message := provider.ChatMessage{InternalID: "complete", Role: "assistant", Content: provider.TextContent("accepted"),
		ReplayScope: "provider-scope", ReplaySourceModel: "original-model", ReplayPrefixHash: "original-prefix",
		ToolCalls: []provider.ToolCall{}, ReasoningDetails: []json.RawMessage{}, ProviderItems: []provider.ProviderItem{},
	}
	if _, err := PublishContext(db, ContextPublication{SessionID: "session", AgentID: "session", Mutation: ContextMutation{Append: []ContextMessageDelta{{Message: message, CreatedAt: 1, Meta: DisplayMeta{DisplayRole: "assistant", DisplayContent: "display remains independent"}}}}}); err != nil {
		t.Fatal(err)
	}
	rows, err := LoadMessagesWithMeta(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := HydrateMessageIdentities(db, "session", rows); err != nil {
		t.Fatal(err)
	}
	message = canonicalPersistedMessage(persistedMessage{Message: message}).Message
	message.DisplayContent = "display remains independent"
	if len(rows) != 1 || !reflect.DeepEqual(rows[0].Msg, message) {
		t.Fatal("resume lost immutable provider metadata, nil shape, or authored display")
	}
}

func BenchmarkPublishContextReplaceUnrelatedArchiveHistory(b *testing.B) {
	for _, unrelated := range []int{0, 2000} {
		b.Run(fmt.Sprintf("unrelated_%d", unrelated), func(b *testing.B) {
			db := OpenAndMigrateAt(b.TempDir())
			defer db.Close()
			if err := CreateSessionRow(db, "target"); err != nil {
				b.Fatal(err)
			}
			for i := 0; i < unrelated; i++ {
				payload, err := json.Marshal([]provider.ChatMessage{{InternalID: fmt.Sprintf("other-%d", i), Role: "user", Content: provider.TextContent("unrelated archived content")}})
				if err != nil {
					b.Fatal(err)
				}
				if _, err := db.Exec(`INSERT INTO agent_context_generations(session_id,agent_id,generation,reason,messages_json,created_at) VALUES(?,?,?,?,?,?)`, "target", fmt.Sprintf("other-%d", i), 1, "old", payload, i+1); err != nil {
					b.Fatal(err)
				}
			}
			state, err := PublishContext(db, ContextPublication{SessionID: "target", AgentID: "root", Mutation: ContextMutation{Replace: &ContextReplacement{Generation: 1, Reason: "start"}}})
			if err != nil {
				b.Fatal(err)
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				state, err = PublishContext(db, ContextPublication{SessionID: "target", AgentID: "root", ExpectedRevision: state.Revision, Mutation: ContextMutation{Replace: &ContextReplacement{Generation: uint64(i + 2), Reason: "replace", Messages: []provider.ChatMessage{{InternalID: "stable", Role: "system", Content: provider.TextContent("checkpoint")}}}}})
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
