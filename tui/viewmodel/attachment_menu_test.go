package viewmodel

import (
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/sessiondb"
	"crude/tui/ui"
	"crude/workspace"
)

func newAttachmentTestApp(t *testing.T) (*App, sessiondb.Reference) {
	t.Helper()
	app := newFullTestApp(t)
	app.ActiveSession = workspace.NewSession("attach-session", t.TempDir(), nil, stubLogger{})
	app.Input.SetReferenceStore(app.ActiveSession.RefStore)
	ref, err := app.ActiveSession.RefStore.CreateReference(sessiondb.CreateRequest{
		Kind:     sessiondb.KindSelectedFile,
		Title:    "notes.txt",
		FilePath: filepath.Join(t.TempDir(), "notes.txt"),
	})
	if err != nil {
		t.Fatal(err)
	}
	return app, ref
}

// TestBuildAttachmentContextMenu guards the per-chip right-click menu: it
// exposes preview/rename/remove and adds "open in editor" for file-backed
// attachments. Plain-draft clicks keep the clipboard menu.
func TestBuildAttachmentContextMenu(t *testing.T) {
	app, ref := newAttachmentTestApp(t)
	app.Input.SetInput("see " + ref.DisplayName() + " now")

	ranges := app.Input.RefRanges()
	if len(ranges) != 1 {
		t.Fatalf("ranges = %+v, want exactly the chip", ranges)
	}
	st := app.buildAttachmentContextMenu(ranges[0])
	if st == nil {
		t.Fatal("attachment menu missing")
	}
	labels := map[string]bool{}
	for _, sec := range st.Sections {
		for _, item := range sec.Items {
			labels[item.Label] = true
		}
	}
	for _, want := range []string{"Open", "Preview in Crude", "Open with default app", "Show in file manager", "Rename…", "Remove", "Open in editor"} {
		if !labels[want] {
			t.Fatalf("attachment menu missing %q: %v", want, labels)
		}
	}

	// A click away from any chip must NOT produce the attachment menu.
	app.inputRect = rectForInputMenu()
	if _, ok := app.refRangeAtMouse(tea.Mouse{X: 90, Y: 1}); ok {
		t.Fatal("plain draft position resolved to a chip")
	}
}

// Detaching must not break references already owned by sent messages.
func TestRemoveAttachmentStripsTokenAndPreservesRef(t *testing.T) {
	app, ref := newAttachmentTestApp(t)
	app.Input.SetInput("keep " + ref.DisplayName() + " tail")
	ranges := app.Input.RefRanges()
	if len(ranges) != 1 {
		t.Fatalf("ranges = %+v", ranges)
	}

	app.removeAttachment(ranges[0])
	if got := app.Input.Value(); got != "keep  tail" {
		t.Fatalf("draft after removal = %q", got)
	}
	deleted, ok := app.ActiveSession.RefStore.GetReference(ref.ID)
	if !ok || !deleted.Active() {
		t.Fatal("draft removal deleted a retained reference")
	}
	if ids := app.Input.ReferencedIDs(); len(ids) != 0 {
		t.Fatalf("stale referenced IDs after removal: %v", ids)
	}
}

// TestReplaceRefSpansRelabelsDraft guards rename-in-place: chip spans are
// captured before the store rename, then every occurrence swaps to the new
// label and still resolves to the same reference ID.
func TestReplaceRefSpansRelabelsDraft(t *testing.T) {
	app, ref := newAttachmentTestApp(t)
	app.Input.SetInput(ref.DisplayName() + " plus " + ref.DisplayName())
	if got := app.Input.Value(); got != "[File1] plus [File1]" {
		t.Fatalf("unexpected seed draft %q", got)
	}

	spans := app.refRangesFor(ref.ID)
	if len(spans) != 2 {
		t.Fatalf("captured spans = %+v, want both occurrences", spans)
	}
	final := "[release notes]"
	if _, ok := app.ActiveSession.RefStore.RenameReference(ref.ID, final); !ok {
		t.Fatal("store rename failed")
	}
	app.replaceRefSpans(spans, final)
	want := final + " plus " + final
	if got := app.Input.Value(); got != want {
		t.Fatalf("draft after rename = %q, want %q", got, want)
	}
	ids := app.Input.ReferencedIDs()
	if len(ids) != 1 || ids[0] != ref.ID {
		t.Fatalf("renamed chips must resolve to the same reference: %v", ids)
	}
}

func TestReferenceLabelsSnapshotActiveAttachments(t *testing.T) {
	app, ref := newAttachmentTestApp(t)
	labels := app.referenceLabels([]string{"missing", ref.ID, ref.ID})
	if len(labels) != 1 || labels[0] != ref.DisplayName() {
		t.Fatalf("reference labels = %v", labels)
	}
}

func TestSubmitInputCarriesReferenceLabelsIntoLiveConversation(t *testing.T) {
	app, ref := newAttachmentTestApp(t)
	agentID := addBackendTestOrchestrator(t, app)

	runAppCommand(t, app, app.submitInput(agentID, ref.DisplayName()+" review this", []string{ref.ID}))

	node := app.ws.AgentMgr().Get(agentID)
	if node == nil || node.State == nil {
		t.Fatal("attachment agent missing")
	}
	messages := node.State.Messages.Messages()
	if len(messages) != 2 || len(messages[0].ReferenceLabels) != 1 ||
		messages[0].ReferenceLabels[0] != ref.DisplayName() ||
		messages[1].Role != workspace.MessageRoleStatus {
		t.Fatalf("live message reference labels = %+v", messages)
	}
}

func rectForInputMenu() ui.Rect { return ui.Rect{X: 0, Y: 0, W: 100, H: 4} }
