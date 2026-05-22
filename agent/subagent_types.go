package agent

import (
	"context"
	"database/sql"
	"sync"
	"sync/atomic"
	"time"

	"crude/safety"
	"crude/statemachine"
)

type AgentID string

type SubAgentStatus string

const (
	SubAgentCompleted     SubAgentStatus = "completed"
	SubAgentFailedTimeout SubAgentStatus = "failed_timeout"
	SubAgentFailedQuota   SubAgentStatus = "failed_quota"
	SubAgentFailedError   SubAgentStatus = "failed_error"
	SubAgentFailedPolicy  SubAgentStatus = "failed_policy"
	SubAgentCancelled     SubAgentStatus = "cancelled"
	SubAgentRunning       SubAgentStatus = "running"
)

const (
	CatMain       = "main"
	CatResearcher = "researcher"
	CatCrafter    = "crafter"
	CatAeron      = "aeron"
)

type SubAgentSpec struct {
	ID             AgentID       `json:"id"`
	Name           string        `json:"name"`
	Category       string        `json:"category"`
	Description    string        `json:"description"`
	Instructions   string        `json:"instructions"`
	Model          string        `json:"model"`
	Provider       string        `json:"provider"`
	PolicyName     string        `json:"policy"`
	Policy         safety.Policy `json:"-"`
	AllowedTools   []string      `json:"allowed_tools"`
	DeniedTools    []string      `json:"denied_tools"`
	MaxSteps       int           `json:"max_steps"`
	Depth          int           `json:"depth"`
	FileOwnership  []string      `json:"file_ownership"`
	ParentID       AgentID       `json:"parent_id"`
	TimeoutSeconds int           `json:"timeout_seconds"`
}

type SubAgentResult struct {
	AgentID      AgentID        `json:"agent_id"`
	Name         string         `json:"name"`
	Category     string         `json:"category"`
	SessionID    string         `json:"session_id"`
	Status       SubAgentStatus `json:"status"`
	SummaryGist  string         `json:"summary_gist"`
	ResultPath   string         `json:"result_path"`
	LogPath      string         `json:"log_path"`
	FilesRead    []string       `json:"files_read"`
	FilesWritten []string       `json:"files_written"`
	Errors       []string       `json:"errors"`
	PromptTokens int            `json:"prompt_tokens"`
	CompTokens   int            `json:"completion_tokens"`
	CostUSD      float64        `json:"cost_usd"`
	DurationMs   int64          `json:"duration_ms"`
}

type SubAgentHandle struct {
	Spec      SubAgentSpec
	Agent     *Agent
	Cancel    context.CancelFunc
	ResultCh  chan SubAgentResult
	BlockCh   chan string
	StartedAt time.Time
	SessionID string
	DB        *sql.DB
	SM        *statemachine.ConversationStateMachine
	StepCount         atomic.Int32
	mu                sync.Mutex
	totalPromptTokens int
	totalCompTokens   int
	totalCostUSD      float64
	errors            []string
	termReason        string
	reportGist        string
	reportPath        string
}

func (h *SubAgentHandle) AddUsage(promptT, compT int) {
	h.mu.Lock()
	h.totalPromptTokens += promptT
	h.totalCompTokens += compT
	h.mu.Unlock()
}

func (h *SubAgentHandle) AddCost(cost float64) {
	h.mu.Lock()
	h.totalCostUSD += cost
	h.mu.Unlock()
}

func (h *SubAgentHandle) AddError(msg string) {
	h.mu.Lock()
	h.errors = append(h.errors, msg)
	h.mu.Unlock()
}

func (h *SubAgentHandle) Usage() (promptTokens, compTokens int, costUSD float64, errs []string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.totalPromptTokens, h.totalCompTokens, h.totalCostUSD, h.errors
}

func (h *SubAgentHandle) SetTermReason(reason string) {
	h.mu.Lock()
	h.termReason = reason
	h.mu.Unlock()
}

func (h *SubAgentHandle) TermReason() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.termReason
}

func (h *SubAgentHandle) SetReport(gist, path string) {
	h.mu.Lock()
	h.reportGist = gist
	h.reportPath = path
	h.mu.Unlock()
}

func (h *SubAgentHandle) Report() (gist, path string, hasReport bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.reportGist, h.reportPath, h.reportGist != ""
}
