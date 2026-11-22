package viewmodel

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/core/port"
	"crude/desktopnotify"
	ttypes "crude/types"
	"crude/workspace"
)

// Desktop notifications tell a user who is away from Crude that an agent
// needs an answer or an approval, stopped, or finished. The rules that keep
// them useful and quiet are in docs/reference/desktop-notifications.md:
//
//   - Only an away user is notified. Present means that this terminal has
//     focus and its user acted in the last "Away after" minutes; the core
//     combines the reports of every attached shell and elects one shell to
//     notify, so a user with several shells or computers gets one message.
//   - Only news is shown: a change that happened while the user was present,
//     or that existed when this shell attached, is never announced later.
//   - One message per workspace, replaced in place. It always shows the
//     current state, and it is withdrawn when the user comes back, when the
//     agent no longer waits, or when another shell takes over.
//   - Changes settle for desktopNoticeSettle before a message, so a question
//     answered at once makes no message, and a burst makes one. After a
//     banner, more banners wait desktopNoticeCooldown unless the news is more
//     urgent; the message still updates, without a banner.
const (
	desktopNoticeSettle   = 1500 * time.Millisecond
	desktopNoticeCooldown = 30 * time.Second
	desktopNoticeLines    = 3
)

// DesktopNotifier delivers desktop notifications. *desktopnotify.Center
// implements it; every method returns at once.
type DesktopNotifier interface {
	Show(desktopnotify.Notification)
	Withdraw(key string)
	Events() <-chan desktopnotify.Event
}

// noticeKind orders news by urgency: a larger kind is more urgent.
type noticeKind uint8

const (
	noticeFinished noticeKind = iota + 1
	noticeStopped
	noticeApproval
	noticeAnswer
)

type noticeItem struct {
	agentID  string
	kind     noticeKind
	turn     uint64
	label    string
	detail   string
	appeared time.Time
	// unseen marks news that arrived while the user was away.
	unseen bool
}

func (i *noticeItem) identity() string { return fmt.Sprintf("%s/%d/%d", i.agentID, i.kind, i.turn) }

// desktopNotices is the shell's notification state for the attached
// workspace. It is owned by the Update goroutine.
type desktopNotices struct {
	sink DesktopNotifier

	// Delivery state reported by the sink.
	readyKnown bool
	readyErr   error

	// items holds at most one news item per agent.
	items    map[string]*noticeItem
	baseline bool

	// Presence: this shell's own, and the core's election. lastActive is
	// the latest input or focus gain, or the shell's start.
	lastActive    time.Time
	localPresent  bool
	localKnown    bool
	electionKnown bool
	present       bool
	notifier      bool

	reportedPresent  bool
	reportedNotifies bool
	reported         bool

	// The message on the desktop now.
	key       string
	shown     []string // item identities in the shown message
	shownTop  noticeKind
	target    string // agent that a click opens
	lastLoud  time.Time
	settling  bool
	settleGen uint64
}

type desktopNoticeEventMsg struct {
	sink  DesktopNotifier
	event desktopnotify.Event
	ok    bool
}

type desktopNoticeSettleMsg struct{ generation uint64 }

// ConfigureDesktopNotifications binds the process's notifier before the
// first frame. A nil notifier leaves desktop notifications off.
func (m *App) ConfigureDesktopNotifications(sink DesktopNotifier) {
	m.desktop.sink = sink
}

func (m *App) desktopNoticeEventsCmd() tea.Cmd {
	sink := m.desktop.sink
	if sink == nil {
		return nil
	}
	events := sink.Events()
	return func() tea.Msg {
		event, ok := <-events
		return desktopNoticeEventMsg{sink: sink, event: event, ok: ok}
	}
}

// desktopNoticeSettings returns the user's choices for the attached
// workspace; the settings replica answers from memory.
func (m *App) desktopNoticeSettings() ttypes.DesktopNotificationSettings {
	if m.ws == nil {
		off := false
		return ttypes.DesktopNotificationSettings{Enabled: &off}
	}
	return m.ws.LoadAppSettings().UI.DesktopNotifications
}

// desktopNotifies reports whether this shell can notify and the user wants it.
func (m *App) desktopNotifies() bool {
	d := &m.desktop
	if d.sink == nil || !d.readyKnown || d.readyErr != nil || m.ws == nil {
		return false
	}
	_, active := m.ws.DesktopPresencePreferences()
	return active
}

// away reports whether news arriving now is unseen: nobody is present here
// or in another shell, as far as this shell knows.
func (d *desktopNotices) away() bool {
	return !d.localPresent && (!d.electionKnown || !d.present)
}

