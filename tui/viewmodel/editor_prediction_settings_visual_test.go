package viewmodel

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"golang.org/x/term"

	"crude/agent/provider"
	"crude/agent/session"
	settingsui "crude/tui/screens/settingspages"
	"crude/tui/ui"
	"crude/types/setting"
	"crude/workspace/fileio"
)

// Preference/consent acceptance only: suggestions stay Off until the native
// choice is saved. This does not accept a prediction service, provider
// request, budget meter, automatic prediction, or distinct-host transport.
//
// Native capture (parent-owned, sequential 160x40 then 80x24):
// CRUDE_VISUAL_PREDICTION_SETTINGS=1
// CRUDE_PREDICTION_SETTINGS_VISUAL_RESULT=<private result file>
// CRUDE_BIN=<compiled viewmodel test binary>
// CRUDE_ARGS='-test.run=^TestPredictionSettingsVisualAcceptance$ -test.count=1 -test.timeout=180s'
//
// Identical scripts/tuishot.sh steps at both grids, ending in clean quit:
//
//	key:F4
//	key:Tab key:ctrl+a type:0 key:F2 key:Right key:F4
//	key:F3 key:Return key:F4
//	key:F2 key:Tab key:ctrl+a key:BackSpace type:4200 key:F4
//	key:F3 key:Return key:F4
//	key:F2 key:Left key:F4
//	key:F3 key:Return key:F4
//	quit:F9
//
// F2 focuses only prediction.mode; F3 focuses only the actual footer. F4 is a
// read-only observer, waiting at most eight seconds for real save/publication
// completion. None sets field values, invokes Save, manufactures an ACK, grants
// authority, or retires work. Every scored action is ordinary App.Update input.
func TestPredictionSettingsVisualAcceptance(t *testing.T) {
	if os.Getenv("CRUDE_VISUAL_PREDICTION_SETTINGS") != "1" {
		t.Skip("dedicated sequential terminal capture; preferences only")
	}
	width, height, err := term.GetSize(int(os.Stdin.Fd()))
	if err != nil {
		t.Fatal("native prediction preferences acceptance requires a terminal")
	}
	runPredictionSettingsVisual(t, false, width, height)
}

func TestPredictionSettingsVisualHeadless(t *testing.T) {
	for _, size := range [][2]int{{160, 40}, {80, 24}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			runPredictionSettingsVisual(t, true, size[0], size[1])
		})
	}
}

const predictionSettingsPrivateDraft = "private prediction settings visual draft"

// Expectations are literal journey outcomes, not derived from a UI collector or
// the persistence implementation. Account/format are configured separately.
var predictionSettingsVisualPhases = []struct {
	name         string
	draftMode    setting.EditorPredictionMode
	draftCap     int64
	savedMode    setting.EditorPredictionMode
	savedCap     int64
	grant, dirty bool
	enters       int
}{
	{"Off paint", setting.PredictionOff, 0, setting.PredictionOff, 0, false, false, 0},
	{"Light zero-cap draft", setting.PredictionAsk, 0, setting.PredictionOff, 0, false, true, 0},
	{"zero-cap Save refused", setting.PredictionAsk, 0, setting.PredictionOff, 0, false, true, 1},
	{"exact cap 4200 paint", setting.PredictionAsk, 4200, setting.PredictionOff, 0, false, true, 1},
	{"saved Light bound grant", setting.PredictionAsk, 4200, setting.PredictionAsk, 4200, true, false, 2},
	{"Off draft retains committed Light", setting.PredictionOff, 4200, setting.PredictionAsk, 4200, true, true, 2},
	{"saved Off clears grant", setting.PredictionOff, 4200, setting.PredictionOff, 4200, false, false, 3},
}

type predictionSettingsVisualProbe struct{}

type predictionSettingsVisualModel struct {
	*App
	calls              *atomic.Int32
	rootIdentity       string
	connectionIdentity string
	originalGrant      string
	phase              int
	enters             int
	checkPending       bool
	checkUntil         time.Time
	failure            error
	observations       []string
	settled            chan int
}

func (m *predictionSettingsVisualModel) body() *settingsui.WorkspaceSettingsSurface {
	return m.settingsSurface(m.overlayHost.Window(ui.OverlayWorkspaceSettings))
}

