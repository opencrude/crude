package viewmodel

import (
	"path/filepath"
	"strings"
	"testing"

	"crude/workspace"
)

// shortWorkspacePath keeps the header identity short for real workspaces and
// carries no identity for unresolved roots.
func TestShortWorkspacePathCollapsesHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	outside := filepath.Join(filepath.Dir(home), "elsewhere")
	cases := []struct {
		name string
		root string
		want string
	}{
		{"home itself", home, "~"},
		{"workspace under home", filepath.Join(home, "ocd", "crude"), "~" + string(filepath.Separator) + filepath.Join("ocd", "crude")},
		{"sibling of home", outside, outside},
		{"outside home", filepath.FromSlash("/opt/projects/alpha"), filepath.FromSlash("/opt/projects/alpha")},
		{"unresolved root", ".", ""},
		{"blank root", "   ", ""},
		{"filesystem root", string(filepath.Separator), ""},
	}
	for _, tc := range cases {
		if got := shortWorkspacePath(tc.root); got != tc.want {
			t.Fatalf("%s: shortWorkspacePath(%q) = %q, want %q", tc.name, tc.root, got, tc.want)
		}
	}
}

// A workspace folder named like the app must still be identified: the old
// basename segment was suppressed for a folder called "crude".
func TestHeaderShowsWorkspacePathWhenFolderMatchesAppName(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	app := newFullTestApp(t)
	app.width, app.height = 120, 28
	app.RootPath = filepath.Join(home, "ocd", "crude")
	app.gitPanel.SetSnapshot(workspace.GitSnapshot{Branch: "feature/x"})
	app.paintShellFrame()

	wantPath := "~" + string(filepath.Separator) + filepath.Join("ocd", "crude")
	segments := app.statusHeaderLeftSegments()
	if len(segments) != 3 {
		t.Fatalf("header identity segments = %+v, want app, workspace path and branch", segments)
	}
	if segments[0].Text != "Crude" || segments[1].Text != wantPath || segments[2].Text == "" {
		t.Fatalf("header identity segments = %+v, want [Crude %s branch]", segments, wantPath)
	}
	if segments[1].Priority >= segments[2].Priority {
		t.Fatalf("workspace path must outrank the branch when width is tight: %+v", segments)
	}
	header := rowText(app, 0)
	for _, want := range []string{"Crude", wantPath, "feature/x"} {
		if !strings.Contains(header, want) {
			t.Fatalf("header missing active workspace identity %q: %q", want, header)
		}
	}
}

// Without a resolved workspace root the header stays on the app name and Git
// branch; it never invents a placeholder path.
func TestHeaderOmitsUnresolvedWorkspacePath(t *testing.T) {
	app := newFullTestApp(t)
	app.width, app.height = 120, 28
	app.gitPanel.SetSnapshot(workspace.GitSnapshot{Branch: "main"})
	app.paintShellFrame()

	segments := app.statusHeaderLeftSegments()
	if len(segments) != 2 || segments[0].Text != "Crude" {
		t.Fatalf("header identity segments = %+v, want [Crude branch]", segments)
	}
	header := rowText(app, 0)
	if strings.Contains(header, "~") || strings.Contains(header, "unnamed") {
		t.Fatalf("header invented a workspace identity: %q", header)
	}
}
