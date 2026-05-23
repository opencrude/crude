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

func (m *SubAgentManager) Cancel(id AgentID) (SubAgentResult, bool) {
	m.mu.Lock()
	handle, ok := m.active[id]
	if !ok {
		m.mu.Unlock()
		return SubAgentResult{}, false
	}
	delete(m.active, id)
	handle.Finished = true
	m.finished[id] = handle
	m.mu.Unlock()

	handle.Cancel()
	result := SubAgentResult{
		AgentID:   id,
		Name:      handle.Spec.Name,
		Category:  handle.Spec.Category,
		Status:    SubAgentCancelled,
		SessionID: handle.SessionID,
	}

	m.mu.Lock()
	m.completed = append(m.completed, result)
	m.completedMap[id] = result
	m.mu.Unlock()

	return result, true
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
			HandleID:      string(id),
			Name:          h.Spec.Name,
			Category:      h.Spec.Category,
			Status:        string(SubAgentRunning),
			ElapsedSeconds: int(time.Since(h.StartedAt).Seconds()),
			StepsUsed:     int(h.StepCount.Load()),
			MaxSteps:      h.Spec.MaxSteps,
			Model:         h.Spec.Model,
			FilesClaimed:  h.Spec.FileOwnership,
		})
	}
	if includeCompleted {
		for id, h := range m.finished {
			status := "unknown"
			resultPath := ""
			summaryGist := ""
			if r, ok := m.completedMap[id]; ok {
				status = string(r.Status)
				resultPath = r.ResultPath
				summaryGist = r.SummaryGist
			}
			entries = append(entries, SubAgentStatusEntry{
				HandleID:      string(id),
				Name:          h.Spec.Name,
				Category:      h.Spec.Category,
				Status:        status,
				ElapsedSeconds: int(time.Since(h.StartedAt).Seconds()),
				StepsUsed:     int(h.StepCount.Load()),
				MaxSteps:      h.Spec.MaxSteps,
				Model:         h.Spec.Model,
				FilesClaimed:  h.Spec.FileOwnership,
				ResultPath:    resultPath,
				SummaryGist:   summaryGist,
			})
		}
	}
	return entries
}



type SubAgentStatusEntry struct {
	HandleID       string   `json:"handle_id"`
	Name           string   `json:"name"`
	Category       string   `json:"category"`
	Status         string   `json:"status"`
	ElapsedSeconds int      `json:"elapsed_seconds"`
	StepsUsed      int      `json:"steps_used"`
	MaxSteps       int      `json:"max_steps"`
	Model          string   `json:"model"`
	FilesClaimed   []string `json:"files_claimed"`
	ResultPath     string   `json:"result_path,omitempty"`
	SummaryGist    string   `json:"summary_gist,omitempty"`
}
