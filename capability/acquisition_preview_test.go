package capability

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"crude/skills"
	captypes "crude/types/capability"
)

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
