package agent

import (
	"context"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"crude/fileio"
	"crude/logger"
	"crude/provider"
	"crude/safety"
	"crude/tools"
)

type AgentEvent struct {
	Type    string
	Data    any
	AgentID string
}

const (
	maxSteps          = 10000
	loopDetectWindow  = 8
	loopDetectRepeats = 4
	contextSafetyPct  = 0.85
)

type StepRecord struct {
	Content      string
	ToolName     string
	ToolInput    string
	ToolResult   string
	FinishReason string
}

type Agent struct {
	Provider    *provider.Provider
	Model       string
	AgentID     AgentID
	Policy      safety.Policy
	Depth       int
	ParentID    AgentID
	MaxSteps      int
	FilteredTools []tools.ToolDef
	ToolFn        func(ctx context.Context, tool string, params map[string]any) (tools.ToolResult, error)
	logger      *logger.Logger
	turn        *logger.TurnLogger
	Emit        func(AgentEvent)
	Ctx         context.Context
	Yolo        bool
	AsyncMgr    *fileio.AsyncCommandManager
	SubAgentMgr *SubAgentManager
	client      *http.Client
	toolCallSeq int

	fileMu           sync.Map
	writeReportCalled bool
}

func (a *Agent) fileMutex(path string) *sync.Mutex {
	val, _ := a.fileMu.LoadOrStore(path, &sync.Mutex{})
	return val.(*sync.Mutex)
}

func (a *Agent) SetToolCallSeqStart(n int) { a.toolCallSeq = n }
func (a *Agent) ToolCallSeq() int           { return a.toolCallSeq }

func NewAgent(p *provider.Provider, model string, lg *logger.Logger) *Agent {
	return &Agent{
		Provider: p,
		Model:    model,
		ToolFn:   func(ctx context.Context, tool string, params map[string]any) (tools.ToolResult, error) { return tools.ToolResult{}, nil },
		logger:   lg,
		turn:     lg.Turn(),
		client: &http.Client{
			Timeout: 300 * time.Second,
			Transport: &http.Transport{
				DialContext: (&net.Dialer{
					Timeout:   30 * time.Second,
					KeepAlive: 30 * time.Second,
				}).DialContext,
				MaxIdleConns:        10,
				MaxIdleConnsPerHost: 10,
				IdleConnTimeout:     90 * time.Second,
				TLSHandshakeTimeout: 10 * time.Second,
			},
		},
	}
}

func IsToolError(content string) bool {
	prefixes := []string{"Error:", "Command failed:", "Search error:", "ArXiv search error:", "Fetch error:", "JavaScript error:", "[SAFETY BLOCK]"}
	for _, p := range prefixes {
		if strings.HasPrefix(strings.TrimSpace(content), p) {
			return true
		}
	}
	return false
}
