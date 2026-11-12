package viewmodel

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/document"
	"crude/shellport"
	"crude/types"
)

// peerFirstCompletions lets another shell commit a change after the core has
// validated the accepted row and before this shell's insertion reaches the
// document owner: the race that an optimistic rebase used to hide.
type peerFirstCompletions struct {
	shellport.Completions
	peer func() error
	err  chan error
}

func (p *peerFirstCompletions) InspectLSPCompletion(ctx context.Context, set, digest, item string, resolve bool) (types.LSPCompletionInspection, error) {
	result, err := p.Completions.InspectLSPCompletion(ctx, set, digest, item, resolve)
	if err == nil && !resolve {
		p.err <- p.peer()
	}
	return result, err
}

func TestAcceptedSuggestionNeverCommitsOverAnUnseenPeerChange(t *testing.T) {
	initial := "package p\n\npr"
	shells, eds, _, engine, _ := newTypingCompletionFixture(t, initial)
	app := shells[0].app
	eds[0].SetCursorPosition(2, 2)
	engine.base.mu.Lock()
	engine.response = `[{"label":"printValue","filterText":"printValue","kind":3,"insertTextFormat":2,"textEdit":{"range":{"start":{"line":2,"character":0},"end":{"line":2,"character":3}},"newText":"fmt.Println(${1:value})$0"},"additionalTextEdits":[{"range":{"start":{"line":1,"character":0},"end":{"line":1,"character":0}},"newText":"import \"fmt\"\n"}]}]`
	engine.base.mu.Unlock()
	renameConnectedKey(t, shells[0], tea.KeyPressMsg{Code: 'i', Text: "i"})
	remoteJourneyUntil(t, shells, func() bool {
		runAppCommand(t, app, app.autoCompletionCmd(time.Now()))
		return app.completion != nil && app.completion.screen != nil
	})
	source := eds[0].DocumentState()
	peer, ok := shells[1].app.ws.(shellport.LiveDocuments)
	if !ok {
		t.Fatal("the second shell has no shared document port")
	}
	hook := &peerFirstCompletions{Completions: app.completion.port, err: make(chan error, 1), peer: func() error {
		// A disjoint edit: an optimistic rebase would still apply the old
		// suggestion and its import to text the provider never saw.
		r, err := peer.EditDocument(context.Background(), document.Change{DocumentID: source.DocumentID, RequestID: "peer-first",
			Owner: peer.DocumentOwner(), BaseRevision: source.Revision, GroupID: "peer-first",
			Edits: []document.Edit{{Start: 0, End: 0, Text: "// peer\n"}}})
		if err == nil && !r.Accepted {
			err = errors.New(r.Error)
		}
		return err
	}}
	app.completion.port = hook
	_, read := app.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	app.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	if eds[0].Value() != initial+"i" || !eds[0].HoldsDocumentInput() {
		t.Fatal("typing after Tab did not wait for the acceptance", eds[0].Value())
	}
	runAppCommand(t, app, read)
	if err := <-hook.err; err != nil {
		t.Fatal("peer change was not committed first:", err)
	}
	want := "// peer\npackage p\n\nprix"
	remoteJourneyUntil(t, shells, func() bool {
		return eds[0].Value() == want && eds[1].Value() == want && !eds[0].HoldsDocumentInput() &&
			eds[0].DocumentState().PendingEdits == 0 && !eds[0].DocumentState().InFlight
	})
	state := app.liveEditors[eds[0].Primary()]
	if state == nil || state.err != nil || eds[0].DocumentState().Conflict != nil || eds[0].SnippetActive() {
		t.Fatal("a refused suggestion left a conflict or its snippet behind", fmt.Sprint(state))
	}
	if app.Input.Value() != "private draft" {
		t.Fatal("the private draft changed")
	}
}
