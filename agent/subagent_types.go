package agent

import (
	"context"
	"database/sql"
	"time"

	"crude/safety"
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
	StepCount int
}
