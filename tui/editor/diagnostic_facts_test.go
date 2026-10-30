package editor

import (
	"strings"
	"testing"

	"crude/types"
)

func TestDiagnosticPublicationRequiresActualCanonicalSource(t *testing.T) {
	for _, mismatch := range []string{"workspace", "document", "path", "lifetime", "revision", "disk", "unversioned", "reported-version", "stale", "limited"} {
		t.Run(mismatch, func(t *testing.T) {
			ed := loadEditorText(t, "qualified.txt", "\t😀bad")
			setTestDiagnostics(ed, nil)
			facts := testDiagnosticFacts(ed, []LSPDiagnostic{{Line: 0, Col: 3, EndCol: 6, Severity: DiagError}})
			switch mismatch {
			case "workspace":
				facts.Boundary.Source.WorkspaceGeneration = "other-workspace"
			case "document":
				facts.Boundary.Source.DocumentID = "reopened"
			case "path":
				facts.Boundary.Source.Path = "other.txt"
			case "lifetime":
				facts.Boundary.Source.Lifetime = "reopened"
			case "revision":
				facts.Boundary.Source.Revision--
			case "disk":
				facts.Boundary.Source.Kind = "disk"
			case "unversioned":
				facts.Binding, facts.ReportedVersion = "unversioned", nil
			case "reported-version":
				v := 100
				facts.ReportedVersion = &v
			case "stale":
				facts.State = types.InsightStale
			case "limited":
				facts.Limited = true
			}
			ed.SetDiagnostics(facts)
			if len(ed.Diagnostics()) != 0 || len(ed.DiagnosticsAt(0, 6)) != 0 || len(ed.lineDiagnostics(0)) != 0 || ed.FirstError() != nil {
				t.Fatal("non-current source painted or authorized interaction")
			}
			if got := ed.DiagnosticFacts(); got.ID != facts.ID || got.Boundary.Source != facts.Boundary.Source {
				t.Fatal("rejected projection discarded or restamped provenance")
			}
		})
	}
}

func TestFutureDiagnosticWaitsForCanonicalSourceAndCannotStampDraft(t *testing.T) {
	ed := loadEditorText(t, "future.txt", "before")
	setTestDiagnostics(ed, nil)
	facts := testDiagnosticFacts(ed, []LSPDiagnostic{{Line: 0, Col: 2, EndCol: 5, Severity: DiagError}})
	facts.Boundary.Source.Revision = 42
	ed.SetDiagnostics(facts)
	if len(ed.Diagnostics()) != 0 || len(ed.lineDiagnostics(0)) != 0 {
		t.Fatal("future source painted before acknowledged text")
	}
	s := ed.DocumentSnapshot()
	s.Revision, s.Text = 42, []byte("\t😀bad")
	if err := ed.ResetDocument(s); err != nil {
		t.Fatal(err)
	}
	ed.RefreshDiagnostics()
	if d := ed.FirstError(); d != nil {
		// UTF-16 column 2 splits the surrogate, despite tolerant underline paint.
		t.Fatal("split-surrogate publication authorized F8")
	}
	if len(ed.lineDiagnostics(0)) != 1 {
		t.Fatal("exact canonical source did not receive its bounded paint")
	}
	ed.InsertRune('x')
	newFacts := facts
	newFacts.ID = "new-publication-against-pending-baseline"
	ed.SetDiagnostics(newFacts)
	if len(ed.lineDiagnostics(0)) != 0 || ed.FirstError() != nil {
		t.Fatal("new baseline publication stamped optimistic draft")
	}
}

func TestDiagnosticContextUnknownEmptyIsVisibleNotClean(t *testing.T) {
	ed := loadEditorText(t, "empty.txt", "source")
	setTestDiagnostics(ed, nil)
	facts := testDiagnosticFacts(ed, nil)
	facts.Boundary.Context = types.InsightContext{UnknownInputs: []string{"build flags", "dependencies"}}
	ed.SetDiagnostics(facts)
	status := ed.DiagnosticStatus()
	if !strings.Contains(status, "partial/context unknown") || strings.Contains(status, "clean") || ed.DiagnosticFacts().ID == "" {
		t.Fatalf("unknown empty observation looks certified: %q", status)
	}
	facts.ID, facts.Binding, facts.ReportedVersion = "unversioned-empty", "unversioned", nil
	ed.SetDiagnostics(facts)
	if !strings.Contains(ed.DiagnosticStatus(), "unverified") {
		t.Fatal("unversioned empty observation erased coverage uncertainty")
	}
}

func TestDiagnosticUnknownContextCanPaintSourceButRemainsQualified(t *testing.T) {
	ed := loadEditorText(t, "unknown.txt", "\t😀bad")
	setTestDiagnostics(ed, nil)
	facts := testDiagnosticFacts(ed, []LSPDiagnostic{{Line: 0, Col: 3, EndCol: 6, Severity: DiagWarning}})
	facts.Boundary.Context.UnknownInputs = []string{"configuration"}
	ed.SetDiagnostics(facts)
	if len(ed.DiagnosticsAt(0, 6)) != 1 || len(ed.lineDiagnostics(0)) != 1 || !strings.Contains(ed.DiagnosticStatus(), "partial/context unknown") {
		t.Fatal("exact-source partial-context observation was hidden or over-certified")
	}
}

