package viewmodel

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/editor"
	"crude/tui/rendering"
	"crude/tui/surface"
	"crude/tui/ui"
	ttypes "crude/types"
	"crude/workspace"
)

func gitGutterChar(t *testing.T, ed *editor.Editor, line int) rune {
	t.Helper()
	surf := surface.NewSurface("ed", 50, 6, cell.TransparentBlank())
	ed.Render(surf, ui.PaintContext{Theme: ed.Theme(), Focused: true})
	return surf.Buffer().Get(ed.GutterWidth()-2, line).Char
}

func TestEditorGitAnnotationIgnoresStaleGeneration(t *testing.T) {
	app, paths := newInputContractEditorApp(t, "main.go")
	path := paths[0]
	ws := app.workspaceSwitchGen.Load()
	app.editorGitGen.Store(2)

	app.applyEditorGitAnnotation(editorGitAnnotationMsg{
		generation: 1,
		workspace:  ws,
		path:       path,
		revision:   app.EditorState.Editor.Revision(),
		annotation: workspace.GitFileAnnotation{
			Hunks: []workspace.GitHunk{{NewLines: []string{"added\n"}}},
		},
	})
	if got := gitGutterChar(t, app.EditorState.Editor, 0); got != ' ' {
		t.Fatalf("stale generation painted git gutter %q", got)
	}

	app.applyEditorGitAnnotation(editorGitAnnotationMsg{
		generation: 2,
		workspace:  ws,
		path:       path,
		revision:   app.EditorState.Editor.Revision(),
		annotation: workspace.GitFileAnnotation{
			Hunks: []workspace.GitHunk{{NewLines: []string{"added\n"}}},
			Blame: []workspace.GitBlameLine{{
				Line: 1, Hash: "abc123def456", ShortHash: "abc123d", Author: "Ada",
			}},
		},
	})
	if got := gitGutterChar(t, app.EditorState.Editor, 0); got != '▌' {
		t.Fatalf("current generation git gutter=%q want rail", got)
	}
	if got := app.EditorState.Editor.CursorBlameSummary(); got != "Not committed yet" {
		t.Fatalf("changed-line blame summary=%q", got)
	}
}

func TestEditorGitAnnotationIgnoresWorkspaceSwitch(t *testing.T) {
	app, paths := newInputContractEditorApp(t, "switched.go")
	app.editorGitGen.Store(1)
	app.applyEditorGitAnnotation(editorGitAnnotationMsg{
		generation: 1,
		workspace:  app.workspaceSwitchGen.Load() + 1,
		path:       paths[0],
		revision:   app.EditorState.Editor.Revision(),
		annotation: workspace.GitFileAnnotation{
			Hunks: []workspace.GitHunk{{NewLines: []string{"added\n"}}},
		},
	})
	if got := gitGutterChar(t, app.EditorState.Editor, 0); got != ' ' {
		t.Fatalf("cross-workspace annotation painted git gutter %q", got)
	}
}

func TestEditorContextMenuCopyBlameCommit(t *testing.T) {
	app, paths := newInputContractEditorApp(t, "blame.go")
	menu := app.buildEditorContextMenu(tea.Mouse{})
	item, ok := contextMenuItem(menu, "Copy blame commit")
	if !ok {
		t.Fatal("editor menu is missing Copy blame commit")
	}
	if !item.Disabled {
		t.Fatal("Copy blame commit must stay disabled until blame is loaded")
	}

	app.editorGitGen.Store(1)
	app.applyEditorGitAnnotation(editorGitAnnotationMsg{
		generation: 1,
		workspace:  app.workspaceSwitchGen.Load(),
		path:       paths[0],
		revision:   app.EditorState.Editor.Revision(),
		annotation: workspace.GitFileAnnotation{
			HeadContent: app.EditorState.Editor.Value(),
			Blame: []workspace.GitBlameLine{{
				Line: 1, Hash: "deadbeefcafebabe", ShortHash: "deadbee", Author: "Ada",
			}},
		},
	})
	menu = app.buildEditorContextMenu(tea.Mouse{})
	item, ok = contextMenuItem(menu, "Copy blame commit")
	if !ok || item.Disabled {
		t.Fatal("Copy blame commit should enable once the cursor line has blame")
	}
}

