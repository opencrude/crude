package workspace

import (
	"fmt"
	"testing"
	"time"

	types "crude/types"
)

func TestStatusCenterDeduplicatesAndPersistsNotificationState(t *testing.T) {
	wdb := openTestWorkspaceDB(t)
	center := NewStatusCenter(wdb.DB, nopLogger{})
	id := center.Notify(types.NotificationRecord{
		DedupKey: "lsp:gopls", Owner: types.ResourceRef{Kind: types.ResourceLSP, ID: "gopls"},
		Severity: types.SeverityError, Source: "lsp", Summary: "gopls failed", Detail: "first",
	})
	if got := center.Notify(types.NotificationRecord{
		DedupKey: "lsp:gopls", Owner: types.ResourceRef{Kind: types.ResourceLSP, ID: "gopls"},
		Severity: types.SeverityError, Source: "lsp", Summary: "gopls failed", Detail: "second",
	}); got != id {
		t.Fatalf("deduplicated ID changed: %q != %q", got, id)
	}
	if !center.MarkRead(id, true) || !center.ResolveNotification(id) {
		t.Fatal("could not update notification lifecycle")
	}
	center.Close()

	reopened := NewStatusCenter(wdb.DB, nopLogger{})
	defer reopened.Close()
	if err := reopened.Hydrate(); err != nil {
		t.Fatal(err)
	}
	notices := reopened.Notifications(false)
	if len(notices) != 1 || notices[0].ID != id || notices[0].Occurrences != 2 || notices[0].Detail != "second" || !notices[0].Read || notices[0].ResolvedAt.IsZero() {
		t.Fatalf("notification did not round-trip: %+v", notices)
	}
}

func TestStatusCenterKeepsActivityAndIssueCountersIndependent(t *testing.T) {
	center := NewStatusCenter(nil, nil)
	defer center.Close()
	center.SetOperation(types.OperationSnapshot{ID: "running", Owner: types.ResourceRef{Kind: types.ResourceAgent, ID: "agent"}, Label: "Waiting", State: types.OperationWaiting, StartedAt: time.Now()})
	center.SetOperation(types.OperationSnapshot{ID: "done", Owner: types.ResourceRef{Kind: types.ResourceFile, ID: "file"}, Label: "Saved", State: types.OperationSucceeded, StartedAt: time.Now().Add(-time.Second)})
	center.SetIssue(types.IssueSnapshot{ID: "save", Owner: types.ResourceRef{Kind: types.ResourceFile, ID: "file"}, Severity: types.SeverityError, Summary: "Save failed"})
	center.Notify(types.NotificationRecord{DedupKey: "save", Severity: types.SeverityError, Summary: "Save failed"})
	if operations := center.Operations(); len(operations) != 2 || operations[0].ID != "running" {
		t.Fatalf("active operation was not sorted before recent history: %+v", operations)
	}
	if center.UnreadCount() != 1 || len(center.Issues()) != 1 {
		t.Fatalf("issue and unread notice were conflated: unread=%d issues=%d", center.UnreadCount(), len(center.Issues()))
	}
	center.MarkRead(center.Notifications(false)[0].ID, true)
	if center.UnreadCount() != 0 || center.Issues()[0].ResolvedAt.IsZero() == false {
		t.Fatal("reading a notice incorrectly resolved the live issue")
	}
}

func TestStatusCenterBoundsNotificationHistory(t *testing.T) {
	center := NewStatusCenter(nil, nil)
	defer center.Close()
	for i := 0; i < statusNotificationLimit+100; i++ {
		center.Notify(types.NotificationRecord{Severity: types.SeverityError, Summary: fmt.Sprintf("failure %d", i)})
	}
	notices := center.Notifications(false)
	if len(notices) != statusNotificationLimit {
		t.Fatalf("notification history=%d want=%d", len(notices), statusNotificationLimit)
	}
	if notices[0].Summary != fmt.Sprintf("failure %d", statusNotificationLimit+99) {
		t.Fatalf("newest notification was not retained: %q", notices[0].Summary)
	}
}

