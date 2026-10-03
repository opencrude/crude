package memorybrowser

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/memory"
	"crude/tui/cell"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

func testRecords() []memory.ScoredRecord {
	now := time.Now().UnixNano()
	return []memory.ScoredRecord{
		{Record: memory.Record{
			ID: "mem-1", Revision: 1, Kind: memory.KindConvention, Title: "Validation gate",
			Content: "Run go test ./... and golangci-lint before handoff.",
			Pinned:  true, Importance: 80, UseCount: 4, Status: memory.StatusActive,
			Source: memory.SourceTool, Provenance: "session:s1 turn:3",
			AppliesWhen: "Before handing off a Go change", ScopePaths: []string{"tui/**"},
			Evidence:  []memory.Evidence{{SessionID: "s1", ToolCallID: "tool-test", Kind: memory.EvidenceValidation, Outcome: memory.OutcomeSucceeded, Summary: "Focused test succeeded"}},
			UpdatedAt: now, CreatedAt: now,
		}},
		{Record: memory.Record{
			ID: "mem-2", Revision: 2, Kind: memory.KindLesson, Title: "moveWordLeft climbs blank lines",
			Content: "A swallowed continue parks the cursor on blank lines.",
			Status:  memory.StatusActive, Importance: 55, Source: memory.SourceAuto,
			UpdatedAt: now, CreatedAt: now,
		}},
	}
}

func browserHost(t *testing.T, records []memory.ScoredRecord) (*Screen, *ui.Host) {
	t.Helper()
	th := theme.DefaultDark()
	screen := New(th)
	request := screen.Open()().(SearchMsg)
	screen.ApplySearch(request, records, nil, nil)
	component := ui.NewComponentWidget(th, screen, 78, 20)
	screen.SetFocusHandler(component.Focus)
	screen.FocusList()
	host := component.Host()
	host.Resize(78, 20)
	host.Layout()
	return screen, host
}

func paintHost(host *ui.Host, w, h int) *surface.CellBuffer {
	host.Resize(w, h)
	host.Layout()
	buf := surface.NewCellBuffer(w, h)
	host.Paint(buf)
	return buf
}

func bufferText(buf *surface.CellBuffer) string {
	var b strings.Builder
	for y := 0; y < buf.Height(); y++ {
		for x := 0; x < buf.Width(); x++ {
			r := buf.Get(x, y).Char
			if r == 0 {
				r = ' '
			}
			b.WriteRune(r)
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func commandMessage[T any](t *testing.T, result ui.EventResult) T {
	t.Helper()
	if result.Cmd == nil {
		t.Fatalf("result did not emit a command: %#v", result)
	}
	value := result.Cmd()
	message, ok := value.(T)
	if !ok {
		t.Fatalf("command message=%T (%#v)", value, value)
	}
	return message
}

func TestBrowserPaintShowsRecordsAndDetail(t *testing.T) {
	screen, host := browserHost(t, testRecords())
	text := bufferText(paintHost(host, 78, 20))
	for _, want := range []string{
		"Knowledge",
		"Episodes",
		"Validation gate",
		"moveWordLeft climbs blank lines",
		"Inspect",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("paint missing %q:\n%s", want, text)
		}
	}
	request := commandMessage[DetailMsg](t, host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter}))
	if request.Target != screen || request.ID != "mem-1" {
		t.Fatalf("detail intent = %#v", request)
	}
	detail := DetailData{Record: testRecords()[0].Record, Revisions: []memory.Revision{{Record: testRecords()[0].Record, Op: "remember"}}}
	screen.ApplyDetail(request, detail, nil)
	host.MarkLayout()
	text = bufferText(paintHost(host, 78, 20))
	if !strings.Contains(text, "Knowledge · Validation gate") || !strings.Contains(text, "Revision: 1") {
		t.Fatalf("detail missing:\n%s", text)
	}
}