func (m *predictionSettingsVisualModel) form() *ui.Form {
	body := m.body()
	if body != nil {
		for _, child := range body.Children() {
			if form, ok := child.(*ui.Form); ok && form.ID() == settingsui.WorkspaceSettingsFormID {
				return form
			}
		}
	}
	return nil
}

func (m *predictionSettingsVisualModel) focusMode() error {
	body, form := m.body(), m.form()
	if body == nil || form == nil {
		return fmt.Errorf("prediction preferences lost their mounted native form")
	}
	body.FocusForm()
	if !form.FocusField("prediction.mode") {
		return fmt.Errorf("native form cannot focus prediction.mode")
	}
	// Fixture-only category/focus navigation bypasses an input EventResult.
	// Invalidate its retained frame just as the normal navigation router does;
	// observing or editing values never repairs presentation on the reader.
	window := m.overlayHost.Window(ui.OverlayWorkspaceSettings)
	if frame, ok := window.Content.(*ui.WindowFrameWidget); ok {
		frame.Host().MarkLayout()
	}
	window.MarkContentDirty()
	return nil
}

func (m *predictionSettingsVisualModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var command tea.Cmd
	if key, ok := msg.(tea.KeyPressMsg); ok {
		switch key.Code {
		case tea.KeyF2:
			if err := m.focusMode(); err != nil {
				m.failure = err
				return m, tea.Quit
			}
			_, command = m.App.Update(predictionSettingsVisualProbe{})
		case tea.KeyF3:
			if body := m.body(); body != nil {
				body.FocusActions()
			} else {
				m.failure = fmt.Errorf("footer focus lost its mounted settings owner")
				return m, tea.Quit
			}
			_, command = m.App.Update(predictionSettingsVisualProbe{})
		case tea.KeyF4:
			if m.checkPending || m.phase >= len(predictionSettingsVisualPhases) {
				m.failure = fmt.Errorf("unexpected duplicate observation at phase %d", m.phase)
				return m, tea.Quit
			}
			m.checkPending = true
			m.checkUntil = time.Now().Add(8 * time.Second)
			_, command = m.App.Update(predictionSettingsVisualProbe{})
		case tea.KeyF9:
			if m.checkPending || m.phase != len(predictionSettingsVisualPhases) || m.calls.Load() != 0 {
				m.failure = fmt.Errorf("prediction preferences journey incomplete: phases=%d/7 pending=%t HTTP=%d",
					m.phase, m.checkPending, m.calls.Load())
			}
			return m, tea.Quit
		default:
			if key.Code == tea.KeyEnter {
				m.enters++
			}
			_, command = m.App.Update(msg)
		}
	} else {
		_, command = m.App.Update(msg)
	}
	if m.checkPending {
		ready, err := m.observe()
		if err != nil {
			m.failure = fmt.Errorf("phase %d: %w", m.phase, err)
			return m, tea.Quit
		}
		if ready {
			m.checkPending = false
		} else if time.Now().After(m.checkUntil) {
			m.failure = fmt.Errorf("phase %d: real settings save/publication did not settle within eight seconds", m.phase)
			return m, tea.Quit
		} else {
			command = tea.Batch(command, tea.Tick(10*time.Millisecond, func(time.Time) tea.Msg {
				return predictionSettingsVisualProbe{}
			}))
		}
	}
	return m, command
}

