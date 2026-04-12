package session

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"crude/agent"
	"crude/dag"
	"crude/embed"
	"crude/fileio"
	"crude/logger"
	"crude/provider"
	"crude/rag"
	"crude/safety"
	"crude/statemachine"
	"crude/tools"
	"crude/tui"
)

type Config struct {
	ActiveProvider string
	Providers      map[string]ProviderConfig
}

type ProviderConfig struct {
	APIKey      string `json:"api_key,omitempty"`
	Model       string `json:"model,omitempty"`
	EmbedModel  string `json:"embed_model,omitempty"`
	ProviderURL string `json:"provider_url,omitempty"`
}

type Session struct {
	cfg *Config
	mu  sync.Mutex

	provider  *provider.Provider
	exec      fileio.Executer
	editor    *fileio.Editor
	logger    *logger.Logger
	permStore *safety.PermissionStore
	ragEngine *rag.Engine
	sm        *statemachine.ConversationStateMachine

	msgs          []agent.APIMessage
	msgsMu        sync.Mutex
	pendingBlock  bool
	blockMu       sync.Mutex
	lastBlockResp string
	blockRespMu   sync.Mutex
	msgCounter    int
	msgCounterMu  sync.Mutex

	sessionID    string
	sessionStart time.Time
	ragCacheDir  string
	knowledgeDir string
}

func New(cfg *Config, homeDir string) (*Session, error) {
	activeCfg := cfg.GetActiveProviderConfig()
	p := provider.FromPreset(cfg.ActiveProvider, activeCfg.APIKey, activeCfg.ProviderURL)
	exec := fileio.NewLocalExec()

	sessionDir := filepath.Join(homeDir, ".crude", "sessions")
	sessionID := fmt.Sprintf("session-%s", time.Now().Format("20060102-150405"))
	lg, err := logger.New(logger.Config{
		SessionDir: sessionDir,
		SessionID:  sessionID,
		Console:    false,
	})
	if err != nil {
		return nil, fmt.Errorf("logger: %w", err)
	}
	lg.Info("session.start", "version", "1.0")
	lg.Info("session.config", "provider", cfg.ActiveProvider, "model", activeCfg.Model, "embed_model", activeCfg.EmbedModel)

	ragCacheDir := filepath.Join(homeDir, ".crude", "rag")
	embedClient := embed.NewClient(activeCfg.APIKey, activeCfg.EmbedModel, p.BaseURL)
	ragEngine := rag.New(embedClient, ragCacheDir)

	knowledgeDir := filepath.Join(homeDir, ".crude", "knowledge")
	if err := loadKnowledgeBase(ragEngine, knowledgeDir, lg); err != nil {
		lg.Info("agent.reflection", "content", fmt.Sprintf("knowledge base load error: %v", err))
	}

	permStore := safety.NewPermissionStore()

	fileioSessionID := fileio.GenerateSessionID()
	var editor *fileio.Editor
	if ed, edErr := fileio.NewEditorWithLogger(fileioSessionID, lg); edErr != nil {
		lg.Info("agent.reflection", "content", fmt.Sprintf("editor init error: %v", edErr))
	} else {
		editor = ed
	}

	sm := statemachine.NewConversationStateMachine(lg)

	return &Session{
		cfg:          cfg,
		provider:     p,
		exec:         exec,
		editor:       editor,
		logger:       lg,
		permStore:    permStore,
		ragEngine:    ragEngine,
		sm:           sm,
		sessionID:    sessionID,
		sessionStart: time.Now(),
		ragCacheDir:  ragCacheDir,
		knowledgeDir: knowledgeDir,
	}, nil
}

func (s *Session) Close() {
	if s.editor != nil {
		s.editor.Close()
	}
	s.logger.Info("session.end", "duration_ms", 0)
	s.logger.Close()
}

func (c *Config) GetActiveProviderConfig() ProviderConfig {
	if c.Providers == nil {
		return ProviderConfig{}
	}
	if cfg, ok := c.Providers[c.ActiveProvider]; ok {
		return cfg
	}
	return ProviderConfig{}
}

