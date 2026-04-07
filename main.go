package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"crude/agent"
	"crude/backend"
	"crude/embed"
	"crude/provider"
	"crude/rag"
	"crude/safety"
	"crude/statemachine"
	"crude/tools"
	"crude/tui"
)

const systemPrompt = `You are Crude — a grounding-first AI agent for system operations.

## CRUDE Primitives
Every action maps to exactly one primitive:
- Create  → write_file, copy_file
- Read    → read_file, list_directory, glob_files, grep_files, execute_command (non-mutating), web_search, web_fetch, arxiv_search
- Update  → edit_file, write_file (overwrite), execute_command (state-changing), move_file
- Delete  → delete_file
- Execute → execute_command (scripts, services, processes), run_javascript

## Path Resolution — CRITICAL
- When the user refers to "the project", "files", "directory", "codebase", or similar WITHOUT specifying a path, ALWAYS use "." (current working directory)
- Examples: "show me the project structure" → list_directory(".") ; "what files are here" → list_directory(".") ; "read the config" → if unsure, start with list_directory(".")
- NEVER guess or invent absolute paths. If you don't know where something is, start from "." and navigate from there
- All relative paths are resolved from the current working directory

## Mandatory Protocol — enforce strictly on every turn
1. Ground before acting. Never assume file contents, directory structure, or system state.
   Start with Read operations until you have enough observed evidence to act. No exceptions.
2. Prefer surgical edits. Use edit_file for targeted changes to existing files.
   Only use write_file when creating a new file or performing a full intentional rewrite.
3. Verify after mutations. After edit_file or write_file, read back the relevant section to confirm.
4. State your plan before chains of destructive operations (Delete, Execute).
5. Use cwd on execute_command when working inside a specific project directory.

## Tool selection guide
| Goal                            | Tool                         |
|---------------------------------|------------------------------|
| Find files by name/pattern      | glob_files                   |
| Find files by content           | grep_files                   |
| Inspect a large file partially  | read_file with offset+limit  |
| Edit one section of a file      | edit_file                    |
| Create a new file               | write_file                   |
| Run a command in a project dir  | execute_command + cwd        |
| Check directory structure       | list_directory               |
| Search the web                  | web_search                   |
| Fetch a web page's content      | web_fetch                    |
| Find academic papers            | arxiv_search                 |
| Calculate or process data       | run_javascript               |

## Calculations — NEVER GUESS
When you need to compute anything (math, unit conversion, statistics, data transformation),
use run_javascript instead of estimating. Example:
  run_javascript({code: "Math.sqrt(144) * 2.54"})
Available: full ES5+, Math, JSON, Array methods, console.log.
Helpers: sum(arr), mean(arr), round(value, decimals).

Be concise, exact, and direct. Never guess paths, file contents, or calculation results — always verify first.`

var (
	sessionMsgs          []agent.APIMessage
	sessionMsgsMu        sync.Mutex
	pendingBlock         bool
	pendingBlockMu       sync.Mutex
	lastBlockingResponse string
	lastBlockingMu       sync.Mutex
	activeBackend        backend.Executor
)

