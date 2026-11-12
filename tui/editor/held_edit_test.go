package editor

import (
	"errors"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/document"
	"crude/types"
)

// heldSuggestion replaces "pr" on the first line with "print".
var heldSuggestion = types.CompletionItem{Label: "print",
	TextEdit: &types.LSPTextEdit{Line: 0, Col: 0, EndLine: 0, EndCol: 2, NewText: "print"}}

// heldEditor accepts a suggestion under a hold and types x behind it, as a user
// who presses Tab and keeps typing before the document owner answers.
func heldEditor(t *testing.T) (*Editor, document.Snapshot) {
	t.Helper()
	e, s := projectionEditor(t, "pr\nend")
	// Suggestions require an analysed canonical source identity.
	s.AnalysisGeneration, s.AnalysisLifetime = "analysis", "open"
	if err := e.InstallDocument(s); err != nil {
		t.Fatal(err)
	}
	e.SetCursorPosition(0, 2)
	hold, ok := e.HoldDocumentInput()
	if !ok {
		t.Fatal("a settled projection refused a hold")
	}
	e.HandleEvent(tea.KeyPressMsg{Code: 'x', Text: "x"})
	if e.Value() != "pr\nend" || !e.HoldsDocumentInput() {
		t.Fatal("typing did not wait behind the acceptance", e.Value())
	}
	if _, current := e.CurrentInsightSource(); !current {
		t.Fatal("held input made the unchanged canonical source look stale")
	}
	if err := e.ApplyHeldCompletion(hold, heldSuggestion); err != nil {
		t.Fatal(err)
	}
	if e.Value() != "print\nend" {
		t.Fatal("held suggestion was not applied", e.Value())
	}
	return e, s
}

func TestHeldCompletionCommitsAtItsRevisionThenReplaysTyping(t *testing.T) {
	e, s := heldEditor(t)
	c, err := e.CaptureDocumentEdits(projectionOwner, "accept", "accept")
	if err != nil || c.BaseRevision != s.Revision {
		t.Fatal("guarded edit did not describe its original revision", c, err)
	}
	e.DrainDocumentUndo()
	if e.Value() != "print\nend" {
		t.Fatal("typing replayed before the insertion was committed", e.Value())
	}
	if err := e.AcceptDocumentReceipt(document.Receipt{DocumentID: s.ID, RequestID: "accept", Before: 1, Revision: 2, Accepted: true}); err != nil {
		t.Fatal(err)
	}
	e.DrainDocumentUndo()
	if e.Value() != "printx\nend" || e.HoldsDocumentInput() || e.ConsumeRefusedHeldEdit() {
		t.Fatal("typing did not follow the committed insertion", e.Value())
	}
}

func TestHeldCompletionRefusedByOwnerLeavesViewAndKeepsTyping(t *testing.T) {
	e, s := heldEditor(t)
	if _, err := e.CaptureDocumentEdits(projectionOwner, "accept", "accept"); err != nil {
		t.Fatal(err)
	}
	// Another shell committed first; the owner refuses the stale base.
	err := e.AcceptDocumentReceipt(document.Receipt{DocumentID: s.ID, RequestID: "accept", Before: 2, Revision: 2, Error: document.ErrRevision.Error()})
	if !errors.Is(err, ErrHeldEditRefused) {
		t.Fatal("refused guarded edit was retained for a rebase", err)
	}
	if e.Value() != "pr\nend" || e.CursorCol() != 2 || e.DocumentState().PendingEdits != 0 || e.DocumentState().Conflict != nil {
		t.Fatal("refused insertion did not leave the view exactly", e.Value(), e.CursorCol(), e.DocumentState())
	}
	if !e.ConsumeRefusedHeldEdit() || e.ConsumeRefusedHeldEdit() {
		t.Fatal("refusal was not reported exactly once")
	}
	peer := document.Change{DocumentID: s.ID, BaseRevision: 1, Edits: []document.Edit{{Start: 0, End: 0, Text: "// "}}}
	after := s
	after.Revision, after.Text = 2, []byte("// pr\nend")
	if err := e.ApplyDocumentDelta(peer, after); err != nil {
		t.Fatal(err)
	}
	e.DrainDocumentUndo()
	if e.Value() != "// prx\nend" {
		t.Fatal("typing was lost or reordered after the refusal", e.Value())
	}
}

