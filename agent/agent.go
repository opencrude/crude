package agent

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"

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
	loopDetectWindow     = 8
	loopDetectRepeats    = 4
	contextSafetyPct     = 0.85
	maxAutoContinuations = 4
)

type StepRecord struct {
	Content      string
	ToolName     string
	ToolInput    string
	ToolResult   string
	FinishReason string
}

type Agent struct {
	Provider      *provider.Provider
	Model         string
	AgentID       AgentID
	Policy        safety.Policy
	Depth         int
	ParentID      AgentID
	MaxRetries    int
	FilteredTools []tools.ToolDef
	ToolFn        func(ctx context.Context, tool string, params map[string]any) (tools.ToolResult, error)
	logger        *logger.Logger
	turn          *logger.TurnLogger
	Emit          func(AgentEvent)
	Ctx           context.Context
	Yolo          bool
	// YoloMode supplies the owning session's live policy. Yolo remains the
	// fallback for standalone agents and always-YOLO sub-agents.
	YoloMode    func() bool
	AsyncMgr    *fileio.AsyncCommandManager
	SubAgentMgr *SubAgentManager
	// testHTTPClient, when set, is injected into the provider transport for
	// tests. Production agents use the provider's own tuned transport.
	testHTTPClient *http.Client
	toolCallSeq    int

	fileMu            sync.Map
	writeReportCalled bool
	autoHandleMu      sync.Mutex
	autoHandleCounter int
}

func (a *Agent) yoloEnabled() bool {
	if a != nil && a.YoloMode != nil {
		return a.YoloMode()
	}
	return a != nil && a.Yolo
}

func (a *Agent) providerDisplayName() string {
	if a == nil || a.Provider == nil {
		return "provider"
	}
	if name := strings.TrimSpace(a.Provider.Name); name != "" {
		return name
	}
	if preset := provider.GetPreset(a.Provider.ID); preset != nil && preset.Name != "" {
		return preset.Name
	}
	if id := strings.TrimSpace(a.Provider.ID); id != "" {
		return id
	}
	return "provider"
}

func (a *Agent) NextAutoHandle() string {
	a.autoHandleMu.Lock()
	defer a.autoHandleMu.Unlock()
	a.autoHandleCounter++
	return fmt.Sprintf("auto-%d", a.autoHandleCounter)
}

func (a *Agent) fileMutex(path string) *sync.Mutex {
	val, _ := a.fileMu.LoadOrStore(path, &sync.Mutex{})
	return val.(*sync.Mutex)
}

func (a *Agent) SetToolCallSeqStart(n int) { a.toolCallSeq = n }
func (a *Agent) ToolCallSeq() int          { return a.toolCallSeq }
func (a *Agent) SetHTTPClient(client *http.Client) {
	if client != nil {
		a.testHTTPClient = client
	}
}

func NewAgent(p *provider.Provider, model string, lg *logger.Logger) *Agent {
	var turn *logger.TurnLogger
	if lg != nil {
		turn = lg.Turn()
	}
	return &Agent{
		Provider: p,
		Model:    model,
		ToolFn: func(ctx context.Context, tool string, params map[string]any) (tools.ToolResult, error) {
			return tools.ToolResult{}, nil
		},
		logger: lg,
		turn:   turn,
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
