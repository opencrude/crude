package workspace

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"crude/fileio"
	"crude/types"
)

func commandNoticeWorkspace(center *StatusCenter) *Workspace {
	mgr := NewAgentManager()
	mgr.AddOrchestrator("agent", &AgentState{})
	return &Workspace{AgentMgr: mgr, Status: center, SessionsByAgent: map[string]*Session{
		"agent": {SessionID: "session", AgentID: "agent"},
	}}
}

func failedCommandNotice() AgentNotice {
	now := time.Now()
	return AgentNotice{Type: NoticeAsyncCommandStatus, AgentID: "agent", Commands: []fileio.AsyncCmdInfo{{
		Handle: "reused", Command: "test command", PID: 123, Status: fileio.AsyncFailed,
		StartedAt: now.Add(-time.Second), EndedAt: now, Error: "exit status 1",
	}}}
}

func TestAgentCommandNoticeRepeatsPreserveOutcomeAndUserState(t *testing.T) {
	center := NewStatusCenter(nil, nil)
	defer center.Close()
	ws := commandNoticeWorkspace(center)
	notice := failedCommandNotice()
	notice.Commands[0].Status = fileio.AsyncRunning
	notice.Commands[0].EndedAt = time.Time{}
	notice.Commands[0].Error = ""
	ws.ApplyAgentNotice(notice)
	operations := center.Operations()
	if len(operations) != 1 || operations[0].State != types.OperationRunning || !operations[0].Cancellable ||
		len(center.Notifications(true)) != 0 {
		t.Fatalf("running command was not recorded quietly: %+v", operations)
	}
	notice.Commands[0].Status = fileio.AsyncFailed
	notice.Commands[0].EndedAt = time.Now()
	notice.Commands[0].Error = "exit status 1"
	ws.ApplyAgentNotice(notice)
	n := center.Notifications(true)
	if len(n) != 1 || n[0].Occurrences != 1 || n[0].Source != "background-command" ||
		n[0].Owner != (types.ResourceRef{Kind: types.ResourceAgent, ID: "agent", SessionID: "session"}) ||
		len(n[0].Actions) != 1 || n[0].Actions[0].Label != "Inspect agent" {
		t.Fatalf("failure lost source, ownership or inspection: %+v", n)
	}
	if ops := center.Operations(); len(ops) != 1 || ops[0].ID != operations[0].ID || ops[0].State != types.OperationFailed ||
		ops[0].Cancellable || ops[0].OutputRef != "reused" || ops[0].Error != "exit status 1" {
		t.Fatalf("command did not settle its exact operation: %+v", ops)
	}
	id := n[0].ID
	for _, stage := range []struct {
		name   string
		change func()
	}{
		{"unread", func() {}},
		{"read", func() { center.MarkRead(id, true) }},
		{"dismissed", func() { center.Dismiss(id) }},
		{"resolved", func() { center.ResolveNotification(id) }},
	} {
		t.Run(stage.name, func(t *testing.T) {
			stage.change()
			before, ops, version := center.Notifications(true), center.Operations(), center.Version()
			for range 200 {
				notice.Commands[0].Duration += time.Second
				notice.Commands[0].OutputBytes++
				ws.ApplyAgentNotice(notice)
			}
			if center.Version() != version || !reflect.DeepEqual(before, center.Notifications(true)) ||
				!reflect.DeepEqual(ops, center.Operations()) {
				t.Fatalf("same outcome reset state, timestamps, ordering or version: %+v", center.Notifications(true))
			}
		})
	}
	// A delayed running projection must not reopen a final outcome.
	notice.Commands[0].Status = fileio.AsyncRunning
	version := center.Version()
	ws.ApplyAgentNotice(notice)
	if center.Version() != version || center.Operations()[0].State != types.OperationFailed {
		t.Fatal("an observed settled command returned to running")
	}
}