func (m *predictionSettingsVisualModel) observe() (bool, error) {
	want := predictionSettingsVisualPhases[m.phase]
	body, form := m.body(), m.form()
	if body == nil || form == nil || body.Model().Scope() != setting.ScopeWorkspace {
		return false, fmt.Errorf("lost the mounted Workspace / Code Intelligence page")
	}
	model := body.Model()
	// Preference pages use the category heading, not the collection/editor
	// breadcrumb returned by PaneTitle (which is intentionally empty here).
	categories, selected := model.Categories(), model.SelectedCategory()
	if selected < 0 || selected >= len(categories) || categories[selected].Name != "Code Intelligence" {
		return false, fmt.Errorf("lost the selected Code Intelligence preference category")
	}
	if m.calls.Load() != 0 {
		return false, fmt.Errorf("preferences caused forbidden provider HTTP calls: %d", m.calls.Load())
	}
	if m.Input.Value() != predictionSettingsPrivateDraft {
		return false, fmt.Errorf("native settings input consumed the private composer")
	}
	draft, saved := model.AppSettings(), m.ws.LoadAppSettings()
	if draft.Analysis.Trust != setting.AnalysisUndecided || saved.Analysis.Trust != setting.AnalysisUndecided ||
		m.ws.AppDefaults().Prediction.Mode != setting.PredictionOff ||
		m.ws.AppDefaults().Prediction.HourlyTokenLimit != 0 {
		return false, fmt.Errorf("prediction changed independent analysis Trust or application defaults")
	}
	if model.Saving() {
		return false, nil
	}
	if m.phase == 4 || m.phase == 6 {
		if model.SaveError() != "" {
			// Do not print operational errors that might contain connection data.
			return false, fmt.Errorf("native footer Save reported an error")
		}
		if len(model.Edits()) != 0 || saved.Prediction.Mode != want.savedMode ||
			saved.Prediction.HourlyTokenLimit != want.savedCap {
			return false, nil // Actual publication may still be queued.
		}
	}
	if m.enters != want.enters ||
		draft.Prediction != (setting.WorkspacePrediction{
			Mode: want.draftMode, HourlyTokenLimit: want.draftCap,
		}) ||
		saved.Prediction != (setting.WorkspacePrediction{
			Mode: want.savedMode, HourlyTokenLimit: want.savedCap,
		}) || (len(model.Edits()) != 0) != want.dirty {
		return false, fmt.Errorf("ordinary keys did not retain exact draft/committed mode, cap, or dirty state; Enter=%d", m.enters)
	}
	for _, leaf := range []struct{ id, value string }{
		{"prediction.mode", string(want.draftMode)},
		{"prediction.hourly_token_limit", strconv.FormatInt(want.draftCap, 10)},
	} {
		field := form.Field(leaf.id)
		wantValue := leaf.value
		if m.phase == 0 && leaf.id == "prediction.hourly_token_limit" {
			wantValue = "200000"
		}
		if field == nil || field.FieldState().Value.String() != wantValue ||
			field.FieldState().ReadOnly || field.FieldState().Disabled {
			return false, fmt.Errorf("mounted editable field %s lost its exact native value", leaf.id)
		}
	}
	overlay := m.ws.LoadWorkspaceOverrides()
	rootBound := overlay.PredictionRootIdentity == m.rootIdentity
	connectionBound := overlay.PredictionConnectionIdentity == m.connectionIdentity
	grantPresent := strings.HasPrefix(overlay.PredictionGrantID, "prediction-") &&
		len(overlay.PredictionGrantID) > len("prediction-")
	if want.grant {
		if !rootBound || !connectionBound || !grantPresent {
			return false, fmt.Errorf("saved Light lacks the exact fixture root/connection or owner-generated grant")
		}
		if m.phase == 4 {
			m.originalGrant = overlay.PredictionGrantID
		} else if overlay.PredictionGrantID != m.originalGrant {
			return false, fmt.Errorf("unsaved Off draft replaced the committed Light grant")
		}
	} else if overlay.PredictionRootIdentity != "" || overlay.PredictionConnectionIdentity != "" ||
		overlay.PredictionGrantID != "" {
		return false, fmt.Errorf("Off/invalid enable retained or invented authority metadata")
	}
	if overlay.AnalysisRootIdentity != "" {
		return false, fmt.Errorf("prediction consent created an independent analysis grant")
	}
	if m.phase < 4 {
		if len(overlay.Prediction) != 0 {
			return false, fmt.Errorf("unsaved or refused enable wrote a partial preference overlay")
		}
	} else {
		persisted, err := setting.DecodeWorkspacePrediction(overlay.Prediction)
		if err != nil || persisted != saved.Prediction {
			return false, fmt.Errorf("published durable preference overlay differs from exact committed choice")
		}
	}
	if m.phase == 2 {
		if model.SaveError() != "Correct the highlighted Code Intelligence fields before saving." ||
			!form.Field("prediction.mode").FieldState().HasErrors() ||
			!form.Field("prediction.hourly_token_limit").FieldState().HasErrors() {
			return false, fmt.Errorf("footer Enter did not visibly refuse enabled zero-cap consent")
		}
	}
	paint := ansi.Strip(m.App.View().RenderContent())
	for _, label := range []string{"Workspace Settings", "Code Intelligence", "Save", "Close"} {
		if !strings.Contains(paint, label) {
			return false, fmt.Errorf("native frame does not paint literal %q", label)
		}
	}
	// The mode row is near the bottom of a long real page. Native focus, not
	// observer scrolling, reveals it; exact cap entry reveals the next row.
	if m.phase == 3 {
		if !strings.Contains(paint, "Hourly limit") || !strings.Contains(paint, "4200") {
			return false, fmt.Errorf("native cap row does not paint literal Hourly cap / 4200")
		}
	} else {
		label := "Off"
		if want.draftMode == setting.PredictionAsk {
			label = "When I ask"
		}
		if !strings.Contains(paint, "Errand suggestions") || !strings.Contains(paint, label) {
			return false, fmt.Errorf("native prediction row does not paint literal AI predictions / %s: form=%+v scroll=%d viewport=%d mode=%+v\n%s",
				label, form.Bounds(), form.Scroll().Offset(), form.Scroll().Viewport(),
				form.Field("prediction.mode").Bounds(), paint)
		}
	}
	if want.dirty && model.SaveError() == "" {
		// Require readable status on the actual footer row, never accept the
		// unrelated category-rail marker as evidence that Save is unsaved.
		visible := false
		for _, line := range strings.Split(paint, "\n") {
			if strings.Contains(line, "Save") && strings.Contains(line, "Close") &&
				strings.Contains(line, "● Unsaved") {
				visible = true
			}
		}
		if !visible {
			return false, fmt.Errorf("native footer lost visible unsaved status:\n%s", paint)
		}
	}
	// At the constrained grid the error status is elided beside Save/Close.
	// Its exact full model text was independently checked above.
	if m.phase == 2 && (!strings.Contains(paint, "Correct") || !strings.Contains(paint, "positive hourly")) {
		return false, fmt.Errorf("native footer does not paint its zero-cap Save refusal")
	}
	line := fmt.Sprintf("F4 VERIFIED phase=%d name=%q grid=%dx%d draft=%s cap=%d committed=%s committed_cap=%d root_bound=%t connection_bound=%t grant_present=%t analysis=static_only defaults=off private_composer=preserved provider_HTTP=0\n",
		m.phase, want.name, m.width, m.height, want.draftMode, want.draftCap, want.savedMode, want.savedCap,
		rootBound, connectionBound, grantPresent)
	m.observations = append(m.observations, line)
	if _, err := fmt.Fprint(os.Stderr, line); err != nil {
		return false, fmt.Errorf("cannot write sanitized observation")
	}
	m.phase++
	m.settled <- m.phase
	return true, nil
}

