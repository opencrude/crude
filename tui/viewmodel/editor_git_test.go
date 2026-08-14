package viewmodel

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/editor"
	"crude/tui/surface"
	"crude/tui/ui"
	"crude/workspace"
)

func gitGutterChar(t *testing.T, ed *editor.Editor, line int) rune {
	t.Helper()
	surf := surface.NewSurface("ed", 50, 6, cell.TransparentBlank())
	ed.Render(surf, ui.PaintContext{Theme: ed.Theme(), Focused: true})
	return surf.Buffer().Get(ed.GutterWidth()-2, line).Char
}

func TestEditorGitAnnotationIgnoresStaleGeneration(t *testing.T) {
	app, paths := newInputContractEditorAppAug13(t, "main.go")
	path := paths[0]
	ws := app.workspaceSwitchGen.Load()
	app.editorGitGen.Store(2)

	app.applyEditorGitAnnotation(editorGitAnnotationMsg{
		generation: 1,
		workspace:  ws,
		path:       path,
		annotation: workspace.GitFileAnnotation{
			Edits: []workspace.GitLineKind{workspace.GitLineAdded},
		},
	})
	if got := gitGutterChar(t, app.EditorState.Editor, 0); got != ' ' {
		t.Fatalf("stale generation painted git gutter %q", got)
	}

	app.applyEditorGitAnnotation(editorGitAnnotationMsg{
		generation: 2,
		workspace:  ws,
		path:       path,
		annotation: workspace.GitFileAnnotation{
			Edits: []workspace.GitLineKind{workspace.GitLineAdded},
			Blame: []workspace.GitBlameLine{{
				Line: 1, Hash: "abc123def456", ShortHash: "abc123d", Author: "Ada",
			}},
		},
	})
	if got := gitGutterChar(t, app.EditorState.Editor, 0); got != '+' {
		t.Fatalf("current generation git gutter=%q want +", got)
	}
	if got := app.EditorState.Editor.CursorBlameSummary(); !strings.Contains(got, "Ada") {
		t.Fatalf("blame summary=%q want Ada", got)
	}
}

func TestEditorGitAnnotationIgnoresWorkspaceSwitch(t *testing.T) {
	app, paths := newInputContractEditorAppAug13(t, "switched.go")
	app.editorGitGen.Store(1)
	app.applyEditorGitAnnotation(editorGitAnnotationMsg{
		generation: 1,
		workspace:  app.workspaceSwitchGen.Load() + 1,
		path:       paths[0],
		annotation: workspace.GitFileAnnotation{
			Edits: []workspace.GitLineKind{workspace.GitLineAdded},
		},
	})
	if got := gitGutterChar(t, app.EditorState.Editor, 0); got != ' ' {
		t.Fatalf("cross-workspace annotation painted git gutter %q", got)
	}
}

func TestEditorContextMenuCopyBlameCommit(t *testing.T) {
	app, paths := newInputContractEditorAppAug13(t, "blame.go")
	menu := app.buildEditorContextMenu(tea.Mouse{})
	item, ok := contextMenuItemAug13(menu, "Copy blame commit")
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
		annotation: workspace.GitFileAnnotation{
			Blame: []workspace.GitBlameLine{{
				Line: 1, Hash: "deadbeefcafebabe", ShortHash: "deadbee", Author: "Ada",
			}},
		},
	})
	menu = app.buildEditorContextMenu(tea.Mouse{})
	item, ok = contextMenuItemAug13(menu, "Copy blame commit")
	if !ok || item.Disabled {
		t.Fatal("Copy blame commit should enable once the cursor line has blame")
	}
}