func TestBrowserSearchIsTypedAndRejectsOldResults(t *testing.T) {
	screen, host := browserHost(t, testRecords())
	screen.FocusSearch()
	var first, latest SearchMsg
	for index, character := range "cursor" {
		latest = commandMessage[SearchMsg](t, host.Handle(tea.KeyPressMsg{Code: character, Text: string(character)}))
		if index == 0 {
			first = latest
		}
	}
	if latest.Target != screen || latest.Query != "cursor" || latest.Sequence <= first.Sequence || len(screen.records) != 2 {
		t.Fatalf("search intent = %#v; should not search synchronously", latest)
	}
	if screen.ApplySearch(first, nil, nil, nil) {
		t.Fatal("old query replaced retained rows")
	}
	if !screen.ApplySearch(latest, testRecords()[1:], nil, nil) {
		t.Fatal("current query rejected")
	}
	host.MarkLayout()
	host.Layout()
	text := bufferText(paintHost(host, 78, 20))
	if !strings.Contains(text, "moveWordLeft") {
		t.Fatalf("search should keep the lesson:\n%s", text)
	}
	if strings.Contains(text, "Validation gate") {
		t.Fatalf("search should hide the convention:\n%s", text)
	}
}

func TestBrowserRetireCancelFirstAndCASPreview(t *testing.T) {
	screen, host := browserHost(t, testRecords())
	request := commandMessage[DetailMsg](t, host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter}))
	screen.ApplyDetail(request, DetailData{Record: testRecords()[0].Record}, nil)
	result := screen.HandleAction(ui.EventContext{}, ui.Action{ID: "memorybrowser.retire"})
	if result.Cmd != nil {
		t.Fatal("opening confirmation started a mutation")
	}
	host.MarkLayout()
	host.Layout()
	host.Focus(ActionsID)
	if result := host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter}); result.Cmd != nil || screen.page != pageDetail {
		t.Fatalf("default Enter must cancel: %#v page=%s", result, screen.page)
	}
	screen.HandleAction(ui.EventContext{}, ui.Action{ID: "memorybrowser.retire"})
	host.MarkLayout()
	host.Layout()
	host.Focus(ActionsID)
	host.Handle(tea.KeyPressMsg{Code: tea.KeyRight})
	mutation := commandMessage[MutationMsg](t, host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter}))
	if mutation.Target != screen || mutation.ID != "mem-1" || mutation.ExpectedRevision != 1 || mutation.Kind != MutationRetire {
		t.Fatalf("retire preview = %#v", mutation)
	}
	screen.detail.Record.Revision = 5
	if mutation.ExpectedRevision != 1 {
		t.Fatal("preview was mutated after confirmation")
	}
	screen.ApplyMutation(mutation, memory.Record{}, memory.ErrRevisionConflict)
	if screen.page != pageRetire || screen.retirePreview == nil || screen.retirePreview.Revision != 1 {
		t.Fatal("conflict discarded the immutable retirement preview")
	}
	host.MarkLayout()
	host.Layout()
	host.Focus(ActionsID)
	if result := host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter}); result.Cmd != nil || screen.page != pageDetail {
		t.Fatal("retirement conflict did not restore Cancel as the safe default")
	}
}

func TestBrowserNarrowRetireCancelAndConfirmation(t *testing.T) {
	screen, host := browserHost(t, testRecords())
	detail := commandMessage[DetailMsg](t, host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter}))
	screen.ApplyDetail(detail, DetailData{Record: testRecords()[0].Record}, nil)
	paintHost(host, 20, 11)
	screen.HandleAction(ui.EventContext{}, ui.Action{ID: "memorybrowser.retire"})
	host.MarkLayout()
	host.Layout()
	host.Focus(ActionListID)
	if result := host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter}); result.Cmd != nil || screen.page != pageDetail {
		t.Fatal("narrow default confirmation must cancel")
	}
	screen.HandleAction(ui.EventContext{}, ui.Action{ID: "memorybrowser.retire"})
	host.MarkLayout()
	host.Layout()
	host.Focus(ActionListID)
	host.Handle(tea.KeyPressMsg{Code: tea.KeyDown})
	mutation := commandMessage[MutationMsg](t, host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter}))
	if mutation.ExpectedRevision != 1 || mutation.Kind != MutationRetire {
		t.Fatalf("narrow confirmation preview = %#v", mutation)
	}
}