// markSeen records that the user is back: nothing that exists now is news.
func (d *desktopNotices) markSeen() {
	for _, item := range d.items {
		item.unseen = false
	}
}

// observeDesktopPresence derives this shell's presence after input, focus
// and clock ticks, and reports a change to the core. active marks input or
// a focus gain. It does no I/O.
func (m *App) observeDesktopPresence(now time.Time, active bool) {
	d := &m.desktop
	if d.sink == nil || m.ws == nil {
		return
	}
	if active || d.lastActive.IsZero() {
		d.lastActive = now
	}
	awayMinutes, _ := m.ws.DesktopPresencePreferences()
	idle := now.Sub(d.lastActive) >= time.Duration(awayMinutes)*time.Minute
	present := !m.terminalBlurred && !idle
	if !d.localKnown || present != d.localPresent {
		wasKnown := d.localKnown
		d.localPresent, d.localKnown = present, true
		switch {
		case present:
			d.markSeen()
			m.withdrawDesktopNotice()
		case wasKnown && idle && !m.terminalBlurred:
			// The user stopped acting while the terminal kept focus. News
			// since the last input was probably not seen.
			for _, item := range d.items {
				if item.appeared.After(d.lastActive) {
					item.unseen = true
				}
			}
		}
	}
	m.reportDesktopPresence()
}

func (m *App) reportDesktopPresence() {
	d := &m.desktop
	reporter, ok := m.ws.(shellport.Presence)
	if !ok || !d.localKnown {
		return
	}
	notifies := m.desktopNotifies()
	if d.reported && d.reportedPresent == d.localPresent && d.reportedNotifies == notifies {
		return
	}
	d.reported, d.reportedPresent, d.reportedNotifies = true, d.localPresent, notifies
	reporter.ReportPresence(d.localPresent, notifies)
}

// applyDesktopElection adopts the core's election for this workspace.
func (m *App) applyDesktopElection(msg shellport.PresenceChangedMsg) tea.Cmd {
	d := &m.desktop
	if msg.Owner != m.ws || d.sink == nil {
		return nil
	}
	if msg.Present && (!d.electionKnown || !d.present) {
		d.markSeen()
	}
	d.electionKnown, d.present, d.notifier = true, msg.Present, msg.Notify
	return m.syncDesktopNotice()
}

// observeDesktopStatus turns the replicated agent status into news items.
// The first status after attach is the baseline: it is never news.
func (m *App) observeDesktopStatus(now time.Time) tea.Cmd {
	d := &m.desktop
	if d.sink == nil || m.ws == nil || m.ws.Status() == nil {
		return nil
	}
	if d.items == nil {
		d.items = make(map[string]*noticeItem)
	}
	current := make(map[string]bool)
	for _, status := range m.ws.Status().Agents() {
		kind, ok := desktopNoticeKind(status, func() bool { return m.isErrandAgent(status.Ref.ID) })
		if !ok {
			continue
		}
		id := status.Ref.ID
		current[id] = true
		if item := d.items[id]; item != nil && item.kind == kind && item.turn == status.TurnGeneration {
			item.label, item.detail = status.Label, status.Detail
			continue
		}
		d.items[id] = &noticeItem{
			agentID: id, kind: kind, turn: status.TurnGeneration, label: status.Label, detail: status.Detail,
			appeared: now, unseen: d.baseline && d.away(),
		}
	}
	for id := range d.items {
		if !current[id] {
			delete(d.items, id)
		}
	}
	d.baseline = true
	return m.syncDesktopNotice()
}

// desktopNoticeKind classifies one agent's status. A question or approval
// from any agent needs the user. A stop or a finish is news only for an
// agent that the user started: a child reports to its parent, and the
// workspace Errand works in the background.
func desktopNoticeKind(status ttypes.AgentStatusSnapshot, errand func() bool) (noticeKind, bool) {
	if status.Hidden {
		return 0, false
	}
	if status.NeedsAction {
		if status.Phase == ttypes.AgentApproval {
			return noticeApproval, true
		}
		return noticeAnswer, true
	}
	var kind noticeKind
	switch status.Phase {
	case ttypes.AgentNeedsAttention, ttypes.AgentInterrupted, ttypes.AgentFailed:
		kind = noticeStopped
	case ttypes.AgentCompleted:
		kind = noticeFinished
	default:
		return 0, false
	}
	if status.Ref.ParentID != "" || errand() {
		return 0, false
	}
	return kind, true
}

