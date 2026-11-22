package remote

import (
	"fmt"
	"testing"
	"time"

	"crude/types"
)

func denseNoticeReplica() (*statusReplica, time.Time) {
	now := time.Now()
	var notices []types.NotificationRecord
	for i := range 153 {
		notices = append(notices, types.NotificationRecord{ID: fmt.Sprint(i), UpdatedAt: now.Add(-time.Duration(i) * time.Second)})
	}
	s := newStatusReplica(nil)
	s.install(statusSnapshot{Version: 1, Notifications: notices})
	return s, now
}

func BenchmarkDenseNoticeFooterReads(b *testing.B) {
	s, now := denseNoticeReplica()
	b.ReportAllocs()
	for b.Loop() {
		_ = s.UnreadCount()
		_, _ = s.LatestTransient(now, 5*time.Second)
	}
}

func TestDenseNoticeFooterReads(t *testing.T) {
	s, now := denseNoticeReplica()
	if n := testing.AllocsPerRun(10, func() {
		if s.UnreadCount() != 153 {
			t.Fatal("wrong unread count")
		}
		if _, ok := s.LatestTransient(now, time.Second); ok {
			t.Fatal("history shown as a toast")
		}
	}); n != 0 {
		t.Fatalf("reading a footer badge and toast allocated %v times per frame", n)
	}
	s.transient = []types.NotificationRecord{{ID: "local", Transient: true, UpdatedAt: now}}
	s.install(statusSnapshot{Version: 2, Notifications: []types.NotificationRecord{
		{ID: "dismissed", Dismissed: true}, {ID: "read", Read: true}, {ID: "unread"},
		{ID: "core", Transient: true, UpdatedAt: now.Add(time.Second)},
	}})
	if toast, ok := s.LatestTransient(now.Add(time.Second), time.Second); !ok || toast.ID != "core" || s.UnreadCount() != 1 {
		t.Fatalf("wrong newest toast or unread count: %+v", toast)
	}
	if _, ok := s.LatestTransient(now.Add(time.Minute), time.Second); ok {
		t.Fatal("expired toast stayed visible")
	}
}

func TestStatusReplicaRejectsOlderSnapshotWithoutLosingNotices(t *testing.T) {
	s := newStatusReplica(nil)
	s.install(statusSnapshot{Version: 4, Notifications: []types.NotificationRecord{{ID: "current", Summary: "Current evidence"}}})
	if s.install(statusSnapshot{Version: 3}) || len(s.Notifications(false)) != 1 {
		t.Fatal("older status snapshot erased current Notices")
	}
	s.resetRevision()
	if !s.install(statusSnapshot{Version: 1, Notifications: []types.NotificationRecord{{ID: "recovered", Summary: "Cold recovered notice"}}}) || s.Notifications(false)[0].ID != "recovered" {
		t.Fatal("reconnected core could not publish its new version domain")
	}
}