func TestBrowserConfirmationFocusAdaptsWhenActionLayoutChanges(t *testing.T) {
	screen, host := browserHost(t, testRecords())
	detail := commandMessage[DetailMsg](t, host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter}))
	screen.ApplyDetail(detail, DetailData{Record: testRecords()[0].Record}, nil)
	paintHost(host, 24, 11)
	if !screen.compactActions {
		t.Fatal("detail actions should use narrow list")
	}
	screen.HandleAction(ui.EventContext{}, ui.Action{ID: "memorybrowser.menu"})
	host.MarkLayout()
	host.Layout()
	host.Focus(ActionListID)
	for index, item := range screen.actionList.Items() {
		if item.ID == "memorybrowser.retire" {
			screen.actionList.SelectIndex(index)
			break
		}
	}
	result := host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	if result.Cmd != nil || host.Focused() != ActionsID {
		t.Fatalf("Cancel must own focus after list-to-bar adaptation: %s %#v", host.Focused(), result)
	}
	result = host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	if result.Cmd != nil || screen.page != pageDetail {
		t.Fatal("default Enter mutated after action-layout adaptation")
	}
}

func TestBrowserEmptyState(t *testing.T) {
	_, host := browserHost(t, nil)
	text := bufferText(paintHost(host, 78, 14))
	if !strings.Contains(text, "No knowledge yet") {
		t.Fatalf("empty state missing:\n%s", text)
	}
}

func TestBrowserErrorIsNotEmptyAndCloseReopenPreservesQuery(t *testing.T) {
	screen, host := browserHost(t, nil)
	screen.search.field.SetValue("retained query")
	old := screen.Refresh()().(SearchMsg)
	screen.ApplySearch(old, nil, nil, errors.New("database offline"))
	host.MarkLayout()
	text := bufferText(paintHost(host, 78, 14))
	if !strings.Contains(text, "Search failed") || !strings.Contains(text, "Retry") || strings.Contains(text, "No knowledge") {
		t.Fatalf("error is presented as empty:\n%s", text)
	}
	screen.Close()
	reopened := screen.Open()().(SearchMsg)
	if screen.Query() != "retained query" || reopened.Epoch == old.Epoch || screen.ApplySearch(old, testRecords(), nil, nil) {
		t.Fatal("close/reopen ownership or retained query failed")
	}
}

func TestBrowserFullDetailScrollAndCopy(t *testing.T) {
	screen, host := browserHost(t, testRecords())
	request := commandMessage[DetailMsg](t, host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter}))
	record := testRecords()[0].Record
	detail := DetailData{Record: record, Revisions: []memory.Revision{{Record: record, Op: "remember"}}, Feedback: []memory.Feedback{{RevisionRef: memory.RevisionRef{ID: record.ID, Revision: 1}, Kind: memory.FeedbackUseful, Evidence: record.Evidence}}, Exposures: []memory.Exposure{{RevisionRef: memory.RevisionRef{ID: record.ID, Revision: 1}, Kind: "automatic", SessionID: "s2"}}}
	screen.ApplyDetail(request, detail, nil)
	host.MarkLayout()
	paintHost(host, 78, 14)
	host.Focus(DetailID)
	for _, want := range []string{"Run go test", "Applicability", "Before handing off", "tool-test", "session:s1 turn:3", "Immutable revision history", "useful", "not usefulness"} {
		if !strings.Contains(screen.reader.area.Value(), want) {
			t.Fatalf("complete detail missing %q", want)
		}
	}
	initial := screen.reader.area.ScrollOffset()
	host.Handle(tea.KeyPressMsg{Code: tea.KeyPgDown})
	host.Handle(tea.KeyPressMsg{Code: tea.KeyPgDown})
	if screen.reader.area.ScrollOffset() <= initial {
		t.Fatal("detail does not scroll")
	}
	offset := screen.reader.area.ScrollOffset()
	host.MarkLayout()
	paintHost(host, 78, 14)
	if screen.reader.area.ScrollOffset() != offset {
		t.Fatal("repaint reset detail scroll")
	}
	var copied string
	screen.SetClipboardCopy(func(text string) tea.Cmd { return func() tea.Msg { copied = text; return nil } })
	result := screen.HandleAction(ui.EventContext{}, ui.Action{ID: "memorybrowser.copy"})
	result.Cmd()
	if copied != screen.reader.area.Value() {
		t.Fatal("copy truncated the detail")
	}
	before := screen.reader.area.Value()
	host.Handle(tea.KeyPressMsg{Code: 'x', Text: "x"})
	if screen.reader.area.Value() != before {
		t.Fatal("read-only detail was edited")
	}
}

