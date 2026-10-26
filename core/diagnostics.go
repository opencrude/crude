package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"sync"
	"time"

	"crude/buildinfo"
	"crude/coreprotocol"
	"crude/types/inspect"
)

const diagnosticInventoryLimit = 2048
const diagnosticActorTimeout = 50 * time.Millisecond

// InspectDiagnostics observes existing owners without changing runtime lifecycle
// decisions. Status/reserveIdle remain the fail-closed shutdown authorities.
func (r *Runtime) InspectDiagnostics(ctx context.Context, store *inspect.Store, caller coreprotocol.Owner, workspaceID string, query inspect.Request) (inspect.Page, error) {
	r.mu.Lock()
	c := r.clients[caller.ClientID]
	valid := c != nil && c.Owner == caller && !r.closed
	r.mu.Unlock()
	if !valid {
		return inspect.Page{}, errors.New("diagnostic caller is unavailable or stale")
	}
	select {
	case <-c.done:
		return inspect.Page{}, errors.New("diagnostic caller is closing")
	default:
	}
	if workspaceID != "" {
		c.mu.Lock()
		attached := c.attached[workspaceID] != nil
		c.mu.Unlock()
		if !attached {
			return inspect.Page{}, errors.New("diagnostic workspace is not attached to this caller")
		}
	}
	return r.inspectDiagnosticsAuthorized(ctx, store, fmt.Sprintf("client:%s:%d", caller.ClientID, caller.Epoch), c, workspaceID, query)
}

// Only gateway-admitted native callers may bypass transport-owner lookup. The
// gateway supplies a run-bound owner key and its already-authorized workspace.
// A nil connection never grants access to any retained request metadata.
func (r *Runtime) inspectDiagnosticsAuthorized(ctx context.Context, store *inspect.Store, ownerKey string, caller *Connection, workspaceID string, query inspect.Request) (inspect.Page, error) {
	if store == nil || ownerKey == "" {
		return inspect.Page{}, errors.New("diagnostic store and authenticated owner required")
	}
	if query.SnapshotID == "" && query.View == "" {
		query.View = "summary"
	}
	if query.View != "" && query.View != "summary" && query.View != "clients" && query.View != "workspaces" && query.View != "replication" && query.View != "requests" && query.View != "listeners" {
		return inspect.Page{}, errors.New("view must be summary, clients, workspaces, replication, requests or listeners")
	}
	// Binding scope into the owner also prevents continuation after switching the
	// authorized workspace, even when continuation omits its original selectors.
	ownerKey += "\x00workspace:" + workspaceID
	return store.Read(ctx, ownerKey, query, func(ctx context.Context) (inspect.Observation, error) {
		return r.collectDiagnostics(ctx, caller, workspaceID, query), nil
	})
}

type runtimeDiagnosticSource struct {
	WorkspaceID    string    `json:"workspace_id,omitempty"`
	Scope          string    `json:"scope"`
	Partial        bool      `json:"partial"`
	InventoryLimit int       `json:"inventory_limit"`
	ActorTimeoutMS int64     `json:"actor_timeout_ms"`
	PID            int       `json:"pid"`
	ProtocolBuild  string    `json:"protocol_build"`
	Taken          time.Time `json:"taken"`
}

type runtimeDiagnosticSummary struct {
	PID                   int       `json:"pid"`
	Build                 string    `json:"build"`
	ProtocolBuild         string    `json:"protocol_build"`
	OwnerGeneration       string    `json:"owner_generation"`
	Server                bool      `json:"server"`
	StartedAt             time.Time `json:"started_at"`
	Closed                bool      `json:"closed"`
	Draining              bool      `json:"draining"`
	Stopping              bool      `json:"stopping"`
	RetainedClients       int       `json:"retained_clients"`
	CarrierPresent        int       `json:"carrier_present"`
	CarrierUnavailable    int       `json:"carrier_unavailable"`
	CarrierCountsComplete bool      `json:"carrier_counts_complete"`
	Workspaces            int       `json:"retained_workspaces"`
	Opening               int       `json:"opening_workspaces"`
	CommandLimit          int       `json:"commands_per_client"`
	CommandBytes          int       `json:"command_preimage_bytes_per_client"`
	TopicLimit            int       `json:"observed_topics_per_client"`
}