func TestInspectEditorChangeStaysOpenAcrossEquivalentGitRefresh(t *testing.T) {
	app, paths := newInputContractEditorApp(t, "inspect.go")
	ed := app.EditorState.Editor
	hunk := editor.GitHunk{OldLines: []string{"old\n"}, NewLines: []string{"new\n"}}
	ed.SetGitAnnotation(ed.Revision(), "old\n", []editor.GitHunk{hunk}, nil, false)
	app.inspectEditorChange(ed, 0)
	if app.overlayHost.Window(editorChangesOverlay) == nil {
		t.Fatal("change inspector did not open")
	}
	app.editorGitGen.Store(1)
	app.applyEditorGitAnnotation(editorGitAnnotationMsg{
		generation: 1, workspace: app.workspaceSwitchGen.Load(), path: paths[0],
		revision:   app.EditorState.Editor.Revision(),
		annotation: workspace.GitFileAnnotation{HeadContent: "old\n", Hunks: []workspace.GitHunk{{OldLines: []string{"old\n"}, NewLines: []string{"new\n"}}}},
	})
	if app.overlayHost.Window(editorChangesOverlay) == nil {
		t.Fatal("equivalent refresh closed immutable inspection")
	}
}

// Typing must not recompute the gutter Git projection: the burst coalesces into
// one refresh that runs only after the typing pause, so markers stop blinking
// per keystroke and the Git subprocess stops churning.
func TestGutterRefreshWaitsForTypingToPause(t *testing.T) {
	app := newFullTestApp(t)
	base := time.Now()

	app.markEditorGitDirty(base)
	app.markEditorGitDirty(base.Add(100 * time.Millisecond))
	app.markEditorGitDirty(base.Add(200 * time.Millisecond))

	if app.editorGitDue(base.Add(300 * time.Millisecond)) {
		t.Fatal("gutter refreshed while the user was still typing")
	}
	if app.editorGitDue(base.Add(500 * time.Millisecond)) {
		t.Fatal("gutter refreshed 300ms after the last keystroke, want the idle delay")
	}
	if !app.editorGitDue(base.Add(700 * time.Millisecond)) {
		t.Fatal("gutter never refreshed after typing paused")
	}
}

// A continuous burst may not hold the projection stale forever, but it also must
// not refresh on every keystroke: the hold cap is the only mid-burst refresh.
func TestGutterRefreshHoldsBoundedDuringContinuousTyping(t *testing.T) {
	app := newFullTestApp(t)
	base := time.Now()
	for i := 0; i < 40; i++ {
		app.markEditorGitDirty(base.Add(time.Duration(i) * 100 * time.Millisecond))
	}
	if app.editorGitDue(base.Add(1 * time.Second)) {
		t.Fatal("hold cap fired earlier than the configured limit")
	}
	if !app.editorGitDue(base.Add(3 * time.Second)) {
		t.Fatal("continuous typing held the gutter stale past the hold limit")
	}
}

// Non-typing facts (save, rename, source control, disk change, tab switch) must
// still refresh immediately instead of waiting for a typing pause.
func TestGutterRefreshForNonTypingFactsIsImmediate(t *testing.T) {
	app := newFullTestApp(t)
	app.markEditorGitStaleNow()
	if !app.editorGitDue(time.Now()) {
		t.Fatal("forced gutter refresh waited for an idle typing period")
	}
}