func TestBrowserCorrectionConflictKeepsDraftEvidenceAndRevision(t *testing.T) {
	screen, host := browserHost(t, testRecords())
	request := commandMessage[DetailMsg](t, host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter}))
	screen.ApplyDetail(request, DetailData{Record: testRecords()[0].Record}, nil)
	screen.HandleAction(ui.EventContext{}, ui.Action{ID: "memorybrowser.correct"})
	screen.formModel.SetValue("content", ui.StringValue("User correction must survive a late extraction"))
	screen.formModel.SetValue("applies_when", ui.StringValue("Only for focused tests"))
	mutation := commandMessage[MutationMsg](t, screen.HandleAction(ui.EventContext{}, ui.Action{ID: "memorybrowser.save"}))
	screen.ApplyMutation(mutation, memory.Record{}, memory.ErrRevisionConflict)
	draft, ok := screen.Draft()
	if !ok || draft.Content != mutation.Replacement.Content || draft.AppliesWhen != mutation.Replacement.AppliesWhen || draft.Revision != 1 || len(draft.Evidence) != 1 || draft.Evidence[0].ToolCallID != "tool-test" {
		t.Fatalf("conflict changed correction draft: %#v", draft)
	}
	if screen.page != pageCorrect || screen.MutationError() == nil {
		t.Fatal("conflict closed the correction")
	}
	if screen.ReloadDetail() != nil {
		t.Fatal("reload must not overwrite a correction draft")
	}
}

func TestBrowserEpisodesAreNotMutableKnowledge(t *testing.T) {
	screen, host := browserHost(t, testRecords())
	request := commandMessage[SearchMsg](t, screen.HandleAction(ui.EventContext{}, ui.Action{ID: "tabs.activate", Payload: ModeEpisodes}))
	if request.Mode != ModeEpisodes {
		t.Fatalf("mode = %#v", request)
	}
	episode := memory.Episode{ID: "ep1", SessionID: "s1", Method: "go test", Outcome: memory.OutcomeFailed, Situation: "Investigate failure", Conditions: "narrow terminal"}
	screen.ApplySearch(request, nil, []memory.Episode{episode}, nil)
	host.MarkLayout()
	host.Layout()
	host.Focus(ListID)
	detail := commandMessage[DetailMsg](t, host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter}))
	screen.ApplyDetail(detail, DetailData{Episode: episode}, nil)
	if !strings.Contains(screen.reader.area.Value(), "failed") || !strings.Contains(screen.reader.area.Value(), "not verified general knowledge") {
		t.Fatal("episode outcome/source distinction missing")
	}
	for _, action := range []string{"pin", "correct", "retire"} {
		result := screen.HandleAction(ui.EventContext{}, ui.Action{ID: "memorybrowser." + action})
		if result.Cmd != nil || screen.draft != nil || screen.retirePreview != nil {
			t.Fatalf("episode accepted %s", action)
		}
	}
}

