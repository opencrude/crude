package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"crude/provider"
	"crude/session"
	"crude/sessiondb"
	"crude/tui"
	"crude/tui/rendering"
	ttypes "crude/tui/types"
	"crude/tui/viewmodel"
)

type App struct {
	fc              FileConfig
	sessCfg         *session.Config
	home            string
	sessionsRoot    string
	sm              *session.SessionManager
	isResuming      bool
	openSettings    bool
	resumeSessionID string
	resumeNextID    string
}

type Opts struct {
	FC              FileConfig
	SessCfg         *session.Config
	Home            string
	SessionsRoot    string
	ResumeSessionID string
	IsResuming      bool
	OpenSettings    bool
}

func New(opts Opts) (*App, error) {
	sessCfg := opts.SessCfg
	home := opts.Home
	sessionsRoot := opts.SessionsRoot

	sm := session.NewSessionManager()

	var err error
	if opts.IsResuming && opts.ResumeSessionID != "" {
		dbPath := filepath.Join(sessionsRoot, opts.ResumeSessionID+".db")
		_, err = sm.ResumeSession(dbPath, sessCfg, home)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to resume session %s: %v\n", opts.ResumeSessionID, err)
			fmt.Fprintln(os.Stderr, "Starting new session instead.")
			_, err = sm.NewSession(sessCfg, home)
		}
	} else {
		_, err = sm.NewSession(sessCfg, home)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to create session: %w", err)
	}

	return &App{
		fc:              opts.FC,
		sessCfg:         sessCfg,
		home:            home,
		sessionsRoot:    sessionsRoot,
		sm:              sm,
		isResuming:      opts.IsResuming,
		openSettings:    opts.OpenSettings,
		resumeSessionID: opts.ResumeSessionID,
	}, nil
}

func (a *App) Session() *session.Session {
	info := a.sm.First()
	if info == nil {
		return nil
	}
	return info.Session
}

func (a *App) Run() {
	sessCfg := a.sessCfg
	firstInfo := a.sm.First()
	if firstInfo == nil {
		return
	}
	sess := firstInfo.Session

	username := session.ResolveUsername()
	initial := session.ConfigToSettings(sessCfg)

	var providerPresets []tui.ProviderPreset
	for _, p := range provider.Presets {
		providerPresets = append(providerPresets, tui.ProviderPreset{
			ID:       p.ID,
			Name:     p.Name,
			HelpText: p.HelpText,
			BaseURL:  p.BaseURL,
		})
	}

	var sessionMetas []sessiondb.SessionMeta
	if a.sessionsRoot != "" && !a.isResuming {
		sessionMetas, _, _ = sessiondb.ListSessionsPaginated(a.sessionsRoot, sessiondb.ListSessionsOpts{Limit: 30, Offset: 0})
	}

	tuiInst, err := tui.Start(sessCfg.GetActiveDefaultCategory().Model, username, initial, providerPresets, LoadHistory(), sess.Logger(), sessionMetas, sess.SessionID(), sess.SessionDir(), sess.DB())
	if err != nil {
		sess.Logger().Error("SESSION", "tui_start_failed", "error", err.Error())
		fmt.Fprintf(os.Stderr, "Failed to start TUI: %v\n", err)
		return
	}

	// For resumed sessions, start orchestrator immediately to display history.
	// For new sessions, defer to OnNewSession callback (user clicks "New Session").
	if a.isResuming {
		orch, err := a.sm.StartOrchestrator(firstInfo.ID, tuiInst)
		if err != nil {
			sess.Logger().Error("SESSION", "start_failed", "error", err.Error())
		} else {
			tuiInst.AddOrchestrator(orch.ID, orch.InputCh, orch.BtwCh, orch.CancelCh, orch.BlockResolveCh)
		}
	}

	if a.isResuming {
		a.hydrateTUI(tuiInst, sess)
	}

	a.wireTUI(tuiInst, sess)

	if a.openSettings || a.fc.Providers[a.fc.ActiveProvider].APIKey == "" {
		tuiInst.OpenSettings()
	}

	tuiInst.Wait()

	exitMsg := a.renderExitMessage()
	if exitMsg != "" {
		fmt.Fprint(os.Stderr, exitMsg)
	}

	if a.resumeNextID != "" {
		a.runResumeSession(username, initial, providerPresets)
	}
}