// A refresh clears the pending burst so the next keystroke starts a new idle
// window instead of inheriting the previous one.
func TestGutterRefreshResetsTheTypingBurst(t *testing.T) {
	app, _ := newInputContractEditorApp(t, "burst.go")
	app.markEditorGitDirty(time.Now())
	if cmd := app.refreshEditorGitCmd(); cmd == nil {
		t.Fatal("refresh command missing")
	}
	app.markEditorGitDirty(time.Now().Add(10 * time.Millisecond))
	if app.editorGitDue(time.Now().Add(100 * time.Millisecond)) {
		t.Fatal("stale burst timestamps survived a dispatched refresh")
	}
}

func TestBackgroundTabSyncDoesNotRestartEditorServices(t *testing.T) {
	app, paths := newInputContractEditorApp(t, "stable.go")
	ed := app.EditorState.Editor
	_ = app.View()
	if cmd := app.refreshEditorGitCmd(); cmd == nil {
		t.Fatal("initial annotation request missing")
	}
	generation := app.editorGitGen.Load()
	for range 64 {
		app.syncAgentWindowTabs()
		app.Update(ttypes.TickMsg(time.Now()))
		if app.editorGitGen.Load() != generation || app.editorGitDue(time.Now()) {
			t.Fatal("unchanged editor selection restarted Git work during background synchronization")
		}
	}
	if app.EditorState.Editor != ed || app.EditorState.ActiveView != paths[0] {
		t.Fatal("background synchronization changed the selected editor lifetime")
	}
	ed.SetGitAnnotation(ed.Revision(), ed.Value(), nil, nil, false)
	ed.SetCursorPosition(0, 0)
	ed.ScrollToCursor()
	before := ed.Value()
	app.setFocusEditor()
	app.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	for range 32 {
		app.syncAgentWindowTabs()
		if app.editorGitDue(time.Now()) {
			t.Fatal("background synchronization bypassed the typing debounce")
		}
	}
	app.forceFrame()
	painted := rendering.StripAnsi(app.View().RenderContent())
	if ed.Value() != "x"+before || !strings.Contains(painted, "xpackage main") {
		t.Fatalf("typed content was not retained and painted: text=%q, frame=%s", ed.Value(), painted)
	}
	if !app.editorGitDue(time.Now().Add(editorGitIdleDelay)) {
		t.Fatal("real edits no longer schedule a bounded annotation refresh")
	}
}

// A transient Git failure must not blank the projection the user is reading,
// while a deterministic failure still clears it.
func TestTransientGitFailureKeepsTheProjection(t *testing.T) {
	app, paths := newInputContractEditorApp(t, "transient.go")
	ed := app.EditorState.Editor
	ed.SetGitAnnotation(ed.Revision(), "", []editor.GitHunk{{OldStart: 0, NewStart: 0, NewLines: []string{"package main\n"}}}, nil, false)
	if got := gitGutterChar(t, ed, 0); got != '▌' {
		t.Fatalf("annotation did not paint: %q", got)
	}

	app.editorGitGen.Store(3)
	app.applyEditorGitAnnotation(editorGitAnnotationMsg{
		generation: 3, workspace: app.workspaceSwitchGen.Load(), path: paths[0],
		revision: ed.Revision(), err: context.Canceled,
	})
	if got := gitGutterChar(t, ed, 0); got != '▌' {
		t.Fatalf("canceled refresh blanked the gutter: %q", got)
	}

	app.applyEditorGitAnnotation(editorGitAnnotationMsg{
		generation: 3, workspace: app.workspaceSwitchGen.Load(), path: paths[0],
		revision: ed.Revision(), err: errors.New("not a git repository"),
	})
	if got := gitGutterChar(t, ed, 0); got != ' ' {
		t.Fatalf("deterministic failure kept a stale projection: %q", got)
	}
}