func TestBrowserDetailAndMutationCloseReopenRejectStaleResults(t *testing.T) {
	screen, host := browserHost(t, testRecords())
	detail := commandMessage[DetailMsg](t, host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter}))
	screen.ApplyDetail(detail, DetailData{Record: testRecords()[0].Record}, nil)
	screen.HandleAction(ui.EventContext{}, ui.Action{ID: "memorybrowser.correct"})
	screen.formModel.SetValue("content", ui.StringValue("Retained user draft"))
	screen.formModel.SetValue("paths", ui.StringValue(""))
	mutation := commandMessage[MutationMsg](t, screen.HandleAction(ui.EventContext{}, ui.Action{ID: "memorybrowser.save"}))
	if mutation.Replacement.ScopePaths == nil || len(mutation.Replacement.ScopePaths) != 0 {
		t.Fatal("empty scope became omitted instead of a deliberate clear")
	}
	screen.Close()
	screen.Open()
	if screen.ApplyDetail(detail, DetailData{Record: testRecords()[1].Record}, nil) || screen.ApplyMutation(mutation, testRecords()[1].Record, nil) {
		t.Fatal("stale completion replaced reopened draft")
	}
	draft, ok := screen.Draft()
	if !ok || draft.Content != "Retained user draft" || screen.formModel.Field("content").ReadOnly {
		t.Fatal("hide/reopen lost or froze the draft")
	}
}

func TestBrowserPaintDoesNotEscapeNestedBounds(t *testing.T) {
	for _, width := range []int{1, 8, 24, 80} {
		for _, height := range []int{1, 6, 12} {
			screen, _ := browserHost(t, testRecords())
			for _, page := range []string{pageBrowse, pageBrowseActions, pageDetail, pageCorrect, pageRetire, pageMenu} {
				if page == pageDetail {
					screen.detail = DetailData{Record: testRecords()[0].Record}
					screen.setReader(detailText(screen.detail))
				}
				if page == pageCorrect {
					screen.beginCorrection()
				}
				if page == pageRetire {
					screen.beginRetire()
				}
				screen.showPage(page)
				screen.Layout(ui.LayoutContext{}, ui.Rect{X: 3, Y: 2, W: width, H: height})
				buffer := surface.NewCellBuffer(width+6, height+4)
				buffer.Fill(cell.NewCell('~', cell.NoColor, cell.NoColor, 0))
				screen.Paint(buffer, 0, 0, ui.PaintContext{})
				for _, child := range screen.Children() {
					child.Paint(buffer, 3, 2, ui.PaintContext{})
				}
				for row := 0; row < buffer.Height(); row++ {
					for column := 0; column < buffer.Width(); column++ {
						if column >= 3 && column < width+3 && row >= 2 && row < height+2 {
							continue
						}
						if buffer.Get(column, row).Char != '~' {
							t.Fatalf("%s at %dx%d paints outside slot at %d,%d", page, width, height, column, row)
						}
					}
				}
			}
		}
	}
}

func TestBrowserDockHeaderAndSharedActions(t *testing.T) {
	screen, host := browserHost(t, testRecords())
	screen.SetDockHeaderInset(9)
	paintHost(host, 100, 18)
	if got := screen.tabs.Bounds(); got.W != 91 || got.H != ui.StripHeight {
		t.Fatalf("tab strip = %#v", got)
	}
	if search, actions := screen.search.Bounds(), screen.actions.Bounds(); search.Y != ui.StripHeight || actions.Y != search.Y || actions.X < search.X+search.W+2 || actions.X+actions.W > 100 {
		t.Fatalf("search=%#v actions=%#v", search, actions)
	}
	if got := screen.list.Bounds(); got.W < 90 || got.Y <= screen.search.Bounds().Y {
		t.Fatalf("browse body lost width or overlaps search: %#v", got)
	}
	buf := paintHost(host, 100, 18)
	for x := 91; x < 100; x++ {
		if got := buf.Get(x, 0).Char; got != 0 && got != ' ' {
			t.Fatalf("reserved top row painted at %d: %q", x, got)
		}
	}
	if got := buf.Get(99, 1).Char; got == 0 || got == ' ' {
		t.Fatalf("full-width tab baseline missing at right edge: %q", got)
	}
	screen.Layout(ui.LayoutContext{}, ui.Rect{W: 22, H: 12})
	if !screen.browseOverflow || screen.browseMore.Bounds().Y != screen.search.Bounds().Y {
		t.Fatalf("narrow browse lost aligned search and More: search=%#v actions=%#v", screen.search.Bounds(), screen.browseMore.Bounds())
	}
	screen.showPage(pageDetail)
	screen.Layout(ui.LayoutContext{}, ui.Rect{W: 80, H: 12})
	if got := screen.title.Bounds().W; got != 71 {
		t.Fatalf("detail title width = %d", got)
	}
	screen.showPage(pageBrowse)
	host.MarkLayout()
	paintHost(host, 100, 18)
	result := host.Handle(tea.MouseClickMsg{X: screen.actions.Bounds().X + 3, Y: screen.actions.Bounds().Y, Button: tea.MouseLeft})
	request := commandMessage[DetailMsg](t, result)
	if request.ID != testRecords()[0].ID || screen.page != pageDetail {
		t.Fatalf("shared action click did not inspect the selected record: %#v", request)
	}
}