// desktopNews returns the news to show now, most urgent first, or nothing
// when this shell must not notify.
func (m *App) desktopNews() []*noticeItem {
	d := &m.desktop
	if !d.electionKnown || !d.notifier || d.present || d.localPresent || !m.desktopNotifies() {
		return nil
	}
	settings := m.desktopNoticeSettings()
	var news []*noticeItem
	for _, item := range d.items {
		if item.unseen && desktopNoticeWanted(settings, item.kind) {
			news = append(news, item)
		}
	}
	slices.SortFunc(news, func(a, b *noticeItem) int {
		if a.kind != b.kind {
			return int(b.kind) - int(a.kind)
		}
		if order := a.appeared.Compare(b.appeared); order != 0 {
			return order
		}
		return strings.Compare(a.agentID, b.agentID)
	})
	return news
}

// desktopNoticeWanted applies the user's choice of news kinds.
func desktopNoticeWanted(settings ttypes.DesktopNotificationSettings, kind noticeKind) bool {
	switch kind {
	case noticeAnswer, noticeApproval:
		return settings.InputOrDefault()
	case noticeStopped:
		return settings.StoppedOrDefault()
	case noticeFinished:
		return settings.FinishedOrDefault()
	}
	return false
}

// syncDesktopNotice withdraws a stale message at once and lets a new or
// changed message settle before it is shown.
func (m *App) syncDesktopNotice() tea.Cmd {
	d := &m.desktop
	news := m.desktopNews()
	if len(news) == 0 {
		m.withdrawDesktopNotice()
		return nil
	}
	if slices.Equal(noticeIdentities(news), d.shown) || d.settling {
		return nil
	}
	d.settling = true
	generation := d.settleGen
	return tea.Tick(desktopNoticeSettle, func(time.Time) tea.Msg { return desktopNoticeSettleMsg{generation: generation} })
}

func (m *App) flushDesktopNotice(msg desktopNoticeSettleMsg, now time.Time) {
	d := &m.desktop
	if msg.generation != d.settleGen || !d.settling {
		return
	}
	d.settling = false
	news := m.desktopNews()
	if len(news) == 0 {
		m.withdrawDesktopNotice()
		return
	}
	identities := noticeIdentities(news)
	if slices.Equal(identities, d.shown) {
		return
	}
	fresh := false
	for _, identity := range identities {
		if !slices.Contains(d.shown, identity) {
			fresh = true
			break
		}
	}
	top := news[0].kind
	loud := fresh && (d.lastLoud.IsZero() || now.Sub(d.lastLoud) >= desktopNoticeCooldown || top > d.shownTop)
	title, body := desktopNoticeText(news, filepath.Base(m.ws.RootPath()))
	d.key = desktopNoticeKey(m.ws.RootPath())
	d.sink.Show(desktopnotify.Notification{Key: d.key, Title: title, Body: body, Quiet: !loud, Silent: !m.desktopNoticeSettings().SoundOrDefault()})
	d.shown, d.shownTop, d.target = identities, top, news[0].agentID
	if loud {
		d.lastLoud = now
	}
}

func (m *App) withdrawDesktopNotice() {
	d := &m.desktop
	d.settling = false
	d.settleGen++
	if d.key != "" && d.sink != nil {
		d.sink.Withdraw(d.key)
	}
	d.key, d.shown, d.shownTop, d.target = "", nil, 0, ""
}

// resetDesktopNotices starts over for a new workspace attachment.
func (m *App) resetDesktopNotices() {
	m.withdrawDesktopNotice()
	old := m.desktop
	m.desktop = desktopNotices{sink: old.sink, readyKnown: old.readyKnown, readyErr: old.readyErr, lastActive: old.lastActive, lastLoud: old.lastLoud, settleGen: old.settleGen}
}

func (m *App) applyDesktopNoticeEvent(msg desktopNoticeEventMsg) tea.Cmd {
	d := &m.desktop
	if msg.sink != d.sink || !msg.ok {
		return nil
	}
	next := m.desktopNoticeEventsCmd()
	switch msg.event.Kind {
	case desktopnotify.Ready:
		d.readyKnown, d.readyErr = true, msg.event.Err
		if lg := m.desktopLogger(); msg.event.Err != nil && lg != nil {
			lg.Info("NOTIFY", "desktop_notifications_unavailable", "error", msg.event.Err.Error())
		}
		m.reportDesktopPresence()
		return tea.Batch(next, m.syncDesktopNotice())
	case desktopnotify.Failed:
		if lg := m.desktopLogger(); msg.event.Err != nil && lg != nil {
			lg.Warn("NOTIFY", "desktop_notification_failed", "error", msg.event.Err.Error())
		}
		if msg.event.Key == desktopNoticeTestKey && msg.event.Err != nil {
			m.notifyStatus(ttypes.SeverityInfo, "desktop-notifications", "Crude cannot show desktop notifications", desktopNoticeProblem(msg.event.Err), m.desktopNoticeOwner())
		}
	case desktopnotify.Activated:
		if msg.event.Key == d.key && d.key != "" {
			target := d.target
			d.markSeen()
			m.withdrawDesktopNotice()
			m.restoreAgent(target)
		}
	}
	return next
}

