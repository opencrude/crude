package agent

import (
	"fmt"
	"sync"
	"time"
)

type SubAgentManager struct {
	mu            sync.Mutex
	active        map[AgentID]*SubAgentHandle
	finished      map[AgentID]*SubAgentHandle
	completed     []SubAgentResult
	completedMap  map[AgentID]SubAgentResult
	maxConcurrent int
}

func NewSubAgentManager(maxConcurrent int) *SubAgentManager {
	if maxConcurrent <= 0 {
		maxConcurrent = 4
	}
	return &SubAgentManager{
		active:        make(map[AgentID]*SubAgentHandle),
		finished:      make(map[AgentID]*SubAgentHandle),
		completed:     make([]SubAgentResult, 0),
		completedMap:  make(map[AgentID]SubAgentResult),
		maxConcurrent: maxConcurrent,
	}
}

func (m *SubAgentManager) Validate(spec SubAgentSpec) error {
	if spec.Name == "" {
		return fmt.Errorf("sub-agent name is required")
	}
	if spec.Description == "" {
		return fmt.Errorf("sub-agent description is required")
	}
	if spec.Category != CatResearcher && spec.Category != CatCrafter && spec.Category != CatAeron {
		return fmt.Errorf("invalid category %q: must be researcher, crafter, or aeron", spec.Category)
	}
	return nil
}

func (m *SubAgentManager) Register(handle *SubAgentHandle) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.active) >= m.maxConcurrent {
		return fmt.Errorf("maximum concurrent sub-agents (%d) reached", m.maxConcurrent)
	}
	m.active[handle.Spec.ID] = handle
	return nil
}

func (m *SubAgentManager) MarkFinished(id AgentID, result SubAgentResult) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if handle, ok := m.active[id]; ok {
		handle.Finished = true
		m.finished[id] = handle
		delete(m.active, id)
	}
	if _, exists := m.completedMap[id]; !exists {
		m.completed = append(m.completed, result)
	} else {
		for i := range m.completed {
			if m.completed[i].AgentID == id {
				m.completed[i] = result
				break
			}
		}
	}
	m.completedMap[id] = result
}

// MarkInterrupted releases a volatile handle without inventing a terminal
// result. Its durable recovery task remains owned by sessiondb.
func (m *SubAgentManager) MarkInterrupted(id AgentID) {
	m.mu.Lock()
	delete(m.active, id)
	m.mu.Unlock()
}

// HydrateCompleted restores a durable result after process restart. Hydrated
// handles are intentionally non-runnable; they exist so poll/list status keeps
// the same semantics across resume.
func (m *SubAgentManager) HydrateCompleted(result SubAgentResult) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id := result.AgentID
	if _, exists := m.completedMap[id]; exists {
		return
	}
	startedAt := time.Now()
	if result.DurationMs > 0 {
		startedAt = startedAt.Add(-time.Duration(result.DurationMs) * time.Millisecond)
	}
	handle := &SubAgentHandle{
		Spec: SubAgentSpec{
			ID: id, Name: result.Name, Category: result.Category,
		},
		SessionID: result.SessionID,
		StartedAt: startedAt,
		Finished:  true,
	}
	m.finished[id] = handle
	m.completed = append(m.completed, result)
	m.completedMap[id] = result
}

func (m *SubAgentManager) GetCompleted(id AgentID) (SubAgentResult, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.completedMap[id]
	return r, ok
}

func (m *SubAgentManager) GetFinished(id AgentID) (*SubAgentHandle, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	h, ok := m.finished[id]
	return h, ok
}

func (m *SubAgentManager) GetActive(id AgentID) (*SubAgentHandle, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	h, ok := m.active[id]
	return h, ok
}

func (m *SubAgentManager) ActiveCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.active)
}

func (m *SubAgentManager) ActiveHandles() []*SubAgentHandle {
	m.mu.Lock()
	defer m.mu.Unlock()
	handles := make([]*SubAgentHandle, 0, len(m.active))
	for _, h := range m.active {
		handles = append(handles, h)
	}
	return handles
}

func (m *SubAgentManager) CompletedResults() []SubAgentResult {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]SubAgentResult, len(m.completed))
	copy(cp, m.completed)
	return cp
}

func (m *SubAgentManager) ListStatus(includeCompleted bool) []SubAgentStatusEntry {
	m.mu.Lock()
	defer m.mu.Unlock()
	var entries []SubAgentStatusEntry
	for id, h := range m.active {
		entries = append(entries, SubAgentStatusEntry{
			AgentID:        id,
			Name:           h.Spec.Name,
			Category:       h.Spec.Category,
			Status:         string(SubAgentRunning),
			ElapsedSeconds: int(time.Since(h.StartedAt).Seconds()),
			StepsUsed:      int(h.StepCount.Load()),
			Model:          h.Spec.Model,
			FilesClaimed:   h.Spec.FileOwnership,
		})
	}
	if includeCompleted {
		for id, h := range m.finished {
			status := "unknown"
			summaryGist := ""
			reportID := ""
			reportRevision := 0
			if r, ok := m.completedMap[id]; ok {
				status = string(r.Status)
				summaryGist = r.SummaryGist
				reportID = r.ReportID
				reportRevision = r.ReportRevision
			}
			entries = append(entries, SubAgentStatusEntry{
				AgentID:        id,
				Name:           h.Spec.Name,
				Category:       h.Spec.Category,
				Status:         status,
				ElapsedSeconds: int(time.Since(h.StartedAt).Seconds()),
				StepsUsed:      int(h.StepCount.Load()),
				Model:          h.Spec.Model,
				FilesClaimed:   h.Spec.FileOwnership,
				ReportID:       reportID,
				ReportRevision: reportRevision,
				SummaryGist:    summaryGist,
			})
		}
	}
	return entries
}

type SubAgentStatusEntry struct {
	AgentID        AgentID  `json:"agent_id"`
	Name           string   `json:"name"`
	Category       string   `json:"category"`
	Status         string   `json:"status"`
	ElapsedSeconds int      `json:"elapsed_seconds"`
	StepsUsed      int      `json:"steps_used"`
	Model          string   `json:"model"`
	FilesClaimed   []string `json:"files_claimed"`
	ReportID       string   `json:"report_id,omitempty"`
	ReportRevision int      `json:"report_revision,omitempty"`
	SummaryGist    string   `json:"summary_gist,omitempty"`
}