type clientDiagnostic struct {
	Owner                    coreprotocol.Owner `json:"owner"`
	CarrierPresent           bool               `json:"carrier_present"`
	Carrier                  string             `json:"carrier,omitempty"`
	RTTNS                    int64              `json:"rtt_ns"`
	GraceDeadline            time.Time          `json:"grace_deadline,omitempty"`
	Closing                  bool               `json:"closing"`
	Attachments              int                `json:"attachment_count"`
	CurrentWorkspaceAttached bool               `json:"current_workspace_attached"`
	Commands                 int                `json:"commands"`
	Executing                int                `json:"executing"`
	AwaitingFinalACK         int                `json:"awaiting_final_ack"`
	CommandBytes             int                `json:"command_preimage_bytes"`
	CommandCapacity          int                `json:"command_capacity"`
	CommandByteCapacity      int                `json:"command_byte_capacity"`
	OldestCommandAgeMS       int64              `json:"oldest_command_age_ms"`
	ControlOutstanding       int                `json:"control_outstanding"`
	ControlCapacity          int                `json:"control_capacity"`
	ControlBytes             int                `json:"control_bytes"`
	ControlByteCapacity      int                `json:"control_byte_capacity"`
	MutationReceipts         int                `json:"mutation_receipts"`
	MutationReceiptCapacity  int                `json:"mutation_receipt_capacity"`
	PrivateQueued            int                `json:"private_queued"`
	PrivateBytes             int                `json:"private_bytes"`
	PrivateCapacity          int                `json:"private_capacity"`
	PrivateByteCapacity      int                `json:"private_byte_capacity"`
	ObservedTopics           int                `json:"observed_topics"`
	TopicsPartial            bool               `json:"topics_partial"`
}

func (c *Connection) diagnosticClient(workspaceID string) clientDiagnostic {
	v := clientDiagnostic{Owner: c.Owner, CommandCapacity: sessionCommandLimit, CommandByteCapacity: sessionCommandBytes, ControlCapacity: coreprotocol.ControlMaxOutstanding, ControlByteCapacity: coreprotocol.ControlMaxQueuedBytes, MutationReceiptCapacity: controlReceiptLimit, PrivateCapacity: coreprotocol.SessionPrivateMaxOutstanding, PrivateByteCapacity: coreprotocol.SessionPrivateMaxBytes}
	c.carrierMu.Lock()
	v.CarrierPresent, v.GraceDeadline, v.Closing = c.carrier != nil, c.graceDeadline, c.closing
	if c.carrier != nil {
		v.Carrier = string(c.carrier.Carrier())
		v.RTTNS = int64(c.carrier.RTT())
	}
	c.carrierMu.Unlock()
	c.mu.Lock()
	v.Attachments, v.CurrentWorkspaceAttached = len(c.attached), c.attached[workspaceID] != nil
	v.ControlOutstanding, v.ControlBytes = c.controlOutstanding, c.controlBytes
	for _, receipt := range c.controlReceipts {
		if !receipt.readOnly {
			v.MutationReceipts++
		}
	}
	c.mu.Unlock()
	c.commandMu.Lock()
	v.Commands, v.CommandBytes = len(c.commands), c.commandBytes
	for _, cmd := range c.commands {
		select {
		case <-cmd.done:
			v.AwaitingFinalACK++
		default:
			v.Executing++
		}
		if !cmd.admitted.IsZero() {
			v.OldestCommandAgeMS = max(v.OldestCommandAgeMS, time.Since(cmd.admitted).Milliseconds())
		}
	}
	c.commandMu.Unlock()
	c.privateMu.Lock()
	v.PrivateQueued, v.PrivateBytes = len(c.privateEffects), c.privateBytes
	c.privateMu.Unlock()
	c.topicMu.Lock()
	v.ObservedTopics = len(c.topicObservations)
	v.TopicsPartial = c.topicObservationDropped > 0
	c.topicMu.Unlock()
	return v
}

