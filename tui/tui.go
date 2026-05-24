package tui

import (
	tea "github.com/charmbracelet/bubbletea"

	"crude/sessiondb"
	"crude/tui/viewmodel"
	"crude/tui/types"
)

type TUI struct {
	app     *viewmodel.App
	Program *tea.Program
	InputCh <-chan string
	BtwCh   <-chan string
	doneCh  chan struct{}

	CancelCh chan struct{}

	OnQuit func()

	OnExitMessage func()

	OnResumeSession func(string)

	OnCancelSubAgent func(agentID string)

	OnHardCancel func()

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

var global *TUI

func Start(modelName string, username string, initial Settings, presets []ProviderPreset, history []string, lg interface {
	Info(tag, msg string, pairs ...any)
	Warn(tag, msg string, pairs ...any)
	Error(tag, msg string, pairs ...any)
	Debug(tag, msg string, pairs ...any)
}, sessions []sessiondb.SessionMeta) (*TUI, error) {
	inputCh := make(chan string, 8)
	btwCh := make(chan string, 8)
	cancelCh := make(chan struct{}, 1)

	t := &TUI{
		InputCh:         inputCh,
		BtwCh:           btwCh,
		CancelCh:        cancelCh,
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

	a := viewmodel.NewApp(modelName, username, inputCh, btwCh, cancelCh, initial, onSave, fetchFn, getProviderCfgFn, lg, sessions)
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
	program := tea.NewProgram(a, tea.WithAltScreen(), tea.WithMouseAllMotion())
	t.app = a
	t.Program = program

	go func() {
		if _, err := program.Run(); err != nil {
			lg.Error("TUI", "program_error", "error", err.Error())
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
	t.SendAgentEventWithID(typ, data, "main")
}

func (t *TUI) SendAgentEventWithID(typ string, data any, agentID string) {
	t.Program.Send(types.AgentMsg(AgentEventMsg{Type: typ, Data: data, AgentID: agentID}))
}

func (t *TUI) GetInputCh() <-chan string {
	return t.InputCh
}

func (t *TUI) GetCancelCh() <-chan struct{} {
	return t.CancelCh
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

func GetGlobal() *TUI {
	return global
}
