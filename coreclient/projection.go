package coreclient

import (
	"fmt"
	"slices"
	"sort"
	"time"

	"crude/coreprotocol"
	"crude/types"
	"crude/workspace"
)

// WorkspaceView is a shell-owned read projection, not a workspace runtime.
// InstallSnapshot and ApplyChanges must run on the shell's serialized UI thread.
// Reads (including paint) perform no RPC, storage access or semantic reduction.
type WorkspaceView struct {
	Client                                   *Client
	ID, RootPath, Username, InitialModelName string
	StartTime                                time.Time
	Logger                                   workspace.Logger
	AgentMgr                                 *workspace.AgentManager
	PaneMgr                                  *workspace.PaneManager
	Sessions                                 []*workspace.Session
	SessionsByID, SessionsByAgent            map[string]*workspace.Session
	InitialSettings                          types.Settings
	revision                                 uint64
	agents                                   map[string]workspace.AgentSnapshot
	membership                               map[string]map[string]bool
	appSettings                              types.AppSettings
	connectionIssues                         map[string]string
}

func NewWorkspaceView(client *Client, snapshot coreprotocol.WorkspaceSnapshot) (*WorkspaceView, error) {
	v := &WorkspaceView{Client: client, AgentMgr: workspace.NewAgentManager(), PaneMgr: workspace.NewPaneManager(),
		SessionsByID: make(map[string]*workspace.Session), SessionsByAgent: make(map[string]*workspace.Session),
		agents: make(map[string]workspace.AgentSnapshot), membership: make(map[string]map[string]bool)}
	if err := v.InstallSnapshot(snapshot); err != nil {
		return nil, err
	}
	return v, nil
}

func (v *WorkspaceView) Revision() uint64 { return v.revision }

func (v *WorkspaceView) Revisions() map[string]uint64 {
	result := make(map[string]uint64, len(v.agents))
	for _, node := range v.AgentMgr.AllNodes() {
		result[node.ID] = node.State.Messages.Version()
	}
	return result
}

// InstallSnapshot reconciles a resynchronization without replacing surviving
// session, node, state, reference-cache or conversation identities. Like
// every replica install it takes ownership of its freshly decoded argument;
// copying transcripts here would put that cost on the UI goroutine.
func (v *WorkspaceView) InstallSnapshot(snapshot coreprotocol.WorkspaceSnapshot) error {
	if snapshot.ID == "" || v.ID != "" && snapshot.ID != v.ID {
		return fmt.Errorf("%w: wrong workspace", ErrResync)
	}
	if snapshot.Revision < v.revision {
		return fmt.Errorf("%w: stale snapshot", ErrResync)
	}
	changes := coreprotocol.AgentChangesResult{Revision: snapshot.Revision}
	seen := make(map[string]bool)
	for _, agent := range snapshot.Agents {
		if seen[agent.ID] {
			return fmt.Errorf("%w: duplicate agent", ErrResync)
		}
		seen[agent.ID] = true
		order := make([]string, len(agent.Messages))
		for i, message := range agent.Messages {
			order[i] = message.InternalID
		}
		changes.Agents = append(changes.Agents, coreprotocol.AgentDelta{State: agent, Conversation: workspace.ConversationDelta{
			Reset: true, Revision: agent.ConversationRevision, Order: order, Upserts: agent.Messages}})
	}
	for id := range v.agents {
		if !seen[id] {
			changes.Removed = append(changes.Removed, id)
		}
	}
	if err := v.validateChanges(changes); err != nil {
		return err
	}
	sessionIDs := make(map[string]bool)
	for _, s := range snapshot.Sessions {
		if s.ID == "" || sessionIDs[s.ID] || s.AgentID != "" && !seen[s.AgentID] {
			return fmt.Errorf("%w: invalid session", ErrResync)
		}
		sessionIDs[s.ID] = true
	}
	for _, a := range snapshot.Agents {
		if a.SessionID != "" && !sessionIDs[a.SessionID] {
			return fmt.Errorf("%w: missing session", ErrResync)
		}
	}
	detached := snapshot
	v.ID, v.RootPath, v.Username, v.StartTime = detached.ID, detached.Root, detached.Username, detached.StartedAt
	v.InitialSettings = types.Settings{Theme: detached.Settings.Theme, Editor: detached.Settings.Editor, UI: detached.Settings.UI}
	v.appSettings = v.InitialSettings.AppPart()
	v.installSessions(detached.Sessions)
	if _, err := v.installChanges(changes); err != nil {
		return err
	}
	v.InitialModelName = ""
	if len(v.Sessions) > 0 {
		v.InitialModelName = v.Sessions[0].Settings.Effective.Model
	}
	return nil
}