func (m *App) desktopLogger() workspace.Logger {
	if m.ws == nil {
		return nil
	}
	return m.ws.Logger()
}

const desktopNoticeTestKey = "test"

// testDesktopNotification shows a sample at once, without the presence
// rules, so that the user can check the system settings.
func (m *App) testDesktopNotification() {
	d := &m.desktop
	switch {
	case d.sink == nil:
		m.notifyStatus(ttypes.SeverityInfo, "desktop-notifications", "Crude cannot show desktop notifications", desktopNoticeProblem(desktopnotify.ErrUnsupported), m.desktopNoticeOwner())
	case d.readyKnown && d.readyErr != nil && !errors.Is(d.readyErr, desktopnotify.ErrNoService):
		// No service yet is worth a try: the service may have started.
		m.notifyStatus(ttypes.SeverityInfo, "desktop-notifications", "Crude cannot show desktop notifications", desktopNoticeProblem(d.readyErr), m.desktopNoticeOwner())
	default:
		settings := m.desktopNoticeSettings()
		body := "Crude shows a message like this when an agent needs you and you are away."
		if !settings.Active() {
			body = "Desktop notifications are off. To turn them on, open Settings > Notifications."
		}
		d.sink.Show(desktopnotify.Notification{Key: desktopNoticeTestKey, Title: "Test notification", Body: body, Silent: !settings.SoundOrDefault()})
	}
}

func (m *App) desktopNoticeOwner() ttypes.ResourceRef {
	if m.ws == nil {
		return ttypes.ResourceRef{Kind: ttypes.ResourceWorkspace}
	}
	return ttypes.ResourceRef{Kind: ttypes.ResourceWorkspace, ID: m.ws.RootPath()}
}

// desktopNoticeProblem states why notifications cannot show and what to do.
func desktopNoticeProblem(err error) string {
	switch {
	case errors.Is(err, desktopnotify.ErrRemoteSession):
		return "Crude runs in an SSH session, so a notification would show on the remote computer. To get notifications, run crude --ssh on this computer."
	case errors.Is(err, desktopnotify.ErrBlocked):
		return "Notifications for Crude are turned off in the system settings. Turn them on there."
	case errors.Is(err, desktopnotify.ErrNoService):
		return "No notification service answers on this desktop. Start one, for example the notification daemon of your desktop."
	case errors.Is(err, desktopnotify.ErrUnsupported):
		return "This operating system is not supported yet."
	}
	return err.Error()
}

func noticeIdentities(news []*noticeItem) []string {
	out := make([]string, len(news))
	for i, item := range news {
		out[i] = item.identity()
	}
	return out
}

// desktopNoticeKey names the one message of a workspace.
func desktopNoticeKey(root string) string { return "workspace:" + root }

// desktopNoticeText writes the message. One item names the agent and the
// news; several items count them and list the most urgent.
func desktopNoticeText(news []*noticeItem, workspace string) (string, string) {
	if len(news) == 1 {
		item := news[0]
		body := workspace
		if item.kind == noticeStopped && item.detail != "" {
			body = item.detail + " · " + workspace
		}
		return noticeHeadline(item), body
	}
	action := 0
	for _, item := range news {
		if item.kind != noticeFinished {
			action++
		}
	}
	var title string
	switch {
	case action == len(news):
		title = agentsNeedYou(action)
	case action == 0:
		title = fmt.Sprintf("%d agents finished", len(news))
	default:
		title = fmt.Sprintf("%s, %d finished", agentsNeedYou(action), len(news)-action)
	}
	lines := make([]string, 0, desktopNoticeLines+1)
	for i, item := range news {
		if i == desktopNoticeLines {
			lines = append(lines, fmt.Sprintf("%d more", len(news)-i))
			break
		}
		lines = append(lines, noticeHeadline(item))
	}
	return title, strings.Join(lines, "\n") + "\n" + workspace
}

func agentsNeedYou(count int) string {
	if count == 1 {
		return "1 agent needs you"
	}
	return fmt.Sprintf("%d agents need you", count)
}

func noticeHeadline(item *noticeItem) string {
	label := item.label
	if label == "" {
		label = "Agent"
	}
	switch item.kind {
	case noticeAnswer:
		return label + " needs your answer"
	case noticeApproval:
		return label + " needs your approval"
	case noticeStopped:
		return label + " stopped"
	}
	return label + " finished"
}
