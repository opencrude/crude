package workspace

// WorkspaceActivity contains sampled facts for one retained workspace. An
// unobserved count is not a measured zero. It is never restored from catalog
// metadata or inferred from a shell's private buffers.
type WorkspaceActivity struct {
	Agents, Terminals, Unsaved ActivityCount
}

// ActivityCount is known only after the owning service was observed.
type ActivityCount struct {
	Value    int
	Observed bool
}

// RecentActivity samples the retained authorities. Call on the workspace owner
// to serialize access to the agent inventory and service lifetimes. Neither a
// database, process inspection nor a new service is needed for this read.
func (w *Workspace) RecentActivity() WorkspaceActivity {
	var out WorkspaceActivity
	if w.AgentMgr != nil {
		out.Agents.Observed = true
		for _, node := range w.AgentMgr.byID {
			// Live turns include provider and tool waits and prompts. A parked
			// recovery or a dormant schedule is not a running agent.
			if node.State.HasCancellableWork() {
				out.Agents.Value++
			}
		}
	}
	out.Terminals = recentTerminals(w.TermMgr)
	out.Unsaved = recentUnsavedDocuments(w.LiveDocuments)
	return out
}

func recentTerminals(m *TerminalManager) ActivityCount {
	if m == nil {
		return ActivityCount{Observed: true} // terminal service has not been created
	}
	if !m.mu.TryLock() {
		return ActivityCount{} // never wait for terminal operations on the actor
	}
	defer m.mu.Unlock()
	out := ActivityCount{Observed: true}
	for _, terminal := range m.items {
		// This is an open-PTY count, not a process or running-job count.
		if terminal.alive {
			out.Value++
		}
	}
	return out
}

func recentUnsavedDocuments(s *DocumentService) ActivityCount {
	if s == nil || !s.mu.TryLock() {
		return ActivityCount{} // save or recovery can hold this lock across I/O
	}
	defer s.mu.Unlock()
	if s.closed || s.stateErr != nil {
		return ActivityCount{}
	}
	out := ActivityCount{Observed: true}
	for _, doc := range s.docs {
		if doc.snapshot.Dirty {
			out.Value++
		}
	}
	return out
}