// InstallSessions reconciles the session set without transcripts, retaining
// surviving session identities and their reference replicas. It takes
// ownership of snapshots.
func (v *WorkspaceView) InstallSessions(snapshots []coreprotocol.SessionSnapshot) error {
	ids := make(map[string]bool)
	for _, s := range snapshots {
		if s.ID == "" || ids[s.ID] {
			return fmt.Errorf("%w: invalid session", ErrResync)
		}
		ids[s.ID] = true
	}
	v.installSessions(snapshots)
	v.rebindSessions()
	return nil
}

func (v *WorkspaceView) installSessions(snapshots []coreprotocol.SessionSnapshot) {
	ids := make(map[string]bool, len(snapshots))
	sessions := make([]*workspace.Session, 0, len(snapshots))
	v.connectionIssues = make(map[string]string)
	for _, s := range snapshots {
		ids[s.ID] = true
		if s.ConnectionIssue != "" {
			v.connectionIssues[s.ID] = s.ConnectionIssue
		}
		owner := v.SessionsByID[s.ID]
		if owner == nil {
			owner = workspace.NewReplicaSession(s.ID, nil)
			v.SessionsByID[s.ID] = owner
		}
		v.installSession(owner, s, v.InitialSettings)
		sessions = append(sessions, owner)
	}
	for id := range v.SessionsByID {
		if !ids[id] {
			delete(v.SessionsByID, id)
		}
	}
	v.Sessions = sessions
	for _, s := range sessions {
		s.AgentState = nil
		if node := v.AgentMgr.Get(s.AgentID); node != nil {
			s.AgentState = node.State
		}
	}
}

// rebindSessions recomputes agent ownership after a session-set change.
func (v *WorkspaceView) rebindSessions() {
	v.SessionsByAgent = make(map[string]*workspace.Session, len(v.agents))
	for id, a := range v.agents {
		if s := v.SessionsByID[a.SessionID]; s != nil {
			v.SessionsByAgent[id] = s
		}
	}
}

// ApplyChanges installs committed facts exactly once. Conversation revisions
// fence missed token publications; a bad batch is rejected before installation.
// ApplyChanges installs an exclusively owned batch; the caller must not use
// or share changes afterwards.
func (v *WorkspaceView) ApplyChanges(changes coreprotocol.AgentChangesResult) ([]string, error) {
	if changes.Revision <= v.revision {
		return nil, nil
	}
	changes.Removed = v.expandRemoved(changes.Removed)
	for _, delta := range changes.Agents {
		if delta.State.SessionID != "" && v.SessionsByID[delta.State.SessionID] == nil {
			return nil, fmt.Errorf("%w: unknown session", ErrResync)
		}
	}
	if err := v.validateChanges(changes); err != nil {
		return nil, err
	}
	return v.installChanges(changes)
}

func (v *WorkspaceView) expandRemoved(ids []string) []string {
	removed := make(map[string]bool)
	var visit func(*workspace.AgentNode)
	visit = func(node *workspace.AgentNode) {
		if node == nil || removed[node.ID] {
			return
		}
		removed[node.ID] = true
		for _, child := range node.Children {
			visit(child)
		}
	}
	for _, id := range ids {
		visit(v.AgentMgr.Get(id))
		removed[id] = true
	}
	result := make([]string, 0, len(removed))
	for id := range removed {
		result = append(result, id)
	}
	sort.Strings(result)
	return result
}