func (c *Config) SetActiveProviderConfig(providerID string, cfg ProviderConfig) {
	if c.Providers == nil {
		c.Providers = make(map[string]ProviderConfig)
	}
	c.Providers[providerID] = cfg
}

func (c *Config) EffectiveProviderID() string {
	if c.ActiveProvider != "" {
		return c.ActiveProvider
	}
	return "openrouter"
}

func (s *Session) LogPath() string {
	return s.logger.Path()
}

func (s *Session) Editor() *fileio.Editor { return s.editor }

func (s *Session) Config() *Config { return s.cfg }

func (s *Session) newAgent() *agent.Agent {
	activeCfg := s.cfg.GetActiveProviderConfig()
	a := agent.NewAgent(s.provider, activeCfg.Model, s.logger)
	a.ToolFn = func(tool string, params map[string]any) (string, error) {
		tc := tools.ToolCall{
			ID:   fmt.Sprintf("dag-%s", tool),
			Type: "function",
			Function: tools.FunctionCall{
				Name:      tool,
				Arguments: mustXMLArgs(params),
			},
		}
		result := tools.Execute(tc, s.exec, s.editor)
		if strings.HasPrefix(result, "Error:") {
			return result, fmt.Errorf("%s", result)
		}
		return result, nil
	}
	return a
}

func (s *Session) HandleUndo(cmd string) tui.UndoResultMsg {
	if s.editor == nil {
		return tui.UndoResultMsg{Error: "file editor not available"}
	}
	switch {
	case cmd == "/undo":
		if err := s.editor.UndoLast(); err != nil {
			return tui.UndoResultMsg{Error: err.Error()}
		}
		journalLog := s.editor.Log()
		desc := "Undo complete. No more operations to undo."
		if len(journalLog) > 0 {
			desc = fmt.Sprintf("Undo complete. %d operation(s) remaining in journal.", len(journalLog))
		}
		return tui.UndoResultMsg{Desc: desc}
	case strings.HasPrefix(cmd, "/undo-n"):
		n := 1
		parts := strings.Fields(cmd)
		if len(parts) > 1 {
			fmt.Sscanf(parts[1], "%d", &n)
		}
		if n <= 0 {
			n = 1
		}
		if err := s.editor.UndoN(n); err != nil {
			return tui.UndoResultMsg{Error: err.Error()}
		}
		return tui.UndoResultMsg{Desc: fmt.Sprintf("Undid last %d file operation(s).", n)}
	case cmd == "/undo-all":
		if err := s.editor.UndoSession(); err != nil {
			return tui.UndoResultMsg{Error: err.Error()}
		}
		return tui.UndoResultMsg{Desc: "All file operations this session have been reverted."}
	case cmd == "/changes":
		files := s.editor.ChangedFiles()
		if len(files) == 0 {
			return tui.UndoResultMsg{Desc: "No file changes this session."}
		}
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("%d file(s) changed this session:\n", len(files)))
		for _, f := range files {
			rel, relErr := filepath.Rel(".", f)
			if relErr != nil {
				rel = f
			}
			sb.WriteString("  " + rel + "\n")
		}
		sb.WriteString("Use /undo to revert the last operation, /undo-all to revert everything.")
		return tui.UndoResultMsg{Desc: sb.String()}
	}
	return tui.UndoResultMsg{}
}

func (s *Session) HandleSettingsSave(tuiSettings tui.Settings) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	newCfg := settingsToConfig(tuiSettings, s.cfg)
	activeCfg := newCfg.GetActiveProviderConfig()
	if activeCfg.APIKey == "" {
		return fmt.Errorf("API key cannot be empty — settings not saved")
	}

	s.cfg = newCfg
	s.provider = provider.FromPreset(s.cfg.ActiveProvider, activeCfg.APIKey, activeCfg.ProviderURL)
	embedClient := embed.NewClient(activeCfg.APIKey, activeCfg.EmbedModel, s.provider.BaseURL)
	s.ragEngine = rag.New(embedClient, s.ragCacheDir)
	if err := loadKnowledgeBase(s.ragEngine, s.knowledgeDir, s.logger); err != nil {
		s.logger.Info("agent.reflection", "content", fmt.Sprintf("knowledge base reload error: %v", err))
	}
	return nil
}