func main() {
	// ── Flags ────────────────────────────────────────────────
	apiKeyFlag := flag.String("key", "", "API key (or set CRUDE_API_KEY)")
	modelFlag := flag.String("model", "", "Model to use")
	providerFlag := flag.String("provider", "", "Provider ID (openrouter, openai, anthropic, etc.)")
	sysPromptFlag := flag.String("system", "", "Override system prompt")
	backendFlag := flag.String("backend", "", "Backend to use: local, ssh, api, custom")
	settingsFlag := flag.Bool("settings", false, "Open settings on startup")
	flag.Parse()

	log.SetOutput(os.Stderr)
	log.SetFlags(0)

	// ── Config ───────────────────────────────────────────────
	cfg := loadConfig()

	if *apiKeyFlag != "" {
		cfg.APIKey = *apiKeyFlag
		cfg.ProviderAPIKey = *apiKeyFlag
	}
	if cfg.APIKey == "" {
		cfg.APIKey = os.Getenv("CRUDE_API_KEY")
		if cfg.APIKey == "" {
			cfg.APIKey = os.Getenv("OPENROUTER_API_KEY")
		}
		cfg.ProviderAPIKey = cfg.APIKey
	}
	if *providerFlag != "" {
		cfg.ProviderID = *providerFlag
	}
	if cfg.ProviderID == "" {
		cfg.ProviderID = "openrouter"
	}
	if *modelFlag != "" {
		cfg.Model = *modelFlag
	}
	if *backendFlag != "" {
		cfg.Backend = *backendFlag
	}

	prompt := systemPrompt
	if *sysPromptFlag != "" {
		prompt = *sysPromptFlag
	}

	// ── Provider ─────────────────────────────────────────────
	activeProvider := provider.FromPreset(cfg.ProviderID, cfg.ProviderAPIKey, cfg.ProviderURL)

	// ── Backend ──────────────────────────────────────────────
	activeBackend = initBackend(cfg)

	// ── Safety ───────────────────────────────────────────────
	home, _ := os.UserHomeDir()
	auditDir := filepath.Join(home, ".crude", "audit")
	auditLogger, err := safety.NewAuditLogger(auditDir)
	if err != nil {
		log.Printf("[safety] audit logger error: %v", err)
	}
	rollbackHandler := safety.NewRollbackHandler(activeBackend)

	// ── Agent ────────────────────────────────────────────────
	ag := newAgent(activeProvider, cfg.Model, activeBackend, auditLogger, rollbackHandler)

	// ── RAG engine ───────────────────────────────────────────
	ragCacheDir := filepath.Join(home, ".crude", "rag")
	embedClient := embed.NewClient(cfg.APIKey, cfg.EmbedModel, activeProvider.BaseURL)
	ragEngine := rag.New(embedClient, ragCacheDir)

	knowledgeDir := filepath.Join(home, ".crude", "knowledge")
	if err := loadKnowledgeBase(ragEngine, knowledgeDir); err != nil {
		log.Printf("[rag] knowledge base load error: %v", err)
	}

	// ── State machine ────────────────────────────────────────
	sm := statemachine.NewConversationStateMachine(nil)
	sessionID := "cli-session"

	// ── TUI ──────────────────────────────────────────────────
	initial := configToSettings(cfg)

	// Build provider presets for TUI selector.
	var providerPresets []tui.ProviderPreset
	for _, p := range provider.Presets {
		providerPresets = append(providerPresets, tui.ProviderPreset{
			ID:       p.ID,
			Name:     p.Name,
			HelpText: p.HelpText,
			BaseURL:  p.BaseURL,
		})
	}

	tuiInst, err := tui.Start(cfg.Model, initial, providerPresets, loadHistory())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to start TUI: %v\n", err)
		os.Exit(1)
	}

	tui.OnHistorySave = func(history []string) {
		if err := saveHistory(history); err != nil {
			log.Printf("[history] save error: %v", err)
		}
	}

	// Redirect logs to file so they don't bleed into TUI alt screen.
	logDir := filepath.Join(home, ".crude")
	os.MkdirAll(logDir, 0o755)
	if logFile, err := os.OpenFile(filepath.Join(logDir, "crude.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
		log.SetOutput(logFile)
	}

	tuiInst.OnQuit = func() {
		if auditLogger != nil {
			auditLogger.Close()
		}
		os.Exit(0)
	}

	tuiInst.OnSettingsSave = func(s tui.Settings) {
		newCfg := settingsToConfig(s, cfg)

		if newCfg.APIKey == "" {
			tuiInst.SetError("API key cannot be empty — settings not saved")
			return
		}

		cfg = newCfg
		if err := saveConfig(cfg); err != nil {
			tuiInst.SetError("Failed to save config: " + err.Error())
		}

		activeProvider = provider.FromPreset(cfg.ProviderID, cfg.ProviderAPIKey, cfg.ProviderURL)
		activeBackend = initBackend(cfg)

		ag = newAgent(activeProvider, cfg.Model, activeBackend, auditLogger, safety.NewRollbackHandler(activeBackend))
		embedClient = embed.NewClient(cfg.APIKey, cfg.EmbedModel, activeProvider.BaseURL)
		ragEngine = rag.New(embedClient, ragCacheDir)
		if err := loadKnowledgeBase(ragEngine, knowledgeDir); err != nil {
			log.Printf("[rag] knowledge base reload error: %v", err)
		}
		tuiInst.UpdateModelName(cfg.Model)
	}

	tuiInst.FetchModelsFn = func(providerID, baseURL, apiKey string) ([]tui.ModelEntry, error) {
		// Build a temporary provider from the form's current state (not the saved activeProvider).
		p := provider.FromPreset(providerID, apiKey, baseURL)
		models, err := FetchModels(p)
		if err != nil {
			return nil, err
		}
		entries := make([]tui.ModelEntry, len(models))
		for i, m := range models {
			entries[i] = tui.ModelEntry{ID: m.ID, Name: m.Name}
		}
		return entries, nil
	}

	if *settingsFlag || cfg.APIKey == "" {
		tuiInst.OpenSettings()
	}

	// ── Blocking flow ─────────────────────────────────────────
	blockResolveCh := make(chan string, 1)

	// ── Main agent loop ──────────────────────────────────────
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[agent] panic recovered: %v", r)
				tuiInst.SendAgentEvent("error", fmt.Sprintf("Internal error: %v", r))
			}
		}()
		for userText := range tuiInst.InputCh {
			if cfg.APIKey == "" {
				tuiInst.SendAgentEvent("error", "No API key configured. Press Ctrl+S to open settings.")
				continue
			}

			pendingBlockMu.Lock()
			isBlocked := pendingBlock
			pendingBlock = false
			pendingBlockMu.Unlock()
			if isBlocked {
				lastBlockingMu.Lock()
				lastBlockingResponse = userText
				lastBlockingMu.Unlock()
				blockResolveCh <- userText
				continue
			}

			sessionMsgsMu.Lock()
			sessionMsgs = append(sessionMsgs, agent.APIMessage{
				Role:    "user",
				Content: userText,
			})
			msgsCopy := make([]agent.APIMessage, len(sessionMsgs))
			copy(msgsCopy, sessionMsgs)
			sessionMsgsMu.Unlock()

			logAgent("starting agent loop for user input: %q", userText)
			sm.CreateGoal(sessionID, "respond", userText, statemachine.PriorityNormal, false)
			ragEngine.ResetObservations()

			var finalContent string
			ag.Run(msgsCopy, prompt, func(evt agent.AgentEvent) {
				tuiInst.SendAgentEvent(evt.Type, evt.Data)
				if evt.Type == "blocking" {
					pendingBlockMu.Lock()
					pendingBlock = true
					pendingBlockMu.Unlock()
				}
				if evt.Type == "done" {
					if s, ok := evt.Data.(string); ok {
						finalContent = s
					}
				}
			}, ragEngine, blockResolveCh, sm, sessionID)

			logAgent("agent.Run completed, finalContent: %q", finalContent)
			// Reset blocking state — the blocking node has been resolved.
			pendingBlockMu.Lock()
			pendingBlock = false
			pendingBlockMu.Unlock()

			// If a blocking node captured a response but no done content was emitted,
			// use the blocking response as the assistant's reply.
			lastBlockingMu.Lock()
			if lastBlockingResponse != "" && finalContent == "" {
				finalContent = lastBlockingResponse
			}
			lastBlockingResponse = ""
			lastBlockingMu.Unlock()

			if finalContent != "" {
				sessionMsgsMu.Lock()
				sessionMsgs = append(sessionMsgs, agent.APIMessage{
					Role:    "assistant",
					Content: finalContent,
				})
				sessionMsgsMu.Unlock()
			}
		}
	}()

	tuiInst.Wait()
}

