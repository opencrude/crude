package remote

import (
	"testing"

	"crude/types"
)

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
