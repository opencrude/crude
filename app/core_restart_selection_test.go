package app

import (
	"context"
	"errors"
	"testing"

	"crude/coreprotocol"
	"crude/shellport/remote"
	startuptui "crude/tui/startup"
)

func TestCoreRestartFailedAttachmentNeverOpensChooserOrAlternateWorkspace(t *testing.T) {
	root := t.TempDir()
	selection := coreprotocol.StartupSnapshot{Root: root}
	fail := errors.New("exact destination became unavailable after inspection")
	attaches, choices := 0, 0
	chosen, ws, _, err := attachStartupSelection(t.Context(), root, true, selection,
		func(context.Context, coreprotocol.StartupSnapshot) (startuptui.Selection, error) {
			choices++
			return startuptui.Selection{Root: t.TempDir()}, nil
		}, func(_ context.Context, candidate string) (*remote.Workspace, error) {
			attaches++
			if candidate != root {
				t.Fatal("private startup attached a replacement workspace")
			}
			return nil, fail
		})
	if !errors.Is(err, fail) || chosen != "" || ws != nil || attaches != 1 || choices != 0 {
		t.Fatalf("private failure entered ordinary startup: err=%v chosen=%q attachments=%d choices=%d", err, chosen, attaches, choices)
	}
}

func TestCoreRestartOrdinaryAttachmentFailureRetainsRepairChooser(t *testing.T) {
	root, alternative := t.TempDir(), t.TempDir()
	selection := coreprotocol.StartupSnapshot{Root: root}
	attaches, choices := 0, 0
	expected := new(remote.Workspace)
	chosen, ws, _, err := attachStartupSelection(t.Context(), root, false, selection,
		func(_ context.Context, observed coreprotocol.StartupSnapshot) (startuptui.Selection, error) {
			choices++
			if observed.PathError == "" || observed.Root != root {
				t.Fatal("ordinary recovery lost its failure owner")
			}
			return startuptui.Selection{Root: alternative}, nil
		}, func(_ context.Context, candidate string) (*remote.Workspace, error) {
			attaches++
			if attaches == 1 {
				return nil, errors.New("initial root failed")
			}
			if candidate != alternative {
				t.Fatal("ordinary selection was ignored")
			}
			return expected, nil
		})
	if err != nil || chosen != alternative || ws != expected || attaches != 2 || choices != 1 {
		t.Fatal("ordinary startup repair was broken", chosen, attaches, choices, err)
	}
}