type workspaceDiagnostic struct {
	ID              string                   `json:"workspace_id"`
	Available       bool                     `json:"available"`
	Unavailable     string                   `json:"unavailable,omitempty"`
	Revision        uint64                   `json:"revision"`
	Subscribers     int                      `json:"subscribers"`
	Sessions        int                      `json:"model_sessions"`
	Operations      int                      `json:"operations"`
	SettingsBusy    bool                     `json:"settings_busy"`
	SettingsQueued  int                      `json:"settings_queued"`
	ControlQueued   int                      `json:"control_queued"`
	Retiring        bool                     `json:"retiring"`
	IdleFenced      bool                     `json:"idle_fenced"`
	Lifecycle       bool                     `json:"lifecycle"`
	Commands        int                      `json:"queued_commands"`
	CommandCapacity int                      `json:"command_capacity"`
	Ingress         int                      `json:"queued_agent_events"`
	IngressCapacity int                      `json:"agent_event_capacity"`
	Services        int                      `json:"queued_service_events"`
	ServiceCapacity int                      `json:"service_event_capacity"`
	Resources       *resourceLaneObservation `json:"resource_lane"`
	Terminals       *resourceLaneObservation `json:"terminal_lane"`
}

func diagnosticWorkspace(ctx context.Context, a *actor) workspaceDiagnostic {
	v := workspaceDiagnostic{ID: a.id}
	ctx, cancel := context.WithTimeout(ctx, diagnosticActorTimeout)
	defer cancel()
	err := a.inspect(ctx, func() {
		v.Available = true
		v.Revision, v.Subscribers, v.Sessions, v.Operations = a.revision, len(a.subscribers), len(a.ws.Sessions), a.inFlight
		v.SettingsBusy, v.SettingsQueued, v.ControlQueued = a.settingsBusy, len(a.settingsQueue), len(a.controlQueue)
		v.Retiring, v.IdleFenced, v.Lifecycle = a.retiring, a.idleFenced, a.lifecycle
	})
	if err != nil {
		v.Available = false
		v.Unavailable = "actor_unavailable"
		if errors.Is(err, context.DeadlineExceeded) {
			v.Unavailable = "actor_timeout"
		}
	}
	// Channel occupancy and lane snapshots are owner-safe even if the actor is
	// stalled; no running callback writes to this value after inspect returns.
	v.Commands, v.CommandCapacity = len(a.commands), cap(a.commands)
	v.Ingress, v.IngressCapacity = len(a.ingress), cap(a.ingress)
	v.Services, v.ServiceCapacity = len(a.serviceEvents), cap(a.serviceEvents)
	v.Resources, v.Terminals = a.resourceLane.observation(), a.terminalLane.observation()
	return v
}