func (a *App) wireTUI(tuiInst *tui.TUI, sess *session.Session) {
	tuiInst.GetProviderConfigFn = func(providerID string) tui.ProviderConfig {
		if cfg, ok := sess.Config().Providers[providerID]; ok {
			return tui.ProviderConfig{
				APIKey:      cfg.APIKey,
				ProviderURL: cfg.ProviderURL,
			}
		}
		return tui.ProviderConfig{}
	}

	viewmodel.OnHistorySave = func(history []string) {
		if err := SaveHistory(history); err != nil {
			sess.Logger().Warn("HISTORY", "save_failed", "error", err.Error())
		}
	}

	tuiInst.OnNewSession = func() *viewmodel.NewSessionResult {
		firstInfo := a.sm.First()
		if firstInfo == nil {
			tuiInst.SetError("no session available")
			return nil
		}
		orch, err := a.sm.StartOrchestrator(firstInfo.ID, tuiInst)
		if err != nil {
			tuiInst.SetError(err.Error())
			return nil
		}
		return &viewmodel.NewSessionResult{
			OrchID:         orch.ID,
			InputCh:        orch.InputCh,
			BtwCh:          orch.BtwCh,
			CancelCh:       orch.CancelCh,
			BlockResolveCh: orch.BlockResolveCh,
		}
	}

	tuiInst.OnQuit = func() {
		sess.Close()
	}

	tuiInst.OnExitMessage = func() {}

	tuiInst.OnSettingsSave = func(s tui.Settings) error {
		if err := sess.HandleSettingsSave(s); err != nil {
			return err
		}
		newFC := SessionToFileConfig(sess.Config())
		if err := SaveFileConfig(newFC); err != nil {
			return fmt.Errorf("failed to save config: %w", err)
		}
		tuiInst.UpdateModelName(sess.ModelName())
		return nil
	}

	tuiInst.FetchModelsFn = func(providerID, baseURL, apiKey string) ([]tui.ModelEntry, error) {
		return sess.HandleFetchModels(providerID, baseURL, apiKey)
	}

	tuiInst.OnYoloCmd = func(enabled bool) {
		sess.SetYolo(enabled)
	}

	tuiInst.OnConfirmResult = func(result tui.ConfirmResult) {}

	tuiInst.OnQuestionResult = func(result ttypes.QuestionResult) {}

	tuiInst.OnUndoCmd = func(cmd string) {
		result := sess.HandleUndo(cmd)
		tuiInst.Program.Send(result)
	}

	tuiInst.OnResumeSession = func(sessionID string) {
		a.resumeNextID = sessionID
	}

	tuiInst.OnCancelSubAgent = func(agentID string) {
		runner := sess.SubAgentRunner()
		if runner != nil {
			runner.CancelSubAgent(map[string]any{"handle_id": agentID})
		}
	}

	tuiInst.OnHardCancel = func() {
		sess.HardCancel()
	}

	tuiInst.OnDeleteSession = func(sessionID string) {
		if err := sessiondb.DeleteSession(a.sessionsRoot, sessionID); err != nil {
			sess.Logger().Warn("SESSION", "delete_failed", "id", sessionID, "error", err.Error())
		}
	}

	tuiInst.OnListSessions = func() []sessiondb.SessionMeta {
		metas, _, _ := sessiondb.ListSessionsPaginated(a.sessionsRoot, sessiondb.ListSessionsOpts{Limit: 30, Offset: 0})
		return metas
	}
}

func (a *App) hydrateTUI(tuiInst *tui.TUI, sess *session.Session) {
	msgs := sess.MessagesWithMeta()
	if len(msgs) == 0 {
		return
	}
	chatMsgs := convertMessagesForDisplay(msgs)
	tuiInst.HydrateMessages(chatMsgs)

	if db := sess.DB(); db != nil {
		if refs, err := sessiondb.LoadPromptReferences(db); err == nil && len(refs) > 0 {
			tuiInst.SetReferences(refs)
		}
	}
}

func (a *App) runResumeSession(username string, initial ttypes.Settings, providerPresets []tui.ProviderPreset) {
	sessionID := a.resumeNextID

	dbPath := filepath.Join(a.sessionsRoot, sessionID+".db")
	info, err := a.sm.ResumeSession(dbPath, a.sessCfg, a.home)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to resume session %s: %v\n", sessionID, err)
		return
	}
	sess := info.Session

	tuiInst, err := tui.Start(sess.ModelName(), username, initial, providerPresets, LoadHistory(), sess.Logger(), nil, sess.SessionID(), sess.SessionDir(), sess.DB())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to start TUI: %v\n", err)
		return
	}

	orch, err := a.sm.StartOrchestrator(info.ID, tuiInst)
	if err != nil {
		sess.Logger().Error("SESSION", "start_failed", "error", err.Error())
	} else {
		tuiInst.AddOrchestrator(orch.ID, orch.InputCh, orch.BtwCh, orch.CancelCh, orch.BlockResolveCh)
	}

	a.hydrateTUI(tuiInst, sess)
	a.wireTUI(tuiInst, sess)
	tuiInst.UpdateModelName(sess.ModelName())

	a.resumeNextID = ""

	tuiInst.Wait()

	exitMsg := a.renderExitMessage()
	if exitMsg != "" {
		fmt.Fprint(os.Stderr, exitMsg)
	}

	if a.resumeNextID != "" {
		a.runResumeSession(username, initial, providerPresets)
	}
}

