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