func (r *Runtime) collectDiagnostics(ctx context.Context, caller *Connection, workspaceID string, query inspect.Request) inspect.Observation {
	result := inspect.Observation{}
	source := runtimeDiagnosticSource{WorkspaceID: workspaceID, Scope: "same_user_core_metadata", InventoryLimit: diagnosticInventoryLimit, ActorTimeoutMS: diagnosticActorTimeout.Milliseconds(), PID: os.Getpid(), ProtocolBuild: buildinfo.ProtocolBuild, Taken: time.Now()}
	if workspaceID != "" {
		source.Scope = "current_workspace_details_and_core_counts"
	}
	appendRecord := func(kind, id string, value any) {
		if len(result.Records) >= diagnosticInventoryLimit {
			source.Partial = true
			return
		}
		if !inspectionMatch(query.Filter, id, kind) {
			return
		}
		raw, _ := json.Marshal(value)
		result.Records = append(result.Records, inspect.Record{Kind: kind, ID: id, Value: raw})
	}
	summary := runtimeDiagnosticSummary{PID: os.Getpid(), ProtocolBuild: buildinfo.ProtocolBuild, CommandLimit: sessionCommandLimit, CommandBytes: sessionCommandBytes, TopicLimit: diagnosticTopicLimit}
	r.mu.Lock()
	summary.RetainedClients, summary.Workspaces, summary.Opening = len(r.clients), len(r.workspaces), len(r.creating)
	summary.Closed, summary.Draining, summary.Stopping = r.closed, r.draining, r.stopping
	clients := make([]*Connection, 0, min(len(r.clients), diagnosticInventoryLimit))
	for _, c := range r.clients {
		if query.View == "clients" && !inspectionMatch(query.Filter, c.Owner.ClientID, "client") {
			continue
		}
		if len(clients) >= diagnosticInventoryLimit {
			source.Partial = true
			break
		}
		clients = append(clients, c)
	}
	actors := make([]*actor, 0, min(len(r.workspaces), diagnosticInventoryLimit))
	for _, a := range r.workspaces {
		if workspaceID != "" && a.id != workspaceID {
			continue
		}
		if query.View == "workspaces" && !inspectionMatch(query.Filter, a.id, "workspace") {
			continue
		}
		if len(actors) >= diagnosticInventoryLimit {
			source.Partial = true
			break
		}
		actors = append(actors, a)
	}
	r.mu.Unlock()
	r.lifetime.mu.Lock()
	summary.Build, summary.OwnerGeneration, summary.Server, summary.StartedAt = r.lifetime.buildID, r.lifetime.ownerGeneration, r.lifetime.server, r.lifetime.started
	r.lifetime.mu.Unlock()
	sort.Slice(clients, func(i, j int) bool { return clients[i].Owner.ClientID < clients[j].Owner.ClientID })
	sort.Slice(actors, func(i, j int) bool { return actors[i].id < actors[j].id })
	switch query.View {
	case "listeners":
		if r.remote == nil {
			appendRecord("listeners", "listeners", map[string]any{"available": false, "reason": "remote admission owner not installed"})
		} else {
			// Configuration is not a listening socket. The admission owner already
			// receives exact live endpoints from the listener lifecycle owner.
			r.remote.mu.Lock()
			config, endpoints := r.remote.config, r.remote.endpoints
			r.remote.mu.Unlock()
			appendRecord("listeners", "listeners", map[string]any{"available": true, "enabled": config.Enabled, "configured_quic": config.QUICAddress, "configured_tls": config.TLSAddress, "live_quic": endpoints.QUICAddress, "live_tls": endpoints.TLSAddress, "session_grace_seconds": config.GracePeriod.Seconds()})
		}
	case "summary":
		for _, c := range clients {
			c.carrierMu.Lock()
			present := c.carrier != nil
			c.carrierMu.Unlock()
			if present {
				summary.CarrierPresent++
			} else {
				summary.CarrierUnavailable++
			}
		}
		summary.CarrierCountsComplete = len(clients) == summary.RetainedClients
		appendRecord("runtime", "runtime", summary)
	case "clients":
		for _, c := range clients {
			appendRecord("client", c.Owner.ClientID, c.diagnosticClient(workspaceID))
		}
	case "workspaces":
		result.Notes = append(result.Notes, "Workspace records describe actor capacity and lifecycle, not canonical issue, notification, agent, language-server or capability health. Read diagnostics.workspace for those owners; empty queues alone do not establish workspace health.")
		// A fixed worker pool prevents one stalled actor consuming every other
		// owner's deadline or spawning an unbounded diagnostic goroutine fanout.
		values := make([]workspaceDiagnostic, len(actors))
		jobs := make(chan int)
		bounded, cancel := context.WithTimeout(ctx, 750*time.Millisecond)
		defer cancel()
		var wg sync.WaitGroup
		for range min(4, len(actors)) {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := range jobs {
					values[i] = diagnosticWorkspace(bounded, actors[i])
				}
			}()
		}
		for i := range actors {
			jobs <- i
		}
		close(jobs)
		wg.Wait()
		for _, v := range values {
			if !v.Available {
				source.Partial = true
			}
			appendRecord("workspace", v.ID, v)
		}
	case "replication":
		for _, a := range actors {
			appendRecord("topic_hub", a.id+"/workspace", a.topic.hub.observation(false))
			for _, name := range workspaceStateTopics {
				slot := a.states.slots[name]
				if slot == nil {
					continue
				}
				slot.mu.Lock()
				wanted, published := slot.wanted, slot.published
				slot.mu.Unlock()
				appendRecord("state_topic", a.id+"/"+name, struct {
					Wanted    uint64              `json:"wanted_generation"`
					Published uint64              `json:"published_generation"`
					Hub       topicHubObservation `json:"hub"`
				}{wanted, published, slot.hub.observation(true)})
			}
		}
		visited := 0
	subscriptions:
		for _, c := range clients {
			c.topicMu.Lock()
			for _, entry := range c.topicObservations {
				visited++
				if len(result.Records) >= diagnosticInventoryLimit || visited > diagnosticInventoryLimit {
					source.Partial = true
					c.topicMu.Unlock()
					break subscriptions
				}
				if workspaceID != "" && entry.WorkspaceID != workspaceID && (entry.WorkspaceID != "" || c != caller) {
					continue
				}
				v := *entry
				if !v.PendingSince.IsZero() {
					v.PendingAgeMS = max(int64(0), time.Since(v.PendingSince).Milliseconds())
				}
				appendRecord("subscription", fmt.Sprintf("%s:%d/%s/%s/%s", c.Owner.ClientID, c.Owner.Epoch, v.WorkspaceID, v.Topic, v.ResourceID), struct {
					Owner coreprotocol.Owner `json:"owner"`
					Topic topicObservation   `json:"observation"`
				}{c.Owner, v})
			}
			if c.topicObservationDropped > 0 {
				source.Partial = true
			}
			c.topicMu.Unlock()
		}
	case "requests":
		if caller == nil {
			result.Notes = append(result.Notes, "Retained request metadata is unavailable without an exact transport caller; other clients are counts only in clients view.")
			break
		}
		caller.commandMu.Lock()
		for _, cmd := range caller.commands {
			if workspaceID != "" && cmd.request.WorkspaceID != workspaceID {
				continue
			}
			state := "executing"
			var completed time.Time
			select {
			case <-cmd.done:
				state = "awaiting_final_ack"
				completed = cmd.completed
			default:
			}
			appendRecord("request", cmd.request.ID, struct {
				Method    string    `json:"method"`
				State     string    `json:"state"`
				Admitted  time.Time `json:"admitted_at"`
				Completed time.Time `json:"completed_at,omitempty"`
				Bytes     int       `json:"preimage_bytes"`
			}{diagnosticMethod(cmd.request.Method), state, cmd.admitted, completed, len(cmd.request.Payload)})
		}
		caller.commandMu.Unlock()
	}
	sort.Slice(result.Records, func(i, j int) bool {
		a, b := result.Records[i], result.Records[j]
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		return a.ID < b.ID
	})
	if source.Partial {
		result.Notes = append(result.Notes, "Partial owner observation: unavailable actors or bounded inventory omitted; missing values are not zero.")
	}
	result.Source, _ = json.Marshal(source)
	return result
}

// Request methods come from an untrusted frame. Only method-shaped identifiers,
// not arbitrary strings supplied in a failed request, belong in diagnostics.
func diagnosticMethod(value string) string {
	if len(value) > 80 || value == "" {
		return "unrecognized"
	}
	for _, r := range value {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '.' && r != '_' {
			return "unrecognized"
		}
	}
	return value
}
