package capability

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"crude/skills"
	captypes "crude/types/capability"
)

func TestSkillInstallSurvivesSetupNeedsAndRefreshesAtActivation(t *testing.T) {
	service, _, source := capabilityFixture(t)
	definition := "---\nname: review\ndescription: Review evidence.\nversion: 1\nallowed-tools: Read, Grep\n" +
		"compatibility: Requires a filesystem client with internet access.\nmetadata:\n  crude.requires: crude-install-fixture-helper\n---\nReview instructions.\n"
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte(definition), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", t.TempDir())
	entries, err := service.Discover(t.Context(), "review", captypes.KindSkill, 10)
	if err != nil || len(entries) != 1 {
		t.Fatalf("setup hid a discoverable skill: %+v %v", entries, err)
	}
	actor := captypes.RuntimeActor{ID: "root", Effects: []captypes.Effect{captypes.EffectRead}}
	// Activation includes durable acquisition; a later setup refusal must not
	// lose that install or re-download/re-approve it on retry.
	_, err = service.Activate(t.Context(), actor, entries[0].ID, false)
	var approval *captypes.ApprovalRequired
	if !errors.As(err, &approval) {
		t.Fatalf("missing install approval: %v", err)
	}
	_, err = service.Activate(approval.CapabilityApprovalContext(t.Context()), actor, entries[0].ID, false)
	if !errors.Is(err, skills.ErrUnavailable) || !strings.Contains(err.Error(), "installed but needs setup") {
		t.Fatalf("setup was not distinguished from install: %v", err)
	}
	installed := service.store.(*testRegistrationStore).load(t)
	if installed.Skills[entries[0].ID].Digest == "" {
		t.Fatal("setup refusal discarded the durable install")
	}
	if err := service.Configure(t.Context(), installed); err != nil {
		t.Fatal(err)
	}
	entry := service.Snapshot().Entries[0]
	if !entry.Installed || entry.State != "setup required" || entry.Description != "Review evidence." {
		t.Fatalf("cold settings lost installed identity/details: %+v", entry)
	}
	if _, err := service.InspectSkill(t.Context(), entry.ID); err != nil {
		t.Fatalf("setup prevented reading installed instructions: %v", err)
	}
	helper := filepath.Join(t.TempDir(), "crude-install-fixture-helper")
	if err := os.WriteFile(helper, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	// A real executable path is portable; no script is run by the check.
	if os.PathSeparator == '\\' {
		if err := os.Rename(helper, helper+".exe"); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", filepath.Dir(helper))
	result, err := service.Activate(t.Context(), actor, entry.ID, false)
	if err != nil || len(result.Content) != 1 || !strings.Contains(result.Content[0].Text, "Review instructions.") {
		t.Fatalf("repaired dependency did not activate the same install: %+v %v", result, err)
	}
}
