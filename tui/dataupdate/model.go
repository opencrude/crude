// Package dataupdate shows the core's data update: the detached page a user
// sees the first time a new Crude starts with data that an older Crude saved.
// It shows the update's progress, the backup it keeps and anything it could
// not update. It owns no data and no connection: the shell feeds it the
// statuses the core publishes.
package dataupdate

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"crude/core/coreprotocol"
	"crude/tui/cell"
	"crude/tui/compositor"
	"crude/tui/rendering"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

// Result is the user's choice when the page closes.
type Result int

const (
	// Continue opens Crude.
	Continue Result = iota
	// Quit closes this window; the core keeps updating.
	Quit
)

// Status is one item of the status feed.
type Status struct {
	Update coreprotocol.DataUpdate
	// Err ends the feed: the core closed the connection without a final status.
	Err error
}

const (
	continueID ui.ComponentID = "dataupdate/continue"
	retryID    ui.ComponentID = "dataupdate/retry"
	quitID     ui.ComponentID = "dataupdate/quit"
	barID      ui.ComponentID = "dataupdate/bar"
)

// finishedPause shows a clean finish at 100% before Crude opens.
const finishedPause = 900 * time.Millisecond

type statusMsg Status
type frameMsg struct{}
type continueMsg struct{ generation int }

// Model is the standalone Tea model of the page.
type Model struct {
	ctx        context.Context
	feed       <-chan Status
	retry      func()
	status     coreprotocol.DataUpdate
	feedErr    error
	result     Result
	generation int
	rate       rateMeter
	root       *page
	host       *ui.Host
	comp       *compositor.Compositor
	surf       *surface.Surface
	closed     bool
}

// Run shows the page until the update finishes or the user quits.
func Run(ctx context.Context, first coreprotocol.DataUpdate, feed <-chan Status, retry func()) (Result, error) {
	m := NewModel(ctx, first, feed, retry)
	_, err := tea.NewProgram(m, tea.WithContext(ctx), tea.WithWidthMethod(ansi.GraphemeWidth)).Run()
	return m.result, err
}

// NewModel builds the page for a first status.
func NewModel(ctx context.Context, first coreprotocol.DataUpdate, feed <-chan Status, retry func()) *Model {
	th := theme.FromPreset(first.Theme)
	tokens := ui.NewTokenSet(th)
	m := &Model{ctx: ctx, feed: feed, retry: retry, result: Quit}
	m.root = newPage(tokens)
	m.host = ui.NewHost(th, m.root)
	m.comp = compositor.NewCompositor(80, 24)
	m.surf = surface.NewSurface("dataupdate", 80, 24, cell.BlankCell(tokens.Color(ui.ColorBg)))
	m.host.Resize(80, 24)
	m.apply(Status{Update: first})
	return m
}

// Result reports the choice after the program ends.
func (m *Model) Result() Result { return m.result }

// Init starts the frame clock and follows the first status. An update can
// already be finished by the time the shell opens this page.
func (m *Model) Init() tea.Cmd { return tea.Batch(m.followStatus(), frame()) }

func (m *Model) followStatus() tea.Cmd {
	if m.feedErr != nil {
		return nil
	}
	if !m.status.Final {
		return m.next()
	}
	if m.status.Phase != coreprotocol.DataUpdateFinished || len(m.status.Problems) != 0 {
		return nil
	}
	m.generation++
	generation := m.generation
	return tea.Tick(finishedPause, func(time.Time) tea.Msg { return continueMsg{generation} })
}

func (m *Model) next() tea.Cmd {
	if m.feed == nil {
		return nil
	}
	feed, ctx := m.feed, m.ctx
	return func() tea.Msg {
		select {
		case s, ok := <-feed:
			if !ok {
				return nil
			}
			return statusMsg(s)
		case <-ctx.Done():
			return nil
		}
	}
}

func frame() tea.Cmd {
	return tea.Tick(33*time.Millisecond, func(time.Time) tea.Msg { return frameMsg{} })
}

