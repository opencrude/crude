package viewmodel

import (
	"testing"
	"time"

	ttypes "crude/types"
)

// The notice row's trailing column is a short age. "Read" is the normal
// state and is never repeated on every row (the reported clutter was
// "Read  12h46m ago" on each line).
func TestNoticeAgeColumnIsShortAndMarksOnlyExceptions(t *testing.T) {
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	updated := now.Add(-12*time.Hour - 46*time.Minute)
	cases := []struct {
		notice ttypes.NotificationRecord
		want   string
	}{
		{ttypes.NotificationRecord{UpdatedAt: updated, Read: true}, "12h"},
		{ttypes.NotificationRecord{UpdatedAt: updated}, "● 12h"},
		{ttypes.NotificationRecord{UpdatedAt: now.Add(-27 * time.Second), Read: true}, "now"},
		{ttypes.NotificationRecord{UpdatedAt: updated, Read: true, ResolvedAt: now}, "Resolved · 12h"},
	}
	for _, c := range cases {
		if got := noticeAge(c.notice, now); got != c.want {
			t.Errorf("noticeAge(read=%v resolved=%v) = %q, want %q", c.notice.Read, !c.notice.ResolvedAt.IsZero(), got, c.want)
		}
	}
}
