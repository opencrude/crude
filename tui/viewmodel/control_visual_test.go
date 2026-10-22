package viewmodel

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"crude/core"
	"crude/coreclient"
	"crude/coreprotocol"
	"crude/shellport"
	settingsui "crude/tui/settings"
	"crude/tui/ui"
	"crude/types/control"
)

// Build: go test -c ./tui/viewmodel -o /tmp/control-visual.test
// Run in the dedicated capture terminal with CRUDE_VISUAL_CONTROL=1 and
// -test.run=^TestControlVisualAcceptance$. F2: editor, F3: PTY, F4: saved
// workspace settings, F9: verify/exit. No desktop input is owned by this test.
// CRUDE_CONTROL_VISUAL_RESULT is an optional absolute evidence-file path.
func TestControlVisualAcceptance(t *testing.T) {
	if os.Getenv("CRUDE_VISUAL_CONTROL") != "1" {
		t.Skip("dedicated sequential terminal capture")
	}
	runControlVisual(t, false, 0, 0)
}

// The same tea.Program and framed client run without a physical terminal. This
// checks routing/lifecycle, not visual desktop acceptance.
func TestControlVisualHarnessHeadless(t *testing.T) {
	for _, size := range [][2]int{{160, 40}, {80, 24}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) { runControlVisual(t, true, size[0], size[1]) })
	}
}

