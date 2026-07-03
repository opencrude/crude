package tui

import (
	"database/sql"

	tea "charm.land/bubbletea/v2"

	"crude/provider"
	"crude/sessiondb"
	"crude/tui/types"
	"crude/tui/viewmodel"
)

type TUI struct {
	app     *viewmodel.App
	Program *tea.Program
	doneCh  chan struct{}

	OnQuit func()

	OnExitMessage func()

	OnNewSession func() *viewmodel.NewSessionResult

	OnResumeSession func(string)

	OnCancelSubAgent func(agentID string)

	OnHardCancel func()

	OnOrchestratorReset func(orchID string)

	OnDeleteSession func(string)

	OnListSessions func() []sessiondb.SessionMeta

	OnSettingsSave func(Settings) error

	OnConfirmResult func(ConfirmResult)

	OnQuestionResult func(types.QuestionResult)

	OnYoloCmd func(enabled bool)

	OnUndoCmd func(cmd string)

	FetchModelsFn func(providerID, baseURL, apiKey string) ([]ModelEntry, error)

	GetProviderConfigFn func(providerID string) ProviderConfig

	ProviderPresets []ProviderPreset
}

func (t *TUI) AddOrchestrator(orchID string, inputCh chan<- provider.UserInput, btwCh chan<- provider.UserInput, cancelCh chan<- struct{}, blockResolveCh chan string) {
	t.app.AddOrchestrator(orchID, inputCh, btwCh, cancelCh, blockResolveCh)
}

var global *TUI

func Start(modelName string, username string, initial Settings, presets []ProviderPreset, history []string, lg interface {
	Info(tag, msg string, pairs ...any)
	Warn(tag, msg string, pairs ...any)
	Error(tag, msg string, pairs ...any)
	Debug(tag, msg string, pairs ...any)
}, sessions []sessiondb.SessionMeta, sessionID, sessionDir string, db *sql.DB) (*TUI, error) {
	t := &TUI{
		doneCh:          make(chan struct{}),
		ProviderPresets: presets,
	}
	global = t

	onSave := func(s Settings) error {
		if t.OnSettingsSave != nil {
			return t.OnSettingsSave(s)
		}
		return nil
	}

	fetchFn := func(providerID, baseURL, apiKey string) tea.Cmd {
		return func() tea.Msg {
			if t.FetchModelsFn == nil {
				return types.ModelsErrorMsg("no fetch function configured")
			}
			models, err := t.FetchModelsFn(providerID, baseURL, apiKey)
			if err != nil {
				return types.ModelsErrorMsg(err.Error())
			}
			return types.ModelsListMsg(models)
		}
	}

	getProviderCfgFn := func(providerID string) ProviderConfig {
		if t.GetProviderConfigFn != nil {
			return t.GetProviderConfigFn(providerID)
		}
		return ProviderConfig{}
	}

	a := viewmodel.NewApp(modelName, username, initial, onSave, fetchFn, getProviderCfgFn, lg, sessions, sessionID, sessionDir, db)
	a.SetProviderPresets(t.ProviderPresets)
	a.SetHistory(history)
	a.OnConfirmResult = func(result ConfirmResult) {
		if t.OnConfirmResult != nil {
			t.OnConfirmResult(result)
		}
	}
	a.OnQuestionResult = func(result types.QuestionResult) {
		if t.OnQuestionResult != nil {
			t.OnQuestionResult(result)
		}
	}
	a.OnYoloCmd = func(enabled bool) {
		if t.OnYoloCmd != nil {
			t.OnYoloCmd(enabled)
		}
	}
	a.OnUndoCmd = func(cmd string) {
		if t.OnUndoCmd != nil {
			t.OnUndoCmd(cmd)
		}
	}
	a.OnResumeSession = func(sessionID string) {
		if t.OnResumeSession != nil {
			t.OnResumeSession(sessionID)
		}
	}
	a.OnNewSession = func() *viewmodel.NewSessionResult {
		if t.OnNewSession != nil {
			return t.OnNewSession()
		}
		return nil
	}
	a.OnCancelSubAgent = func(agentID string) {
		if t.OnCancelSubAgent != nil {
			t.OnCancelSubAgent(agentID)
		}
	}
	a.OnHardCancel = func() {
		if t.OnHardCancel != nil {
			t.OnHardCancel()
		}
	}
	a.OnOrchestratorReset = func(orchID string) {
		if t.OnOrchestratorReset != nil {
			t.OnOrchestratorReset(orchID)
		}
	}
	a.SetDeleteSessionFn(func(sessionID string) {
		if t.OnDeleteSession != nil {
			t.OnDeleteSession(sessionID)
		}
	})
	a.SetListSessionsFn(func() []sessiondb.SessionMeta {
		if t.OnListSessions != nil {
			return t.OnListSessions()
		}
		return nil
	})
	program := tea.NewProgram(a)
	t.app = a
	t.Program = program
	a.SetProgram(program)

	go func() {
		if _, err := program.Run(); err != nil {
			lg.Error("TUI", "program_error", "error", err.Error())
		}
		if t.app != nil {
			t.app.StopLSP()
		}
		if t.OnQuit != nil {
			t.OnQuit()
		}
		if t.OnExitMessage != nil {
			t.OnExitMessage()
		}
		select {
		case <-t.doneCh:
		default:
			close(t.doneCh)
		}
	}()

	return t, nil
}

func (t *TUI) Stop() {
	select {
	case <-t.doneCh:
		return
	default:
		close(t.doneCh)
	}
	t.Program.Quit()
}

func (t *TUI) Wait() {
	<-t.doneCh
}

func (t *TUI) ReleaseTerminal() {
	t.Program.ReleaseTerminal()
}

func (t *TUI) SendAgentEvent(typ string, data any) {
	t.SendAgentEventWithID(typ, data, "")
}

func (t *TUI) SendAgentEventWithID(typ string, data any, agentID string) {
	t.Program.Send(types.AgentMsg(AgentEventMsg{Type: typ, Data: data, AgentID: agentID}))
}

func (t *TUI) SetError(err string) {
	t.Program.Send(types.ErrorMsg(err))
}

func (t *TUI) UpdateModelName(name string) {
	t.Program.Send(types.UpdateModelMsg(name))
}

func (t *TUI) HydrateMessages(msgs []types.ChatMessage) {
	t.Program.Send(types.HistoryMessagesMsg(msgs))
}

func (t *TUI) OpenSettings() {
	t.Program.Send(types.OpenSettingsMsg{})
}

func (t *TUI) SetReferences(refs []sessiondb.Reference) {
	t.app.SetReferences(refs)
}

func GetGlobal() *TUI {
	return global
}
