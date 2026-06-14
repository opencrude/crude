package viewmodel

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/provider"
	"crude/tui/theme"
	"crude/tui/window"
)

func TestModeName_Chat(t *testing.T) {
	if modeName(ModeChat) != "chat" {
		t.Fatalf("expected 'chat', got '%s'", modeName(ModeChat))
	}
}

func TestFocusTarget_Launcher(t *testing.T) {
	app := newTestApp(t)

	target := app.focusTarget()
	if target != FocusLauncher {
		t.Fatalf("expected FocusLauncher with no agents, got %v", target)
	}
}

func newTestApp(t *testing.T) *App {
	t.Helper()
	th := theme.DefaultDark()

	app := &App{
		theme:                th,
		Mode:                 ModeChat,
		agents:               make(map[string]*AgentState),
		paneMgr:              NewPaneManager(),
		agentLabels:          make(map[string]string),
		agentModels:          make(map[string]string),
		agentCategories:      make(map[string]string),
		sessionInputChans:    make(map[string]chan<- provider.UserInput),
		sessionBtwChans:      make(map[string]chan<- provider.UserInput),
		sessionCancelChans:   make(map[string]chan<- struct{}),
		sessionBlockResolveCh: make(map[string]chan string),
		subAgentParents:       make(map[string]string),
		startTime:            time.Now(),
	}

	app.win = window.New(th)
	app.overlay = window.New(th)

	return app
}

var _ tea.Model = (*App)(nil)
