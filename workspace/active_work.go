package workspace

// ActiveWork counts work that a core lifetime decision must not abandon.
type ActiveWork struct {
	Turns         int `json:"turns"`
	Children      int `json:"children"`
	AsyncCommands int `json:"async_commands"`
	Compaction    int `json:"compaction"`
	MemoryJobs    int `json:"memory_jobs"`
	Goals         int `json:"goals"`
	Loops         int `json:"loops"`
}

// Any reports whether any counted work is active.
func (a ActiveWork) Any() bool {
	return a.Turns+a.Children+a.AsyncCommands+a.Compaction+a.MemoryJobs+a.Goals+a.Loops > 0
}

// ActiveWork inspects every open session. Callers serialize it with workspace
// lifecycle changes (the core actor does).
func (w *Workspace) ActiveWork() ActiveWork {
	var out ActiveWork
	if w == nil {
		return out
	}
	for _, frontend := range w.Sessions {
		if frontend == nil || frontend.backend == nil {
			continue
		}
		a := frontend.backend.Activity()
		out.Turns += a.Turns
		out.Children += a.Children
		out.AsyncCommands += a.AsyncCommands
		out.Compaction += a.Compaction
		out.MemoryJobs += a.MemoryJobs
		goals, loops := frontend.backend.AutonomousWork()
		out.Goals += goals
		out.Loops += loops
	}
	if w.AgentMgr != nil && out.Turns == 0 && w.AgentMgr.AnyActive() {
		out.Turns = 1
	}
	return out
}
