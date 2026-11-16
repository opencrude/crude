package core

import (
	"testing"
	"time"

	"crude/coreprotocol"
)

func TestElectPresenceNobodyNotifiesWhileTheUserIsPresent(t *testing.T) {
	at := time.Unix(100, 0)
	got := electPresence(map[coreprotocol.Owner]shellPresence{
		{ClientID: "laptop", Epoch: 1}:  {notifies: true, changed: at.Add(time.Hour)},
		{ClientID: "desktop", Epoch: 1}: {present: true, notifies: true, changed: at},
	})
	if !got.Present || got.Notifier != (coreprotocol.Owner{}) {
		t.Fatalf("election = %+v, want present and no notifier", got)
	}
}

func TestElectPresenceChoosesTheShellTheUserLeftLast(t *testing.T) {
	at := time.Unix(100, 0)
	desktop, laptop, ssh := coreprotocol.Owner{ClientID: "desktop", Epoch: 1}, coreprotocol.Owner{ClientID: "laptop", Epoch: 2}, coreprotocol.Owner{ClientID: "ssh", Epoch: 1}
	got := electPresence(map[coreprotocol.Owner]shellPresence{
		desktop: {notifies: true, changed: at.Add(2 * time.Minute)},
		laptop:  {notifies: true, changed: at},
		// The shell that cannot notify was left last; it is not elected.
		ssh: {notifies: false, changed: at.Add(time.Hour)},
	})
	if got.Present || got.Notifier != desktop {
		t.Fatalf("election = %+v, want %+v", got, desktop)
	}
	if got := electPresence(map[coreprotocol.Owner]shellPresence{ssh: {changed: at}}); got.Notifier != (coreprotocol.Owner{}) || got.Present {
		t.Fatalf("election = %+v, want nobody when no shell can notify", got)
	}
	if got := electPresence(nil); got != (coreprotocol.PresenceSnapshot{}) {
		t.Fatalf("empty election = %+v", got)
	}
}

func TestElectPresenceIsDeterministicForEqualTimes(t *testing.T) {
	at := time.Unix(100, 0)
	a, b := coreprotocol.Owner{ClientID: "a", Epoch: 1}, coreprotocol.Owner{ClientID: "b", Epoch: 1}
	for range 50 {
		got := electPresence(map[coreprotocol.Owner]shellPresence{a: {notifies: true, changed: at}, b: {notifies: true, changed: at}})
		if got.Notifier != b {
			t.Fatalf("election = %+v, want %+v every time", got, b)
		}
	}
}

func TestReportPresenceKeepsTheTimeOfThePresenceChange(t *testing.T) {
	a := &actor{}
	owner := coreprotocol.Owner{ClientID: "shell", Epoch: 1}
	left := time.Unix(100, 0)
	a.reportPresence(owner, coreprotocol.ShellPresenceReport{Present: true, Notifies: true}, left.Add(-time.Minute))
	a.reportPresence(owner, coreprotocol.ShellPresenceReport{Present: false, Notifies: true}, left)
	// The user turns notifications off and on while away: the shell still
	// counts as left at the original time.
	a.reportPresence(owner, coreprotocol.ShellPresenceReport{Present: false, Notifies: false}, left.Add(time.Minute))
	a.reportPresence(owner, coreprotocol.ShellPresenceReport{Present: false, Notifies: true}, left.Add(2*time.Minute))
	if got := a.presence[owner]; !got.changed.Equal(left) || got.present || !got.notifies {
		t.Fatalf("presence = %+v, want away since %v", got, left)
	}
	a.forgetPresence(owner)
	if len(a.presence) != 0 {
		t.Fatal("a detached shell stays in the election")
	}
}