func TestDiagnosticFiltersKeepDetachedBatch(t *testing.T) {
	ed := loadEditorText(t, "filter.txt", "\t😀bad")
	setTestDiagnostics(ed, nil)
	facts := testDiagnosticFacts(ed, []LSPDiagnostic{{Line: 0, Col: 3, EndCol: 6, Severity: DiagError, Message: "original"}})
	ed.SetDiagnostics(facts)
	facts.Diagnostics[0].Message = "caller mutated"
	ed.SetDiagnosticsVisible(false)
	if len(ed.Diagnostics()) != 0 || len(ed.lineDiagnostics(0)) != 0 || !strings.Contains(ed.DiagnosticStatus(), "hidden") {
		t.Fatal("hidden projection still interactive or unlabelled")
	}
	bufferFacts := ed.DocumentBuffer().Diagnostics()
	bufferFacts.Diagnostics[0].Message = "buffer reader mutated"
	if ed.DiagnosticFacts().Diagnostics[0].Message != "original" {
		t.Fatal("publication or explicit read aliased authoritative cache")
	}
	ed.SetDiagnosticsVisible(true)
	if d := ed.FirstError(); d == nil || d.Col != 6 || d.EndCol != 9 || d.Message != "original" {
		t.Fatalf("filter destroyed batch or converted UTF-16 twice: %+v", d)
	}
}

func TestReopenedEditorRejectsPreviousLifetimeEvenAtSameRevision(t *testing.T) {
	ed := loadEditorText(t, "reopen.txt", "bad")
	setTestDiagnostics(ed, []LSPDiagnostic{{Line: 0, EndCol: 3, Severity: DiagError}})
	old, snapshot := ed.DiagnosticFacts(), ed.DocumentSnapshot()
	ed.Close()
	snapshot.AnalysisLifetime = "new-open"
	if err := ed.InstallDocument(snapshot); err != nil {
		t.Fatal(err)
	}
	ed.SetDiagnostics(old)
	if len(ed.DiagnosticsAt(0, 0)) != 0 || len(ed.lineDiagnostics(0)) != 0 {
		t.Fatal("close/reopen revived old lifetime")
	}
}

func TestSameQualifiedBatchTwoEditorShellsProjectsWithoutMutation(t *testing.T) {
	one := loadEditorText(t, "two.txt", "\t😀bad")
	setTestDiagnostics(one, nil)
	two := New(one.th)
	t.Cleanup(two.Close)
	if err := two.InstallDocument(one.DocumentSnapshot()); err != nil {
		t.Fatal(err)
	}
	facts := testDiagnosticFacts(one, []LSPDiagnostic{{Line: 0, Col: 3, EndCol: 6, Severity: DiagError}})
	one.SetDiagnostics(facts)
	two.SetDiagnostics(facts)
	one.InsertRune('x')
	if d := two.FirstError(); d == nil || d.Col != 6 || two.DiagnosticFacts().ID != facts.ID ||
		two.DocumentBuffer().Diagnostics().Diagnostics[0].Col != 3 {
		t.Fatal("one shell consumed, mutated, or invalidated another shell's fact")
	}
}

func TestDiagnosticStrictNavigationRejectsMalformedEndpoints(t *testing.T) {
	ed := loadEditorText(t, "strict.txt", "\t😀bad\nsecond")
	setTestDiagnostics(ed, nil)
	for _, diagnostic := range []LSPDiagnostic{
		{Line: 0, Col: 2, EndCol: 3}, // split surrogate
		{Line: 0, Col: 3, EndCol: 2}, // reversed
		{Line: 0, Col: 3, EndLine: 2, EndCol: 0},
		{Line: 0, Col: 3, EndCol: 99},
	} {
		diagnostic.Severity = DiagError
		ed.SetDiagnostics(testDiagnosticFacts(ed, []LSPDiagnostic{diagnostic}))
		if ed.FirstError() != nil || len(ed.Diagnostics()) != 0 || len(ed.DiagnosticsAt(0, 6)) != 0 {
			t.Fatalf("malformed range authorized interaction: %+v", diagnostic)
		}
	}
}

func TestNewUnverifiedBatchCannotReuseAcceptedProvisionalInk(t *testing.T) {
	ed := loadEditorText(t, "late.txt", "edit\nbad")
	setTestDiagnostics(ed, []LSPDiagnostic{{Line: 1, EndLine: 1, EndCol: 3, Severity: DiagWarning}})
	old := ed.DiagnosticFacts()
	ed.InsertRune('x')
	if len(ed.lineDiagnostics(1)) != 1 {
		t.Fatal("accepted batch lost unchanged provisional ink")
	}
	late := old
	late.ID, late.Binding, late.ReportedVersion = "late-unverified", "unversioned", nil
	ed.SetDiagnostics(late)
	if len(ed.lineDiagnostics(1)) != 0 || ed.DiagnosticFacts().Boundary.Source.Revision != 41 {
		t.Fatal("new unverified batch reused prior accepted paint or stamped local revision")
	}
}
