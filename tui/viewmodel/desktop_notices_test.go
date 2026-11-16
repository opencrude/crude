package viewmodel

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/desktopnotify"
	"crude/shellport"
	ttypes "crude/types"
	"crude/workspace"
)

type recordingNotifier struct {
	shown     []desktopnotify.Notification
	withdrawn []string
	events    chan desktopnotify.Event
}

func (r *recordingNotifier) Show(n desktopnotify.Notification) { r.shown = append(r.shown, n) }
func (r *recordingNotifier) Withdraw(key string)               { r.withdrawn = append(r.withdrawn, key) }
func (r *recordingNotifier) Events() <-chan desktopnotify.Event {
	return r.events
}

type presenceReport struct{ present, notifies bool }

// presenceWorkspace is a shell attachment that records presence reports
// and serves a chosen notification setting.
type presenceWorkspace struct {
	shellport.Workspace
	settings ttypes.AppSettings
	reports  []presenceReport
}

func (w *presenceWorkspace) ReportPresence(present, notifies bool) {
	w.reports = append(w.reports, presenceReport{present, notifies})
}
func (w *presenceWorkspace) LoadAppSettings() ttypes.AppSettings { return w.settings }

type noticeHarness struct {
	t      *testing.T
	app    *App
	ws     *presenceWorkspace
	sink   *recordingNotifier
	status *workspace.StatusCenter
	now    time.Time
}

// newNoticeHarness returns a shell whose notifier is ready, whose terminal
// lost focus, and whose status baseline is empty.
func newNoticeHarness(t *testing.T) *noticeHarness {
	t.Helper()
	app := newTestApp(t)
	status, ok := app.ws.Status().(*workspace.StatusCenter)
	if !ok {
		t.Fatalf("test workspace status is %T", app.ws.Status())
	}
	ws := &presenceWorkspace{Workspace: app.ws}
	app.ws = ws
	sink := &recordingNotifier{events: make(chan desktopnotify.Event, 4)}
	app.ConfigureDesktopNotifications(sink)
	h := &noticeHarness{t: t, app: app, ws: ws, sink: sink, status: status, now: time.Now()}
	app.applyDesktopNoticeEvent(desktopNoticeEventMsg{sink: sink, event: desktopnotify.Event{Kind: desktopnotify.Ready}, ok: true})
	h.blur()
	h.observe()
	return h
}

func (h *noticeHarness) blur() {
	h.app.terminalBlurred = true
	h.app.observeDesktopPresence(h.now, false)
}

func (h *noticeHarness) focus() {
	h.app.terminalBlurred = false
	h.app.observeDesktopPresence(h.now, true)
}

func (h *noticeHarness) elect(present, notify bool) tea.Cmd {
	return h.app.applyDesktopElection(shellport.PresenceChangedMsg{Owner: h.ws, Present: present, Notify: notify})
}

func (h *noticeHarness) observe() tea.Cmd { return h.app.observeDesktopStatus(h.now) }

func (h *noticeHarness) setAgent(id, label string, phase ttypes.AgentPhase, needsAction bool, turn uint64) tea.Cmd {
	h.status.SetAgent(ttypes.AgentStatusSnapshot{Ref: ttypes.ResourceRef{Kind: ttypes.ResourceAgent, ID: id}, Label: label, Phase: phase, NeedsAction: needsAction, TurnGeneration: turn})
	return h.observe()
}

// settle advances past the settle delay and delivers its timer.
func (h *noticeHarness) settle() {
	h.now = h.now.Add(desktopNoticeSettle)
	h.app.flushDesktopNotice(desktopNoticeSettleMsg{generation: h.app.desktop.settleGen}, h.now)
}

func (h *noticeHarness) lastShown() desktopnotify.Notification {
	h.t.Helper()
	if len(h.sink.shown) == 0 {
		h.t.Fatal("no desktop notification was shown")
	}
	return h.sink.shown[len(h.sink.shown)-1]
}

