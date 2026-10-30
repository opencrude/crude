package editor

import (
	"path/filepath"
	"strings"
	"testing"

	"crude/document"
	"crude/syntax"
	"crude/tui/theme"
	"crude/types"
)

func sharedSyntaxEditor(t *testing.T, source string) (*Editor, document.Snapshot) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "remote.py")
	snapshot := document.Snapshot{ID: "canonical-only", Path: path, Revision: 1, Text: []byte(source), Saved: []byte(source)}
	ed := New(theme.DefaultDark())
	t.Cleanup(func() { closeSyntax(ed) })
	if err := ed.InstallDocument(snapshot); err != nil {
		t.Fatal(err)
	}
	if err := ed.BindDocumentOwner(projectionOwner); err != nil {
		t.Fatal(err)
	}
	if ed.SyntaxCommand() != nil || ed.syntaxJob != nil {
		t.Fatal("clean canonical source started a client-local syntax job")
	}
	return ed, snapshot
}

func syntaxPublication(snapshot document.Snapshot, version uint64, kind syntax.Kind) types.DocumentInsight {
	return types.DocumentInsight{
		DocumentID: snapshot.ID, Path: snapshot.Path, Revision: snapshot.Revision,
		Generation: "core-generation-one", Version: version, State: types.InsightCurrent,
		Syntax: &syntax.Result{Revision: snapshot.Revision, Spans: []syntax.Span{
			{Start: 0, End: uint32(len(snapshot.Text)), Kind: kind},
		}},
	}
}

func TestInsightCanonicalPaintUsesExactRemoteByteSpans(t *testing.T) {
	// The path does not exist on the client. Canonical text includes tab, CJK,
	// combining marks, CRLF and ZWJ; projection must share normal source geometry.
	source := "\t界e\u0301👩‍💻 tail\r\n# remote\r\n"
	ed, snapshot := sharedSyntaxEditor(t, source)
	ed.InstallDocumentInsight(syntaxPublication(snapshot, 1, syntax.Comment))
	for _, width := range []int{120, 40} {
		buf := paintEditor(ed, width, 8)
		if got := buf.Get(ed.gutterWidth()+4, 0); got.Char != '界' || !got.FG.Equal(ed.cellColor(ed.th.Colors.Code.Comment)) {
			t.Fatalf("canonical byte scope not painted at tab/CJK cells: %+v", got)
		}
		if got := buf.Get(ed.gutterWidth(), 1); got.Char != '#' || !got.FG.Equal(ed.cellColor(ed.th.Colors.Code.Comment)) {
			t.Fatalf("CRLF moved canonical line: %+v", got)
		}
	}
	if ed.Value() != source || ed.DocumentState().Revision != 1 || ed.syntaxJob != nil {
		t.Fatal("painting remote insight changed source or started a local authority")
	}
	ed.SetTheme(theme.DefaultLight())
	if got := paintEditor(ed, 80, 8).Get(ed.gutterWidth(), 1); !got.FG.Equal(ed.cellColor(ed.th.Colors.Code.Comment)) {
		t.Fatalf("canonical spans kept old theme ink: %+v", got)
	}
}

func TestInsightFuturePublicationDoesNotErasePrivateParseOrDraft(t *testing.T) {
	ed, snapshot := sharedSyntaxEditor(t, "# before\n")
	ed.InstallDocumentInsight(syntaxPublication(snapshot, 1, syntax.Comment))
	paintEditor(ed, 80, 8)
	ed.SetCursorPosition(0, len("# before"))
	ed.InsertRune('!')
	command := ed.SyntaxCommand()
	if command == nil {
		t.Fatal("optimistic text has no provisional parse")
	}
	job := ed.syntaxJob
	snapshot.Revision++
	snapshot.Text = []byte("# peer future\n")
	if !ed.InstallDocumentInsight(syntaxPublication(snapshot, 2, syntax.String)) {
		t.Fatal("future canonical result not retained for exact later text")
	}
	if ed.syntaxJob != job || job.generation != ed.syntaxGeneration || ed.Value() != "# before!\n" {
		t.Fatal("canonical push cancelled the private parse or replaced its draft")
	}
	command().(SyntaxReadyMsg).Apply()
	if status, detail := ed.InsightStatus(); status != types.InsightProvisional || !strings.Contains(detail, "Provisional") {
		t.Fatalf("private text claimed canonical currency: %q %q", status, detail)
	}
	if got := paintEditor(ed, 80, 8).Get(ed.gutterWidth(), 0); !got.FG.Equal(ed.cellColor(ed.th.Colors.Code.Comment)) {
		t.Fatalf("future shared string leaked onto private comment: %+v", got)
	}
}