// The first keystroke must show its marker without dispatching Git work: the
// editor answers the edit locally and the debounced refresh follows later.
func TestFirstKeystrokePaintsTheGutterWithoutDispatchingGit(t *testing.T) {
	app, _ := newInputContractEditorApp(t, "instant.go")
	ed := app.EditorState.Editor
	ed.SetGitAnnotation(ed.Revision(), ed.Value(), nil, nil, false)
	if got := gitGutterChar(t, ed, 0); got != ' ' {
		t.Fatalf("clean file already marked line 0: %q", got)
	}
	ed.SetCursorPosition(0, 0)
	app.editorGitStale.Store(false)

	app.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})

	if got := gitGutterChar(t, ed, 0); got != '▌' {
		t.Fatalf("first keystroke did not mark its line: %q", got)
	}
	if !app.editorGitStale.Load() {
		t.Fatal("typing did not schedule the authoritative refresh")
	}
	if app.editorGitDue(time.Now()) {
		t.Fatal("keystroke dispatched a Git refresh instead of answering locally")
	}
}

// The authoritative refresh takes over without blanking: the rail keeps its
// marker across the handover and the local claim is retired.
func TestAuthoritativeRefreshTakesOverTheGutterLocallyClaimedLine(t *testing.T) {
	app, paths := newInputContractEditorApp(t, "handover.go")
	ed := app.EditorState.Editor
	ed.SetGitAnnotation(ed.Revision(), ed.Value(), nil, nil, false)
	// First paint establishes the viewport before the cursor moves.
	if got := gitGutterChar(t, ed, 0); got != ' ' {
		t.Fatalf("clean file already marked line 0: %q", got)
	}
	ed.SetCursorPosition(0, 0)
	app.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	if got := gitGutterChar(t, ed, 0); got != '▌' {
		t.Fatalf("local claim missing before the refresh: %q", got)
	}

	app.editorGitGen.Store(5)
	app.applyEditorGitAnnotation(editorGitAnnotationMsg{
		generation: 5, workspace: app.workspaceSwitchGen.Load(), path: paths[0],
		revision: ed.Revision(),
		annotation: workspace.GitFileAnnotation{HeadContent: "package main\n", Hunks: []workspace.GitHunk{{
			OldStart: 0, NewStart: 0, OldLines: []string{"package main\n"}, NewLines: []string{"xpackage main\n"},
		}}},
	})
	if got := gitGutterChar(t, ed, 0); got != '▌' {
		t.Fatalf("handover blanked the marker: %q", got)
	}
	if len(ed.GitHunks()) != 1 {
		t.Fatalf("authoritative hunks missing: %#v", ed.GitHunks())
	}
}

// Typing through the real key path must neither clear the painted marker nor
// dispatch a recompute: the burst stays on the last known projection until the
// tick decides the user has paused.
func TestTypingBurstKeepsThePaintedMarkerAndDefersTheRefresh(t *testing.T) {
	app, _ := newInputContractEditorApp(t, "burst.go")
	ed := app.EditorState.Editor
	ed.SetGitAnnotation(ed.Revision(), "package legacy\n", []editor.GitHunk{
		{OldStart: 0, NewStart: 0, OldLines: []string{"package legacy\n"}, NewLines: []string{"package main\n"}},
	}, nil, false)
	if got := gitGutterChar(t, ed, 0); got != '▌' {
		t.Fatalf("annotation did not paint: %q", got)
	}

	app.editorGitStale.Store(false)
	for i := 0; i < 6; i++ {
		app.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
		if got := gitGutterChar(t, ed, 0); got != '▌' {
			t.Fatalf("keystroke %d cleared the gutter marker: %q", i+1, got)
		}
	}
	if !app.editorGitStale.Load() {
		t.Fatal("typing did not request a gutter refresh")
	}
	if app.editorGitDue(time.Now()) {
		t.Fatal("keystroke dispatched an immediate refresh instead of coalescing")
	}
	if got := ed.Value(); !strings.HasPrefix(got, "xxxxxx") {
		t.Fatalf("keystrokes did not reach the buffer: %q", got)
	}
}