func runPredictionSettingsVisual(t *testing.T, headless bool, width, height int) {
	t.Helper()
	// Package TestMain uses internal/testhome; the real runtime and its framed
	// remote port additionally use private fixture directories, never user data.
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		http.Error(w, "preferences must never contact the provider", http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)
	var connectionIdentity string
	runtime := remoteJourneyRuntimeConfig(t, server.URL, func(cfg *session.Config) {
		cfg.AccountManager = provider.NewAccountManager(cfg.Accounts, nil)
		binding, err := cfg.SuggestionCompleter()
		if err != nil || binding.ConnectionIdentity == "" {
			t.Fatal("fixture could not bind its memory-only Errand connection")
		}
		connectionIdentity = binding.ConnectionIdentity
	})
	root := t.TempDir()
	rootIdentity, err := fileio.DirectoryIdentity(root)
	if err != nil || rootIdentity == "" {
		t.Fatal("fixture could not identify its native directory")
	}
	shell := newRemoteJourneyShell(t, runtime, root)
	_, resize := shell.app.Update(tea.WindowSizeMsg{Width: width, Height: height})
	runAppCommand(t, shell.app, resize)
	// Prepared presentation only: use the production settings opener and
	// existing category-navigation fixture. No preference value/authority edit.
	shell.app.openWorkspaceSettings()
	body := shell.app.settingsSurface(shell.app.overlayHost.Window(ui.OverlayWorkspaceSettings))
	if body == nil {
		t.Fatal("fixture could not mount Workspace Settings")
	}
	selectSettingsCategory(t, body, "Code Intelligence")
	shell.app.Input.SetInput(predictionSettingsPrivateDraft)
	m := &predictionSettingsVisualModel{
		App: shell.app, calls: &calls, rootIdentity: rootIdentity, connectionIdentity: connectionIdentity,
		settled: make(chan int, len(predictionSettingsVisualPhases)),
	}
	if err := m.focusMode(); err != nil {
		t.Fatal(err)
	}
	shell.app.paintShellFrame()
	options := []tea.ProgramOption{tea.WithWidthMethod(ansi.GraphemeWidth)}
	if headless {
		options = append(options, tea.WithInput(nil), tea.WithOutput(io.Discard),
			tea.WithoutRenderer(), tea.WithWindowSize(width, height))
	}
	program := tea.NewProgram(m, options...)
	m.SetProgram(program)
	pumpCtx, cancelPump := context.WithCancel(t.Context())
	pumpDone := make(chan struct{})
	go func() {
		defer close(pumpDone)
		for {
			select {
			case msg := <-shell.fifo:
				program.Send(msg)
			case <-pumpCtx.Done():
				return
			}
		}
	}()
	finished := make(chan struct{})
	driverDone := make(chan struct{})
	if headless {
		go func() {
			defer close(driverDone)
			// Same literal native sequence documented above; the only driver
			// barrier is the shared read-only F4 observer, never a mock ACK.
			groups := [][]tea.KeyPressMsg{
				{},
				{{Code: tea.KeyTab}, {Code: 'a', Mod: tea.ModCtrl}, {Code: '0', Text: "0"}, {Code: tea.KeyF2}, {Code: tea.KeyRight}},
				{{Code: tea.KeyF3}, {Code: tea.KeyEnter}},
				{
					{Code: tea.KeyF2}, {Code: tea.KeyTab}, {Code: 'a', Mod: tea.ModCtrl},
					{Code: tea.KeyBackspace}, {Code: '4', Text: "4"}, {Code: '2', Text: "2"},
					{Code: '0', Text: "0"}, {Code: '0', Text: "0"},
				},
				{{Code: tea.KeyF3}, {Code: tea.KeyEnter}},
				{{Code: tea.KeyF2}, {Code: tea.KeyLeft}},
				{{Code: tea.KeyF3}, {Code: tea.KeyEnter}},
			}
			for _, group := range groups {
				for _, key := range group {
					program.Send(key)
				}
				program.Send(tea.KeyPressMsg{Code: tea.KeyF4})
				select {
				case <-m.settled:
				case <-finished:
					return
				case <-t.Context().Done():
					return
				}
			}
			program.Send(tea.KeyPressMsg{Code: tea.KeyF9})
		}()
	} else {
		close(driverDone)
	}
	_, runErr := program.Run()
	close(finished)
	<-driverDone
	cancelPump()
	<-pumpDone
	m.StopAsyncIO()
	closeErr := m.CloseWorkspaceAttachment()
	if runErr != nil || closeErr != nil || m.failure != nil ||
		m.phase != len(predictionSettingsVisualPhases) || calls.Load() != 0 {
		t.Fatalf("prediction preferences journey: run=%v close=%v failure=%v observations=%d/7 provider_HTTP=%d",
			runErr, closeErr, m.failure, m.phase, calls.Load())
	}
	report := strings.Join(m.observations, "") +
		"PREDICTION_SETTINGS_VISUAL_SUCCESS; real core and remote App, native ordinary preference keys; suggestions remain Off during this preferences-only journey; NOT prediction-service/provider-request/budget-meter acceptance\n"
	t.Logf("prediction preferences: %d/7 phases, provider HTTP=0; suggestions remain Off during this preferences-only journey", m.phase)
	if result := os.Getenv("CRUDE_PREDICTION_SETTINGS_VISUAL_RESULT"); !headless && result != "" {
		if err := os.WriteFile(result, []byte(report), 0600); err != nil {
			t.Fatal(err)
		}
	}
}