func TestAgentCommandNoticeExactExecutionIdentity(t *testing.T) {
	for _, identity := range []string{"start", "pid", "agent", "session"} {
		t.Run(identity, func(t *testing.T) {
			center := NewStatusCenter(nil, nil)
			defer center.Close()
			ws := commandNoticeWorkspace(center)
			notice := failedCommandNotice()
			ws.ApplyAgentNotice(notice)
			firstID := center.Notifications(true)[0].ID
			switch identity {
			case "start":
				notice.Commands[0].StartedAt = notice.Commands[0].StartedAt.Add(time.Nanosecond)
			case "pid":
				notice.Commands[0].PID++
			case "agent":
				ws.AgentMgr.AddOrchestrator("other", &AgentState{})
				ws.SessionsByAgent["other"] = ws.SessionsByAgent["agent"]
				notice.AgentID = "other"
			case "session":
				ws.SessionsByAgent["agent"].SessionID = "other-session"
			}
			for range 200 {
				ws.ApplyAgentNotice(notice)
			}
			n := center.Notifications(true)
			if len(n) != 2 || n[0].ID == firstID || n[0].Occurrences != 1 || n[1].Occurrences != 1 ||
				len(center.Operations()) != 2 {
				t.Fatalf("distinct execution sharing its handle was conflated: %+v", n)
			}
		})
	}
}

func TestAgentCommandNoticeReopenPreservesPersistedEvidence(t *testing.T) {
	db := openTestWorkspaceDB(t)
	center := NewStatusCenter(db.DB, nil)
	ws := commandNoticeWorkspace(center)
	notice := failedCommandNotice()
	ws.ApplyAgentNotice(notice)
	id := center.Notifications(true)[0].ID
	center.MarkRead(id, true)
	center.Dismiss(id)
	center.ResolveNotification(id)
	before := center.Notifications(true)
	// SQLite retains wall-clock instants, not Go's process-local monotonic clock.
	before[0].CreatedAt = before[0].CreatedAt.Round(0)
	before[0].UpdatedAt = before[0].UpdatedAt.Round(0)
	before[0].ResolvedAt = before[0].ResolvedAt.Round(0)
	center.Close()

	reopened := NewStatusCenter(db.DB, nil)
	defer reopened.Close()
	if err := reopened.Hydrate(); err != nil {
		t.Fatal(err)
	}
	ws.Status = reopened
	ws.ApplyAgentNotice(notice) // Operations are live summaries, not persisted jobs.
	version := reopened.Version()
	for range 200 {
		ws.ApplyAgentNotice(notice)
	}
	if reopened.Version() != version || !reflect.DeepEqual(before, reopened.Notifications(true)) {
		t.Fatalf("reopen replayed persisted outcome or reset flags: %+v", reopened.Notifications(true))
	}
}

func TestAgentCommandNoticePruningCannotReplayRetainedJobs(t *testing.T) {
	center := NewStatusCenter(nil, nil)
	defer center.Close()
	ws := commandNoticeWorkspace(center)
	notice := failedCommandNotice()
	notice.Commands[0].StartedAt = time.Now().Add(-2 * time.Hour)
	notice.Commands[0].EndedAt = time.Now().Add(-time.Hour)
	ws.ApplyAgentNotice(notice)
	id := center.Notifications(true)[0].ID
	opID := center.Operations()[0].ID
	for i := range statusNotificationLimit {
		center.Notify(types.NotificationRecord{Summary: fmt.Sprintf("different failure %d", i), Severity: types.SeverityError})
	}
	for i := range statusOperationLimit {
		center.SetOperation(types.OperationSnapshot{ID: fmt.Sprintf("other-operation-%d", i), State: types.OperationSucceeded})
	}
	for _, n := range center.Notifications(true) {
		if n.ID == id {
			t.Fatal("fixture did not prune the command notification")
		}
	}
	for _, op := range center.Operations() {
		if op.ID == opID {
			t.Fatal("fixture did not prune the command operation")
		}
	}
	version := center.Version()
	for range 200 {
		ws.ApplyAgentNotice(notice)
	}
	if center.Version() != version {
		t.Fatal("a repeated retained job republished pruned display history")
	}
	if got := len(center.backgroundCommands["agent"]); got != 1 {
		t.Fatalf("source observations are not bounded to current jobs: %d", got)
	}
	ws.ApplyAgentNotice(AgentNotice{Type: NoticeAsyncCommandStatus, AgentID: "agent"})
	if len(center.backgroundCommands) != 0 {
		t.Fatal("complete empty snapshot retained command tombstones")
	}
	notice.Commands[0].StartedAt = time.Now()
	ws.ApplyAgentNotice(notice)
	center.RemoveAgent("agent")
	if len(center.backgroundCommands) != 0 {
		t.Fatal("removed agent retained command observations")
	}
}

