package session

import (
	"encoding/json"
	"fmt"
	"log"
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
	"crude/logging"
	"crude/provider"
	"crude/rag"
	"crude/safety"
	"crude/statemachine"
	"crude/tools"
	"crude/tui"
)

type Config struct {
	APIKey          string
	Model           string
	EmbedModel      string
	ProviderID      string
	ProviderURL     string
	ProviderAPIKey  string
	ProviderConfigs map[string]ProviderConfig
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

	provider    *provider.Provider
	exec        fileio.Executer
	editor      *fileio.Editor
	auditLogger *safety.AuditLogger
	logger      *logging.InteractionLogger
	permStore   *safety.PermissionStore
	ragEngine   *rag.Engine
	sm          *statemachine.ConversationStateMachine

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
	p := provider.FromPreset(cfg.ProviderID, cfg.ProviderAPIKey, cfg.ProviderURL)
	exec := fileio.NewLocalExec()

	var auditLogger *safety.AuditLogger
	auditDir := filepath.Join(homeDir, ".crude", "audit")
	if al, err := safety.NewAuditLogger(auditDir); err != nil {
		log.Printf("[safety] audit logger error: %v", err)
	} else {
		auditLogger = al
	}

	permStore := safety.NewPermissionStore()

	fileioSessionID := fileio.GenerateSessionID()
	var editor *fileio.Editor
	if ed, err := fileio.NewEditor(fileioSessionID); err != nil {
		log.Printf("[fileio] editor init error: %v", err)
	} else {
		editor = ed
	}

	logDir := filepath.Join(homeDir, ".crude", "logs")
	var interactionLog *logging.InteractionLogger
	if il, err := logging.NewInteractionLogger(logDir); err != nil {
		log.Printf("[logging] interaction logger error: %v", err)
	} else {
		il.LogSessionStart("1.0")
		interactionLog = il
	}

	ragCacheDir := filepath.Join(homeDir, ".crude", "rag")
	embedClient := embed.NewClient(cfg.APIKey, cfg.EmbedModel, p.BaseURL)
	ragEngine := rag.New(embedClient, ragCacheDir)

	knowledgeDir := filepath.Join(homeDir, ".crude", "knowledge")
	if err := loadKnowledgeBase(ragEngine, knowledgeDir); err != nil {
		log.Printf("[rag] knowledge base load error: %v", err)
	}

	sm := statemachine.NewConversationStateMachine(nil)

	return &Session{
		cfg:          cfg,
		provider:     p,
		exec:         exec,
		editor:       editor,
		auditLogger:  auditLogger,
		logger:       interactionLog,
		permStore:    permStore,
		ragEngine:    ragEngine,
		sm:           sm,
		sessionID:    "cli-session",
		sessionStart: time.Now(),
		ragCacheDir:  ragCacheDir,
		knowledgeDir: knowledgeDir,
	}, nil
}

func (s *Session) Close() {
	if s.editor != nil {
		s.editor.Close()
	}
	if s.auditLogger != nil {
		s.auditLogger.Close()
	}
	if s.logger != nil {
		durationMs := time.Since(s.sessionStart).Milliseconds()
		s.logger.LogSessionEnd(durationMs)
		s.logger.Close()
	}
}

func (s *Session) Editor() *fileio.Editor { return s.editor }

func (s *Session) newAgent() *agent.Agent {
	a := agent.NewAgent(s.provider, s.cfg.Model)
	a.AuditLogger = s.auditLogger
	a.Logger = s.logger
	a.ToolFn = func(tool string, params map[string]any) (string, error) {
		tc := tools.ToolCall{
			ID:   fmt.Sprintf("dag-%s", tool),
			Type: "function",
			Function: tools.FunctionCall{
				Name:      tool,
				Arguments: mustJSON(params),
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
	if newCfg.APIKey == "" {
		return fmt.Errorf("API key cannot be empty — settings not saved")
	}

	s.cfg = newCfg
	s.provider = provider.FromPreset(s.cfg.ProviderID, s.cfg.ProviderAPIKey, s.cfg.ProviderURL)
	embedClient := embed.NewClient(s.cfg.APIKey, s.cfg.EmbedModel, s.provider.BaseURL)
	s.ragEngine = rag.New(embedClient, s.ragCacheDir)
	if err := loadKnowledgeBase(s.ragEngine, s.knowledgeDir); err != nil {
		log.Printf("[rag] knowledge base reload error: %v", err)
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
			log.Printf("[agent] panic recovered: %v", r)
			tuiInst.SendAgentEvent("error", fmt.Sprintf("Internal error: %v", r))
		}
	}()

	for userText := range tuiInst.InputCh {
		if s.cfg.APIKey == "" {
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

		s.msgsMu.Lock()
		s.msgs = append(s.msgs, agent.APIMessage{Role: "user", Content: userText})
		msgsCopy := make([]agent.APIMessage, len(s.msgs))
		copy(msgsCopy, s.msgs)
		s.msgsMu.Unlock()

		log.Printf("[agent] starting agent loop for user input: %q", userText)
		s.sm.CreateGoal(s.sessionID, "respond", userText, statemachine.PriorityNormal, false)
		s.ragEngine.ResetObservations()

		s.msgCounterMu.Lock()
		s.msgCounter++
		currentMsgID := fmt.Sprintf("msg-%d", s.msgCounter)
		s.msgCounterMu.Unlock()
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

		log.Printf("[agent] agent.Run completed, finalContent: %q", finalContent)
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
		}
	}
}

func (s *Session) ModelName() string {
	return s.cfg.Model
}

func loadKnowledgeBase(ragEngine *rag.Engine, dir string) error {
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
			log.Printf("[rag] skip %s: %v", name, err)
			continue
		}
		docs[name] = string(content)
	}

	if len(docs) == 0 {
		return nil
	}
	log.Printf("[rag] loading %d knowledge base document(s)", len(docs))
	return ragEngine.AddDocuments(docs)
}

func ConfigToSettings(cfg *Config) tui.Settings {
	providerID := cfg.ProviderID
	if providerID == "" {
		providerID = "openrouter"
	}

	providerURL := cfg.ProviderURL
	apiKey := cfg.ProviderAPIKey
	model := cfg.Model
	embedModel := cfg.EmbedModel

	if pc, ok := cfg.ProviderConfigs[providerID]; ok {
		if pc.ProviderURL != "" {
			providerURL = pc.ProviderURL
		}
		if pc.APIKey != "" {
			apiKey = pc.APIKey
		}
		if pc.Model != "" {
			model = pc.Model
		}
		if pc.EmbedModel != "" {
			embedModel = pc.EmbedModel
		}
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
		providerID = base.ProviderID
	}
	if providerID == "" {
		providerID = "openrouter"
	}

	apiKey := s.APIKey
	if apiKey == "" {
		apiKey = s.ProviderAPIKey
	}

	providerConfigs := base.ProviderConfigs
	if providerConfigs == nil {
		providerConfigs = make(map[string]ProviderConfig)
	}
	providerConfigs[providerID] = ProviderConfig{
		APIKey:      apiKey,
		Model:       s.Model,
		EmbedModel:  s.EmbedModel,
		ProviderURL: s.ProviderURL,
	}

	return &Config{
		APIKey:          apiKey,
		Model:           s.Model,
		EmbedModel:      s.EmbedModel,
		ProviderID:      providerID,
		ProviderURL:     s.ProviderURL,
		ProviderAPIKey:  apiKey,
		ProviderConfigs: providerConfigs,
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

func mustJSON(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(data)
}
