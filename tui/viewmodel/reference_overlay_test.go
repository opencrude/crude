package viewmodel

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/sessiondb"
	"crude/tui/ui"
	"crude/workspace"
)

type heldReferenceRead struct {
	workspace.SessionReferences
	started chan struct{}
	release chan struct{}
}

func (s *heldReferenceRead) ReadReferenceContent(id string) (string, bool, error) {
	close(s.started)
	<-s.release
	return s.SessionReferences.ReadReferenceContent(id)
}

func TestReferenceOverlayRunningReadCannotCrossCloseOrReplacement(t *testing.T) {
	for _, boundary := range []string{"close", "reopen", "workspace", "session", "pane"} {
		t.Run(boundary, func(t *testing.T) {
			app := newTranscriptAttachmentApp(t)
			ref, err := app.RefStore().CreateReference(sessiondb.CreateRequest{Kind: sessiondb.KindPastedText, Content: "STALE SECRET"})
			if err != nil {
				t.Fatal(err)
			}
			held := &heldReferenceRead{SessionReferences: app.RefStore(), started: make(chan struct{}), release: make(chan struct{})}
			app.ActiveSession.RefStore = held
			command := app.beginReferenceOverlay(ref.ID)
			result := make(chan tea.Msg, 1)
			go func() { result <- command() }()
			select {
			case <-held.started:
			case <-time.After(2 * time.Second):
				t.Fatal("reference read did not start")
			}
			screen := app.sessionTextOverlay()
			switch boundary {
			case "close":
				app.overlayHost.Close(ui.OverlayTextOverlay)
			case "reopen":
				app.beginReferenceOverlay(ref.ID)
			case "workspace":
				app.workspaceSwitchGen.Add(1)
			case "session":
				app.ActiveSession = workspace.NewSession("replacement", t.TempDir(), nil, stubLogger{})
			case "pane":
				app.AddOrchestrator("replacement-pane")
				app.switchToAgentView("replacement-pane")
			}
			close(held.release)
			select {
			case msg := <-result:
				_, _ = app.Update(msg)
			case <-time.After(2 * time.Second):
				t.Fatal("cancelled bounded read did not settle")
			}
			if screen.Value() == ref.Content {
				t.Fatal("running stale read changed replacement owner")
			}
		})
	}
}

func TestReferenceOverlayForeignComposerReferenceCannotOpen(t *testing.T) {
	app := newTranscriptAttachmentApp(t)
	ref := sessiondb.Reference{ID: "foreign", SessionID: "other", Kind: sessiondb.KindPastedText, Content: "foreign content"}
	app.RefStore().AddExisting(ref)
	if app.beginReferenceOverlay(ref.ID) != nil || app.overlayHost.IsOpen(ui.OverlayTextOverlay) {
		t.Fatal("foreign composer reference opened")
	}
}
