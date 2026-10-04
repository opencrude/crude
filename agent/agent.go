package agent

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"

	"crude/fileio"
	"crude/logger"
	rootprompt "crude/prompt"
	"crude/provider"
	"crude/safety"
	"crude/tools"
	ttypes "crude/types"
	"crude/types/reasoning"
)

type AgentEvent struct {
	Type    string
	Data    any
	AgentID string
}

const (
	loopDetectWindow        = 8
	loopDetectRepeats       = 4
	maxAutoContinuations    = 4
	maxFinalReportReminders = 1
)

type StepRecord struct {
	Content      string
	ToolName     string
	ToolInput    string
	ToolResult   string
	FinishReason string
}

type Agent struct {
	Provider                 *provider.Provider
	Model                    string
	ReasoningContentKind     reasoning.Kind
	condensedSamples         int
	rawSamples               int
	sawRawReasoning          bool
	CompactionProvider       *provider.Provider
	CompactionTargetModel    string
	CompactionTargetWindow   int
	CompactionTargetTokens   int
	CompactionMaxOutput      int
	CompactionAgentProfile   string
	AgentID                  AgentID
	TurnGeneration           uint64
	Policy                   safety.Policy
	CommandPolicy            safety.CommandPolicy
	PermissionReviewer       PermissionReviewer
	Depth                    int
	ParentID                 AgentID
	WorkDir                  string
	GuidancePaths            []string
	MaxRetries               int
	MaxOutputTokens          int
	ContextCompactionPercent int
	ContextArchive           []provider.ChatMessage
	FrozenPrefix             []provider.ChatMessage
	lastActiveContext        []provider.ChatMessage
	FilteredTools            []tools.ToolDef
	// PrepareStep snapshots live tools and runtime notices between requests,
	// never while a parallel tool batch is using the previous snapshot.
	PrepareStep func() ([]tools.ToolDef, []provider.ChatMessage)
	// FlushRuntimeMessages fences the durable inbox before an empty lane can
	// end a turn. Async delivery alone cannot prove there is no pending input.
	FlushRuntimeMessages func(context.Context) error
	// Runtime notices are admitted at model-request boundaries; human steering
	// uses the separately fenced runtime-message lane.
	PrepareRuntimeNotices  func() []provider.ChatMessage
	CommitRuntimeNotices   func([]provider.ChatMessage) error
	RuntimeNoticesAccepted func()
	ToolFn                 func(ctx context.Context, tool string, params map[string]any) (tools.ToolResult, error)
	ToolMetadata           func(string) (tools.ToolMetadata, bool)
	ToolResultAccepted     func(string, string, []provider.ChatMessage)
	logger                 *logger.Logger
	turn                   *logger.TurnLogger
	Emit                   func(AgentEvent)
	PrepareCompaction      func(CompactionSource) error
	CommitCompaction       func(CompactionCommit) error
	ContinuationContext    func(context.Context) (rootprompt.SessionContinuityData, error)
	LatestHumanTurnState   func(context.Context) (string, bool, error)
	GuidanceCacheLoad      func(string) (string, bool)
	GuidanceCacheStore     func(string, string)
	Ctx                    context.Context
	Yolo                   bool
	// YoloMode supplies the owning session's live policy to roots and children.
	// Yolo remains the fallback for standalone agents.
	YoloMode    func() bool
	AsyncMgr    *fileio.AsyncCommandManager
	SubAgentMgr *SubAgentManager
	// testHTTPClient, when set, is injected into the provider transport for
	// tests. Production agents use the provider's own tuned transport.
	testHTTPClient       *http.Client
	toolCallSeq          int
	compactionRetryDelay func(int) time.Duration
	contextBaseline      contextMeter

	fileMu          sync.Map
	reportPublished bool
	guidance        *projectGuidance
}

// SeedContextUsage carries the latest provider-reported request baseline across
// Session's per-turn Agent instances. requestMessages is the prior assistant
// boundary in the new request's canonical message slice.
func (a *Agent) SeedContextUsage(inputTokens, completionTokens, requestMessages int) {
	if a == nil {
		return
	}
	a.contextBaseline.observe(inputTokens, completionTokens, requestMessages)
}

func (a *Agent) ActiveContext() []provider.ChatMessage {
	if a == nil {
		return nil
	}
	return append([]provider.ChatMessage(nil), a.lastActiveContext...)
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
		Provider:                 p,
		Model:                    model,
		ContextCompactionPercent: ttypes.DefaultContextCompactionPercent,
		CommandPolicy:            safety.DefaultCommandPolicy(),
		ToolFn: func(ctx context.Context, tool string, params map[string]any) (tools.ToolResult, error) {
			return tools.ToolResult{}, nil
		},
		logger: lg,
		turn:   turn,
	}
}

func IsToolError(content string) bool {
	prefixes := []string{"Error:", "Command failed:", "Search error:", "ArXiv search error:", "Fetch error:", "[SAFETY BLOCK]"}
	for _, p := range prefixes {
		if strings.HasPrefix(strings.TrimSpace(content), p) {
			return true
		}
	}
	return false
}
