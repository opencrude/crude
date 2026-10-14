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
	"crude/types/control"
	"crude/types/reasoning"
)

type AgentEvent struct {
	Type     string
	Data     any
	AgentID  string
	Identity ttypes.RuntimeEventIdentity
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
	Provider *provider.Provider
	Model    string
	// MediaReadPolicy is the exact resolved role's immutable input-modality
	// snapshot for this Agent instance. Native reads and dynamic tool results
	// must use it instead of another role's settings or the mutable model catalog.
	// Nil remains available for standalone agents that inherit catalog policy.
	MediaReadPolicy        *tools.MediaReadPolicy
	ControlTarget          *control.Target
	ReasoningContentKind   reasoning.Kind
	condensedSamples       int
	rawSamples             int
	sawRawReasoning        bool
	CompactionProvider     *provider.Provider
	CompactionTargetModel  string
	CompactionTargetWindow int
	CompactionTargetTokens int
	CompactionMaxOutput    int
	CompactionAgentProfile string
	AgentID                AgentID
	// RunID is the immutable durable owner of this instance's effects. Never
	// discover a replacement run from current inventory during tool execution.
	RunID                     string
	TurnGeneration            uint64
	Policy                    safety.Policy
	CommandPolicy             safety.CommandPolicy
	PermissionReviewer        PermissionReviewer
	Depth                     int
	ParentID                  AgentID
	WorkDir                   string
	GuidancePaths             []string
	MaxRetries                int
	MaxOutputTokens           int
	ContextCompactionPercent  int
	CompactionRetentionTokens int
	ContextArchive            []provider.ChatMessage
	FrozenPrefix              []provider.ChatMessage
	lastActiveContext         []provider.ChatMessage
	FilteredTools             []tools.ToolDef
	// PrepareStep snapshots live tools and runtime notices between requests,
	// never while a parallel tool batch is using the previous snapshot.
	PrepareStep func() ([]tools.ToolDef, []provider.ChatMessage)
	// RequestBoundary lets the owner apply a pending connection change (account,
	// model, thinking effort) strictly between requests, before the step reads
	// the provider. It reports whether the connection changed.
	RequestBoundary func() bool
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
	Checkpoint             func(archive, active []provider.ChatMessage) error
	// CheckpointTools stores unfinished wrapper evidence separately from immutable
	// accepted messages. A later completed result must not rewrite that evidence
	// as though it were an already-committed protocol message.
	CheckpointTools      func(archive, active, inFlight []provider.ChatMessage) error
	ContinuationContext  func(context.Context) (rootprompt.SessionContinuityData, error)
	LatestHumanTurnState func(context.Context) (string, bool, error)
	GuidanceCacheLoad    func(string) (string, bool)
	GuidanceCacheStore   func(string, string)
	Ctx                  context.Context
	Yolo                 bool
	// YoloMode supplies the owning session's live policy to roots and children.
	// Yolo remains the fallback for standalone agents.
	YoloMode    func() bool
	AsyncMgr    *fileio.AsyncCommandManager
	SubAgentMgr *SubAgentManager
	// testHTTPClient, when set, is injected into the provider transport for
	// tests. Production agents use the provider's own tuned transport.
	testHTTPClient       *http.Client
	compactionRetryDelay func(int) time.Duration
	contextBaseline      contextMeter

	fileMu              sync.Map
	reportPublished     bool
	preserveReportState bool
	guidance            *projectGuidance

	runFailure      error
	runFailureKind  RecoveryFailureKind
	runRecoveryKind rootprompt.RecoveryKind
	lastArchive     []provider.ChatMessage
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

func (a *Agent) ArchiveContext() []provider.ChatMessage {
	if a == nil {
		return nil
	}
	return append([]provider.ChatMessage(nil), a.lastArchive...)
}

func (a *Agent) RunFailure() error {
	if a == nil {
		return nil
	}
	return a.runFailure
}

func (a *Agent) RunRecoveryKind() rootprompt.RecoveryKind {
	if a == nil {
		return ""
	}
	return a.runRecoveryKind
}

func (a *Agent) RunFailureKind() RecoveryFailureKind {
	if a == nil {
		return ""
	}
	return a.runFailureKind
}

func (a *Agent) setRunFailure(err error, kind rootprompt.RecoveryKind, failureKind ...RecoveryFailureKind) {
	if a == nil || err == nil {
		return
	}
	a.runFailure = err
	if len(failureKind) > 0 && failureKind[0] != "" {
		a.runFailureKind = failureKind[0]
	}
	if kind != "" {
		a.runRecoveryKind = kind
	}
}

func (a *Agent) RestorePublishedReport() {
	if a != nil {
		a.reportPublished = true
	}
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
		Provider:                  p,
		Model:                     model,
		ContextCompactionPercent:  ttypes.DefaultContextCompactionPercent,
		CompactionRetentionTokens: ttypes.DefaultContextRetentionTokens,
		CommandPolicy:             safety.DefaultCommandPolicy(),
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
