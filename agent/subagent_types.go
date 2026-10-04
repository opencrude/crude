package agent

import (
	"context"
	"database/sql"
	"sync"
	"sync/atomic"
	"time"

	"crude/safety"
	ttypes "crude/types"
)

type AgentID string

type SubAgentStatus string

const (
	SubAgentCompleted    SubAgentStatus = "completed"
	SubAgentCrashed      SubAgentStatus = "crashed"
	SubAgentFailedQuota  SubAgentStatus = "failed_quota"
	SubAgentFailedError  SubAgentStatus = "failed_error"
	SubAgentFailedPolicy SubAgentStatus = "failed_policy"
	SubAgentCancelled    SubAgentStatus = "cancelled"
	SubAgentRunning      SubAgentStatus = "running"
)

const (
	CatResearcher = "researcher"
	CatCrafter    = "crafter"
	CatAeron      = "aeron"
)

type SubAgentSpec struct {
	ID               AgentID           `json:"id"`
	Name             string            `json:"name"`
	Category         string            `json:"category"`
	Description      string            `json:"description"`
	Instructions     string            `json:"instructions"`
	Model            string            `json:"model"`
	Provider         string            `json:"provider"`
	AccountID        string            `json:"account_id,omitempty"`
	ProviderURL      string            `json:"provider_url,omitempty"`
	APIKey           string            `json:"-"`
	APIStandard      string            `json:"api_standard,omitempty"`
	ThinkingEffort   string            `json:"thinking_effort,omitempty"`
	PolicyName       string            `json:"policy"`
	Policy           safety.Policy     `json:"-"`
	AllowedTools     []string          `json:"allowed_tools"`
	DeniedTools      []string          `json:"denied_tools"`
	SkillModes       map[string]string `json:"skill_modes,omitempty"`
	Depth            int               `json:"depth"`
	FileOwnership    []string          `json:"file_ownership"`
	ParentID         AgentID           `json:"parent_id"`
	ParentTurn       uint64            `json:"parent_turn"`
	ParentToolCallID string            `json:"parent_tool_call_id,omitempty"`
	HistoryMessageID string            `json:"history_message_id,omitempty"`

	ModelContextWindow int      `json:"model_context_window,omitempty"`
	ModelMaxOutput     int      `json:"model_max_output,omitempty"`
	ModelInputPrice    float64  `json:"model_input_price,omitempty"`
	ModelOutputPrice   float64  `json:"model_output_price,omitempty"`
	ModelCachePrice    float64  `json:"model_cache_price,omitempty"`
	ModelModalities    []string `json:"model_modalities"`
	// Sampling overrides; nil leaves the backend default in effect.
	ModelTemperature         *float64 `json:"model_temperature,omitempty"`
	ModelTopP                *float64 `json:"model_top_p,omitempty"`
	ContextCompactionPercent *int     `json:"context_compaction_percent,omitempty"`
}

type SubAgentResult struct {
	AgentID          AgentID        `json:"agent_id"`
	Name             string         `json:"name"`
	Category         string         `json:"category"`
	SessionID        string         `json:"session_id"`
	Status           SubAgentStatus `json:"status"`
	SummaryGist      string         `json:"summary_gist"`
	ReportID         string         `json:"report_id,omitempty"`
	ReportRevision   int            `json:"report_revision,omitempty"`
	LogPath          string         `json:"log_path"`
	FilesRead        []string       `json:"files_read"`
	FilesWritten     []string       `json:"files_written"`
	Errors           []string       `json:"errors"`
	PromptTokens     int            `json:"prompt_tokens"`
	CompTokens       int            `json:"completion_tokens"`
	CostUSD          float64        `json:"cost_usd"`
	DurationMs       int64          `json:"duration_ms"`
	HistoryMessageID string         `json:"history_message_id,omitempty"`
}

type SubAgentHandle struct {
	Spec              SubAgentSpec
	Agent             *Agent
	Cancel            context.CancelFunc
	ResultCh          chan SubAgentResult
	BlockCh           chan ttypes.BlockResponse
	StartedAt         time.Time
	SessionID         string
	RuntimeEpoch      uint64
	RunID             string
	RunVersion        uint64
	DB                *sql.DB
	StepCount         atomic.Int32
	Finished          bool
	mu                sync.Mutex
	totalPromptTokens int
	totalCompTokens   int
	totalCostUSD      float64
	errors            []string
	termReason        string
	reportID          string
	reportRevision    int
	reportDone        bool
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

func (h *SubAgentHandle) ReportDone() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.reportDone
}

func (h *SubAgentHandle) SetReportArtifact(id string, revision int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.reportID = id
	h.reportRevision = revision
	h.reportDone = id != "" && revision > 0
}

func (h *SubAgentHandle) ReportArtifact() (string, int, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.reportID, h.reportRevision, h.reportDone
}

func (h *SubAgentHandle) SetRunIdentity(id string, version uint64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.RunID, h.RunVersion = id, version
}

func (h *SubAgentHandle) RunIdentity() (string, uint64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.RunID, h.RunVersion
}

func (h *SubAgentHandle) SetRunVersion(version uint64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.RunVersion = version
}
