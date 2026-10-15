package capabilitypicker

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	captypes "crude/types/capability"
)

func pickerFixture() captypes.Snapshot {
	return captypes.Snapshot{
		Settings: captypes.Settings{
			Skills: map[string]captypes.SkillConfig{
				"review": {ID: "review", Name: "Code review", Scope: captypes.ScopeWorkspace},
				"guide":  {ID: "guide", Name: "Project guide"},
				"legacy": {ID: "legacy", Name: "Legacy notes", Disabled: true},
			},
			Connections: map[string]captypes.ConnectionConfig{"docs": {ID: "docs", Name: "Documentation", Transport: "http", URL: "https://docs.example"}},
		},
		Entries: []captypes.Entry{
			{ID: "review", Kind: captypes.KindSkill, Description: "Reviews diffs for correctness and missing tests."},
			{ID: "docs", Kind: captypes.KindMCP, Description: "Library documentation lookup."},
		},
	}
}

type pickerHarness struct {
	screen *Screen
	host   *ui.Host
	buf    *surface.CellBuffer
}

func newHarness(t *testing.T, snapshot captypes.Snapshot, w, h int) *pickerHarness {
	t.Helper()
	screen := New(theme.DefaultDark(), snapshot)
	host := ui.NewHost(theme.DefaultDark(), screen)
	screen.SetFocusHandler(func(id ui.ComponentID) { host.Focus(id) })
	screen.SetSession(true, nil)
	host.Resize(w, h)
	return &pickerHarness{screen: screen, host: host}
}

func (p *pickerHarness) paint() string {
	p.host.MarkLayout()
	p.host.Layout()
	w, h := p.host.Root().Bounds().W, p.host.Root().Bounds().H
	p.buf = surface.NewCellBuffer(w, h)
	p.host.Paint(p.buf)
	var text strings.Builder
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if c := p.buf.Get(x, y); c.Width != 0 {
				text.WriteRune(c.Char)
			}
		}
		text.WriteByte('\n')
	}
	return text.String()
}

func (p *pickerHarness) card(id string) *card { return p.screen.cards[id] }

func (p *pickerHarness) click(t *testing.T, id ui.ComponentID, x, y int) ui.EventResult {
	t.Helper()
	r, ok := p.host.ScreenRect(id)
	if !ok {
		t.Fatalf("%s is not mounted", id)
	}
	result := p.host.Handle(tea.MouseClickMsg{X: r.X + x, Y: r.Y + y, Button: tea.MouseLeft})
	p.host.Handle(tea.MouseReleaseMsg{X: r.X + x, Y: r.Y + y, Button: tea.MouseLeft})
	return result
}

func TestPickerPaintsCompactCardsWithSwitchesAndOneSettingsButton(t *testing.T) {
	p := newHarness(t, pickerFixture(), 100, 30)
	p.screen.SetSessionLabel("Jeff")
	paint := p.paint()
	if strings.Contains(paint, "MCPs") || !strings.Contains(strings.Split(paint, "\n")[1], "Skills · Jeff") || strings.TrimSpace(strings.Split(paint, "\n")[0]) != "" {
		t.Fatalf("view needs its own padded heading and no tabs:\n%s", paint)
	}
	for _, want := range []string{"Code review", "Project guide", "Reviews diffs for", "Off in Settings", "Automatic · All workspaces", "Install & manage", "╭", "╰"} {
		if !strings.Contains(paint, want) {
			t.Fatalf("missing %q:\n%s", want, paint)
		}
	}
	for _, removed := range []string{"Deactivate", "Activate", "[ Close ]", "skills active", "future agent use"} {
		if strings.Contains(paint, removed) {
			t.Fatalf("old picker chrome %q remains:\n%s", removed, paint)
		}
	}
	// Cards stack in one list column at every width.
	first, second := p.card("review").Bounds(), p.card("legacy").Bounds()
	if second.Y <= first.Y || second.X != first.X || second.W != first.W {
		t.Fatalf("cards are not a list: %+v %+v", first, second)
	}
	// A long description fills its rows inside the card, the last ending in "…".
	long := pickerFixture()
	long.Entries[0].Description = strings.Repeat("word ", 80)
	p.screen.SetSnapshot(long)
	p.paint()
	review := p.card("review").Bounds()
	row := func(y int) string {
		var text strings.Builder
		for x := review.X + 2; x < review.X+review.W-2; x++ {
			text.WriteRune(p.buf.Get(x, y).Char)
		}
		return strings.TrimSpace(text.String())
	}
	last := review.Y + 1 + descriptionLines
	if !strings.HasPrefix(row(review.Y+2), "word") || !strings.HasPrefix(row(last-1), "word") || !strings.HasSuffix(row(last), "…") || p.buf.Get(review.X, last+1).Char != '╰' {
		t.Fatalf("description does not fill %d rows inside the card:\n%s\n%s", descriptionLines, row(review.Y+2), row(last))
	}
	// The switch knob is a square of painted cells, never a dot glyph.
	sw := switchRect(review.W)
	for x := sw.X; x < sw.X+sw.W; x++ {
		if c := p.buf.Get(review.X+x, review.Y+sw.Y); c.Char != ' ' {
			t.Fatalf("switch painted a glyph %q", c.Char)
		}
	}
	knob, track := p.buf.Get(review.X+sw.X+sw.W-1, review.Y+sw.Y).BG, p.buf.Get(review.X+sw.X, review.Y+sw.Y).BG
	if knob == track || track != p.screen.tokens.Color(ui.ColorSuccess) {
		t.Fatal("on switch is not a success track with a right knob")
	}
}

