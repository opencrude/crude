package viewmodel

import (
	"encoding/json"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/clientstate"
	"crude/shellport"
)

func attachViewMemoryProfile(t *testing.T, store *clientstate.Store, shell remoteJourneyShell, profile string) clientstate.Attachment {
	t.Helper()
	attachment, err := store.Acquire(t.Context(), profile, clientstate.AcquireOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := shell.app.ConfigureComposerDrafts(store, attachment, shell.app.ws.(shellport.Connection).WorkspaceID()); err != nil {
		t.Fatal(err)
	}
	return attachment
}

// The user minimizes an agent and hides dotfiles in one terminal, while a
// second terminal on the same workspace keeps its own view. When the first
// terminal comes back, the minimized agent stays minimized, the agent it was
// looking at is shown again and dotfiles stay hidden.
func TestReturningClientKeepsItsOwnViewChoices(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	store, err := clientstate.Open(filepath.Join(t.TempDir(), "clients.db"))
	if err != nil {
		t.Fatal(err)
	}
	// Registered first, so it closes after every shell's teardown barrier.
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	runtime := remoteJourneyRuntime(t, "http://127.0.0.1:1")
	root := t.TempDir()
	left := newRemoteJourneyShell(t, runtime, root)
	leftProfile := attachViewMemoryProfile(t, store, left, "left-terminal")
	minimized := remoteJourneyCreate(t, left)
	kept := remoteJourneyCreate(t, left)
	right := newRemoteJourneyShell(t, runtime, root)
	attachViewMemoryProfile(t, store, right, "right-terminal")
	shells := []remoteJourneyShell{left, right}
	remoteJourneyUntil(t, shells, func() bool { return right.app.ws.AgentMgr().Get(kept) != nil })

	_, cmd := left.app.Update(tea.KeyPressMsg{Code: tea.KeyRight}) // any input ends the frame
	runAppCommand(t, left.app, cmd)
	left.app.minimizeAgent(minimized)
	left.app.restoreAgent(kept)
	left.app.sidebarScreen.SetShowHidden(false) // what the tree's Ctrl+. does
	_, cmd = left.app.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	runAppCommand(t, left.app, cmd)

	if right.app.AgentView.IsHidden(minimized) || !agentTabPresent(right.app, minimized) || !right.app.Tree.Model.ShowHidden() {
		t.Fatal("one terminal's view choices changed another terminal")
	}
	// Leaving the workspace runs the teardown barrier; the profile is then
	// released, exactly as closing the terminal does.
	left.app.StopAsyncIO()
	if err := store.Release(t.Context(), leftProfile.ClientID, leftProfile.Epoch); err != nil {
		t.Fatal(err)
	}
	left.app.composerDraftClient, left.app.viewMemory = nil, nil
	// The other terminal saves its own view later; that must not win.
	right.app.flushViewMemory()

	back := newRemoteJourneyShell(t, runtime, root)
	attachViewMemoryProfile(t, store, back, "left-terminal")
	shells = append(shells, back)
	remoteJourneyUntil(t, shells, func() bool {
		return back.app.ws.AgentMgr().Get(minimized) != nil && back.app.AgentView.ActiveID() == kept
	})
	assertAgentIsPaintedView(t, back.app, kept)
	if agentTabPresent(back.app, minimized) || !back.app.AgentView.IsHidden(minimized) {
		t.Fatal("an agent minimized in this terminal came back as an open tab")
	}
	if back.app.Tree.Model.ShowHidden() {
		t.Fatal("the tree forgot that this terminal hides dotfiles")
	}
	// The returning view stays restorable: one click brings the agent back.
	back.app.restoreAgent(minimized)
	assertAgentIsPaintedView(t, back.app, minimized)

	raw, _, err := store.LoadViewMemory(t.Context(), mustProfile(t, store, "right-terminal"), back.app.composerDraftClient.workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	var memory viewMemory
	if err := json.Unmarshal(raw, &memory); err != nil {
		t.Fatal(err)
	}
	if len(memory.MinimizedAgents) != 0 || memory.ShowHiddenFiles != nil {
		t.Fatalf("the other terminal stored this terminal's choices: %+v", memory)
	}
}

func mustProfile(t *testing.T, store *clientstate.Store, profile string) clientstate.Attachment {
	t.Helper()
	state, err := store.Profile(t.Context(), profile)
	if err != nil {
		t.Fatal(err)
	}
	return clientstate.Attachment{ClientID: profile, Epoch: state.Epoch}
}
