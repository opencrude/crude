package app

import (
	"errors"
	"strings"
	"testing"

	"crude/clientstate"
)

func TestShellClientProfileAuthorityAndReopen(t *testing.T) {
	home := t.TempDir()
	one, err := openShellDraftProfile(home, "one")
	if err != nil {
		t.Fatal(err)
	}
	if duplicate, err := openShellDraftProfile(home, "one"); !errors.Is(err, ErrClientProfileBusy) {
		if duplicate != nil {
			_ = duplicate.Close()
		}
		t.Fatalf("same live profile admission: %v", err)
	}
	two, err := openShellDraftProfile(home, "two")
	if err != nil {
		_ = one.Close()
		t.Fatal(err)
	}
	for _, profile := range []*shellDraftProfile{one, two} {
		id := profile.attachment.ClientID
		_, err := profile.store.Save(t.Context(), clientstate.Put{ClientID: id, Epoch: profile.attachment.Epoch, RequestID: "save", Key: clientstate.Key{ClientID: id, WorkspaceID: "same-workspace", SessionID: "same-session"}, Draft: clientstate.Draft{Text: id + " draft", Cursor: 2, RefIDs: []string{id + "-ref"}}})
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, profile := range []*shellDraftProfile{one, two} {
		id, epoch := profile.attachment.ClientID, profile.attachment.Epoch
		if err := profile.Close(); err != nil {
			t.Fatal(err)
		}
		reopened, err := openShellDraftProfile(home, id)
		if err != nil {
			t.Fatal(err)
		}
		if reopened.attachment.Epoch != epoch+1 {
			t.Fatalf("clean reopen epoch: %+v", reopened.attachment)
		}
		record, err := reopened.store.Load(id, reopened.attachment.Epoch, clientstate.Key{ClientID: id, WorkspaceID: "same-workspace", SessionID: "same-session"})
		if err != nil || record.Draft.Text != id+" draft" || record.Draft.Cursor != 2 || len(record.Draft.RefIDs) != 1 || record.Draft.RefIDs[0] != id+"-ref" {
			t.Fatalf("private persisted draft: %+v %v", record, err)
		}
		if err := reopened.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestShellClientProfileCrashLeftTakeoverFencesOldOwner(t *testing.T) {
	home := t.TempDir()
	old, err := openShellDraftProfile(home, "crash-left")
	if err != nil {
		t.Fatal(err)
	}
	id, epoch := old.attachment.ClientID, old.attachment.Epoch
	key := clientstate.Key{ClientID: id, WorkspaceID: "workspace", SessionID: "session"}
	if _, err := old.store.Save(t.Context(), clientstate.Put{ClientID: id, Epoch: epoch, RequestID: "before-crash", Key: key, Draft: clientstate.Draft{Text: "retained after crash"}}); err != nil {
		t.Fatal(err)
	}
	// Model process death: release the OS lock without releasing the durable
	// attachment. Keep the old store only to prove all old authority is fenced.
	if err := old.lock.Unlock(); err != nil {
		t.Fatal(err)
	}
	defer old.store.Close()
	next, err := openShellDraftProfile(home, id)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	if next.attachment.Epoch != epoch+1 {
		t.Fatalf("takeover epoch: %+v", next.attachment)
	}
	record, err := next.store.Load(id, next.attachment.Epoch, key)
	if err != nil || record.Draft.Text != "retained after crash" {
		t.Fatalf("takeover lost draft: %+v %v", record, err)
	}
	if _, err := old.store.Load(id, epoch, key); !errors.Is(err, clientstate.ErrStaleEpoch) {
		t.Fatalf("old reader retained authority: %v", err)
	}
	if _, err := old.store.Save(t.Context(), clientstate.Put{ClientID: id, Epoch: epoch, RequestID: "stale", Key: key, Draft: clientstate.Draft{Text: "overwrite"}}); !errors.Is(err, clientstate.ErrStaleEpoch) {
		t.Fatalf("old writer retained authority: %v", err)
	}
	if err := old.store.Release(t.Context(), id, epoch); !errors.Is(err, clientstate.ErrStaleEpoch) {
		t.Fatalf("old release retained authority: %v", err)
	}
	if duplicate, err := openShellDraftProfile(home, id); !errors.Is(err, ErrClientProfileBusy) {
		if duplicate != nil {
			_ = duplicate.Close()
		}
		t.Fatalf("takeover did not own OS lock: %v", err)
	}
}

func TestShellClientProfileInvalidNamesAndTmuxIdentity(t *testing.T) {
	home := t.TempDir()
	for _, name := range []string{"", " leading", "trailing ", "new\nline", "tab\tname", "nul\x00name", strings.Repeat("x", clientstate.MaxIDBytes+1)} {
		profile, err := openShellDraftProfile(home, name)
		if profile != nil {
			_ = profile.Close()
		}
		if !errors.Is(err, clientstate.ErrInvalid) {
			t.Fatalf("invalid name %q: %v", name, err)
		}
	}
	t.Setenv("TMUX", "/private/tmux,123,0")
	t.Setenv("TMUX_PANE", "%1")
	one, err := defaultClientProfile()
	if err != nil {
		t.Fatal(err)
	}
	again, err := defaultClientProfile()
	if err != nil || again != one {
		t.Fatalf("terminal identity unstable: %q %q %v", one, again, err)
	}
	t.Setenv("TMUX_PANE", "%2")
	two, err := defaultClientProfile()
	if err != nil || two == one {
		t.Fatalf("different panes share profile: %q %q %v", one, two, err)
	}
	t.Setenv("TMUX", "/private/other-tmux,123,0")
	other, err := defaultClientProfile()
	if err != nil || other == two {
		t.Fatalf("different tmux servers share profile: %q %q %v", two, other, err)
	}
}
