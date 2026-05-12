package tui

import (
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"crude/tui/input"
	"crude/tui/settings"
	"crude/tui/theme"
	"crude/tui/types"
	"crude/tui/widgets"
)

// TUI wraps the Bubble Tea program and exposes control methods.
type TUI struct {
	app     *App
	Program *tea.Program
	InputCh <-chan string
	doneCh  chan struct{}

	// CancelCh is sent to when the user cancels the current agent operation.
	CancelCh chan struct{}

	// OnQuit is called when the user exits the TUI.
	OnQuit func()

	// OnExitMessage is called after the terminal is fully restored, for final output.
	OnExitMessage func()

	// OnSettingsSave is called when the user saves settings from the inline form.
	OnSettingsSave func(Settings)

	// OnConfirmResult is called when the user responds to a confirmation prompt.
	OnConfirmResult func(ConfirmResult)

	// OnYoloCmd is called when yolo mode is toggled (true = enabled, false = disabled).
	OnYoloCmd func(enabled bool)

	// OnUndoCmd is called when the user invokes an undo slash command.
	OnUndoCmd func(cmd string)

	// FetchModelsFn is called to fetch available models from the provider.
	FetchModelsFn func(providerID, baseURL, apiKey string) ([]ModelEntry, error)

	// GetProviderConfigFn is called to get saved config for a provider.
	// Returns a struct with APIKey, Model, ProviderURL fields.
	GetProviderConfigFn func(providerID string) ProviderConfig

	// ProviderPresets is the list of available provider presets.
	ProviderPresets []ProviderPreset
}

var global *TUI

// Start initialises and starts the TUI, returning the handle and a channel
// that emits the user's submitted messages.
// initial is the current settings to pre-populate the settings panel.
func Start(modelName string, username string, initial Settings, presets []ProviderPreset, history []string) (*TUI, error) {
	inputCh := make(chan string, 8)
	cancelCh := make(chan struct{}, 1)

	t := &TUI{
		InputCh:         inputCh,
		CancelCh:        cancelCh,
		doneCh:          make(chan struct{}),
		ProviderPresets: presets,
	}
	global = t

	// The onSave callback is a closure that calls t.OnSettingsSave if set.
	onSave := func(s Settings) {
		if t.OnSettingsSave != nil {
			t.OnSettingsSave(s)
		}
	}

	fetchFn := func(providerID, baseURL, apiKey string, isOracle bool) tea.Cmd {
		return func() tea.Msg {
			if t.FetchModelsFn == nil {
				if isOracle {
					return types.OracleModelsErrorMsg("no fetch function configured")
				}
				return types.ModelsErrorMsg("no fetch function configured")
			}
			models, err := t.FetchModelsFn(providerID, baseURL, apiKey)
			if err != nil {
				if isOracle {
					return types.OracleModelsErrorMsg(err.Error())
				}
				return types.ModelsErrorMsg(err.Error())
			}
			if isOracle {
				return types.OracleModelsListMsg(models)
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

	app := NewApp(modelName, username, inputCh, cancelCh, initial, onSave, fetchFn, getProviderCfgFn)
	app.SetProviderPresets(t.ProviderPresets)
	app.SetHistory(history)
	// onSave is already a closure capturing t, no need for app.OnSettingsSave
	app.OnConfirmResult = func(result ConfirmResult) {
		if t.OnConfirmResult != nil {
			t.OnConfirmResult(result)
		}
	}
	app.OnYoloCmd = func(enabled bool) {
		if t.OnYoloCmd != nil {
			t.OnYoloCmd(enabled)
		}
	}
	app.OnUndoCmd = func(cmd string) {
		if t.OnUndoCmd != nil {
			t.OnUndoCmd(cmd)
		}
	}
	program := tea.NewProgram(app, tea.WithAltScreen(), tea.WithMouseAllMotion())
	t.app = app
	t.Program = program

	go func() {
		if _, err := program.Run(); err != nil {
			fmt.Printf("TUI error: %v\n", err)
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

// Stop quits the Bubble Tea program gracefully.
func (t *TUI) Stop() {
	select {
	case <-t.doneCh:
		return
	default:
		close(t.doneCh)
	}
	t.Program.Quit()
}

// Wait blocks until the TUI has exited.
func (t *TUI) Wait() {
	<-t.doneCh
}

// ReleaseTerminal restores the terminal (useful on fatal errors before quit).
func (t *TUI) ReleaseTerminal() {
	t.Program.ReleaseTerminal()
}

// SendAgentEvent routes an agent event into the TUI model.
func (t *TUI) SendAgentEvent(typ string, data any) {
	t.Program.Send(agentMsg(AgentEventMsg{Type: typ, Data: data}))
}

// SetError displays a transient error in the error bar.
func (t *TUI) SetError(err string) {
	t.Program.Send(errorMsg(err))
}

// UpdateModelName updates the model name shown in the header.
func (t *TUI) UpdateModelName(name string) {
	t.Program.Send(updateModelMsg(name))
}

// OpenSettings opens the settings overlay programmatically.
func (t *TUI) OpenSettings() {
	t.Program.Send(openSettingsMsg{})
}

// GetGlobal returns the global TUI instance (set by the most recent Start call).
func GetGlobal() *TUI {
	return global
}

func NewApp(modelName string, username string, inputCh chan<- string, cancelCh chan<- struct{}, initial Settings, onSave func(Settings), fetchFn func(providerID, baseURL, apiKey string, isOracle bool) tea.Cmd, getProviderConfigFn func(providerID string) ProviderConfig) *App {
	th := theme.DefaultDark()
	cm := widgets.NewChatModel(th)
	im := input.NewModel(th)
	sb := widgets.NewStatusBar(th)
	sm := settings.NewModel(th, initial, nil, getProviderConfigFn, fetchFn)
	cd := widgets.NewConfirmDialog(th)
	yd := widgets.NewYoloDialog(th)

	return &App{
		theme:          th,
		chat:           cm,
		input:          im,
		statusbar:      sb,
		settings:       sm,
		confirm:        cd,
		yoloDlg:        yd,
		inputCh:        inputCh,
		cancelCh:       cancelCh,
		modelName:      modelName,
		username:       username,
		startTime:      time.Now(),
		lastCancelTime: time.Now(),
		lastSettings:   initial,
	}
}

func (m *App) SetProviderPresets(presets []ProviderPreset) {
	m.ProviderPresets = presets
	m.settings.SetPresets(presets)
}

func (m *App) SetHistory(history []string) {
	m.history = history
	m.input.SetHistory(history)
}

func (m *App) setMode(newMode mode) {
	m.mode = newMode
	switch newMode {
	case modeSettings:
		m.input.ClearInput()
	case modeChat:
		m.formHint = ""
	}
}

func (m *App) History() []string {
	return m.history
}

func (m *App) Init() tea.Cmd {
	return tea.Batch(tickCmd(), tea.EnterAltScreen)
}