func newAgent(p *provider.Provider, model string, exec backend.Executor, auditLogger *safety.AuditLogger, rollbackHandler *safety.RollbackHandler) *agent.Agent {
	a := agent.NewAgent(p, model)
	a.AuditLogger = auditLogger
	a.RollbackHandler = rollbackHandler
	a.ToolFn = func(tool string, params map[string]any) (string, error) {
		tc := tools.ToolCall{
			ID:   fmt.Sprintf("dag-%s", tool),
			Type: "function",
			Function: tools.FunctionCall{
				Name:      tool,
				Arguments: mustJSON(params),
			},
		}
		result := tools.Execute(tc, exec)
		if strings.HasPrefix(result, "Error:") {
			return result, fmt.Errorf("%s", result)
		}
		return result, nil
	}
	return a
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

func initBackend(cfg Config) backend.Executor {
	switch cfg.Backend {
	case "ssh":
		s, err := backend.NewSSH(backend.SSHConfig{
			Host:     cfg.SSH.Host,
			Port:     cfg.SSH.Port,
			User:     cfg.SSH.User,
			Password: cfg.SSH.Password,
			KeyFile:  cfg.SSH.KeyFile,
		})
		if err != nil {
			tui.SetBackendLabel("local (ssh failed)")
			return backend.NewLocal()
		}
		tui.SetBackendLabel("ssh:" + cfg.SSH.Host)
		return s
	case "api":
		tui.SetBackendLabel("api")
		return backend.NewAPI(backend.APIConfig{
			BaseURL:    cfg.APIBaseURL,
			AuthHeader: cfg.APIAuthHeader,
			AuthValue:  cfg.APIAuthValue,
		})
	case "custom":
		tui.SetBackendLabel("custom")
		return backend.NewCustom(backend.CustomConfig{
			ExecCmd:   cfg.CustomExecCmd,
			ReadCmd:   cfg.CustomReadCmd,
			WriteCmd:  cfg.CustomWriteCmd,
			DeleteCmd: cfg.CustomDeleteCmd,
			ListCmd:   cfg.CustomListCmd,
		})
	default:
		tui.SetBackendLabel("local")
		return backend.NewLocal()
	}
}

func configToSettings(cfg Config) tui.Settings {
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
		Backend:        cfg.Backend,
		SSHHost:        cfg.SSH.Host,
		SSHPort:        fmt.Sprintf("%d", cfg.SSH.Port),
		SSHUser:        cfg.SSH.User,
		SSHPassword:    cfg.SSH.Password,
		SSHKeyFile:     cfg.SSH.KeyFile,
	}
}