func (s *Session) HandleFetchModels(providerID, baseURL, apiKey string) ([]tui.ModelEntry, error) {
	p := provider.FromPreset(providerID, apiKey, baseURL)
	models, err := provider.FetchModels(p)
	if err != nil {
		return nil, err
	}
	entries := make([]tui.ModelEntry, len(models))
	for i, m := range models {
		entries[i] = tui.ModelEntry{ID: m.ID, Name: m.Name}
	}
	return entries, nil
}

func (s *Session) Run(tuiInst *tui.TUI, blockResolveCh chan string) {
	defer func() {
		if r := recover(); r != nil {
			s.logger.Error("agent.panic", "recovered", fmt.Sprintf("%v", r))
			s.logger.Error("session.error", "error", fmt.Sprintf("panic: %v", r))
			tuiInst.SendAgentEvent("error", fmt.Sprintf("Internal error: %v", r))
		}
	}()

	for userText := range tuiInst.InputCh {
		activeCfg := s.cfg.GetActiveProviderConfig()
		if activeCfg.APIKey == "" {
			tuiInst.SendAgentEvent("error", "No API key configured. Press Ctrl+S to open settings.")
			continue
		}

		s.blockMu.Lock()
		isBlocked := s.pendingBlock
		s.pendingBlock = false
		s.blockMu.Unlock()
		if isBlocked {
			s.blockRespMu.Lock()
			s.lastBlockResp = userText
			s.blockRespMu.Unlock()
			blockResolveCh <- userText
			continue
		}

		s.msgCounterMu.Lock()
		s.msgCounter++
		currentMsgID := fmt.Sprintf("msg-%d", s.msgCounter)
		s.msgCounterMu.Unlock()

		s.msgsMu.Lock()
		s.msgs = append(s.msgs, agent.APIMessage{Role: "user", Content: userText})
		msgsCopy := make([]agent.APIMessage, len(s.msgs))
		copy(msgsCopy, s.msgs)
		s.msgsMu.Unlock()
		s.logger.Info("conv.user_msg", "content", userText, "msg_id", currentMsgID)

		s.logger.Info("agent.loop_start", "content", userText, "iteration", 0)
		s.sm.CreateGoal(s.sessionID, "respond", userText, statemachine.PriorityNormal, false)
		s.ragEngine.ResetObservations()

		if s.editor != nil {
			s.editor = s.editor.WithMessage(currentMsgID)
		}

		ag := s.newAgent()
		var finalContent string
		prompt := agent.SystemPrompt

		ag.Run(msgsCopy, prompt, func(evt agent.AgentEvent) {
			if evt.Type == "blocking" {
				data, _ := evt.Data.(map[string]string)
				if data != nil {
					op := dag.CRUDEOp(data["operation"])
					tool := data["tool"]
					target := data["target"]
					if s.permStore.IsAlwaysAllowed(op, tool, target) {
						blockResolveCh <- "allow"
						return
					}
				}
				s.blockMu.Lock()
				s.pendingBlock = true
				s.blockMu.Unlock()
				tuiInst.SendAgentEvent(evt.Type, evt.Data)
				return
			}
			if evt.Type == "confirmation_always" {
				if data, ok := evt.Data.(map[string]string); ok {
					op := dag.CRUDEOp(data["operation"])
					tool := data["tool"]
					target := data["target"]
					s.permStore.GrantAlways(op, tool, target)
				}
				return
			}
			tuiInst.SendAgentEvent(evt.Type, evt.Data)
			if evt.Type == "done" {
				if content, ok := evt.Data.(string); ok {
					finalContent = content
				}
			}
		}, s.ragEngine, blockResolveCh, s.sm, s.sessionID)

		s.logger.Info("agent.loop_end", "content", finalContent, "iteration", 1)
		s.blockMu.Lock()
		s.pendingBlock = false
		s.blockMu.Unlock()

		s.blockRespMu.Lock()
		if s.lastBlockResp != "" && finalContent == "" {
			finalContent = s.lastBlockResp
		}
		s.lastBlockResp = ""
		s.blockRespMu.Unlock()

		if finalContent != "" {
			s.msgsMu.Lock()
			s.msgs = append(s.msgs, agent.APIMessage{Role: "assistant", Content: finalContent})
			s.msgsMu.Unlock()
			s.logger.Info("conv.assistant_msg", "content", finalContent, "msg_id", currentMsgID)
		}
	}
}

