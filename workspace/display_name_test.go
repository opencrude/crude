package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkspaceDisplayName_NeverShowsRawHash(t *testing.T) {
	const hash = "84db4726b4a4528b"

	t.Run("empty_name_uses_path_basename", func(t *testing.T) {
		got := WorkspaceDisplayName(WorkspaceInfo{
			ID:          hash,
			ProjectName: "",
			ProjectDir:  "/home/lenovo/ocd/my-project",
		})
		if got != "my-project" {
			t.Fatalf("got %q want my-project", got)
		}
		if strings.Contains(got, hash) {
			t.Fatalf("display must not contain hash %q", hash)
		}
	})

	t.Run("name_equal_to_id_uses_path", func(t *testing.T) {
		got := WorkspaceDisplayName(WorkspaceInfo{
			ID:          hash,
			ProjectName: hash,
			ProjectDir:  "/tmp/crude-probe",
		})
		if got != "crude-probe" {
			t.Fatalf("got %q want crude-probe", got)
		}
	})

	t.Run("stored_name_preferred", func(t *testing.T) {
		got := WorkspaceDisplayName(WorkspaceInfo{
			ID:          hash,
			ProjectName: "Friendly",
			ProjectDir:  "/home/lenovo/ocd/other",
		})
		if got != "Friendly" {
			t.Fatalf("got %q want Friendly", got)
		}
	})

	t.Run("no_name_no_path_placeholder", func(t *testing.T) {
		got := WorkspaceDisplayName(WorkspaceInfo{ID: hash})
		if got == hash || strings.Contains(got, hash) {
			t.Fatalf("got %q — must not show raw id", got)
		}
		if got != "unnamed workspace" {
			t.Fatalf("got %q want unnamed workspace", got)
		}
	})
}

func TestListWorkspaceInfos_EmptyNameUsesPath(t *testing.T) {
	setTestHome(t)

	project := filepath.Join(t.TempDir(), "readable-name")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	wdb, err := OpenWorkspaceDB(project)
	if err != nil {
		t.Fatalf("OpenWorkspaceDB: %v", err)
	}
	t.Cleanup(func() { _ = wdb.Close() })

	infos, err := ListWorkspaceInfos()
	if err != nil {
		t.Fatal(err)
	}
	wantID := WorkspaceID(project)
	var found *WorkspaceInfo
	for i := range infos {
		if infos[i].ID == wantID {
			found = &infos[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("workspace %s not listed: %#v", wantID, infos)
	}
	if found.ProjectName == found.ID || strings.Contains(found.ProjectName, found.ID) {
		t.Fatalf("ProjectName=%q must not be/contain raw id", found.ProjectName)
	}
	if found.ProjectName != "readable-name" {
		t.Fatalf("ProjectName=%q want readable-name (path-derived)", found.ProjectName)
	}
}

func TestListWorkspaceInfosIncludesCurrentAndHidesMissingProjects(t *testing.T) {
	setTestHome(t)
	current := filepath.Join(t.TempDir(), "crude")
	if err := os.MkdirAll(current, 0o755); err != nil {
		t.Fatal(err)
	}
	currentDB, err := OpenWorkspaceDB(current)
	if err != nil {
		t.Fatal(err)
	}
	if err := currentDB.RegisterSession("root-session", current, "crude", "model", "provider"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = currentDB.Close() })

	stale := filepath.Join(t.TempDir(), "deleted-test-workspace")
	if err := os.MkdirAll(stale, 0o755); err != nil {
		t.Fatal(err)
	}
	staleDB, err := OpenWorkspaceDB(stale)
	if err != nil {
		t.Fatal(err)
	}
	if err := staleDB.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(stale); err != nil {
		t.Fatal(err)
	}

	infos, err := ListWorkspaceInfos()
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 1 {
		t.Fatalf("workspace infos = %+v, want only existing current project", infos)
	}
	if infos[0].ProjectDir != canonicalizePath(current) || infos[0].ProjectName != "crude" || infos[0].SessionCount != 1 {
		t.Fatalf("current workspace identity/metrics = %+v", infos[0])
	}
}