func runControlVisual(t *testing.T, headless bool, width, height int) {
	t.Helper()
	t.Setenv("CRUDE_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	runtime := remoteJourneyRuntime(t, "")
	root := t.TempDir()
	path := filepath.Join(root, "control-guide.txt")
	if err := os.WriteFile(filepath.Join(root, "welcome.txt"), []byte("Control acceptance workspace\n\nF2: connected editor and split\nF3: retained PTY\nF4: saved workspace settings\nF9: verify and exit\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("CONTROL READY\n\nThis file was opened by its original framed control request.\nThe retained core owns the document; this shell paints the replica.\n\nF2  Hide tree, split right, open this file\nF3  Mount one real PTY, resize, hide and show it\nF4  Discover settings, edit Tab size, Save and inspect persistence\nF9  Verify completed milestones and exit\n"), 0600); err != nil {
		t.Fatal(err)
	}
	shell := newRemoteJourneyShell(t, runtime, root)
	m := &controlVisualModel{App: shell.app, runtime: runtime, root: root, path: path, completed: map[int]bool{}, ctx: t.Context()}
	t.Cleanup(func() {
		if m.client != nil {
			if err := m.client.Close(); err != nil {
				t.Error(err)
			}
		}
	})
	opts := []tea.ProgramOption{tea.WithWidthMethod(ansi.GraphemeWidth)}
	m.settled = make(chan struct{}, 4)
	if headless {
		opts = append(opts, tea.WithInput(nil), tea.WithOutput(io.Discard), tea.WithoutRenderer(), tea.WithWindowSize(width, height))
	}
	program := tea.NewProgram(m, opts...)
	m.program = program
	m.SetProgram(program)
	// Preserve any already queued replica messages; all installation now goes
	// through the running Program, never the synchronous test update helper.
	pumpCtx, stopPump := context.WithCancel(t.Context())
	defer stopPump()
	go func() {
		for {
			select {
			case msg := <-shell.fifo:
				program.Send(msg)
			case <-pumpCtx.Done():
				return
			}
		}
	}()
	m.ws.(shellport.ShellControl).StartShellControl()
	if headless {
		go func() {
			for _, key := range []rune{tea.KeyF2, tea.KeyF3, tea.KeyF5, tea.KeyF4} {
				program.Send(tea.KeyPressMsg{Code: key})
				select {
				case <-m.settled:
				case <-t.Context().Done():
					return
				}
			}
			program.Send(tea.KeyPressMsg{Code: tea.KeyF9})
		}()
	}
	if _, err := program.Run(); err != nil {
		t.Fatal(err)
	}
	stopPump()
	if m.failure != nil {
		t.Fatal(m.failure)
	}
	for _, key := range []int{2, 3, 4, 5} {
		if !m.completed[key] {
			t.Errorf("F%d milestone not completed", key)
		}
	}
	if m.rootHost.HasPointerCapture() {
		t.Error("pointer capture remained armed")
	}
	if m.client != nil {
		if err := m.client.Close(); err != nil {
			t.Error(err)
		}
	}
	// Match the product's UI teardown before closing the retained runtime (the
	// shared fixture registers an idempotent backup cleanup).
	m.StopAsyncIO()
	if err := m.CloseWorkspaceAttachment(); err != nil {
		t.Error(err)
	}
	if !t.Failed() {
		if resultPath := os.Getenv("CRUDE_CONTROL_VISUAL_RESULT"); resultPath != "" {
			if err := os.WriteFile(resultPath, []byte("CONTROL_VISUAL_SUCCESS F2 F3 F5 F4; framed receipts, mounted editor, PTY opened and closed, persisted workspace tab_size=8\n"), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
}

type controlVisualDone struct {
	key int
	err error
}
type controlVisualCheck struct {
	check func(*controlVisualModel) error
	reply chan error
}
type controlVisualModel struct {
	*App
	runtime              *core.Runtime
	program              *tea.Program
	client               *coreclient.Client
	ctx                  context.Context
	root, path, terminal string
	seq                  int
	busy                 bool
	completed            map[int]bool
	failure              error
	settled              chan struct{}
}

func (m *controlVisualModel) Init() tea.Cmd { return m.App.Init() }
func (m *controlVisualModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch v := msg.(type) {
	case controlVisualCheck:
		v.reply <- v.check(m)
		return m, nil
	case controlVisualDone:
		m.busy = false
		if v.err != nil {
			m.failure = v.err
			return m, tea.Quit
		}
		m.completed[v.key] = true
		m.settled <- struct{}{}
		return m, nil
	case tea.KeyPressMsg:
		key := 0
		switch v.Code {
		case tea.KeyF2:
			key = 2
		case tea.KeyF3:
			key = 3
		case tea.KeyF4:
			key = 4
		case tea.KeyF5:
			key = 5
		case tea.KeyF9:
			if !m.busy {
				return m, tea.Quit
			}
			return m, nil
		}
		if key != 0 {
			if m.busy || m.completed[key] {
				return m, nil
			}
			m.busy = true
			return m, func() tea.Msg { return controlVisualDone{key, m.journey(key)} }
		}
	}
	_, cmd := m.App.Update(msg)
	return m, cmd
}

// Workers may only query shell state through Update. This also forces a fresh
// mounted frame before checking painted text; no race with the real renderer.
func (m *controlVisualModel) check(fn func(*controlVisualModel) error) error {
	reply := make(chan error, 1)
	m.program.Send(controlVisualCheck{fn, reply})
	select {
	case err := <-reply:
		return err
	case <-m.ctx.Done():
		return m.ctx.Err()
	}
}

func (m *controlVisualModel) connect() error {
	if m.client != nil {
		return nil
	}
	server, client := net.Pipe()
	go func() { _ = core.ServeConnection(m.ctx, m.runtime, server) }()
	ctx, cancel := context.WithTimeout(m.ctx, 10*time.Second)
	defer cancel()
	c, err := coreclient.Dial(ctx, client, "visual-controller")
	if err != nil {
		return err
	}
	m.client = c
	// Like the product MCP adapter, this controller is not a replica: drain
	// unsolicited workspace changes so a busy workspace cannot overflow it.
	go func() {
		for range c.Events() {
		}
	}()
	if err := c.Call(ctx, "", coreprotocol.Attach, coreprotocol.AttachRequest{Root: m.root}, nil); err != nil {
		return err
	}
	var target control.Target
	if err := m.check(func(m *controlVisualModel) error {
		target = m.ws.(shellport.ShellControl).ControlIdentity()
		return nil
	}); err != nil {
		return err
	}
	// Shell registration is asynchronous product startup. Observe it, never
	// retry a mutation or manufacture a registered shell.
	for {
		var shells []coreprotocol.ControlShellInfo
		if err := c.Call(ctx, target.WorkspaceID, coreprotocol.ControlShells, struct{}{}, &shells); err != nil {
			return err
		}
		for _, shell := range shells {
			if shell.Owner == *target.Shell {
				return c.Call(ctx, target.WorkspaceID, coreprotocol.ControlClientRegister, coreprotocol.ControlClientGrant{Effects: []control.Effect{control.Read, control.Write, control.Execute, control.Delete}, Shell: target.Shell}, nil)
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func (m *controlVisualModel) execute(id string, input any, adjust func(*control.Request)) (control.Result, error) {
	var target control.Target
	if err := m.check(func(m *controlVisualModel) error {
		target = m.ws.(shellport.ShellControl).ControlIdentity()
		m.View()
		return nil
	}); err != nil {
		return control.Result{}, err
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return control.Result{}, err
	}
	m.seq++
	r := control.Request{ID: fmt.Sprintf("visual-control-%d", m.seq), CommandID: id, Target: target, Caller: control.Caller{Kind: control.ExternalCaller, ClientID: m.client.Owner().ClientID}, Input: raw}
	if adjust != nil {
		adjust(&r)
	}
	payload, err := json.Marshal(r)
	if err != nil {
		return control.Result{}, err
	}
	ctx, cancel := context.WithTimeout(m.ctx, 15*time.Second)
	defer cancel()
	response, err := m.client.CallRequest(ctx, coreprotocol.Request{ID: r.ID, Owner: m.client.Owner(), WorkspaceID: target.WorkspaceID, Method: coreprotocol.ControlExecute, Payload: payload})
	if err != nil {
		return control.Result{}, err
	}
	if response.Error != nil {
		return control.Result{}, response.Error
	}
	var result control.Result
	if err := json.Unmarshal(response.Payload, &result); err != nil {
		return result, err
	}
	if result.State != control.Applied && result.State != control.Committed {
		return result, fmt.Errorf("%s: %+v", id, result)
	}
	if result.RequestID != r.ID {
		return result, fmt.Errorf("%s lost original request identity", id)
	}
	return result, nil
}

func (m *controlVisualModel) control(suffix string, op ui.ControlOperation) error {
	cursor, id := "", ""
	for {
		result, err := m.execute("shell.controls", control.PageRequest{Limit: 128, Cursor: cursor}, nil)
		if err != nil {
			return err
		}
		var page control.Page
		if err := json.Unmarshal(result.Output, &page); err != nil {
			return err
		}
		for _, node := range page.Nodes {
			if strings.HasSuffix(node.ID, "::"+suffix) {
				id = node.ID
			}
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	if id == "" {
		return fmt.Errorf("missing mounted control %s", suffix)
	}
	intent := shellControlIntent{ComponentID: id, Operation: op}
	result, err := m.execute("shell.describe_control", intent, nil)
	if err != nil {
		return err
	}
	var described struct {
		Descriptor control.Descriptor `json:"descriptor"`
		Node       control.Node       `json:"node"`
	}
	if err := json.Unmarshal(result.Output, &described); err != nil {
		return err
	}
	intent.ExpectedAction = described.Descriptor.ID
	_, err = m.execute("shell.control", intent, func(r *control.Request) {
		r.Target.ResourceID = id
		r.Preconditions = control.Preconditions{Revision: &described.Node.Revision, OpenEpoch: &described.Node.OpenEpoch}
	})
	return err
}

func (m *controlVisualModel) journey(key int) error {
	if err := m.connect(); err != nil {
		return err
	}
	do := func(id string, in any) error { _, err := m.execute(id, in, nil); return err }
	switch key {
	case 2:
		// Workspace home has no mounted main group geometry. Establish its real
		// editor entry point before exercising hide -> split -> open.
		if err := do("file.open", map[string]any{"path": filepath.Join(m.root, "welcome.txt")}); err != nil {
			return err
		}
		if err := do("tree.set_visible", map[string]any{"visible": false}); err != nil {
			return err
		}
		if err := m.check(func(m *controlVisualModel) error {
			m.View()
			if r := m.ensureMainPanes().group(m.Win.ActiveGroup()).Bounds(); r.W < 49 {
				return fmt.Errorf("split prerequisite: shell %dx%d group %+v", m.width, m.height, r)
			}
			return nil
		}); err != nil {
			return err
		}
		if err := do("pane.split", map[string]any{"direction": "right"}); err != nil {
			return err
		}
		result, err := m.execute("file.open", map[string]any{"path": m.path}, nil)
		if err != nil {
			return err
		}
		if len(result.ResourceIDs) != 2 {
			return fmt.Errorf("file lacks exact tab/document receipt: %+v", result)
		}
		return m.check(func(m *controlVisualModel) error {
			paint := ansi.Strip(m.View().Content)
			if m.Tree.Visible || len(m.Win.GroupIDs()) != 2 || m.EditorState.Editor == nil || m.EditorState.Editor.FilePath() != m.path || !strings.Contains(paint, "CONTROL READY") {
				return fmt.Errorf("editor/tree/split did not mount and paint; tree=%t groups=%d editor=%t frame=%dx%d:\n%s", m.Tree.Visible, len(m.Win.GroupIDs()), m.EditorState.Editor != nil, m.width, m.height, paint)
			}
			return nil
		})
	case 3:
		result, err := m.execute("terminal.open", struct{}{}, nil)
		if err != nil {
			return err
		}
		if len(result.ResourceIDs) != 1 {
			return fmt.Errorf("terminal lacks exact identity: %+v", result)
		}
		m.terminal = result.ResourceIDs[0]
		if err := do("terminal.resize_dock", map[string]any{"rows": 8}); err != nil {
			return err
		}
		if err := do("terminal.hide", struct{}{}); err != nil {
			return err
		}
		if err := m.check(func(m *controlVisualModel) error {
			m.View()
			if m.termDock.IsOpen() {
				return fmt.Errorf("hidden terminal still mounted")
			}
			return nil
		}); err != nil {
			return err
		}
		if err := do("terminal.show", map[string]any{"id": m.terminal}); err != nil {
			return err
		}
		return m.check(func(m *controlVisualModel) error {
			paint := ansi.Strip(m.View().Content)
			if len(m.termMgr().List()) != 1 || m.termScreens[m.terminal] == nil || !m.termDockVisible() || !strings.Contains(paint, "Terminal") {
				return fmt.Errorf("one usable terminal did not mount/paint")
			}
			return nil
		})
	case 5:
		if m.terminal == "" {
			return fmt.Errorf("open a terminal before close acceptance")
		}
		if err := do("terminal.close", map[string]any{"id": m.terminal}); err != nil {
			return err
		}
		// Observe replica presentation; never replay the committed close.
		deadline := time.Now().Add(5 * time.Second)
		for {
			closed := false
			if err := m.check(func(m *controlVisualModel) error {
				m.View()
				closed = len(m.termMgr().List()) == 0 && m.termScreens[m.terminal] == nil && !m.termDockVisible()
				return nil
			}); err != nil {
				return err
			}
			if closed {
				return nil
			}
			if time.Now().After(deadline) {
				return fmt.Errorf("committed terminal close did not unmount")
			}
			time.Sleep(10 * time.Millisecond)
		}
	case 4:
		if err := do("settings.open", map[string]any{"scope": "workspace"}); err != nil {
			return err
		}
		search := func() error {
			if err := m.control(string(settingsui.WorkspaceSettingsSearchID), ui.ControlOperation{Verb: "set_value", Value: json.RawMessage(`"Editor"`)}); err != nil {
				return err
			}
			return m.control(string(settingsui.WorkspaceSettingsSearchID), ui.ControlOperation{Verb: "activate"})
		}
		if err := search(); err != nil {
			return err
		}
		if err := m.control("workspace-settings/form/tab-size", ui.ControlOperation{Verb: "set_value", Value: json.RawMessage(`"8"`)}); err != nil {
			return err
		}
		if err := m.check(func(m *controlVisualModel) error {
			paint := ansi.Strip(m.View().Content)
			if !strings.Contains(paint, "Tab size") || !strings.Contains(paint, "8") {
				return fmt.Errorf("edited ordinary field not painted")
			}
			return nil
		}); err != nil {
			return err
		}
		if err := m.control(string(settingsui.WorkspaceSettingsSaveID), ui.ControlOperation{Verb: "activate"}); err != nil {
			return err
		}
		result, err := m.execute("settings.get", map[string]any{"scope": "workspace"}, nil)
		if err != nil {
			return err
		}
		var saved coreprotocol.SettingsFetchResult
		if err := json.Unmarshal(result.Output, &saved); err != nil {
			return err
		}
		if saved.Effective.Editor.TabSize != 8 || !strings.Contains(string(saved.Overrides.Editor), `"tab_size":8`) {
			return fmt.Errorf("Save did not persist effective workspace tab size: %s", result.Output)
		}
		// Save keeps Settings open; Close (clean, so agent-permitted) leaves.
		if err := m.control(string(settingsui.WorkspaceSettingsCancelID), ui.ControlOperation{Verb: "activate"}); err != nil {
			return err
		}
		// Leave a real freshly loaded settings surface visible for capture, not a
		// synthetic success overlay. Its value is read back from the core owner.
		if err := do("settings.open", map[string]any{"scope": "workspace"}); err != nil {
			return err
		}
		return search()
	}
	return fmt.Errorf("unknown visual milestone")
}