func (m *Model) apply(s Status) {
	if s.Err != nil {
		m.feedErr = s.Err
	} else {
		if s.Update.Phase == coreprotocol.DataUpdateUpdating && m.status.Phase != coreprotocol.DataUpdateUpdating {
			m.rate = rateMeter{}
		}
		m.status = s.Update
		m.rate.add(time.Now(), s.Update.Done)
	}
	m.root.set(m.status, m.feedErr, m.rate.remaining(m.status.Total-m.status.Done))
	m.layout()
	if id := m.root.primary(); id != "" {
		m.host.Focus(id)
	}
}

func (m *Model) layout() { m.host.MarkLayout(); m.host.Layout() }

// View paints the page through the same host and compositor as the
// introduction, so the wordmark, colors and controls match.
func (m *Model) View() tea.View {
	m.layout()
	m.host.SetFrame(int(time.Now().UnixMilli()))
	m.comp.BeginFrame()
	m.comp.Root().AddChild(m.surf)
	m.host.Paint(m.surf.Buffer())
	m.comp.Composite()
	v := tea.NewView(m.comp.Present())
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	v.ReportFocus = true
	v.KeyboardEnhancements = ui.KeyboardEnhancements()
	v.WindowTitle, v.IconName = "Crude", "Crude"
	return v
}

// Update applies core progress and routes input to the visible controls.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.closed {
		return m, nil
	}
	msg = ui.NormalizeKeyMsg(msg)
	switch v := msg.(type) {
	case tea.WindowSizeMsg:
		w, h := max(0, v.Width), max(0, v.Height)
		m.host.Resize(w, h)
		m.comp.Resize(w, h)
		m.surf.Resize(w, h)
		m.layout()
		return m, nil
	case tea.BlurMsg:
		m.host.SetFocusActive(false)
		return m, nil
	case tea.FocusMsg:
		m.host.SetFocusActive(true)
		return m, nil
	case frameMsg:
		// Only an unmeasured bar moves; otherwise a slow tick keeps time text fresh.
		if m.root.bar.NeedsFrame() {
			return m, frame()
		}
		return m, tea.Tick(time.Second, func(time.Time) tea.Msg { return frameMsg{} })
	case statusMsg:
		m.apply(Status(v))
		return m, m.followStatus()
	case continueMsg:
		if v.generation == m.generation {
			return m, m.finish(Continue)
		}
		return m, nil
	case tea.KeyPressMsg:
		switch {
		case v.String() == "ctrl+q" || v.Code == tea.KeyEsc && !ui.IsModifiedEscape(v):
			return m, m.finish(Quit)
		case v.Code == tea.KeyTab:
			m.root.cycle(m.host, v.Mod.Contains(tea.ModShift))
			return m, nil
		}
	}
	r := m.host.Handle(msg)
	if r.Action != nil {
		switch r.Action.ID {
		case "dataupdate.continue":
			return m, m.finish(Continue)
		case "dataupdate.quit":
			return m, m.finish(Quit)
		case "dataupdate.retry":
			if m.retry != nil {
				m.retry()
			}
			m.apply(Status{Update: coreprotocol.DataUpdate{Phase: coreprotocol.DataUpdateChecking, Version: m.status.Version, Theme: m.status.Theme}})
			// The pending feed read still owns the next core status.
			return m, nil
		}
	}
	m.layout()
	return m, r.Cmd
}

func (m *Model) finish(result Result) tea.Cmd {
	m.result, m.closed = result, true
	return tea.Quit
}

// rateMeter estimates the time left from the progress of the last seconds.
type rateMeter struct {
	start, last    time.Time
	startDone      int64
	lastDone       int64
	bytesPerSecond float64
}

func (r *rateMeter) add(now time.Time, done int64) {
	if r.start.IsZero() {
		r.start, r.startDone = now, done
	}
	if elapsed := now.Sub(r.last).Seconds(); !r.last.IsZero() && elapsed > 0 && done >= r.lastDone {
		current := float64(done-r.lastDone) / elapsed
		if r.bytesPerSecond == 0 {
			r.bytesPerSecond = current
		} else {
			r.bytesPerSecond = 0.8*r.bytesPerSecond + 0.2*current
		}
	}
	r.last, r.lastDone = now, done
}

