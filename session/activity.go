package session

import (
	"crude/fileio"
	"crude/types"
)

// Activity is a point-in-time count of work that must not be abandoned by a
// core lifetime decision. It never blocks on, signals or advances that work.
type Activity struct {
	Turns         int
	Children      int
	AsyncCommands int
	Compaction    int
	MemoryJobs    int
}

// Any reports whether any counted work is active.
func (a Activity) Any() bool {
	return a.Turns+a.Children+a.AsyncCommands+a.Compaction+a.MemoryJobs > 0
}

// Activity inspects the session's running turns, children, background
// commands, compaction and memory jobs.
func (s *Session) Activity() Activity {
	if s == nil {
		return Activity{}
	}
	out := Activity{Turns: int(s.activeTurns.Load()) + int(s.historyFinalizers.Load())}
	if runner := s.SubAgentRunner(); runner != nil {
		out.Children = int(runner.running.Load())
	}
	if s.asyncMgr != nil {
		for _, info := range s.asyncMgr.List() {
			if info.Status == fileio.AsyncRunning {
				out.AsyncCommands++
			}
		}
	}
	if s.compactionActive("") {
		out.Compaction = 1
	}
	if s.memoryJobs.extracting.Load() {
		out.MemoryJobs++
	}
	if s.memoryJobs.maintaining.Load() {
		out.MemoryJobs++
	}
	// Orchestrator turns not yet reflected in the counters still count as one.
	if out.Turns == 0 && s.IsTurnActive() && !out.Any() {
		out.Turns = 1
	}
	return out
}

// AutonomousWork counts retained schedules and goals that can own future work.
// It reads only session memory and does not advance either owner.
func (s *Session) AutonomousWork() (goals, loops int) {
	if s == nil {
		return 0, 0
	}
	s.goalMu.Lock()
	if s.goal.present && s.goal.record.Goal.Status == types.GoalActive && !s.readyGoalHandoverLocked() {
		goals = 1
	}
	s.goalMu.Unlock()
	s.loopMu.Lock()
	for _, loop := range s.loops {
		if !loop.Paused {
			loops++
		}
	}
	s.loopMu.Unlock()
	return goals, loops
}