func TestPickerSwitchTogglesSessionAndCardClickSelects(t *testing.T) {
	p := newHarness(t, pickerFixture(), 100, 30)
	p.paint()
	review := p.card("review")
	sw := switchRect(review.Bounds().W)
	result := p.click(t, review.ID(), sw.X+1, sw.Y)
	if result.Action == nil || result.Action.ID != "capability-picker.toggle" || result.Action.Payload != "review" {
		t.Fatal("switch click did not request the exact toggle", result.Action)
	}
	id, enabled, ok := p.screen.BeginToggle(result.Action.Payload)
	if !ok || id != "review" || enabled {
		t.Fatal("an on skill must request off", id, enabled, ok)
	}
	if _, _, ok := p.screen.BeginToggle("review"); ok {
		t.Fatal("a saving switch admitted a second change")
	}
	if p.screen.on(review.item) {
		t.Fatal("switch did not move optimistically")
	}
	p.screen.FinishToggle("review", errors.New("disk full"))
	if !p.screen.on(review.item) || !strings.Contains(p.paint(), "Could not save: disk full") {
		t.Fatal("failed toggle did not revert and explain")
	}
	p.screen.SetSession(true, []string{"review"})
	p.screen.FinishToggle("review", nil)
	if p.screen.on(p.card("review").item) || strings.Contains(p.paint(), "Could not save") {
		t.Fatal("session gate did not turn the card off")
	}
	if _, enabled, ok := p.screen.BeginToggle("review"); !ok || !enabled {
		t.Fatal("an off skill must request on")
	}
	p.screen.FinishToggle("review", nil)

	// A click elsewhere on the card expands it in place with every fact,
	// never toggling; a second click collapses it.
	p.screen.SetCategory(captypes.KindMCP)
	paint := p.paint()
	for _, want := range []string{"Documentation", "Library documentation lookup.", "Not connected · http"} {
		if !strings.Contains(paint, want) {
			t.Fatalf("card missing %q:\n%s", want, paint)
		}
	}
	collapsed := p.card("docs").Bounds().H
	p.click(t, p.card("docs").ID(), 3, 2)
	paint = p.paint()
	if p.host.Focused() != p.card("docs").ID() || p.screen.Busy() || p.card("docs").Bounds().H <= collapsed {
		t.Fatal("card body click did not expand in place or toggled")
	}
	for _, want := range []string{"▾", "Connection", "Transport", "Endpoint", "https://docs.example", "Installed for", "All workspaces"} {
		if !strings.Contains(paint, want) {
			t.Fatalf("expanded card missing %q:\n%s", want, paint)
		}
	}
	if result := p.host.Handle(tea.KeyPressMsg{Code: tea.KeySpace}); result.Action == nil || result.Action.ID != "capability-picker.toggle" || result.Action.Payload != "docs" {
		t.Fatal("Space on a card did not toggle the exact item", result.Action)
	}
	p.host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	if p.paint(); p.card("docs").Bounds().H != collapsed || p.screen.Busy() {
		t.Fatal("Enter did not collapse the card or it toggled")
	}
	if result := p.host.Handle(tea.KeyPressMsg{Code: tea.KeyEsc}); result.Action == nil || result.Action.ID != "capability-picker.close" {
		t.Fatal("Esc on the grid did not close")
	}
}