func (v *WorkspaceView) validateChanges(changes coreprotocol.AgentChangesResult) error {
	parents := make(map[string]string, len(v.agents)+len(changes.Agents))
	for id, agent := range v.agents {
		parents[id] = agent.ParentID
	}
	removed := make(map[string]bool)
	for _, id := range changes.Removed {
		removed[id] = true
		delete(parents, id)
	}
	seen := make(map[string]bool)
	for _, delta := range changes.Agents {
		a := delta.State
		if a.ID == "" || seen[a.ID] || removed[a.ID] {
			return fmt.Errorf("%w: invalid agent membership", ErrResync)
		}
		seen[a.ID] = true
		if old, exists := v.agents[a.ID]; exists && old.ParentID != a.ParentID {
			return fmt.Errorf("%w: agent reparented", ErrResync)
		}
		parents[a.ID] = a.ParentID
		d := delta.Conversation
		current := uint64(0)
		if node := v.AgentMgr.Get(a.ID); node != nil {
			current = node.State.Messages.Version()
		}
		if d.Revision != a.ConversationRevision || d.Revision < current || !d.Reset && d.BaseRevision != current || d.Reset && d.Order == nil {
			return fmt.Errorf("%w: conversation revision for %s", ErrResync, a.ID)
		}
		if err := validateMembership(v.membership[a.ID], d); err != nil {
			return err
		}
	}
	for id, parent := range parents {
		path := make(map[string]bool)
		for p := id; p != ""; p = parents[p] {
			if path[p] {
				return fmt.Errorf("%w: cyclic agent tree", ErrResync)
			}
			path[p] = true
			if _, exists := parents[p]; !exists {
				return fmt.Errorf("%w: missing parent %s for %s", ErrResync, parent, id)
			}
		}
	}
	return nil
}

func validateMembership(current map[string]bool, d workspace.ConversationDelta) error {
	upserts, removed := make(map[string]bool), make(map[string]bool)
	for _, message := range d.Upserts {
		if message.InternalID == "" || upserts[message.InternalID] {
			return fmt.Errorf("%w: duplicate/empty message", ErrResync)
		}
		upserts[message.InternalID] = true
	}
	for _, id := range d.Removed {
		if id == "" || removed[id] || upserts[id] {
			return fmt.Errorf("%w: conflicting message removal", ErrResync)
		}
		removed[id] = true
	}
	has := func(id string) bool { return upserts[id] || !d.Reset && current[id] && !removed[id] }
	if d.Order == nil {
		for id := range upserts {
			if !current[id] {
				return fmt.Errorf("%w: new message without order", ErrResync)
			}
		}
		if len(removed) != 0 {
			return fmt.Errorf("%w: removal without order", ErrResync)
		}
		return nil
	}
	seen := make(map[string]bool)
	for _, id := range d.Order {
		if id == "" || seen[id] || removed[id] || !has(id) {
			return fmt.Errorf("%w: invalid message order", ErrResync)
		}
		seen[id] = true
	}
	for id := range upserts {
		if !seen[id] {
			return fmt.Errorf("%w: unordered message", ErrResync)
		}
	}
	if !d.Reset {
		for id := range current {
			if !removed[id] && !seen[id] {
				return fmt.Errorf("%w: omitted message", ErrResync)
			}
		}
	}
	return nil
}