// remaining is the estimate, or zero before there is a measured rate.
func (r rateMeter) remaining(left int64) time.Duration {
	if left <= 0 || r.bytesPerSecond <= 0 || r.last.Sub(r.start) < 2*time.Second {
		return 0
	}
	return time.Duration(float64(left) / r.bytesPerSecond * float64(time.Second))
}

func timeLeft(d time.Duration) string {
	switch {
	case d <= 0:
		return ""
	case d < 10*time.Second:
		return "A few seconds left"
	case d < 90*time.Second:
		return fmt.Sprintf("About %d seconds left", int(d.Seconds()+5)/10*10)
	default:
		return fmt.Sprintf("About %d minutes left", int(d.Minutes()+0.5))
	}
}

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

// page is the one component tree of the page.
type page struct {
	ui.Base
	tokens                *ui.TokenSet
	bar                   *ui.ProgressBar
	continueButton, retry *ui.Button
	quit                  *ui.Button
	status                coreprotocol.DataUpdate
	feedErr               error
	left                  time.Duration
	content               ui.Rect
}

func newPage(tokens *ui.TokenSet) *page {
	p := &page{Base: ui.NewBase("dataupdate"), tokens: tokens}
	p.bar = ui.NewProgressBar(barID, tokens)
	p.bar.SetSurfaceBackground(ui.ColorBg)
	p.continueButton = ui.NewButton(continueID, "Continue", "dataupdate.continue")
	p.retry = ui.NewButton(retryID, "Try again", "dataupdate.retry")
	p.quit = ui.NewButton(quitID, "Quit", "dataupdate.quit")
	for _, b := range []*ui.Button{p.continueButton, p.retry, p.quit} {
		b.SetTokens(tokens)
		b.SetSurfaceBackground(tokens.Color(ui.ColorBg))
	}
	p.continueButton.SetVariant(ui.VariantPrimary)
	p.retry.SetVariant(ui.VariantPrimary)
	p.quit.SetVariant(ui.VariantSecondary)
	return p
}

func (p *page) set(status coreprotocol.DataUpdate, feedErr error, left time.Duration) {
	p.status, p.feedErr, p.left = status, feedErr, left
	switch status.Phase {
	case coreprotocol.DataUpdateChecking:
		p.bar.SetMeasurement(0, 0)
	case coreprotocol.DataUpdateFinished:
		p.bar.SetMeasurement(1, 1)
	default:
		p.bar.SetMeasurement(status.Done, max(status.Total, 1))
	}
}

// buttons are the actions this status offers, the primary one first.
func (p *page) buttons() []*ui.Button {
	switch {
	case p.feedErr != nil:
		return []*ui.Button{p.continueButton, p.quit}
	case p.status.Phase == coreprotocol.DataUpdateNoSpace:
		return []*ui.Button{p.retry, p.quit}
	case p.status.Phase == coreprotocol.DataUpdateNewer || p.status.Phase == coreprotocol.DataUpdateBlocked:
		return []*ui.Button{p.quit}
	case p.status.Phase == coreprotocol.DataUpdateFinished && p.status.Final:
		return []*ui.Button{p.continueButton, p.quit}
	}
	return nil
}

func (p *page) primary() ui.ComponentID {
	if buttons := p.buttons(); len(buttons) > 0 {
		return buttons[0].ID()
	}
	return ""
}

func (p *page) cycle(host *ui.Host, back bool) {
	buttons := p.buttons()
	if len(buttons) == 0 {
		return
	}
	at := 0
	for i, b := range buttons {
		if host.Focused() == b.ID() {
			at = i
		}
	}
	step := 1
	if back {
		step = len(buttons) - 1
	}
	host.Focus(buttons[(at+step)%len(buttons)].ID())
}

func (p *page) Children() []ui.Component {
	children := []ui.Component{p.bar}
	for _, b := range p.buttons() {
		children = append(children, b)
	}
	return children
}