func TestInsightReceiptHandoffReplacesProvisionalInk(t *testing.T) {
	ed, snapshot := sharedSyntaxEditor(t, "# before\n")
	ed.InstallDocumentInsight(syntaxPublication(snapshot, 1, syntax.Comment))
	paintEditor(ed, 80, 8)
	ed.SetCursorPosition(0, len("# before"))
	ed.InsertRune('!')
	change, err := ed.CaptureDocumentEdits(projectionOwner, "edit", "group")
	if err != nil {
		t.Fatal(err)
	}
	// Deliberately different but valid test scopes make stale-cache reuse
	// visible. Independent syntax corpus tests check actual grammar scope.
	snapshot.Revision++
	snapshot.Text = []byte("# before!\n")
	ed.InstallDocumentInsight(syntaxPublication(snapshot, 2, syntax.String))
	settleSyntax(t, ed)
	paintEditor(ed, 80, 8)
	if err := ed.AcceptDocumentReceipt(document.Receipt{
		DocumentID: snapshot.ID, RequestID: change.RequestID, Accepted: true, Before: 1, Revision: 2,
	}); err != nil {
		t.Fatal(err)
	}
	if got := paintEditor(ed, 80, 8).Get(ed.gutterWidth(), 0); !got.FG.Equal(ed.cellColor(ed.th.Colors.Code.String)) {
		t.Fatalf("matching receipt left provisional ink installed: %+v", got)
	}
	if ed.SyntaxCommand() != nil {
		t.Fatal("acknowledged text restarted a local semantic authority")
	}
}

func TestInsightPendingAfterEditReceiptKeepsExactProvisionalPresentation(t *testing.T) {
	ed, snapshot := sharedSyntaxEditor(t, "# before\nvalue = 42\n")
	ed.InstallDocumentInsight(syntaxPublication(snapshot, 1, syntax.Comment))
	paintEditor(ed, 80, 8)
	ed.SetCursorPosition(1, len("value"))
	ed.InsertRune('!')
	change, err := ed.CaptureDocumentEdits(projectionOwner, "edit", "group")
	if err != nil {
		t.Fatal(err)
	}
	command := ed.SyntaxCommand()
	if command == nil {
		t.Fatal("no provisional source preparation")
	}
	ready := command().(SyntaxReadyMsg) // completed, not yet installed
	if err := ed.AcceptDocumentReceipt(document.Receipt{
		DocumentID: snapshot.ID, RequestID: change.RequestID, Accepted: true, Before: 1, Revision: 2,
	}); err != nil {
		t.Fatal(err)
	}
	ed.InstallDocumentInsight(types.DocumentInsight{
		DocumentID: snapshot.ID, Path: snapshot.Path, Revision: 2, Generation: "core-generation-one",
		Version: 2, State: types.InsightPending,
	})
	ready.Apply()
	if got := paintEditor(ed, 80, 8).Get(ed.gutterWidth()+2, 0); !got.FG.Equal(ed.cellColor(ed.th.Colors.Code.Comment)) {
		t.Fatalf("receipt followed by shared pending blanked unrelated exact source: %+v", got)
	}
	if state, _ := ed.InsightStatus(); state != types.InsightPending || ed.SyntaxCommand() != nil {
		t.Fatal("retained provisional ink became a new canonical analysis authority")
	}
}

func TestInsightOldRevisionAndNewCoreGeneration(t *testing.T) {
	ed, snapshot := sharedSyntaxEditor(t, "# source\n")
	latest := syntaxPublication(snapshot, 100, syntax.Comment)
	if !ed.InstallDocumentInsight(latest) || ed.InstallDocumentInsight(syntaxPublication(snapshot, 99, syntax.String)) {
		t.Fatal("same-generation ordering failed")
	}
	fresh := syntaxPublication(snapshot, 1, syntax.String)
	fresh.Generation = "new-core-after-restart"
	if !ed.InstallDocumentInsight(fresh) {
		t.Fatal("new core generation was mistaken for an older same-core version")
	}
	if got := paintEditor(ed, 80, 8).Get(ed.gutterWidth()+2, 0); !got.FG.Equal(ed.cellColor(ed.th.Colors.Code.String)) {
		t.Fatalf("fresh core generation did not replace non-cursor ink: %+v", got)
	}
	snapshot.Revision = 0
	if ed.InstallDocumentInsight(syntaxPublication(snapshot, 200, syntax.Comment)) {
		t.Fatal("old source revision accepted as current")
	}
	ed.Close()
	snapshot.Revision = 1
	if ed.InstallDocumentInsight(syntaxPublication(snapshot, 201, syntax.Comment)) {
		t.Fatal("closed editor accepted native handles/state")
	}
}