func (s *Session) ModelName() string {
	return s.cfg.GetActiveProviderConfig().Model
}

func loadKnowledgeBase(ragEngine *rag.Engine, dir string, lg *logger.Logger) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	docs := make(map[string]string)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		ext := strings.ToLower(filepath.Ext(name))
		if ext != ".txt" && ext != ".md" {
			continue
		}
		content, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			lg.Error("fileio.read", "path", name, "latency_ms", 0, "error", err.Error())
			continue
		}
		docs[name] = string(content)
	}

	if len(docs) == 0 {
		return nil
	}
	lg.Info("agent.reflection", "content", fmt.Sprintf("loading %d knowledge base document(s)", len(docs)))
	return ragEngine.AddDocuments(docs)
}

func ConfigToSettings(cfg *Config) tui.Settings {
	providerID := cfg.EffectiveProviderID()

	providerURL := ""
	apiKey := ""
	model := ""
	embedModel := ""

	if pc, ok := cfg.Providers[providerID]; ok {
		providerURL = pc.ProviderURL
		apiKey = pc.APIKey
		model = pc.Model
		embedModel = pc.EmbedModel
	}

	if providerURL == "" {
		p := provider.GetPreset(providerID)
		if p == nil {
			p = provider.GetPreset("openrouter")
		}
		providerURL = p.BaseURL
	}

	return tui.Settings{
		APIKey:         apiKey,
		Model:          model,
		EmbedModel:     embedModel,
		ProviderID:     providerID,
		ProviderURL:    providerURL,
		ProviderAPIKey: apiKey,
	}
}

func settingsToConfig(s tui.Settings, base *Config) *Config {
	providerID := s.ProviderID
	if providerID == "" {
		providerID = base.EffectiveProviderID()
	}

	apiKey := s.APIKey
	if apiKey == "" {
		apiKey = s.ProviderAPIKey
	}

	providers := base.Providers
	if providers == nil {
		providers = make(map[string]ProviderConfig)
	}
	providers[providerID] = ProviderConfig{
		APIKey:      apiKey,
		Model:       s.Model,
		EmbedModel:  s.EmbedModel,
		ProviderURL: s.ProviderURL,
	}

	return &Config{
		ActiveProvider: providerID,
		Providers:      providers,
	}
}

func ResolveUsername() string {
	if u, err := user.Current(); err == nil {
		return u.Username
	}
	if name := os.Getenv("USER"); name != "" {
		return name
	}
	if name := os.Getenv("LOGNAME"); name != "" {
		return name
	}
	return "user"
}

func mustXMLArgs(m map[string]any) string {
	var sb strings.Builder
	sb.WriteString("<args>")
	for k, v := range m {
		sb.WriteString(`<param name="`)
		sb.WriteString(k)
		sb.WriteString(`">`)
		sb.WriteString("<![CDATA[")
		sb.WriteString(fmt.Sprintf("%v", v))
		sb.WriteString("]]>")
		sb.WriteString("</param>")
	}
	sb.WriteString("</args>")
	return sb.String()
}