func (v *WorkspaceView) installChanges(changes coreprotocol.AgentChangesResult) ([]string, error) {
	// Ownership transfers with the batch: it was decoded from the wire by the
	// fetch goroutine and nothing else references it, so installing it
	// requires no copy of the transcript on the UI goroutine.
	deltas := changes.Agents
	changed := append([]string(nil), changes.Removed...)
	for _, id := range changes.Removed {
		v.AgentMgr.Remove(id)
		v.PaneMgr.Remove(id)
		delete(v.agents, id)
		delete(v.membership, id)
	}
	// Parents need not precede children on the wire.
	pending := append([]coreprotocol.AgentDelta(nil), deltas...)
	for len(pending) != 0 {
		next := pending[:0]
		for _, delta := range pending {
			a := delta.State
			if a.ParentID != "" && v.AgentMgr.Get(a.ParentID) == nil {
				next = append(next, delta)
				continue
			}
			node := v.AgentMgr.Get(a.ID)
			if node == nil {
				state := workspace.NewAgentState(a.ID, nil, v.Logger)
				if a.ParentID == "" {
					node = v.AgentMgr.AddOrchestrator(a.ID, state)
				} else {
					node = v.AgentMgr.AddSubAgent(a.ID, a.ParentID, a.Label, a.Model, a.Category, state)
				}
			}
			if err := node.State.Messages.InstallDelta(delta.Conversation); err != nil {
				return nil, err
			}
			node.Label, node.Model, node.Category = a.Label, a.Model, a.Category
			node.State.InstallSnapshot(a)
			pane := v.PaneMgr.GetOrCreate(a.ID, workspace.PaneAgent, a.Label)
			pane.Messages = node.State.Messages
			pane.Title, pane.Done, pane.Status = a.Label, a.Completed, a.Status
			if a.Usage.InputTotal.Known && a.Usage.Output.Known {
				pane.TokenCount = int(a.Usage.InputTotal.Value + a.Usage.Output.Value)
			}
			// Bodies belong only to Conversation, not a second retained snapshot.
			a.Messages = nil
			v.agents[a.ID] = a
			if delta.Conversation.Reset {
				v.membership[a.ID] = make(map[string]bool)
			}
			if v.membership[a.ID] == nil {
				v.membership[a.ID] = make(map[string]bool)
			}
			for _, id := range delta.Conversation.Removed {
				delete(v.membership[a.ID], id)
			}
			for _, message := range delta.Conversation.Upserts {
				v.membership[a.ID][message.InternalID] = true
			}
			changed = append(changed, a.ID)
		}
		pending = next
	}
	v.revision = changes.Revision
	v.SessionsByAgent = make(map[string]*workspace.Session, len(v.agents))
	for id, a := range v.agents {
		if s := v.SessionsByID[a.SessionID]; s != nil {
			v.SessionsByAgent[id] = s
		}
	}
	for _, session := range v.Sessions {
		session.AgentState = nil
		if node := v.AgentMgr.Get(session.AgentID); node != nil {
			session.AgentState = node.State
		}
	}
	return changed, nil
}

func (v *WorkspaceView) LoadAppSettings() types.AppSettings {
	result := v.appSettings
	result.Theme = cloneTheme(result.Theme)
	return result
}

func cloneTheme(theme types.ThemeSettings) types.ThemeSettings {
	for _, p := range []**int{&theme.PadTop, &theme.PadBottom, &theme.PadLeft, &theme.PadRight} {
		if *p != nil {
			value := **p
			*p = &value
		}
	}
	return theme
}
func (v *WorkspaceView) AppDefaults() types.AppSettings               { return v.LoadAppSettings() }
func (v *WorkspaceView) SessionForAgent(id string) *workspace.Session { return v.SessionsByAgent[id] }
func (v *WorkspaceView) AgentActivity(id string) types.AgentActivityView {
	result := v.agents[id].Activity
	if result.Plan != nil {
		plan := *result.Plan
		plan.Steps, plan.Current = slices.Clone(plan.Steps), slices.Clone(plan.Current)
		if plan.Next != nil {
			next := *plan.Next
			plan.Next = &next
		}
		result.Plan = &plan
	}
	return result
}
func (v *WorkspaceView) AgentModelIdentity(id string) types.TurnIdentity {
	a, ok := v.agents[id]
	if !ok {
		return types.TurnIdentity{}
	}
	identity := a.TurnIdentity
	if a.ParentID == "" {
		if s := v.SessionForAgent(id); s != nil {
			identity.ModelID, identity.ProviderID, identity.AccountID = s.Settings.Effective.Model, s.Settings.Effective.ProviderID, s.Settings.Effective.AccountID
		}
	}
	return identity
}
func (v *WorkspaceView) AgentTurnActive(id string) bool {
	node := v.AgentMgr.Get(id)
	return node != nil && node.IsActive()
}
func (v *WorkspaceView) YoloForAgent(id string) bool { return v.agents[id].Yolo }

// SetYolo reflects a requested YOLO change across the owning root's subtree
// until the core's next delta confirms or replaces it.
func (v *WorkspaceView) SetYolo(id string, enabled bool) {
	root := v.AgentMgr.OrchestratorFor(id)
	if root == nil {
		return
	}
	var visit func(*workspace.AgentNode)
	visit = func(node *workspace.AgentNode) {
		if snapshot, ok := v.agents[node.ID]; ok {
			snapshot.Yolo = enabled
			v.agents[node.ID] = snapshot
		}
		for _, child := range node.Children {
			visit(child)
		}
	}
	visit(root)
}
func (v *WorkspaceView) AgentNameFor(id string) string { return v.agents[id].Label }