func TestBrowserNormalNarrowGeometryAndTheme(t *testing.T) {
	for _, width := range []int{0, 1, 8, 24, 40, 80, 120} {
		for _, height := range []int{0, 1, 6, 11, 20} {
			t.Run(fmt.Sprintf("%dx%d", width, height), func(t *testing.T) {
				screen, host := browserHost(t, testRecords())
				for _, page := range []string{pageBrowse, pageBrowseActions, pageDetail, pageCorrect, pageRetire, pageMenu} {
					if page == pageDetail {
						screen.detail = DetailData{Record: testRecords()[0].Record}
						screen.setReader(detailText(screen.detail))
					}
					if page == pageCorrect {
						screen.beginCorrection()
					}
					if page == pageRetire {
						screen.beginRetire()
					}
					screen.showPage(page)
					screen.Layout(ui.LayoutContext{}, ui.Rect{X: 3, Y: 2, W: width, H: height})
					for _, child := range screen.Children() {
						bounds := child.Bounds()
						if bounds.X < 0 || bounds.Y < 0 || bounds.W < 0 || bounds.H < 0 || bounds.X+bounds.W > width || bounds.Y+bounds.H > height {
							t.Fatalf("%s %s bounds=%#v exceed %dx%d", page, child.ID(), bounds, width, height)
						}
					}
					if width > 0 && height > 0 {
						host.MarkLayout()
						paintHost(host, width, height)
					}
				}
				screen.SetTheme(theme.DefaultLight())
				if screen.draft == nil || len(screen.records) != 2 {
					t.Fatal("theme reset retained state")
				}
			})
		}
	}
}

func TestBrowserNarrowSearchMoreRetainsActionsAndQuery(t *testing.T) {
	screen, host := browserHost(t, testRecords())
	screen.search.field.SetValue("retained")
	host.MarkLayout()
	paintHost(host, 40, 16)
	if !screen.browseOverflow || screen.browseMore.Bounds().Y != screen.search.Bounds().Y {
		t.Fatal("narrow browse must align search and More")
	}
	button := screen.browseMore.Bounds()
	host.Handle(tea.MouseClickMsg{X: button.X + 3, Y: button.Y, Button: tea.MouseLeft})
	if screen.page != pageBrowseActions || host.Focused() != ActionListID {
		t.Fatal("More did not open the accessible actions list")
	}
	for _, action := range []string{"memorybrowser.inspect", "memorybrowser.retry", "memorybrowser.hide", "memorybrowser.back"} {
		found := false
		for _, item := range screen.actionList.Items() {
			found = found || item.ID == action
		}
		if !found {
			t.Fatalf("narrow More lost %s", action)
		}
	}
	host.Handle(tea.KeyPressMsg{Code: tea.KeyEsc})
	if screen.page != pageBrowse || screen.Query() != "retained" || host.Focused() != SearchID {
		t.Fatal("closing More lost browse query or focus")
	}
	host.MarkLayout()
	paintHost(host, 40, 16)
	host.Focus(ListID)
	host.Handle(tea.KeyPressMsg{Code: tea.KeyTab})
	if host.Focused() != BrowseMoreID {
		t.Fatal("list Tab did not focus the aligned More control")
	}
}
