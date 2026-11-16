package core

import (
	"strings"
	"time"

	"crude/coreprotocol"
)

// shellPresence is the latest report of one attached shell. changed is the
// core's time of the shell's last presence change, so that every comparison
// uses one clock even when shells run on other computers.
type shellPresence struct {
	present  bool
	notifies bool
	changed  time.Time
}

// reportPresence records a shell's report on the actor goroutine and
// republishes the election. A report that changes nothing keeps the time of
// the previous change.
func (a *actor) reportPresence(owner coreprotocol.Owner, report coreprotocol.ShellPresenceReport, now time.Time) {
	if a.presence == nil {
		a.presence = make(map[coreprotocol.Owner]shellPresence)
	}
	entry, known := a.presence[owner]
	if known && entry.present == report.Present && entry.notifies == report.Notifies {
		return
	}
	if !known || entry.present != report.Present {
		entry.changed = now
	}
	entry.present, entry.notifies = report.Present, report.Notifies
	a.presence[owner] = entry
	a.queueState(coreprotocol.TopicPresence)
}

// forgetPresence drops a detached shell from the election.
func (a *actor) forgetPresence(owner coreprotocol.Owner) {
	if _, ok := a.presence[owner]; ok {
		delete(a.presence, owner)
		a.queueState(coreprotocol.TopicPresence)
	}
}

// electPresence chooses who notifies. A present user sees Crude, so nobody
// notifies. Else the notifier is the shell that can notify and that the user
// left last: the computer where the user most likely is. Equal times choose
// the larger owner, so every core run elects the same shell.
func electPresence(shells map[coreprotocol.Owner]shellPresence) coreprotocol.PresenceSnapshot {
	var best coreprotocol.Owner
	var bestAt time.Time
	found := false
	for owner, shell := range shells {
		if shell.present {
			return coreprotocol.PresenceSnapshot{Present: true}
		}
		if !shell.notifies {
			continue
		}
		if !found || shell.changed.After(bestAt) || shell.changed.Equal(bestAt) && ownerAfter(owner, best) {
			best, bestAt, found = owner, shell.changed, true
		}
	}
	return coreprotocol.PresenceSnapshot{Notifier: best}
}

func ownerAfter(a, b coreprotocol.Owner) bool {
	if order := strings.Compare(a.ClientID, b.ClientID); order != 0 {
		return order > 0
	}
	return a.Epoch > b.Epoch
}