func TestPickerManageButtonTargetsSettings(t *testing.T) {
	p := newHarness(t, pickerFixture(), 100, 30)
	p.paint()
	result := p.screen.HandleAction(ui.EventContext{}, ui.Action{ID: "capability-picker.manage"})
	if target, _ := result.Action.Payload.(ManageTarget); target.Kind != captypes.KindSkill || target.ID != "" {
		t.Fatal("grid manage target", result.Action.Payload)
	}
}

func TestPickerSwitchesAreReadOnlyWithoutSessionOrRegistration(t *testing.T) {
	p := newHarness(t, pickerFixture(), 100, 30)
	if _, _, ok := p.screen.BeginToggle("legacy"); ok {
		t.Fatal("registration turned off in Settings was toggled per session")
	}
	p.screen.SetSession(false, nil)
	if !strings.Contains(p.paint(), "Open a session") {
		t.Fatal("no-session state is not explained")
	}
	if _, _, ok := p.screen.BeginToggle("review"); ok {
		t.Fatal("toggle admitted without a session")
	}
}

// Focused cards keep readable ink: focus is an accent border, never a fill
// that swallows the name, and no card paints outside its own bounds.
func TestPickerFocusKeepsColorsAndCardsStayInBounds(t *testing.T) {
	snapshot := pickerFixture()
	for i := 0; i < 12; i++ {
		id := fmt.Sprintf("skill-%02d", i)
		snapshot.Settings.Skills[id] = captypes.SkillConfig{ID: id, Name: id}
	}
	p := newHarness(t, snapshot, 90, 24)
	p.paint()
	p.screen.FocusSearch()
	p.host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	p.paint()
	focused := p.screen.visible[0]
	if p.host.Focused() != focused.ID() {
		t.Fatal("Enter in search did not reach the first card")
	}
	r := focused.Bounds()
	tokens := p.screen.tokens
	if c := p.buf.Get(r.X, r.Y); c.FG != tokens.Color(ui.ColorAccent) {
		t.Fatal("focused card border is not accent")
	}
	for x := r.X + 4; x < r.X+4+len(focused.item.name); x++ {
		if c := p.buf.Get(x, r.Y+1); c.FG == c.BG || c.BG == tokens.Color(ui.ColorAccent) {
			t.Fatal("focused card name is unreadable", x)
		}
	}
	// Every cell outside cards, heading, search, scrollbar and footer stays blank.
	occupied := func(x, y int) bool {
		for _, c := range p.screen.visible {
			if b := c.Bounds(); !b.Empty() && b.Contains(x, y) {
				return true
			}
		}
		list := p.screen.list
		return y < list.Y || y >= list.Y+list.H || x >= list.X+list.W-ui.ScrollbarWidth-1
	}
	for y := 0; y < 24; y++ {
		for x := 0; x < 90; x++ {
			if !occupied(x, y) && p.buf.Get(x, y).Char != ' ' && p.buf.Get(x, y).Char != 0 {
				t.Fatalf("stray glyph %q at %d,%d", p.buf.Get(x, y).Char, x, y)
			}
		}
	}
	// Arrow navigation scrolls the grid and keeps focus visible.
	for i := 0; i < 8; i++ {
		p.host.Handle(tea.KeyPressMsg{Code: tea.KeyDown})
		p.paint()
	}
	if p.screen.model.Offset() == 0 {
		t.Fatal("grid did not scroll to the focused card")
	}
	for _, c := range p.screen.visible {
		if p.host.Focused() == c.ID() && c.Bounds().Empty() {
			t.Fatal("focused card scrolled out of view")
		}
	}
	// Refreshing the snapshot keeps focus and scroll.
	offset, focus := p.screen.model.Offset(), p.host.Focused()
	p.screen.SetSnapshot(snapshot)
	p.paint()
	if p.screen.model.Offset() != offset || p.host.Focused() != focus {
		t.Fatal("refresh lost scroll or focus")
	}
}

