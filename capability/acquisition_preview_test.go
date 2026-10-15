package capability

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"crude/skills"
	captypes "crude/types/capability"
)

func TestReviewedRemoteSkillSubtreeIsNotAHostDirectory(t *testing.T) {
	service, settings, _ := capabilityFixture(t)
	archive := skillArchive(t,
		archiveFixtureEntry{name: "repository/skills/ghidra-reverse/SKILL.md", body: "---\nname: ghidra-reverse\ndescription: Inspect binaries with Ghidra.\n---\nRetained instructions.\n"},
		archiveFixtureEntry{name: "repository/skills/ghidra-reverse/references/guide.md", body: "Bundled reference."})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(archive)
	}))
	defer server.Close()
	id := "team:ghidra-reverse"
	service.candidates = map[string]Candidate{id: {
		Entry:      captypes.Entry{ID: id, Name: "ghidra-reverse", Kind: captypes.KindSkill, Source: "team", Path: "skills/ghidra-reverse", Version: "exact-commit"},
		ArchiveURL: server.URL, Subpath: "skills/ghidra-reverse", SkillID: "ghidra-reverse",
	}}
	preview, err := service.PreviewAcquisition(t.Context(), id, captypes.ScopeApplication, "install")
	if err != nil || preview.Token == "" || preview.Candidate.Version != "exact-commit" {
		t.Fatalf("remote review tried to inspect a repository path locally: %+v %v", preview, err)
	}
	if len(service.Snapshot().Entries) != 0 {
		t.Fatal("review registered or activated the skill")
	}
	acquired, err := service.AcquireReviewed(t.Context(), preview)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := skills.Load(acquired.Skills[id].Path)
	if err != nil || loaded.Name != "ghidra-reverse" || loaded.Instructions != "Retained instructions.\n" || len(loaded.Files) != 2 {
		t.Fatalf("reviewed remote acquisition lost pinned skill content: %+v %v", loaded, err)
	}
	settings.Sources["team"] = captypes.SourceConfig{ID: "team", Name: "Changed", Kind: "local", Location: t.TempDir(), Trusted: true}
	if err := service.Configure(t.Context(), settings); err != nil {
		t.Fatal(err)
	}
	if _, err := service.AcquireReviewed(t.Context(), preview); err == nil {
		t.Fatal("changed source accepted the previous review")
	}
}

func TestReviewedAcquisitionIsPassiveAndRejectsChangedPreimage(test *testing.T) {
	root := test.TempDir()
	directory := filepath.Join(root, "source", "guide")
	if err := os.MkdirAll(filepath.Dir(directory), 0700); err != nil {
		test.Fatal(err)
	}
	if _, err := skills.Create(directory, "guide", "Review the actual outcome before claiming success."); err != nil {
		test.Fatal(err)
	}
	service, err := New(root, filepath.Join(root, "cache"), nil)
	if err != nil {
		test.Fatal(err)
	}
	defer service.Close()
	settings := captypes.Normalize(captypes.Settings{Sources: map[string]captypes.SourceConfig{"local": {ID: "local", Name: "Local", Kind: "local", Location: filepath.Dir(directory), Trusted: true}}})
	if err := service.Configure(test.Context(), settings); err != nil {
		test.Fatal(err)
	}
	entries, err := service.Search(test.Context(), "guide", captypes.KindSkill, 10, true)
	if err != nil || len(entries) != 1 {
		test.Fatalf("discovery: %+v %v", entries, err)
	}
	preview, err := service.PreviewAcquisition(test.Context(), entries[0].ID, captypes.ScopeWorkspace, "install")
	if err != nil || preview.Token == "" || len(preview.RequestedAccess) == 0 {
		test.Fatalf("preview: %+v %v", preview, err)
	}
	if _, err := os.Stat(service.cache); !os.IsNotExist(err) || len(service.Snapshot().Entries) != 0 {
		test.Fatal("preview acquired bytes or published a binding")
	}
	settings.Sources["local"] = captypes.SourceConfig{ID: "local", Name: "Changed", Kind: "local", Location: filepath.Dir(directory), Trusted: true}
	if err := service.Configure(test.Context(), settings); err != nil {
		test.Fatal(err)
	}
	if _, err := service.AcquireReviewed(test.Context(), preview); err == nil {
		test.Fatal("changed source was accepted by an older preview")
	}
	preview, err = service.PreviewAcquisition(test.Context(), entries[0].ID, captypes.ScopeWorkspace, "install")
	if err != nil {
		test.Fatal(err)
	}
	acquired, err := service.AcquireReviewed(test.Context(), preview)
	if err != nil || !acquired.Skills[preview.ID].Managed {
		test.Fatalf("reviewed acquisition: %+v %v", acquired, err)
	}
	if len(service.Snapshot().Entries) != 0 {
		test.Fatal("acquiring bytes implicitly configured a binding")
	}
	if err := service.Configure(test.Context(), acquired); err != nil {
		test.Fatal(err)
	}
	if _, err := service.AcquireReviewed(test.Context(), preview); err == nil {
		test.Fatal("changed installed binding accepted an older preview")
	}
	cancelled, cancel := context.WithCancel(test.Context())
	cancel()
	if _, err := service.PreviewAcquisition(cancelled, preview.ID, preview.Scope, "update"); !errors.Is(err, context.Canceled) {
		test.Fatalf("cancelled preview: %v", err)
	}
}