func TestStatusCenterChangedBroadcastsEverySemanticMutation(t *testing.T) {
	c := NewStatusCenter(nil, nil)
	defer c.Close()
	var notice string
	agent := types.AgentStatusSnapshot{Ref: types.ResourceRef{ID: "agent"}}
	op := types.OperationSnapshot{ID: "op", Owner: types.ResourceRef{ID: "agent"}, State: types.OperationRunning}
	issue := types.IssueSnapshot{ID: "issue", Summary: "failed"}
	for _, test := range []struct {
		name    string
		mutate  func()
		changed bool
	}{
		{"agent", func() { c.SetAgent(agent) }, true},
		{"same agent", func() { c.SetAgent(agent) }, false},
		{"remove", func() { c.RemoveAgent("agent") }, true},
		{"missing agent", func() { c.RemoveAgent("agent") }, false},
		{"operation", func() { c.SetOperation(op) }, true},
		{"same operation", func() { c.SetOperation(op) }, false},
		{"finish", func() { c.FinishOperation("op", types.OperationSucceeded, "") }, true},
		{"finished", func() { c.FinishOperation("op", types.OperationSucceeded, "") }, false},
		{"second operation", func() { op.ID = "op2"; c.SetOperation(op) }, true},
		{"finish owner", func() { c.FinishOperations("agent", types.OperationCancelled, "") }, true},
		{"issue", func() { c.SetIssue(issue) }, true},
		{"same issue", func() { c.SetIssue(issue) }, false},
		{"resolve issue", func() { c.ResolveIssue("issue") }, true},
		{"resolved issue", func() { c.ResolveIssue("issue") }, false},
		{"notice", func() { notice = c.Notify(types.NotificationRecord{Summary: "notice", DedupKey: "notice"}) }, true},
		{"dedup occurrence", func() { c.Notify(types.NotificationRecord{Summary: "notice", DedupKey: "notice"}) }, true},
		{"read", func() { c.MarkRead(notice, true) }, true},
		{"already read", func() { c.MarkRead(notice, true) }, false},
		{"resolve notice", func() { c.ResolveNotification(notice) }, true},
		{"resolved notice", func() { c.ResolveNotification(notice) }, false},
		{"dismiss", func() { c.Dismiss(notice) }, true},
		{"dismissed", func() { c.Dismiss(notice) }, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			before, wake := c.Changed()
			_, second := c.Changed()
			if wake == nil || wake != second {
				t.Fatal("unchanged owner did not reuse broadcast")
			}
			test.mutate()
			after, next := c.Changed()
			select {
			case <-wake:
				if !test.changed || after <= before || next == wake {
					t.Fatal("unexpected change broadcast")
				}
			default:
				if test.changed || after != before || next != wake {
					t.Fatal("semantic mutation did not broadcast")
				}
			}
		})
	}
}

func TestStatusCenterHydrateBroadcastsOnlyNewContent(t *testing.T) {
	wdb := openTestWorkspaceDB(t)
	writer := NewStatusCenter(wdb.DB, nil)
	writer.Notify(types.NotificationRecord{Summary: "saved externally"})
	writer.Close()
	c := NewStatusCenter(wdb.DB, nil)
	defer c.Close()
	_, wake := c.Changed()
	if err := c.Hydrate(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-wake:
	default:
		t.Fatal("hydration did not wake status owner")
	}
	if got := c.Notifications(false); len(got) != 1 || got[0].Summary != "saved externally" {
		t.Fatal(got)
	}
	version, wake := c.Changed()
	if err := c.Hydrate(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-wake:
		t.Fatal("duplicate hydration broadcast")
	default:
	}
	if c.Version() != version {
		t.Fatal("duplicate hydration advanced version")
	}
}

func TestStatusCenterChangedSupportsLiteralFixture(t *testing.T) {
	c := &StatusCenter{}
	version, wake := c.Changed()
	if version != 0 || wake == nil {
		t.Fatal("literal fixture has no broadcast")
	}
	c.mu.Lock()
	c.bumpVersionLocked()
	c.mu.Unlock()
	select {
	case <-wake:
	default:
		t.Fatal("literal fixture lost mutation")
	}
}
