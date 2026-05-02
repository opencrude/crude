package tui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
)

// TUI wraps the Bubble Tea program and exposes control methods.
type TUI struct {
	model   *Model
	Program *tea.Program
	InputCh <-chan string
	doneCh  chan struct{}

	// OnQuit is called when the user exits the TUI.
	OnQuit func()

	// OnExitMessage is called after the terminal is fully restored, for final output.
	OnExitMessage func()

	// OnSettingsSave is called when the user saves settings from the inline form.
	OnSettingsSave func(Settings)

	// OnConfirmResult is called when the user responds to a confirmation prompt.
	OnConfirmResult func(ConfirmResult)

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

// ProviderConfig holds configuration for a specific provider.
type ProviderConfig struct {
	APIKey         string
	Model          string
	OracleModel    string
	ProviderURL    string
	ThinkingEffort string
}

var global *TUI

// Start initialises and starts the TUI, returning the handle and a channel
// that emits the user's submitted messages.
// initial is the current settings to pre-populate the settings panel.
func Start(modelName string, username string, initial Settings, presets []ProviderPreset, history []string) (*TUI, error) {
	inputCh := make(chan string, 8)

	t := &TUI{
		InputCh:         inputCh,
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

	fetchFn := func(providerID, baseURL, apiKey string) tea.Cmd {
		return func() tea.Msg {
			if t.FetchModelsFn == nil {
				return ModelsErrorMsg("no fetch function configured")
			}
			models, err := t.FetchModelsFn(providerID, baseURL, apiKey)
			if err != nil {
				return ModelsErrorMsg(err.Error())
			}
			return ModelsListMsg(models)
		}
	}

	getProviderCfgFn := func(providerID string) ProviderConfig {
		if t.GetProviderConfigFn != nil {
			return t.GetProviderConfigFn(providerID)
		}
		return ProviderConfig{}
	}

	model := NewModel(modelName, username, inputCh, initial, onSave, fetchFn, getProviderCfgFn)
	model.SetProviderPresets(t.ProviderPresets)
	model.SetHistory(history)
	model.OnConfirmResult = func(result ConfirmResult) {
		if t.OnConfirmResult != nil {
			t.OnConfirmResult(result)
		}
	}
	model.OnUndoCmd = func(cmd string) {
		if t.OnUndoCmd != nil {
			t.OnUndoCmd(cmd)
		}
	}
	program := tea.NewProgram(model, tea.WithAltScreen(), tea.WithMouseAllMotion())
	t.model = model
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