func formatQuestionAnswers(result ttypes.QuestionResult) string {
	if len(result.Answers) == 0 {
		return "No answers provided"
	}
	n := len(result.Answers)
	var b strings.Builder
	for i := 1; i <= n; i++ {
		key := fmt.Sprintf("%d", i)
		question := result.Questions[key]
		answer := result.Answers[key]
		if question != "" {
			b.WriteString(fmt.Sprintf("Q%d: %s\nA%d: %s", i, question, i, answer))
		} else {
			b.WriteString(fmt.Sprintf("A%d: %s", i, answer))
		}
		if i < n {
			b.WriteString("\n\n")
		}
	}
	return b.String()
}

func (a *App) renderExitMessage() string {
	sess := a.Session()
	if sess == nil || !sess.HasMessages() {
		return ""
	}
	uptime := time.Since(sess.SessionStart()).Truncate(time.Second)
	costSnap := sess.CostSnapshot()

	var b strings.Builder
	m1 := uptime.String()
	m2 := sess.ModelName()
	m3 := ""
	if costSnap.TotalCost > 0 || costSnap.TotalPromptTokens > 0 {
		m3 = rendering.FormatCost(costSnap.TotalCost)
		if costSnap.TotalPromptTokens > 0 {
			m3 += fmt.Sprintf(" (%s↑ %s↓", rendering.FormatTokens(costSnap.TotalPromptTokens), rendering.FormatTokens(costSnap.TotalCompletionTokens))
			if costSnap.TotalCachedTokens > 0 {
				m3 += fmt.Sprintf(" %s cached", rendering.FormatTokens(costSnap.TotalCachedTokens))
			}
			m3 += ")"
		}
	}

	b.WriteString("\n")
	b.WriteString("  ·  ·  ·    " + m1 + "\n")
	b.WriteString(" · · C · ·   " + m2 + "\n")
	b.WriteString("  ·  ·  ·    " + m3 + "\n")
	b.WriteString("\n")
	b.WriteString("crude --resume " + sess.SessionID() + "\n")
	b.WriteString(fmt.Sprintf("%s\n", filepath.Join(sess.SessionDir(), "session.log")))

	return b.String()
}

func ListSessions(sessionsRoot string, limit, offset int) {
	metas, total, err := sessiondb.ListSessionsPaginated(sessionsRoot, sessiondb.ListSessionsOpts{
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error listing sessions: %v\n", err)
		return
	}
	if total == 0 {
		fmt.Println("No sessions found.")
		return
	}
	fmt.Printf("Showing %d-%d of %d sessions\n\n", offset+1, offset+len(metas), total)
	fmt.Printf("%-30s %-20s %-8s %-8s %-20s %-16s %s\n", "ID", "Project", "Turns", "Msgs", "Model", "Created", "First Prompt")
	sep := make([]byte, 140)
	for i := range sep {
		sep[i] = '-'
	}
	fmt.Println(string(sep))
	for _, m := range metas {
		firstPrompt := ""
		if m.FirstPrompt.Valid {
			firstPrompt = m.FirstPrompt.String
		}
		created := ""
		if m.CreatedAt > 0 {
			created = time.Unix(0, m.CreatedAt).Format("2006-01-02 15:04")
		}
		fmt.Printf("%-30s %-20s %-8d %-8d %-20s %-16s %.40s\n", m.ID, m.ProjectName, m.TurnCount, m.MessageCount, m.Model, created, firstPrompt)
	}
}

func DeleteSession(sessionsRoot, sessionID string) {
	if err := sessiondb.DeleteSession(sessionsRoot, sessionID); err != nil {
		fmt.Fprintf(os.Stderr, "Error deleting session: %v\n", err)
		return
	}
	fmt.Printf("Session %s deleted.\n", sessionID)
}

func ResolveResumeID(sessionsRoot, resumeFlag string) (string, error) {
	if resumeFlag == "" {
		return "", nil
	}
	if resumeFlag == "last" || resumeFlag == "true" {
		metas, err := sessiondb.ListSessions(sessionsRoot)
		if err != nil || len(metas) == 0 {
			return "", fmt.Errorf("no sessions found to resume")
		}
		return metas[0].ID, nil
	}
	return resumeFlag, nil
}