func TestPickerSearchFiltersAndExplainsEmpty(t *testing.T) {
	p := newHarness(t, pickerFixture(), 100, 30)
	p.screen.SetQuery("guide")
	paint := p.paint()
	if strings.Contains(paint, "Code review") || !strings.Contains(paint, "Project guide") {
		t.Fatal("search did not filter", paint)
	}
	p.screen.SetQuery("absent")
	if paint = p.paint(); !strings.Contains(paint, "No skills match") {
		t.Fatal("empty search not explained", paint)
	}
	if _, _, ok := p.screen.BeginToggle(nil); ok {
		t.Fatal("empty grid toggled something")
	}
	snapshot := pickerFixture()
	snapshot.Connections = []captypes.ConnectionState{{ID: "docs", State: "connected", Tools: []captypes.Tool{{Name: "mcp__docs__search", OriginalName: "search"}, {Name: "mcp__docs__fetch", OriginalName: "fetch"}}}}
	p.screen.SetQuery("")
	p.screen.SetSnapshot(snapshot)
	p.screen.SetCategory(captypes.KindMCP)
	if paint = p.paint(); !strings.Contains(paint, "Connected · 2 tools") {
		t.Fatal("observed connection not on card", paint)
	}
	snapshot.Connections = nil
	snapshot.Entries = []captypes.Entry{{ID: "docs", Kind: captypes.KindMCP, State: "needs sign-in"}}
	p.screen.SetSnapshot(snapshot)
	if paint = p.paint(); !strings.Contains(paint, "Needs sign-in") {
		t.Fatal("registration attention state not on card", paint)
	}
}

// An empty view explains what it holds and where to start, inside its own
// frame; a search miss says how to recover.
func TestPickerEmptyStateGuidesWhereToStart(t *testing.T) {
	p := newHarness(t, captypes.Snapshot{}, 90, 30)
	paint := p.paint()
	for _, want := range []string{"No skills installed yet", "reusable instructions", "→ Install & manage", "browse Anthropic skills", "SKILL.md", "Or just ask"} {
		if !strings.Contains(paint, want) {
			t.Fatalf("skills guide missing %q:\n%s", want, paint)
		}
	}
	p.screen.SetCategory(captypes.KindMCP)
	paint = p.paint()
	for _, want := range []string{"No MCP servers installed yet", "outside tools and data", "official MCP registry", "remote URL"} {
		if !strings.Contains(paint, want) {
			t.Fatalf("MCP guide missing %q:\n%s", want, paint)
		}
	}
	// The guide is a frame centred both ways within the list area.
	list := p.screen.list
	rows := strings.Split(paint, "\n")
	top, bottom, corner := -1, -1, -1
	for y := list.Y; y < list.Y+list.H; y++ {
		if index := strings.Index(rows[y], "╭"); index >= 0 && top < 0 {
			top, corner = y, len([]rune(rows[y][:index]))
		}
		if strings.Contains(rows[y], "╰") {
			bottom = y
		}
	}
	above, below := top-list.Y, list.Y+list.H-1-bottom
	if top < 0 || corner <= list.X || above-below > 1 || below-above > 1 {
		t.Fatalf("guide is not centred (above %d, below %d):\n%s", above, below, paint)
	}
	p.screen.SetSnapshot(pickerFixture())
	p.screen.SetQuery("zzz")
	if paint = p.paint(); !strings.Contains(paint, "No MCP servers match “zzz”") || !strings.Contains(paint, "Try a shorter name") {
		t.Fatalf("search miss is not explained:\n%s", paint)
	}
}

// Expanding a card near the bottom scrolls just enough to show it whole.
func TestPickerExpandedCardStaysInView(t *testing.T) {
	snapshot := pickerFixture()
	for i := 0; i < 6; i++ {
		id := fmt.Sprintf("skill-%02d", i)
		snapshot.Settings.Skills[id] = captypes.SkillConfig{ID: id, Name: id, Version: "v1", Path: "/skills/" + id}
		snapshot.Entries = append(snapshot.Entries, captypes.Entry{ID: id, Kind: captypes.KindSkill, Description: strings.Repeat("long description ", 20)})
	}
	p := newHarness(t, snapshot, 80, 30)
	p.screen.SetQuery("skill-")
	p.paint()
	var last *card
	for _, c := range p.screen.visible {
		if !c.Bounds().Empty() {
			last = c
		}
	}
	p.click(t, last.ID(), 3, 2)
	p.paint()
	b := last.Bounds()
	list := p.screen.list
	if b.Empty() || b.Y+b.H > list.Y+list.H || b.H <= cardHeight {
		t.Fatalf("expanded card is not fully visible: %+v in %+v", b, list)
	}
	if !strings.Contains(p.paint(), "/skills/"+last.item.name) {
		t.Fatalf("expanded card %s does not show its facts:\n%s", last.item.name, p.paint())
	}
}