// geometry reserves footer actions first. Drop the wordmark before clipping a recovery path.
func (p *page) rows() (top, bar, stores int) {
	c := p.content
	if c.H >= 23 {
		top = ui.BannerHeight + 1
	}
	title, subtitle := p.title()
	bar = top + len(rendering.WordWrap(title, c.W)) + len(rendering.WordWrap(subtitle, c.W)) + 1
	stores = bar + 1 + len(rendering.WordWrap(p.detail(), c.W)) + 1
	return
}

func (p *page) Layout(ctx ui.LayoutContext, r ui.Rect) {
	p.SetBounds(r)
	w := min(112, max(1, r.W-2*max(2, r.W/14)))
	h := min(30, max(1, r.H-2))
	p.content = ui.Rect{X: max(0, (r.W-w)/2), Y: max(0, (r.H-h)/2), W: w, H: h}
	c := p.content
	_, barRow, _ := p.rows()
	p.bar.Layout(ctx, ui.Rect{X: c.X, Y: c.Y + barRow, W: max(0, c.W-6), H: 1})
	x := c.X
	for _, b := range []*ui.Button{p.continueButton, p.retry, p.quit} {
		b.Layout(ctx, ui.Rect{})
	}
	for _, b := range p.buttons() {
		width := b.Measure(ctx, ui.Constraints{MaxW: max(0, c.X+c.W-x), MaxH: 1}).W
		b.Layout(ctx, ui.Rect{X: x, Y: c.Y + max(0, c.H-2), W: min(width, max(0, c.X+c.W-x)), H: 1})
		x += width + 2
	}
}

func (p *page) title() (string, string) {
	s := p.status
	version := s.Version
	if version == "" || version == "dev" {
		version = "This version"
	} else {
		version = "Crude " + version
	}
	switch {
	case p.feedErr != nil:
		return "The update stopped before it finished", "Crude keeps every file it did not update as it was. Continue to try again."
	case s.Phase == coreprotocol.DataUpdateChecking:
		return "Checking your saved data", version + " checks which saved data it must update."
	case s.Phase == coreprotocol.DataUpdateUpdating:
		return "Updating your saved data", version + " makes a backup before it updates each file. You can close this window safely."
	case s.Phase == coreprotocol.DataUpdateNoSpace:
		return "Not enough disk space for the update", "The update copies each file before it changes it."
	case s.Phase == coreprotocol.DataUpdateNewer:
		return "This data is from a newer Crude", version + " cannot open data that a newer Crude saved. Nothing changed."
	case s.Phase == coreprotocol.DataUpdateBlocked:
		return "Crude could not update your saved data", "The file below was not changed. See core.log for details before restarting."
	}
	return "Your saved data is up to date", "Crude opens in a moment."
}

func (p *page) detail() string {
	s := p.status
	switch s.Phase {
	case coreprotocol.DataUpdateUpdating:
		parts := []string{}
		if s.Step != "" {
			parts = append(parts, s.Step)
		}
		parts = append(parts, fmt.Sprintf("%d of %d files complete", s.FilesDone, s.Files))
		if left := timeLeft(p.left); left != "" {
			parts = append(parts, left)
		}
		return strings.Join(parts, " · ")
	case coreprotocol.DataUpdateNoSpace:
		return fmt.Sprintf("The backups need %s. The disk has %s free.", rendering.FormatBytes(s.Needed), rendering.FormatBytes(s.Free))
	case coreprotocol.DataUpdateFinished:
		if s.Files == 0 {
			return "Nothing needed an update."
		}
		return plural(s.Files-len(s.Problems), "file updated", "files updated")
	}
	return ""
}