func settingsToConfig(s tui.Settings, base Config) Config {
	sshConf := base.SSH
	if s.Backend == "ssh" {
		port := base.SSH.Port
		if s.SSHPort != "" {
			var p int
			fmt.Sscanf(s.SSHPort, "%d", &p)
			if p > 0 {
				port = p
			}
		}
		if port == 0 {
			port = 22
		}
		sshConf = SSHConf{
			Host:     s.SSHHost,
			Port:     port,
			User:     s.SSHUser,
			Password: s.SSHPassword,
			KeyFile:  s.SSHKeyFile,
		}
	}

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

	return Config{
		APIKey:          apiKey,
		Model:           s.Model,
		EmbedModel:      s.EmbedModel,
		ProviderID:      providerID,
		ProviderURL:     s.ProviderURL,
		ProviderAPIKey:  apiKey,
		Backend:         s.Backend,
		SSH:             sshConf,
		ProviderConfigs: providerConfigs,
		APIBaseURL:      base.APIBaseURL,
		APIAuthHeader:   base.APIAuthHeader,
		APIAuthValue:    base.APIAuthValue,
		CustomExecCmd:   base.CustomExecCmd,
		CustomReadCmd:   base.CustomReadCmd,
		CustomWriteCmd:  base.CustomWriteCmd,
		CustomDeleteCmd: base.CustomDeleteCmd,
		CustomListCmd:   base.CustomListCmd,
	}
}

func logAgent(format string, args ...any) {
	log.Printf("[agent] "+format, args...)
}

func mustJSON(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(data)
}
