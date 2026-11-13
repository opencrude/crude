package ui

import (
	tea "charm.land/bubbletea/v2"
	"strings"
)

// EditorAction describes an operation owned by the native editor. The registry,
// menu and input router share this table; it does not implement a second editor.
type EditorAction struct {
	ActionDef
	MutatesText bool
}

func editorAction(id ActionID, label string, mutate bool, keys ...string) EditorAction {
	a := EditorAction{ActionDef: ActionDef{ID: id, Label: label, Category: "Edit", FocusContext: "editor"}, MutatesText: mutate}
	for _, key := range keys {
		a.Bindings = append(a.Bindings, KeyBinding{Key: key})
	}
	return a
}

var nativeEditorActions = []EditorAction{
	editorAction("edit.duplicateLine", "Duplicate line or selection", true, "ctrl+d", "ctrl+shift+d"),
	editorAction("edit.moveLineUp", "Move lines up", true, "alt+up", "alt+shift+up"),
	editorAction("edit.moveLineDown", "Move lines down", true, "alt+down", "alt+shift+down"),
	editorAction("edit.deleteLine", "Delete lines", true, "ctrl+shift+k"),
	editorAction("edit.joinLines", "Join lines", true, "ctrl+shift+j"),
	editorAction("edit.lineBelow", "Start line below", true, EditorLineBelowChord),
	editorAction("edit.lineAbove", "Start line above", true, EditorLineAboveChord),
	editorAction("edit.indent", "Indent lines", true),
	editorAction("edit.outdent", "Outdent lines", true),
	editorAction("edit.toggleComment", "Toggle line comment", true, "ctrl+/", "ctrl+_"),
	editorAction("edit.toggleCase", "Toggle case", true, "alt+u"),
	editorAction("edit.upperCase", "Convert to uppercase", true),
	editorAction("edit.lowerCase", "Convert to lowercase", true),
	editorAction("edit.sortLines", "Sort lines", true),
	editorAction("edit.reverseLines", "Reverse lines", true),
	editorAction("edit.transpose", "Transpose characters", true),
	editorAction("move.matchingBracket", "Go to matching bracket", false, "ctrl+alt+b", "ctrl+shift+\\"),
	editorAction("select.matchingBracket", "Select to matching bracket", false, "ctrl+alt+shift+b"),
	editorAction("select.line", "Select line", false, "ctrl+l"),
	editorAction("select.expand", "Expand selection", false, "alt+w"),
	editorAction("select.shrink", "Shrink selection", false, "alt+q"),
	editorAction("move.paragraphPrevious", "Previous paragraph", false, "ctrl+up"),
	editorAction("move.paragraphNext", "Next paragraph", false, "ctrl+down"),
	editorAction("select.paragraphPrevious", "Select to previous paragraph", false, "ctrl+shift+up"),
	editorAction("select.paragraphNext", "Select to next paragraph", false, "ctrl+shift+down"),
	editorAction("move.subwordPrevious", "Previous name part", false, "alt+b"),
	editorAction("move.subwordNext", "Next name part", false, "alt+f"),
	editorAction("select.subwordPrevious", "Select previous name part", false, "alt+shift+left"),
	// Shifted arrow sequences remain distinct in legacy terminals; shifted
	// Alt-letter text can be ambiguous. Shift+Alt+F still formats the file.
	editorAction("select.subwordNext", "Select next name part", false, "alt+shift+right"),
	editorAction("select.nextOccurrence", "Select next occurrence", false, "alt+n"),
	// VS Code: Ctrl+Shift+L and Ctrl+F2 (Change All Occurrences).
	editorAction("select.allOccurrences", "Select all occurrences", false, "alt+f3", "ctrl+shift+l", "ctrl+f2"),
	editorAction("select.caretAbove", "Add caret above", false, "ctrl+alt+shift+up"),
	editorAction("select.caretBelow", "Add caret below", false, "ctrl+alt+shift+down"),
	// VS Code Shift+Alt+I; JetBrains Shift+Alt+G (Add Carets to Ends of Selected Lines).
	editorAction("select.caretsAtLineEnds", "Add carets to line ends", false, "alt+shift+i", "alt+shift+g"),
	editorAction("select.singleCaret", "Keep one caret", false),
	editorAction("select.cursorUndo", "Undo caret move", false, "ctrl+u"),
	editorAction("select.cursorRedo", "Redo caret move", false),
	// VS Code (Windows and Linux): scroll a page without moving the caret.
	editorAction("view.scrollPageUp", "Scroll page up", false, "alt+pgup"),
	editorAction("view.scrollPageDown", "Scroll page down", false, "alt+pgdown"),
	editorAction("edit.formatSelection", "Format selection", true, "alt+f8"),
	editorAction("view.indentation", "Indentation…", false),
	editorAction("view.indentationDetect", "Detect indentation from text", false),
	editorAction("view.indentationInherit", "Use project indentation", false),
	editorAction("edit.indentToSpaces", "Convert indentation to spaces", true),
	editorAction("edit.indentToTabs", "Convert indentation to tabs", true),
	editorAction("view.fold", "Fold block", false, "alt+-"),
	editorAction("view.unfold", "Unfold block", false, "alt+="),
	editorAction("view.foldAll", "Fold all blocks", false),
	editorAction("view.unfoldAll", "Unfold all blocks", false),
	editorAction("view.foldLevel1", "Fold level 1", false),
	editorAction("view.foldLevel2", "Fold level 2", false),
	editorAction("view.foldLevel3", "Fold level 3", false),
	editorAction("view.foldLevel4", "Fold level 4", false),
	editorAction("view.foldLevel5", "Fold level 5", false),
	editorAction("view.foldLevel6", "Fold level 6", false),
	editorAction("view.foldLevel7", "Fold level 7", false),
	editorAction("view.foldLevel8", "Fold level 8", false),
	editorAction("view.foldLevel9", "Fold level 9", false),
}

// EditorActions returns detached metadata. Callers cannot rewrite live bindings.
func EditorActions() []EditorAction {
	out := append([]EditorAction(nil), nativeEditorActions...)
	for i := range out {
		out[i].Bindings = append([]KeyBinding(nil), out[i].Bindings...)
	}
	return out
}
func FindEditorAction(id ActionID) (EditorAction, bool) {
	for _, a := range nativeEditorActions {
		if a.ID == id {
			return a, true
		}
	}
	return EditorAction{}, false
}

var nativeEditorKeys = func() map[string]int {
	keys := make(map[string]int)
	for i, a := range nativeEditorActions {
		for _, key := range a.Bindings {
			if !strings.HasPrefix(key.Key, "chord:") {
				keys[key.Key] = i
			}
		}
	}
	return keys
}()

func MatchEditorAction(key tea.KeyPressMsg) (EditorAction, bool) {
	for _, id := range []ActionID{"edit.lineBelow", "edit.lineAbove"} {
		a, _ := FindEditorAction(id)
		if a.Bindings[0].MatchesKey(key) {
			return a, true
		}
	}
	if i, ok := nativeEditorKeys[strings.ToLower(key.String())]; ok {
		return nativeEditorActions[i], true
	}
	return EditorAction{}, false
}