func TestAgentCommandNoticeRejectsMissingOwnersAndUnknownStatus(t *testing.T) {
	center := NewStatusCenter(nil, nil)
	defer center.Close()
	ws := commandNoticeWorkspace(center)
	notice := failedCommandNotice()
	notice.AgentID = "missing"
	ws.ApplyAgentNotice(notice)
	notice.AgentID = "agent"
	notice.Commands[0].Status = "unknown"
	ws.ApplyAgentNotice(notice)
	delete(ws.SessionsByAgent, "agent")
	notice.Commands[0].Status = fileio.AsyncFailed
	ws.ApplyAgentNotice(notice)
	if center.Version() != 0 {
		t.Fatal("unowned or unknown command evidence was recorded")
	}
}

func TestAgentServiceNoticeExactEventsPreserveUserState(t *testing.T) {
	for _, test := range []struct {
		kind      string
		data      map[string]any
		summary   string
		transient bool
	}{
		{NoticeAttachmentExcluded, map[string]any{"reason": "Attachment could not be read"}, "Attachment could not be read", false},
		{NoticeMemorySaved, map[string]any{"count": 2, "titles": []string{"Saved fact"}}, `Memory: saved "Saved fact" +1 more`, true},
		{NoticeResponseContinuationUnavailable, map[string]any{"reason": "response_not_stored"},
			"Responses delta unavailable: the endpoint declined server storage; full uploads continue", true},
	} {
		t.Run(test.kind, func(t *testing.T) {
			center := NewStatusCenter(nil, nil)
			defer center.Close()
			ws := commandNoticeWorkspace(center)
			identity := types.RuntimeEventIdentity{EventID: "source-event", OwnerID: "session", AgentID: "agent", RuntimeEpoch: 1, Generation: 1, Sequence: 1}
			notice, ok := NoticeFromEvent(AgentEvent{Type: test.kind, AgentID: "agent", Data: test.data, Identity: identity})
			if !ok || notice.Identity != identity {
				t.Fatal("typed notice lost exact event identity")
			}
			ws.ApplyAgentNotice(notice)
			got := center.Notifications(true)
			if len(got) != 1 || got[0].Summary != test.summary || got[0].Transient != test.transient ||
				got[0].Occurrences != 1 || got[0].Owner.SessionID != "session" || got[0].Owner.ID != "agent" {
				t.Fatalf("canonical ingress lost feedback or attribution: %+v", got)
			}
			center.Dismiss(got[0].ID)
			center.ResolveNotification(got[0].ID)
			before, version := center.Notifications(true), center.Version()
			for range 200 {
				ws.ApplyAgentNotice(notice)
			}
			if center.Version() != version || !reflect.DeepEqual(before, center.Notifications(true)) {
				t.Fatal("exact source-event replay reset notification state")
			}
			notice.Identity.EventID, notice.Identity.Sequence = "next-source-event", 2
			ws.ApplyAgentNotice(notice)
			got = center.Notifications(true)
			if len(got) != 2 || got[0].ID == got[1].ID || got[0].Occurrences != 1 {
				t.Fatal("a genuine later service event was suppressed")
			}
		})
	}
}