func (p *page) Paint(buf *surface.CellBuffer, ox, oy int, _ ui.PaintContext) {
	r := p.Bounds()
	bg := p.tokens.Color(ui.ColorBg)
	ui.FillRect(buf, ox, oy, r, cell.BlankCell(bg))
	c := p.content
	x, y := ox+r.X+c.X, oy+r.Y+c.Y
	write := func(row, column int, text string, role ui.SemanticColor, bold bool) {
		if row < 0 || row >= c.H-2 || column >= c.W {
			return
		}
		attr := cell.Attrs(0)
		if bold {
			attr = cell.AttrBold
		}
		buf.WriteString(x+column, y+row, rendering.TruncateVisual(text, c.W-column, "…"), p.tokens.Color(role), bg, attr)
	}
	titleRow, barRow, storesRow := p.rows()
	if titleRow > 0 {
		ui.PaintBanner(buf, x, y, c.W, p.tokens, bg)
	}
	wrapped := func(row, col int, text string, role ui.SemanticColor, bold bool) int {
		for _, line := range rendering.WordWrap(text, max(1, c.W-col)) {
			write(row, col, line, role, bold)
			row++
		}
		return row
	}
	title, subtitle := p.title()
	titleRole := ui.ColorFg
	if p.status.Phase == coreprotocol.DataUpdateNewer || p.status.Phase == coreprotocol.DataUpdateBlocked || p.status.Phase == coreprotocol.DataUpdateNoSpace || p.feedErr != nil {
		titleRole = ui.ColorWarning
	}
	subtitleRow := wrapped(titleRow, 0, title, titleRole, true)
	wrapped(subtitleRow, 0, subtitle, ui.ColorMuted, false)
	if p.status.Phase != coreprotocol.DataUpdateNewer {
		percent := fmt.Sprintf("%3d%%", int(p.bar.Fraction()*100))
		if p.bar.NeedsFrame() {
			percent = ""
		}
		write(barRow, max(0, c.W-5), percent, ui.ColorFg, true)
		wrapped(barRow+1, 0, p.detail(), ui.ColorMuted, false)
	}
	row := storesRow
	glyphs := p.tokens.Glyphs()
	for _, store := range p.status.Stores {
		if store.Files == 0 {
			continue
		}
		mark, role, state := glyphs.SwitchOff, ui.ColorMuted, "waiting"
		switch {
		case store.Done+store.Failed == store.Files && store.Failed > 0:
			mark, role, state = "!", ui.ColorWarning, fmt.Sprintf("%d updated, %d not updated", store.Done, store.Failed)
		case store.Done+store.Failed == store.Files:
			mark, role, state = "✓", ui.ColorSuccess, plural(store.Done, "file updated", "files updated")
		case store.Name == p.status.Step:
			mark, role, state = glyphs.SwitchOn, ui.ColorAccent, fmt.Sprintf("%d of %d", store.Done+store.Failed, store.Files)
		}
		write(row, 0, mark, role, true)
		write(row, 2, store.Name, ui.ColorFg, false)
		write(row, max(2, c.W-rendering.VisibleWidth(state)), state, ui.ColorMuted, false)
		row++
	}
	if row > storesRow {
		row++
	}
	for _, problem := range append(append([]coreprotocol.DataUpdateProblem(nil), p.status.Newer...), p.status.Problems...) {
		if row >= c.H-4 {
			write(row, 0, "More files are listed in core.log.", ui.ColorMuted, false)
			row++
			break
		}
		write(row, 0, "!", ui.ColorWarning, true)
		row = wrapped(row, 2, problem.Path+": "+problem.Detail, ui.ColorFg, false)
	}
	if p.status.Backup != "" && row < c.H-3 {
		write(row, 0, "Backup folder", ui.ColorAccent, true)
		wrapped(row+1, 0, p.status.Backup, ui.ColorFg, false)
	}
	hint := ""
	switch {
	case p.status.Phase == coreprotocol.DataUpdateNewer:
		hint = "Install the newer Crude to open this data."
	case p.status.Phase == coreprotocol.DataUpdateNoSpace:
		hint = "Free some space, then select Try again."
	case len(p.buttons()) == 0:
		hint = "Esc closes this window. The update continues."
	}
	if hint != "" {
		buf.WriteString(x, y+max(0, c.H-1), rendering.TruncateVisual(hint, c.W, "…"), p.tokens.Color(ui.ColorMuted), bg, 0)
	}
}

func (p *page) Handle(_ ui.EventContext, _ ui.Event) ui.EventResult { return ui.EventResult{} }

func (p *page) Semantics() ui.Semantics {
	title, _ := p.title()
	return ui.Semantics{Role: ui.RoleGroup, Label: title}
}
