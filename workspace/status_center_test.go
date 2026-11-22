package workspace

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"crude/types/statuscenter"
)

func TestStatusCenterDeduplicatesAndPersistsNotificationState(t *testing.T) {
	wdb := openTestWorkspaceDB(t)
	center := NewStatusCenter(wdb.DB, nopLogger{})
	id := center.Notify(statuscenter.NotificationRecord{
		DedupKey: "lsp:gopls", Owner: statuscenter.ResourceRef{Kind: statuscenter.ResourceLSP, ID: "gopls"},
		Severity: statuscenter.SeverityError, Source: "lsp", Summary: "gopls failed", Detail: "first",
	})
	if got := center.Notify(statuscenter.NotificationRecord{
		DedupKey: "lsp:gopls", Owner: statuscenter.ResourceRef{Kind: statuscenter.ResourceLSP, ID: "gopls"},
		Severity: statuscenter.SeverityError, Source: "lsp", Summary: "gopls failed", Detail: "second",
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
	center.SetOperation(statuscenter.OperationSnapshot{ID: "running", Owner: statuscenter.ResourceRef{Kind: statuscenter.ResourceAgent, ID: "agent"}, Label: "Waiting", State: statuscenter.OperationWaiting, StartedAt: time.Now()})
	center.SetOperation(statuscenter.OperationSnapshot{ID: "done", Owner: statuscenter.ResourceRef{Kind: statuscenter.ResourceFile, ID: "file"}, Label: "Saved", State: statuscenter.OperationSucceeded, StartedAt: time.Now().Add(-time.Second)})
	center.SetIssue(statuscenter.IssueSnapshot{ID: "save", Owner: statuscenter.ResourceRef{Kind: statuscenter.ResourceFile, ID: "file"}, Severity: statuscenter.SeverityError, Summary: "Save failed"})
	center.Notify(statuscenter.NotificationRecord{DedupKey: "save", Severity: statuscenter.SeverityError, Summary: "Save failed"})
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
		center.Notify(statuscenter.NotificationRecord{Severity: statuscenter.SeverityError, Summary: fmt.Sprintf("failure %d", i)})
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
	agent := statuscenter.AgentStatusSnapshot{Ref: statuscenter.ResourceRef{ID: "agent"}}
	op := statuscenter.OperationSnapshot{ID: "op", Owner: statuscenter.ResourceRef{ID: "agent"}, State: statuscenter.OperationRunning}
	issue := statuscenter.IssueSnapshot{ID: "issue", Summary: "failed"}
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
		{"finish", func() { c.FinishOperation("op", statuscenter.OperationSucceeded, "") }, true},
		{"finished", func() { c.FinishOperation("op", statuscenter.OperationSucceeded, "") }, false},
		{"second operation", func() { op.ID = "op2"; c.SetOperation(op) }, true},
		{"finish owner", func() { c.FinishOperations("agent", statuscenter.OperationCancelled, "") }, true},
		{"issue", func() { c.SetIssue(issue) }, true},
		{"same issue", func() { c.SetIssue(issue) }, false},
		{"resolve issue", func() { c.ResolveIssue("issue") }, true},
		{"resolved issue", func() { c.ResolveIssue("issue") }, false},
		{"notice", func() { notice = c.Notify(statuscenter.NotificationRecord{Summary: "notice", DedupKey: "notice"}) }, true},
		{"dedup occurrence", func() { c.Notify(statuscenter.NotificationRecord{Summary: "notice", DedupKey: "notice"}) }, true},
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
	writer.Notify(statuscenter.NotificationRecord{Summary: "saved externally"})
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

func TestStatusCenterExactEvidenceIsRecordedOnce(t *testing.T) {
	center := NewStatusCenter(nil, nil)
	defer center.Close()
	n := statuscenter.NotificationRecord{ID: "immutable-outcome", DedupKey: "immutable-outcome", Summary: "Background command failed", Severity: statuscenter.SeverityError}
	var wg sync.WaitGroup
	for range 200 {
		wg.Go(func() { center.notifyOnce(n) })
	}
	wg.Wait()
	got := center.Notifications(true)
	if len(got) != 1 || got[0].Occurrences != 1 || center.Version() != 1 {
		t.Fatalf("exact evidence published multiple occurrences: %+v version=%d", got, center.Version())
	}
	center.Dismiss(n.ID)
	center.ResolveNotification(n.ID)
	updated := center.Notifications(true)[0].UpdatedAt
	version := center.Version()
	for range 200 {
		wg.Go(func() { center.notifyOnce(n) })
	}
	wg.Wait()
	got = center.Notifications(true)
	if len(got) != 1 || !got[0].Read || !got[0].Dismissed || got[0].ResolvedAt.IsZero() ||
		!got[0].UpdatedAt.Equal(updated) || center.Version() != version {
		t.Fatalf("exact evidence replay reset user state: %+v version=%d", got, center.Version())
	}
}
