package core

import (
	"fmt"
	"sync"
	"time"

	"crude/metrics"
	"crude/workspace"
)

// Core series. The workspace actor is the core's serialization point: any
// slow case in its loop delays every agent event, request and broadcast of
// that workspace, so its work is attributed by kind.
var (
	coreRequests = metrics.NewTable(metrics.Meta{Name: "core.requests", Group: metrics.GroupCore, Title: "Requests by method",
		Help: "Core time from admission to response for each request method.", Warn: 100_000, Crit: 1_000_000})
	coreActor = metrics.NewLatency(metrics.Meta{Name: "core.actor", Group: metrics.GroupCore, Title: "Workspace actor step",
		Help: "One step of a workspace actor. While a step runs, that workspace's agent events and requests wait.",
		Warn: 10_000, Crit: 50_000})
	coreActorWork = metrics.NewTable(metrics.Meta{Name: "core.actor.work", Group: metrics.GroupCore, Title: "Actor time by work",
		Help: "Workspace actor time per kind of work (request method, agent event, service event, tick).", Warn: 10_000, Crit: 50_000})
	_ = metrics.NewGauge(metrics.Meta{Name: "core.queue", Group: metrics.GroupCore, Title: "Actor backlog", Unit: "items",
		Help: "Aggregate requests, agent events and service events waiting across retained workspace actors. A growing backlog means actors cannot keep up.",
		Warn: 64, Crit: 512}, sampleActorQueues)
	fileEvents = metrics.NewCounter(metrics.Meta{Name: "files.events", Group: metrics.GroupFiles, Title: "File system events",
		Help: "Project file changes observed by the watcher. Builds and installs produce bursts."})
	fileWatchStart = metrics.NewLatency(metrics.Meta{Name: "files.watch", Group: metrics.GroupFiles, Title: "Watcher setup",
		Help: "Walking the project to register directory watches.", Warn: 2_000_000, Crit: 10_000_000})
	receiptLatency = metrics.NewLatency(metrics.Meta{Name: "storage.receipts", Group: metrics.GroupStorage, Title: "Receipt journal commit",
		Help: "Durable request receipts written on the workspace actor before and after each mutation. Slow disks (fsync) show here.",
		Warn: 5_000, Crit: 30_000})
)

var actorQueues = struct {
	sync.Mutex
	owners map[*actor]struct{}
}{owners: make(map[*actor]struct{})}

// trackActorQueue retains only sampler membership, not a second copy of depth.
// The existing metrics sampler is the single writer of the aggregate gauge.
func trackActorQueue(a *actor, live bool) {
	actorQueues.Lock()
	defer actorQueues.Unlock()
	if live {
		actorQueues.owners[a] = struct{}{}
	} else {
		delete(actorQueues.owners, a)
	}
}

// Sampling reads current channel occupancy even while an actor is stuck inside
// a step. End-of-step values alone would hide the very backlog being diagnosed.
// This uses the retained owner membership, not another queue/cursor authority.
func sampleActorQueues() int64 {
	actorQueues.Lock()
	defer actorQueues.Unlock()
	var total int
	for a := range actorQueues.owners {
		total += len(a.commands) + len(a.ingress) + len(a.serviceEvents) + len(a.jobs)
	}
	return int64(total)
}

// actorWorkKey names one actor step for attribution.
func actorWorkKey(kind string, detail any) string {
	switch value := detail.(type) {
	case string:
		if value == "" {
			return kind
		}
		return kind + " " + value
	case workspace.AgentEvent:
		return kind + " " + value.Type
	case nil:
		return kind
	default:
		return kind + " " + fmt.Sprintf("%T", value)
	}
}

func observeActorStep(start time.Time, kind string, detail any) {
	elapsed := time.Since(start)
	key := actorWorkKey(kind, detail)
	coreActor.ObserveLabeled(elapsed, key)
	coreActorWork.Observe(key, elapsed)
}
