package viewmodel

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"crude/core/port"
	"crude/tui/screens/termdock"
	"crude/types"
)

type noticeHistoryReadCounter struct {
	shellport.StatusService
	reads int
}

func (c *noticeHistoryReadCounter) Notifications(dismissed bool) []types.NotificationRecord {
	c.reads++
	return c.StatusService.Notifications(dismissed)
}

type noticeHistoryPort struct {
	shellport.Workspace
	counter *noticeHistoryReadCounter
}

func (p *noticeHistoryPort) Status() shellport.StatusService { return p.counter }

func TestTickReadsNoticeHistoryOnlyForVisiblePanel(t *testing.T) {
	app := newFullTestApp(t)
	status := &noticeHistoryReadCounter{StatusService: app.ws.Status()}
	app.ws = &noticeHistoryPort{app.ws, status}
	status.Notify(types.NotificationRecord{Summary: "Saved notice"})
	app.handleTick(types.TickMsg(time.Now()))
	if status.reads != 0 {
		t.Fatalf("hidden Notices read full history %d times on tick", status.reads)
	}
	app.toggleWorkspacePanel(termdock.PanelNotices)
	app.paintShellFrame()
	if status.reads == 0 || status.UnreadCount() != 0 || !strings.Contains(bufferPlain(app.mainSurf.Buffer()), "Saved notice") {
		t.Fatalf("opening Notices did not read and acknowledge current history")
	}
	app.toggleWorkspacePanel(termdock.PanelNotices)
	status.Notify(types.NotificationRecord{Summary: "Another notice"})
	status.reads = 0
	app.handleTick(types.TickMsg(time.Now()))
	if status.reads != 0 || status.UnreadCount() != 1 {
		t.Fatalf("hidden history read or acknowledged: reads=%d unread=%d", status.reads, status.UnreadCount())
	}
}

func BenchmarkTickHiddenNotices(b *testing.B) {
	app := newFullTestApp(b)
	for i := range 153 {
		app.ws.Status().Notify(types.NotificationRecord{Summary: fmt.Sprintf("Notice %d", i)})
	}
	b.ReportAllocs()
	for b.Loop() {
		// Sample the refresh tick, not the cheap ticks between clock updates.
		app.workspacePanelLastSecond = 0
		app.handleTick(types.TickMsg(time.Now()))
	}
}