func TestAgentServiceBareNoticesRecordEachCanonicalEvent(t *testing.T) {
	center := NewStatusCenter(nil, nil)
	defer center.Close()
	ws := commandNoticeWorkspace(center)
	ws.RootPath = "project"
	// Memory extraction currently emits a workspace service event, without an
	// agent identity. Never infer its owner from an attached shell's focus.
	notice, ok := NoticeFromEvent(AgentEvent{Type: NoticeMemorySaved, Data: map[string]any{"count": 1}})
	if !ok {
		t.Fatal("bare memory event did not become a typed notice")
	}
	ws.ApplyAgentNotice(notice)
	got := center.Notifications(true)
	if len(got) != 1 || got[0].Owner != (types.ResourceRef{Kind: types.ResourceWorkspace, ID: "project"}) ||
		got[0].Summary != "Memory: saved 1 from this session" || !got[0].Read || !got[0].Transient {
		t.Fatalf("bare feedback lost its actual owner: %+v", got)
	}
	// These are two actual source events, not two shell deliveries of one event.
	ws.ApplyAgentNotice(notice)
	if got := center.Notifications(true); len(got) != 1 || got[0].Occurrences != 2 {
		t.Fatal("a genuine repeated bare source event was suppressed")
	}
	version := center.Version()
	notice.Count = 0
	ws.ApplyAgentNotice(notice)
	notice.Count = 1
	notice.AgentID = "agent"
	notice.Identity = types.RuntimeEventIdentity{EventID: "wrong-owner", OwnerID: "other-session", AgentID: "agent", RuntimeEpoch: 1, Generation: 1, Sequence: 1}
	ws.ApplyAgentNotice(notice)
	if center.Version() != version {
		t.Fatal("empty memory result or wrong source owner published feedback")
	}
}

func TestAgentCommandNoticeLifetimeOutlivesAgentTurn(t *testing.T) {
	for _, event := range []string{"done", "error"} {
		t.Run(event, func(t *testing.T) {
			center := NewStatusCenter(nil, nil)
			defer center.Close()
			ws := commandNoticeWorkspace(center)
			notice := failedCommandNotice()
			notice.Commands[0].Status, notice.Commands[0].EndedAt, notice.Commands[0].Error = fileio.AsyncRunning, time.Time{}, ""
			ws.ApplyAgentNotice(notice)
			commandID := center.Operations()[0].ID
			center.SetOperation(types.OperationSnapshot{ID: "request", Owner: types.ResourceRef{Kind: types.ResourceAgent, ID: "agent"},
				Kind: "agent-request", State: types.OperationRunning})
			ws.ApplyAgentStatusEvent(AgentEvent{Type: event, AgentID: "agent", Data: map[string]any{"reason": "completed"}})
			requestState := types.OperationSucceeded
			if event == "error" {
				requestState = types.OperationFailed
			}
			for _, op := range center.Operations() {
				switch op.ID {
				case commandID:
					if op.State != types.OperationRunning || !op.Cancellable {
						t.Fatalf("agent turn ended a command still owned by its manager: %+v", op)
					}
				case "request":
					if op.State != requestState {
						t.Fatalf("request did not retain its real turn outcome: %+v", op)
					}
				}
			}
			notice.Commands[0].Status, notice.Commands[0].EndedAt, notice.Commands[0].Error = fileio.AsyncFailed, time.Now(), "exit status 1"
			ws.ApplyAgentNotice(notice)
			version := center.Version()
			for range 200 {
				ws.ApplyAgentNotice(notice)
			}
			failures := 0
			for _, n := range center.Notifications(true) {
				if n.Source == "background-command" {
					failures++
					if n.Occurrences != 1 {
						t.Fatal("command settlement repeated after the owning turn ended")
					}
				}
			}
			if failures != 1 || center.Version() != version {
				t.Fatal("command settlement was lost or repeated after turn completion")
			}
		})
	}
}