func TestHeldCompletionNeverRebasesOverNewerCanonicalText(t *testing.T) {
	for _, path := range []string{"publication", "sync"} {
		t.Run(path, func(t *testing.T) {
			e, s := heldEditor(t)
			// A disjoint peer edit reaches this shell before the guarded edit is
			// captured. Rebasing would apply the old suggestion to newer text.
			peer := document.Change{DocumentID: s.ID, BaseRevision: 1, Edits: []document.Edit{{Start: 3, End: 6, Text: "done"}}}
			if path == "sync" {
				after := s
				after.Revision, after.Text = 2, []byte("pr\ndone")
				if err := e.ApplyDocumentDelta(peer, after); err != nil {
					t.Fatal(err)
				}
			} else {
				state := s
				state.Revision, state.Text, state.Saved = 2, nil, nil
				plan, err := e.PrepareDocumentUpdate(projectionOwner, 1, []document.Change{peer}, state, nil, false)
				if err != nil {
					t.Fatal(err)
				}
				if err := e.CommitDocumentUpdate(plan); err != nil {
					t.Fatal(err)
				}
			}
			if e.Value() != "pr\ndone" || e.HoldsDocumentInput() || !e.ConsumeRefusedHeldEdit() {
				t.Fatal("old suggestion was rebased onto newer canonical text", e.Value())
			}
			e.DrainDocumentUndo()
			if e.Value() != "prx\ndone" {
				t.Fatal("typing behind the refused suggestion was lost", e.Value())
			}
		})
	}
}

func TestReleasedHoldReplaysTypingAndOnlySettledViewsHold(t *testing.T) {
	e, _ := projectionEditor(t, "pr")
	e.SetCursorPosition(0, 2)
	hold, _ := e.HoldDocumentInput()
	if _, again := e.HoldDocumentInput(); again {
		t.Fatal("two acceptances held the same document")
	}
	e.HandleEvent(tea.KeyPressMsg{Code: '.', Text: "."})
	e.ReleaseDocumentHold(DocumentHold{}) // foreign token
	if !e.HoldsDocumentInput() {
		t.Fatal("a foreign token released the hold")
	}
	e.ReleaseDocumentHold(hold)
	e.DrainDocumentUndo()
	if e.Value() != "pr." || e.HoldsDocumentInput() {
		t.Fatal("released hold lost the typed character", e.Value())
	}
	if _, ok := e.HoldDocumentInput(); ok {
		t.Fatal("unacknowledged typing could be held under a suggestion")
	}
	if err := e.ApplyHeldCompletion(hold, heldSuggestion); !errors.Is(err, ErrDocumentPending) {
		t.Fatal("a released hold applied a suggestion", err)
	}
}

// A rejected peer publication must leave the accepted suggestion and queued
// typing untouched. Validation precedes any optimistic-view mutation.
func TestInvalidPeerDeltaDoesNotRetireHeldCompletion(t *testing.T) {
	e, s := heldEditor(t)
	peer := document.Change{DocumentID: s.ID, BaseRevision: s.Revision, Edits: []document.Edit{{Start: 3, End: 6, Text: "done"}}}
	invalid := s
	invalid.Revision, invalid.Text = 2, []byte("a different snapshot")
	if err := e.ApplyDocumentDelta(peer, invalid); !errors.Is(err, document.ErrRevision) {
		t.Fatal("mismatched peer snapshot was accepted", err)
	}
	if e.Value() != "print\nend" || !e.HoldsDocumentInput() || e.DocumentState().PendingEdits == 0 || e.ConsumeRefusedHeldEdit() {
		t.Fatal("invalid peer delta changed the held suggestion or lost input", e.Value(), e.DocumentState())
	}
	after := s
	after.Revision, after.Text = 2, []byte("pr\ndone")
	if err := e.ApplyDocumentDelta(peer, after); err != nil {
		t.Fatal(err)
	}
	e.DrainDocumentUndo()
	if e.Value() != "prx\ndone" || e.HoldsDocumentInput() {
		t.Fatal("valid peer edit did not preserve later typing", e.Value())
	}
}