func TestDesktopNoticeAwayUserGetsOneMessageForAQuestion(t *testing.T) {
	h := newNoticeHarness(t)
	h.elect(false, true)
	if cmd := h.setAgent("a1", "Ivan", ttypes.AgentQuestion, true, 1); cmd == nil {
		t.Fatal("a question for an away user must start the settle timer")
	}
	if len(h.sink.shown) != 0 {
		t.Fatal("the message must wait for the settle delay")
	}
	h.settle()
	shown := h.lastShown()
	if shown.Title != "Ivan needs your answer" || shown.Quiet {
		t.Fatalf("message = %+v, want a banner that names the agent", shown)
	}
	if !strings.Contains(shown.Body, h.app.ws.RootPath()[strings.LastIndexAny(h.app.ws.RootPath(), `/\`)+1:]) {
		t.Fatalf("body %q does not name the workspace", shown.Body)
	}
	h.settle()
	h.observe()
	if len(h.sink.shown) != 1 {
		t.Fatalf("the same state was shown %d times", len(h.sink.shown))
	}
}

func TestDesktopNoticeNeverAnnouncesWhatTheUserSaw(t *testing.T) {
	h := newNoticeHarness(t)
	// The user is present in another shell when the question arrives.
	h.elect(true, false)
	h.setAgent("a1", "Ivan", ttypes.AgentQuestion, true, 1)
	// Later the user leaves every shell and this one is elected.
	h.elect(false, true)
	h.settle()
	if len(h.sink.shown) != 0 {
		t.Fatalf("seen news was announced: %+v", h.sink.shown)
	}
}

func TestDesktopNoticeBaselineIsNotNews(t *testing.T) {
	app := newTestApp(t)
	status := app.ws.Status().(*workspace.StatusCenter)
	status.SetAgent(ttypes.AgentStatusSnapshot{Ref: ttypes.ResourceRef{Kind: ttypes.ResourceAgent, ID: "old"}, Label: "Old", Phase: ttypes.AgentQuestion, NeedsAction: true})
	ws := &presenceWorkspace{Workspace: app.ws}
	app.ws = ws
	sink := &recordingNotifier{events: make(chan desktopnotify.Event, 1)}
	app.ConfigureDesktopNotifications(sink)
	app.applyDesktopNoticeEvent(desktopNoticeEventMsg{sink: sink, event: desktopnotify.Event{Kind: desktopnotify.Ready}, ok: true})
	app.terminalBlurred = true
	app.observeDesktopPresence(time.Now(), false)
	app.applyDesktopElection(shellport.PresenceChangedMsg{Owner: ws, Notify: true})
	app.observeDesktopStatus(time.Now())
	app.flushDesktopNotice(desktopNoticeSettleMsg{generation: app.desktop.settleGen}, time.Now().Add(time.Minute))
	if len(sink.shown) != 0 {
		t.Fatalf("a question that existed at attach was announced: %+v", sink.shown)
	}
}

func TestDesktopNoticeOnlyTheElectedShellNotifies(t *testing.T) {
	h := newNoticeHarness(t)
	h.elect(false, false)
	h.setAgent("a1", "Ivan", ttypes.AgentApproval, true, 1)
	h.settle()
	if len(h.sink.shown) != 0 {
		t.Fatal("a shell that the core did not elect showed a message")
	}
	// The elected shell closes; the core elects this one. The news is still
	// unseen, so this shell takes it over.
	h.elect(false, true)
	h.settle()
	if got := h.lastShown().Title; got != "Ivan needs your approval" {
		t.Fatalf("title = %q", got)
	}
}

func TestDesktopNoticeWithdrawsWhenTheUserReturnsOrTheAgentStopsWaiting(t *testing.T) {
	h := newNoticeHarness(t)
	h.elect(false, true)
	h.setAgent("a1", "Ivan", ttypes.AgentQuestion, true, 1)
	h.settle()
	key := h.lastShown().Key
	h.focus()
	if len(h.sink.withdrawn) != 1 || h.sink.withdrawn[0] != key {
		t.Fatalf("withdrawn = %v, want %q when the user returns", h.sink.withdrawn, key)
	}
	if last := h.ws.reports[len(h.ws.reports)-1]; !last.present || !last.notifies {
		t.Fatalf("last report = %+v, want present and able to notify", last)
	}

	h.blur()
	h.elect(false, true)
	h.setAgent("a2", "Olga", ttypes.AgentQuestion, true, 1)
	h.settle()
	if got := h.lastShown().Title; got != "Olga needs your answer" {
		t.Fatalf("title = %q; the question that the user saw must not return", got)
	}
	// The question is answered in another shell.
	h.setAgent("a2", "Olga", ttypes.AgentTool, false, 1)
	if got := h.sink.withdrawn[len(h.sink.withdrawn)-1]; got != key {
		t.Fatalf("withdrawn = %v after the answer", h.sink.withdrawn)
	}
}

func TestDesktopNoticeQuestionAnsweredAtOnceMakesNoMessage(t *testing.T) {
	h := newNoticeHarness(t)
	h.elect(false, true)
	h.setAgent("a1", "Ivan", ttypes.AgentApproval, true, 1)
	h.setAgent("a1", "Ivan", ttypes.AgentTool, false, 1)
	h.settle()
	if len(h.sink.shown) != 0 {
		t.Fatalf("a request approved at once made a message: %+v", h.sink.shown)
	}
}

func TestDesktopNoticeBurstMakesOneMessageAndLaterNewsIsQuiet(t *testing.T) {
	h := newNoticeHarness(t)
	h.elect(false, true)
	h.setAgent("a1", "Ivan", ttypes.AgentCompleted, false, 1)
	h.setAgent("a2", "Olga", ttypes.AgentQuestion, true, 1)
	h.settle()
	if len(h.sink.shown) != 1 {
		t.Fatalf("a burst made %d messages", len(h.sink.shown))
	}
	first := h.lastShown()
	if first.Title != "1 agent needs you, 1 finished" {
		t.Fatalf("title = %q", first.Title)
	}
	if !strings.HasPrefix(first.Body, "Olga needs your answer\nIvan finished") || first.Quiet {
		t.Fatalf("message = %+v, want the most urgent first, as a banner", first)
	}
	// Ten seconds later another agent finishes: same message, no banner.
	h.now = h.now.Add(10 * time.Second)
	h.setAgent("a3", "Petr", ttypes.AgentCompleted, false, 1)
	h.settle()
	second := h.lastShown()
	if second.Key != first.Key || !second.Quiet || !strings.Contains(second.Body, "Petr finished") {
		t.Fatalf("update = %+v, want a quiet replacement of %q", second, first.Key)
	}
	// A second request for the user within the cooldown is not urgent news:
	// the banner already asked for the user.
	h.setAgent("a5", "Rita", ttypes.AgentApproval, true, 1)
	h.settle()
	if got := h.lastShown(); !got.Quiet || !strings.Contains(got.Body, "Rita needs your approval") {
		t.Fatalf("update = %+v, want a quiet update that lists Rita", got)
	}
	// After the cooldown, news gets a banner again.
	h.now = h.now.Add(desktopNoticeCooldown)
	h.setAgent("a6", "Lev", ttypes.AgentCompleted, false, 1)
	h.settle()
	if got := h.lastShown(); got.Quiet {
		t.Fatalf("news after the cooldown must get a banner: %+v", got)
	}
}

func TestDesktopNoticeMoreUrgentNewsInterruptsTheCooldown(t *testing.T) {
	h := newNoticeHarness(t)
	h.elect(false, true)
	h.setAgent("a1", "Ivan", ttypes.AgentCompleted, false, 1)
	h.settle()
	if h.lastShown().Quiet {
		t.Fatal("the first message must be a banner")
	}
	h.now = h.now.Add(5 * time.Second)
	h.setAgent("a2", "Olga", ttypes.AgentQuestion, true, 1)
	h.settle()
	got := h.lastShown()
	if got.Quiet || got.Title != "1 agent needs you, 1 finished" {
		t.Fatalf("a question after a finish must get a banner at once: %+v", got)
	}
}

func TestDesktopNoticeTitlesCountNews(t *testing.T) {
	items := []*noticeItem{{label: "A", kind: noticeAnswer}, {label: "B", kind: noticeApproval}, {label: "C", kind: noticeFinished}, {label: "D", kind: noticeFinished}, {label: "E", kind: noticeFinished}}
	title, body := desktopNoticeText(items, "crude")
	if title != "2 agents need you, 3 finished" {
		t.Fatalf("title = %q", title)
	}
	if body != "A needs your answer\nB needs your approval\nC finished\n2 more\ncrude" {
		t.Fatalf("body = %q", body)
	}
	title, _ = desktopNoticeText(items[2:], "crude")
	if title != "3 agents finished" {
		t.Fatalf("title = %q", title)
	}
	title, body = desktopNoticeText([]*noticeItem{{label: "A", kind: noticeStopped, detail: "Provider quota reached"}}, "crude")
	if title != "A stopped" || body != "Provider quota reached · crude" {
		t.Fatalf("stopped = %q / %q", title, body)
	}
}

func TestDesktopNoticeKindSwitchesSkipUnwantedNews(t *testing.T) {
	h := newNoticeHarness(t)
	off := false
	h.ws.settings.UI.DesktopNotifications.Finished = &off
	h.elect(false, true)
	h.setAgent("a1", "Ivan", ttypes.AgentCompleted, false, 1)
	h.settle()
	if len(h.sink.shown) != 0 {
		t.Fatalf("finished work was announced with Finished work off: %+v", h.sink.shown)
	}
	h.setAgent("a2", "Olga", ttypes.AgentNeedsAttention, false, 1)
	h.settle()
	if got := h.lastShown().Title; got != "Olga stopped" {
		t.Fatalf("title = %q", got)
	}
	h.ws.settings.UI.DesktopNotifications.Enabled = &off
	h.app.reportDesktopPresence()
	h.app.syncDesktopNotice()
	if last := h.ws.reports[len(h.ws.reports)-1]; last.notifies {
		t.Fatal("a shell with notifications off must tell the core that it cannot notify")
	}
	if len(h.sink.withdrawn) == 0 {
		t.Fatal("turning notifications off must withdraw the message")
	}
}

func notAeron() bool { return false }

func TestDesktopNoticeChildAndAeronNewsStaysQuiet(t *testing.T) {
	if kind, ok := desktopNoticeKind(ttypes.AgentStatusSnapshot{Ref: ttypes.ResourceRef{ID: "c", ParentID: "p"}, Phase: ttypes.AgentCompleted}, notAeron); ok {
		t.Fatalf("a child's finish is news: %v", kind)
	}
	if _, ok := desktopNoticeKind(ttypes.AgentStatusSnapshot{Ref: ttypes.ResourceRef{ID: "a"}, Phase: ttypes.AgentFailed}, func() bool { return true }); ok {
		t.Fatal("the workspace Aeron's stop is news")
	}
	if kind, ok := desktopNoticeKind(ttypes.AgentStatusSnapshot{Ref: ttypes.ResourceRef{ID: "c", ParentID: "p"}, Phase: ttypes.AgentApproval, NeedsAction: true}, notAeron); !ok || kind != noticeApproval {
		t.Fatalf("a child's approval request must reach the user: %v %v", kind, ok)
	}
	if _, ok := desktopNoticeKind(ttypes.AgentStatusSnapshot{Ref: ttypes.ResourceRef{ID: "a"}, Phase: ttypes.AgentCancelled}, notAeron); ok {
		t.Fatal("a run that the user cancelled is news")
	}
}

func TestDesktopNoticeIdleFocusedTerminalCountsAsAway(t *testing.T) {
	h := newNoticeHarness(t)
	h.focus()
	start := h.now
	// A tick before the idle limit keeps the user present.
	h.app.observeDesktopPresence(start.Add(time.Duration(ttypes.DesktopNotificationAwayDefault)*time.Minute-time.Second), false)
	if last := h.ws.reports[len(h.ws.reports)-1]; !last.present {
		t.Fatal("a focused terminal is present until the idle limit")
	}
	// A question arrives two minutes after the last input; the user is gone.
	h.now = start.Add(2 * time.Minute)
	h.setAgent("a1", "Ivan", ttypes.AgentQuestion, true, 1)
	h.now = start.Add(time.Duration(ttypes.DesktopNotificationAwayDefault)*time.Minute + time.Second)
	h.app.observeDesktopPresence(h.now, false)
	if last := h.ws.reports[len(h.ws.reports)-1]; last.present {
		t.Fatal("an idle terminal must report away")
	}
	h.elect(false, true)
	h.settle()
	if got := h.lastShown().Title; got != "Ivan needs your answer" {
		t.Fatalf("title = %q; news after the last input was unseen", got)
	}
}

func TestDesktopNoticeClickOpensTheAgentAndWithdraws(t *testing.T) {
	h := newNoticeHarness(t)
	h.elect(false, true)
	h.setAgent("a1", "Ivan", ttypes.AgentQuestion, true, 1)
	h.settle()
	key := h.lastShown().Key
	h.app.applyDesktopNoticeEvent(desktopNoticeEventMsg{sink: h.sink, event: desktopnotify.Event{Kind: desktopnotify.Activated, Key: key}, ok: true})
	if len(h.sink.withdrawn) == 0 || h.sink.withdrawn[len(h.sink.withdrawn)-1] != key {
		t.Fatalf("a clicked message must be withdrawn: %v", h.sink.withdrawn)
	}
	h.settle()
	if len(h.sink.shown) != 1 {
		t.Fatal("a clicked message came back")
	}
}

func TestDesktopNoticeWorkspaceSwitchWithdrawsAndStartsOver(t *testing.T) {
	h := newNoticeHarness(t)
	h.elect(false, true)
	h.setAgent("a1", "Ivan", ttypes.AgentQuestion, true, 1)
	h.settle()
	key := h.lastShown().Key
	h.app.resetDesktopNotices()
	if len(h.sink.withdrawn) != 1 || h.sink.withdrawn[0] != key {
		t.Fatalf("withdrawn = %v", h.sink.withdrawn)
	}
	if h.app.desktop.electionKnown || h.app.desktop.baseline || len(h.app.desktop.items) != 0 {
		t.Fatal("a new attachment must start without election and baseline")
	}
	if h.app.desktop.sink != h.sink || !h.app.desktop.readyKnown {
		t.Fatal("the process notifier and its readiness survive a workspace switch")
	}
}

func TestDesktopNoticeReportsCannotNotifyUntilReady(t *testing.T) {
	app := newTestApp(t)
	ws := &presenceWorkspace{Workspace: app.ws}
	app.ws = ws
	sink := &recordingNotifier{events: make(chan desktopnotify.Event, 1)}
	app.ConfigureDesktopNotifications(sink)
	app.observeDesktopPresence(time.Now(), false)
	if len(ws.reports) != 1 || ws.reports[0].notifies || !ws.reports[0].present {
		t.Fatalf("reports = %+v, want one report that cannot notify yet", ws.reports)
	}
	app.applyDesktopNoticeEvent(desktopNoticeEventMsg{sink: sink, event: desktopnotify.Event{Kind: desktopnotify.Ready, Err: desktopnotify.ErrRemoteSession}, ok: true})
	if len(ws.reports) != 1 {
		t.Fatal("an unavailable notifier changed nothing; no report is due")
	}
	app.testDesktopNotification()
	if len(sink.shown) != 0 {
		t.Fatal("the test notification must not try an SSH session's desktop")
	}
	found := false
	for _, n := range app.ws.Status().Notifications(false) {
		if n.Summary == "Crude cannot show desktop notifications" && strings.Contains(n.Detail, "crude --ssh") {
			found = true
		}
	}
	if !found {
		t.Fatal("the test command must say why and what to do")
	}
}

func TestDesktopNoticeFollowsSoundAndAwaySettings(t *testing.T) {
	h := newNoticeHarness(t)
	off := false
	h.ws.settings.UI.DesktopNotifications.Sound = &off
	h.ws.settings.UI.DesktopNotifications.AwayMinutes = 1
	h.focus()
	start := h.now
	h.now = start.Add(30 * time.Second)
	h.setAgent("a1", "Ivan", ttypes.AgentQuestion, true, 1)
	// One minute without input is away with "Away after" set to 1 minute.
	h.now = start.Add(time.Minute + time.Second)
	h.app.observeDesktopPresence(h.now, false)
	if last := h.ws.reports[len(h.ws.reports)-1]; last.present {
		t.Fatal("the away delay from Settings was not used")
	}
	h.elect(false, true)
	h.settle()
	if got := h.lastShown(); got.Quiet || !got.Silent || got.Title != "Ivan needs your answer" {
		t.Fatalf("message = %+v, want a banner without sound", got)
	}
}

func TestDesktopNoticeAllKindsOffMeansCannotNotify(t *testing.T) {
	h := newNoticeHarness(t)
	off := false
	settings := &h.ws.settings.UI.DesktopNotifications
	settings.Input, settings.Stopped, settings.Finished = &off, &off, &off
	h.app.reportDesktopPresence()
	if last := h.ws.reports[len(h.ws.reports)-1]; last.notifies {
		t.Fatal("a shell that wants no news must not take the election from another shell")
	}
}
