package ui

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/theme"
)

func withKeyboardProfile(t *testing.T, profile KeyboardProfile) {
	t.Helper()
	previous := CurrentKeyboardProfile()
	SetKeyboardProfile(profile)
	t.Cleanup(func() { SetKeyboardProfile(previous) })
}

// Every caption names a chord the matcher accepts and the terminal delivers:
// tmux and legacy terminals send Ctrl+Enter and Shift+Enter as plain Enter,
// and Windows Terminal consumes Alt+Enter for fullscreen.
func TestChordLabelsNameDeliverableKeys(t *testing.T) {
	cases := []struct {
		name    string
		profile KeyboardProfile
		want    map[string]string
	}{
		{"tmux/legacy unix", KeyboardProfile{}, map[string]string{
			CommitChord: "Alt+Enter", NewlineChord: "Alt+Enter", PreviousMatchChord: "Shift+F3", ReplaceAllChord: "Alt+Enter",
		}},
		{"windows console", DetectKeyboardProfile("windows", func(string) string { return "" }), map[string]string{
			CommitChord: "Ctrl+Enter", NewlineChord: "Shift+Enter", PreviousMatchChord: "Shift+Enter", ReplaceAllChord: "Ctrl+Alt+Enter",
		}},
		{"kitty protocol", KeyboardProfile{ModifiedEnter: true}, map[string]string{
			CommitChord: "Ctrl+Enter", NewlineChord: "Shift+Enter", PreviousMatchChord: "Shift+Enter", ReplaceAllChord: "Ctrl+Alt+Enter",
		}},
		{"windows terminal hosting a unix shell", DetectKeyboardProfile("linux", func(k string) string {
			if k == "WT_SESSION" {
				return "1"
			}
			return ""
		}), map[string]string{
			NewlineChord: "Ctrl+J",
		}},
	}
	keys := map[string]tea.KeyPressMsg{
		"Alt+Enter":      {Code: tea.KeyEnter, Mod: tea.ModAlt},
		"Ctrl+Enter":     {Code: tea.KeyEnter, Mod: tea.ModCtrl},
		"Shift+Enter":    {Code: tea.KeyEnter, Mod: tea.ModShift},
		"Ctrl+J":         {Code: 'j', Mod: tea.ModCtrl},
		"Shift+F3":       {Code: tea.KeyF3, Mod: tea.ModShift},
		"Ctrl+Alt+Enter": {Code: tea.KeyEnter, Mod: tea.ModCtrl | tea.ModAlt},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withKeyboardProfile(t, tc.profile)
			for token, want := range tc.want {
				got := FormatShortcut(token)
				if got != want {
					t.Errorf("%s caption = %q, want %q", token, got, want)
				}
				if !MatchesShortcut(keys[got], token) {
					t.Errorf("%s caption %q is not accepted by its matcher", token, got)
				}
			}
		})
	}
}

func TestNewlineAndCommitChordsStayDistinctFromEnter(t *testing.T) {
	plain := tea.KeyPressMsg{Code: tea.KeyEnter}
	if IsNewlineChord(plain) || IsCommitChord(plain) {
		t.Fatal("plain Enter must stay the send/newline key of its field")
	}
	if !IsNewlineChord(tea.KeyPressMsg{Code: 'j', Mod: tea.ModCtrl}) {
		t.Fatal("Ctrl+J (LF) must break the line in every terminal")
	}
	if IsNewlineChord(tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModCtrl}) {
		t.Fatal("Ctrl+Enter commits; it must not be a newline chord")
	}
	if IsCommitChord(tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModCtrl | tea.ModAlt}) {
		t.Fatal("Ctrl+Alt+Enter stays a distinct chord")
	}
}

func TestMatchesShortcutIsCaseInsensitive(t *testing.T) {
	if !MatchesShortcut(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl}, "Ctrl+S") {
		t.Fatal(`"Ctrl+S" shortcut must match ctrl+s`)
	}
}

// A keyboard profile change (the terminal answers the keyboard query after the
// bar was built) re-resolves button captions; key routing never changes.
func TestActionBarShortcutCaptionFollowsKeyboardProfile(t *testing.T) {
	withKeyboardProfile(t, KeyboardProfile{})
	bar := NewActionBar("bar", NewTokenSet(theme.DefaultDark()),
		ActionBarButton{Label: "Commit", Action: "commit", Shortcut: CommitChord, Enabled: true},
		ActionBarButton{Label: "Save", Action: "save", Shortcut: "Ctrl+S", Enabled: true},
	)
	host := NewHost(theme.DefaultDark(), bar)
	host.Resize(60, 1)
	host.Layout()
	if got := bar.ButtonLabelsForTest(); got[0] != "Commit (Alt+Enter)" || got[1] != "Save (Ctrl+S)" {
		t.Fatalf("labels = %q", got)
	}
	SetKeyboardProfile(KeyboardProfile{ModifiedEnter: true})
	host.Resize(61, 1)
	host.Layout()
	if got := bar.ButtonLabelsForTest(); got[0] != "Commit (Ctrl+Enter)" {
		t.Fatalf("label after profile change = %q", got[0])
	}
	host.Focus(bar.ID())
	for _, key := range []tea.KeyPressMsg{{Code: tea.KeyEnter, Mod: tea.ModCtrl}, {Code: tea.KeyEnter, Mod: tea.ModAlt}} {
		if res := host.Handle(key); res.Action == nil || res.Action.ID != "commit" {
			t.Fatalf("%s did not commit: %+v", key.String(), res.Action)
		}
	}
	if res := host.Handle(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl}); res.Action == nil || res.Action.ID != "save" {
		t.Fatalf(`"Ctrl+S" button did not fire on ctrl+s: %+v`, res.Action)
	}
}

// Captions for modified Enter come only from the chord tokens: a hard-coded
// "Ctrl+Enter"/"Alt+Enter"/"Shift+Enter" string names a key that some terminal
// does not deliver. Key-matching code compares Bubble Tea key strings in
// switch cases, which the scan allows.
func TestNoHardCodedModifiedEnterCaptions(t *testing.T) {
	caption := regexp.MustCompile(`(?i)(ctrl|alt|shift)\+(alt\+)?enter`)
	root := filepath.Join("..")
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") ||
			filepath.Base(path) == "chords.go" {
			return nil
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		matchKeys := map[*ast.BasicLit]bool{}
		ast.Inspect(file, func(n ast.Node) bool {
			if clause, ok := n.(*ast.CaseClause); ok {
				for _, expr := range clause.List {
					if lit, ok := expr.(*ast.BasicLit); ok {
						matchKeys[lit] = true
					}
				}
			}
			return true
		})
		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING || matchKeys[lit] {
				return true
			}
			value, _ := strconv.Unquote(lit.Value)
			if caption.MatchString(value) {
				t.Errorf("%s: hard-coded modified-Enter caption %q; use a chord token (ui.CommitChord, ui.NewlineChord, …) with FormatShortcut", fset.Position(lit.Pos()), value)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