// UsageSummary rolls up disjoint agent summaries. Context and model selection
// always remain local to the inspected agent, even for a session/subtree total.
func (v *WorkspaceView) UsageSummary(id string, scope types.UsageScope) (types.UsageSummary, bool) {
	a, exists := v.agents[id]
	if !exists {
		return types.UsageSummary{}, false
	}
	result := a.Usage
	result.Models = slices.Clone(result.Models)
	result.Scope = scope
	if scope == types.UsageThisAgent {
		return result, result.Requests > 0
	}
	result.Requests, result.Turns, result.AgentTurns, result.Models = 0, 0, 0, nil
	result.TurnsKnown = true
	result.Cost = types.CostValue{Known: true, Confidence: types.ConfidenceMeasured}
	values := []*types.UsageValue{&result.InputTotal, &result.UncachedInput, &result.CacheRead, &result.CacheWrite, &result.Output, &result.Reasoning}
	for _, value := range values {
		*value = types.UsageValue{Known: true, Confidence: types.ConfidenceMeasured}
	}
	ids := make([]string, 0, len(v.agents))
	for key := range v.agents {
		ids = append(ids, key)
	}
	sort.Strings(ids)
	for _, key := range ids {
		other := v.agents[key]
		selected := false
		switch scope {
		case types.UsageSession:
			selected = other.SessionID == a.SessionID
		case types.UsageSubtree:
			for p := key; p != ""; p = v.agents[p].ParentID {
				if p == id {
					selected = true
					break
				}
			}
		}
		if !selected || other.Usage.Requests == 0 {
			continue
		}
		u := other.Usage
		result.Requests += u.Requests
		result.Turns += u.Turns
		result.AgentTurns += u.AgentTurns
		result.TurnsKnown = result.TurnsKnown && u.TurnsKnown
		sources := []types.UsageValue{u.InputTotal, u.UncachedInput, u.CacheRead, u.CacheWrite, u.Output, u.Reasoning}
		for i, source := range sources {
			values[i].Value += source.Value
			values[i].Known = values[i].Known && source.Known
			values[i].Confidence = combineConfidence(values[i].Confidence, source.Confidence)
		}
		result.Cost.USD += u.Cost.USD
		result.Cost.Known = result.Cost.Known && u.Cost.Known
		result.Cost.Confidence = combineConfidence(result.Cost.Confidence, u.Cost.Confidence)
		if result.Cost.Source == "" {
			result.Cost.Source = u.Cost.Source
		} else if result.Cost.Source != u.Cost.Source {
			result.Cost.Source = "mixed"
		}
		for _, model := range u.Models {
			found := false
			for i := range result.Models {
				m := &result.Models[i]
				if m.ModelID == model.ModelID && m.ProviderID == model.ProviderID && m.AccountID == model.AccountID {
					m.Requests += model.Requests
					m.Turns += model.Turns
					m.AgentTurns += model.AgentTurns
					m.TurnsKnown = m.TurnsKnown && model.TurnsKnown
					m.Cost.USD += model.Cost.USD
					m.Cost.Known = m.Cost.Known && model.Cost.Known
					m.Cost.Confidence = combineConfidence(m.Cost.Confidence, model.Cost.Confidence)
					found = true
					break
				}
			}
			if !found {
				result.Models = append(result.Models, model)
			}
		}
	}
	return result, result.Requests > 0
}

func combineConfidence(a, b types.ValueConfidence) types.ValueConfidence {
	if a == types.ConfidenceUnknown || b == types.ConfidenceUnknown {
		return types.ConfidenceUnknown
	}
	if b > a {
		return b
	}
	return a
}

// SessionConnectionIssue is the core's repair reason for an open session.
func (v *WorkspaceView) SessionConnectionIssue(sessionID string) string {
	return v.connectionIssues[sessionID]
}

// SessionSettingsRevision fences the next settings mutation for a session.
func (v *WorkspaceView) SessionSettingsRevision(sessionID string) uint64 {
	return v.SessionsByID[sessionID].SettingsRevision()
}
