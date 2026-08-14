package filepicker

import (
	"errors"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/ui"
)

func TestAsyncDirectoryLoadSuppressesFilterAndActivation(t *testing.T) {
	for _, mode := range []Mode{ModeFile, ModeFolder} {
		t.Run(modeName(mode), func(t *testing.T) {
			screen, host := pickerHost(t, mode)
			screen.BeginAsyncDirectoryLoad(filepath.FromSlash("/root/pending"))

			if !screen.AsyncDirectoryLoading() {
				t.Fatal("picker did not enter loading state")
			}
			if got := screen.VisibleCount(); got != 0 {
				t.Fatalf("loading rows=%d, want 0", got)
			}

			result := host.Handle(tea.KeyPressMsg{Code: 's', Text: "s"})
			if !result.Handled {
				t.Fatalf("typing while loading was not handled: %#v", result)
			}
			if got := screen.VisibleCount(); got != 0 {
				t.Fatalf("filter repopulated %d stale rows while loading", got)
			}
			if result = host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter}); result.Cmd != nil || result.Action != nil {
				t.Fatalf("Enter activated while loading: %#v", result)
			}

			staleEntry := Entry{Name: "src", Path: filepath.FromSlash("/root/src"), IsDir: true}
			result = screen.HandleAction(ui.EventContext{}, ui.Action{
				ID:      "filepicker.entry.activate",
				Payload: ui.ListItem{Payload: staleEntry},
			})
			if result.Cmd != nil || result.Action != nil {
				t.Fatalf("stale entry action activated while loading: %#v", result)
			}

			if mode == ModeFolder {
				result = screen.HandleAction(ui.EventContext{}, ui.Action{ID: "filepicker.select"})
				if result.Cmd != nil || result.Action != nil {
					t.Fatalf("current-folder action activated while loading: %#v", result)
				}
				if !host.Focus(ActionsID) {
					t.Fatal("could not focus folder actions")
				}
				result = host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
				if result.Cmd != nil || result.Action != nil {
					t.Fatalf("folder action-bar Enter activated while loading: %#v", result)
				}
			}
		})
	}
}

func TestFailedFolderNavigationRestoresExactlyOneParentRow(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	child := filepath.Join(root, "child")
	screen := New(nil)
	screen.Open(root, ModeFolder, []Entry{{Name: "child", Path: child, IsDir: true}})

	screen.BeginAsyncDirectoryLoad(filepath.Join(root, "unreadable"))
	if !screen.FailAsyncDirectoryLoad(errors.New("read denied")) {
		t.Fatal("failed load was not restored")
	}

	parentRows := 0
	for _, entry := range screen.entries {
		if entry.Name == ".." {
			parentRows++
		}
	}
	if parentRows != 1 {
		t.Fatalf("restored parent rows=%d, want exactly 1; entries=%#v", parentRows, screen.entries)
	}
	if got := screen.VisibleCount(); got != 2 {
		t.Fatalf("restored visible rows=%d, want parent plus child", got)
	}
	if got := screen.Current(); got != root {
		t.Fatalf("restored directory=%q, want %q", got, root)
	}
}

func TestPickerIntentEpochAndSequenceRejectStaleMessages(t *testing.T) {
	screen := New(nil)
	screen.Open(filepath.FromSlash("/root"), ModeFile, pickerEntries())

	first := commandMessage[NavigateMsg](t, screen.navigate(filepath.FromSlash("/root/first")))
	second := commandMessage[NavigateMsg](t, screen.navigate(filepath.FromSlash("/root/second")))
	if first.Epoch == 0 || first.Sequence == 0 {
		t.Fatalf("first navigation omitted lifecycle identity: %#v", first)
	}
	if second.Epoch != first.Epoch || second.Sequence <= first.Sequence {
		t.Fatalf("navigation identities did not increase: first=%#v second=%#v", first, second)
	}
	if first.ValidFor(screen) {
		t.Fatal("older navigation remained current after a newer intent")
	}
	if !second.ValidFor(screen) {
		t.Fatal("newest navigation was not current")
	}
	if screen.BeginAsyncDirectoryLoadFor(first.Path, first.Intent()) {
		t.Fatal("stale navigation started a directory load")
	}
	if !screen.BeginAsyncDirectoryLoadFor(second.Path, second.Intent()) {
		t.Fatal("current navigation did not start a directory load")
	}
	if screen.CompleteAsyncDirectoryLoadFor(
		first.Intent(),
		first.Path,
		[]Entry{{Name: "stale.go", Path: filepath.Join(first.Path, "stale.go")}},
	) {
		t.Fatal("stale navigation completed the current directory load")
	}
	if !screen.CancelAsyncDirectoryLoadFor(second.Intent()) {
		t.Fatal("current directory load could not be cancelled")
	}

	selected := commandMessage[SelectMsg](t, screen.selectPath(filepath.FromSlash("/root/a.txt"), false))
	cancelled := commandMessage[CancelMsg](t, screen.cancel())
	if selected.ValidFor(screen) {
		t.Fatal("older selection remained current after cancel")
	}
	if !cancelled.ValidFor(screen) {
		t.Fatal("newest cancel was not current")
	}
	if cancelled.Sequence <= selected.Sequence {
		t.Fatalf("cancel sequence=%d, want greater than select sequence=%d", cancelled.Sequence, selected.Sequence)
	}

	oldEpoch := cancelled.Epoch
	screen.Close()
	if screen.AsyncDirectoryLoading() {
		t.Fatal("Close retained async directory state")
	}
	screen.Open(filepath.FromSlash("/root"), ModeFile, pickerEntries())
	if cancelled.ValidFor(screen) {
		t.Fatal("cancel from a prior open lifecycle passed validation")
	}
	if screen.OpenEpoch() <= oldEpoch {
		t.Fatalf("reopened epoch=%d, want greater than %d", screen.OpenEpoch(), oldEpoch)
	}
	reopened := commandMessage[NavigateMsg](t, screen.navigate(filepath.FromSlash("/root/reopened")))
	if !reopened.ValidFor(screen) {
		t.Fatal("reopened picker navigation was not current")
	}
	if reopened.Sequence <= cancelled.Sequence {
		t.Fatalf("reopened sequence=%d, want greater than %d", reopened.Sequence, cancelled.Sequence)
	}
}

func modeName(mode Mode) string {
	if mode == ModeFolder {
		return "folder"
	}
	return "file"
}
