package viewmodel

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"

	tea "charm.land/bubbletea/v2"

	"crude/core/port"
	"crude/terminalvt"
	"crude/tui/screens/shellterm"
	"crude/tui/ui"
	roottypes "crude/types"
	"crude/workspace"
)

type terminalCreatedMsg struct {
	service    shellport.TerminalService
	generation uint64
	info       workspace.TerminalInfo
	lease      workspace.TerminalLease
	err        error
}
type terminalProjectionMsg struct {
	service    shellport.TerminalService
	generation uint64
	command    shellterm.Command
	projection workspace.TerminalProjection
	lease      workspace.TerminalLease
	err        error
}

// terminalClaim is why this shell asks for a terminal's input control.
type terminalClaim uint8

const (
	// claimMount takes free control when this shell shows a terminal. Its
	// refusal is silent: another window controls it and this shell views it.
	claimMount terminalClaim = iota + 1
	// claimInput takes free control for keys typed into a viewer. The keys
	// are sent after control is granted.
	claimInput
	// claimTakeOver is the explicit menu choice. It also replaces a controller
	// in another window.
	claimTakeOver
)

// claimTerminalInput asks for input control of a mounted interactive
// terminal. A request waits for the answer to an equal or stronger one; a
// stronger claim (typing, then the explicit takeover) is sent at once.
func (m *App) claimTerminalInput(id string, claim terminalClaim) tea.Cmd {
	screen := m.termScreens[id]
	if screen == nil || screen.ReadOnly() || screen.Exited() || m.terminalClosed[id] {
		return nil
	}
	if screen.Controlled() {
		return nil
	}
	if pending, ok := m.terminalClaims[id]; ok && claim <= pending {
		return nil
	}
	if m.terminalClaims == nil {
		m.terminalClaims = make(map[string]terminalClaim)
	}
	m.terminalClaims[id] = claim
	screen.ClaimStarted()
	kind := map[terminalClaim]shellterm.CommandKind{
		claimMount: shellterm.CommandTakeControl, claimInput: shellterm.CommandClaimInput, claimTakeOver: shellterm.CommandTakeOver,
	}[claim]
	cmd := m.terminalCommand(shellterm.Command{ID: id, Kind: kind})
	if cmd == nil {
		// No terminal service can answer: keep no claim and no held input.
		delete(m.terminalClaims, id)
		screen.ControlRefused()
	}
	return cmd
}

// claimCommand reports a request for input control.
func claimCommand(kind shellterm.CommandKind) bool {
	return kind == shellterm.CommandTakeControl || kind == shellterm.CommandClaimInput || kind == shellterm.CommandTakeOver
}

// applyTerminalClaim settles one refused input-control request. While a
// stronger request waits, its answer decides. A refusal drops the keys kept
// for the claim; only a claim the user caused explains the refusal.
func (m *App) applyTerminalClaim(id string, kind shellterm.CommandKind, err error) {
	answered := map[shellterm.CommandKind]terminalClaim{
		shellterm.CommandTakeControl: claimMount, shellterm.CommandClaimInput: claimInput, shellterm.CommandTakeOver: claimTakeOver,
	}[kind]
	screen := m.termScreens[id]
	if pending := m.terminalClaims[id]; pending > answered || screen == nil || screen.Controlled() {
		return
	}
	delete(m.terminalClaims, id)
	screen.ControlRefused()
	if answered == claimMount {
		return
	}
	if errors.Is(err, workspace.ErrTerminalControl) {
		m.notifyTerminalControlElsewhere(id)
		return
	}
	m.reportTerminalIOError("control", id, err)
}

// notifyTerminalControlElsewhere explains why typing here does nothing.
func (m *App) notifyTerminalControlElsewhere(id string) {
	if m.Feedback == nil {
		return
	}
	name := "This terminal"
	if info, ok := m.terminalInfo(id); ok {
		name = info.DisplayName()
	}
	m.Feedback.NotifyInfo(name + " gets input from another window. To type here, right-click it: Take input control.")
}

func (m *App) terminalService() shellport.TerminalService {
	if m.ws == nil {
		return nil
	}
	service, err := m.ws.EnsureTerminals()
	if err != nil {
		m.reportTerminalIOError("initialize", "", err)
		return nil
	}
	return service
}

func (m *App) terminalController() workspace.TerminalController {
	if m.terminalClientID == "" {
		m.terminalClientID = fmt.Sprintf("shell-%016x", rand.Uint64())
	}
	return workspace.TerminalController{ClientID: m.terminalClientID, Epoch: 1}
}

func (m *App) terminalPlacement(id string) workspace.TerminalPlacement {
	return m.terminalPlacements[id]
}
func (m *App) setTerminalPlacement(id string, value workspace.TerminalPlacement) {
	if m.terminalPlacements == nil {
		m.terminalPlacements = make(map[string]workspace.TerminalPlacement)
	}
	m.terminalPlacements[id] = value
}

func (m *App) terminalCommand(command shellterm.Command) tea.Cmd {
	service := m.terminalService()
	if service == nil {
		return nil
	}
	generation, owner := m.workspaceSwitchGen.Load(), m.terminalController()
	lease := m.terminalLeases[command.ID]
	command.Paste = append([]byte(nil), command.Paste...)
	viewport := shellterm.Command{Start: -1}
	if screen := m.termScreens[command.ID]; screen != nil {
		viewport = screen.ViewportRead()
	}
	if command.Kind != shellterm.CommandReadRows {
		command.Viewport = viewport.Viewport
	}
	if stream, ok := service.(shellport.StreamingTerminals); ok {
		stream.StartGrid(command.ID)
		if command.Kind != shellterm.CommandReadRows && !claimCommand(command.Kind) {
			input := workspace.TerminalInput{Lease: lease, Key: command.Key, Mouse: command.Mouse, Data: command.Paste, Focused: command.Focused, Cols: command.Cols, Rows: command.Rows}
			switch command.Kind {
			case shellterm.CommandKey:
				input.Kind = "key"
			case shellterm.CommandMouse:
				input.Kind = "mouse"
			case shellterm.CommandPaste:
				input.Kind = "paste"
			case shellterm.CommandFocus:
				input.Kind = "focus"
			case shellterm.CommandResize:
				input.Kind = "resize"
			case shellterm.CommandRename:
				input.Kind = "rename"
			case shellterm.CommandClear:
				input.Kind = "clear"
			}
			for i := 0; i < max(1, command.Repeat); i++ {
				if err := stream.SendInput(input); err != nil {
					if screen := m.termScreens[command.ID]; screen != nil && command.Kind == shellterm.CommandResize {
						screen.ResizeFailed()
					}
					m.reportTerminalIOError("input", command.ID, err)
					break
				}
				if command.Kind == shellterm.CommandKey {
					m.predictTerminalKey(command.ID, command.Key)
				}
			}
			return nil
		}
	}
	run := func() tea.Msg {
		result := terminalProjectionMsg{service: service, generation: generation, command: command}
		for i := 0; i < max(1, command.Repeat); i++ {
			switch command.Kind {
			case shellterm.CommandKey:
				result.err = service.Key(lease, command.Key)
			case shellterm.CommandMouse:
				result.err = service.Mouse(lease, command.Mouse)
			case shellterm.CommandPaste:
				result.err = service.Paste(lease, command.Paste)
			case shellterm.CommandFocus:
				result.err = service.Focus(lease, command.Focused)
			case shellterm.CommandResize:
				result.err = service.Resize(lease, command.Cols, command.Rows)
			case shellterm.CommandRename:
				result.err = service.Rename(lease, string(command.Paste))
			case shellterm.CommandClear:
				result.err = service.Clear(lease)
			case shellterm.CommandReadRows:
				result.projection, result.err = service.Rows(command.ID, terminalvt.RowRequest{Start: command.Start, Count: command.Count, ViewportStart: command.ViewportStart, HistoryEpoch: command.HistoryEpoch, HistoryBase: command.HistoryBase})
			case shellterm.CommandTakeControl:
				result.lease, result.err = service.Acquire(command.ID, owner)
			case shellterm.CommandTakeOver:
				result.lease, result.err = service.TakeOver(command.ID, owner)
			case shellterm.CommandClaimInput:
				result.lease, result.err = service.AcquireFromAbsent(command.ID, owner)
			default:
				result.err = fmt.Errorf("invalid terminal command")
			}
			if result.err != nil {
				break
			}
		}
		_, streaming := service.(shellport.StreamingTerminals)
		if result.err == nil && command.Kind != shellterm.CommandReadRows && !streaming {
			result.projection, result.err = service.Rows(command.ID, terminalvt.RowRequest{Start: viewport.Start, HistoryEpoch: viewport.HistoryEpoch, HistoryBase: viewport.HistoryBase})
		}
		return result
	}
	if command.Kind == shellterm.CommandReadRows {
		return run
	}
	// Admission happens on Update, not when Bubble Tea happens to execute a
	// command goroutine. One bounded FIFO preserves keystroke/paste/resize order.
	if len(m.terminalQueue) >= 256 {
		if screen := m.termScreens[command.ID]; screen != nil && command.Kind == shellterm.CommandResize {
			screen.ResizeFailed()
		}
		m.reportTerminalIOError("input", command.ID, workspace.ErrTerminalBackpressure)
		return nil
	}
	m.terminalQueue = append(m.terminalQueue, run)
	return m.nextTerminalCommand()
}

func (m *App) nextTerminalCommand() tea.Cmd {
	if m.terminalBusy || len(m.terminalQueue) == 0 {
		return nil
	}
	run := m.terminalQueue[0]
	m.terminalQueue[0] = nil
	m.terminalQueue = m.terminalQueue[1:]
	m.terminalBusy = true
	return run
}

func (m *App) terminalRefresh(id string) tea.Cmd {
	if stream, ok := m.ws.Terminals().(shellport.StreamingTerminals); ok {
		stream.StartGrid(id)
		return nil
	}
	if m.terminalReads == nil {
		m.terminalReads = make(map[string]bool)
		m.terminalDirty = make(map[string]bool)
	}
	if m.terminalReads[id] {
		m.terminalDirty[id] = true
		return nil
	}
	m.terminalReads[id] = true
	read := shellterm.Command{ID: id, Kind: shellterm.CommandReadRows, Start: -1}
	if screen := m.termScreens[id]; screen != nil {
		read = screen.ViewportRead()
	}
	return m.terminalCommand(read)
}

func (m *App) applyTerminalCreated(msg terminalCreatedMsg) tea.Cmd {
	if msg.service != m.ws.Terminals() || msg.generation != m.workspaceSwitchGen.Load() {
		return nil
	}
	if msg.err != nil {
		m.reportTerminalIOError("create", "", msg.err)
		return nil
	}
	info, present := m.terminalInfo(msg.info.ID)
	if !present {
		m.reportTerminalIOError("create", msg.info.ID, errors.New("created terminal is no longer in inventory"))
		return nil
	}
	if m.terminalLeases == nil {
		m.terminalLeases = make(map[string]workspace.TerminalLease)
	}
	m.terminalLeases[msg.info.ID] = msg.lease
	screen := m.ensureTermScreen(info.ID, info.Title)
	screen.SetControlled(true)
	if m.ws.LoadAppSettings().Terminal.Placement == roottypes.TerminalPlacementFloating {
		m.floatTerminalInfo(info)
	} else {
		m.dockTerminalInfo(info)
	}
	return m.terminalRefresh(msg.info.ID)
}

func (m *App) applyTerminalProjection(msg terminalProjectionMsg) tea.Cmd {
	if msg.service != m.ws.Terminals() || msg.generation != m.workspaceSwitchGen.Load() {
		return nil
	}
	mutating := msg.command.Kind != shellterm.CommandReadRows
	if mutating {
		m.terminalBusy = false
	}
	var nextInput tea.Cmd
	if mutating {
		nextInput = m.nextTerminalCommand()
	}
	id := msg.command.ID
	claim := claimCommand(msg.command.Kind)
	if m.terminalClosed[id] {
		delete(m.terminalClaims, id)
		return nextInput
	}
	if claim && msg.err != nil {
		m.applyTerminalClaim(id, msg.command.Kind, msg.err)
		return nextInput
	}
	if claim {
		delete(m.terminalClaims, id)
	}
	if _, streaming := msg.service.(shellport.StreamingTerminals); streaming && claim && msg.err == nil {
		if m.terminalLeases == nil {
			m.terminalLeases = make(map[string]workspace.TerminalLease)
		}
		m.terminalLeases[id] = msg.lease
		if screen := m.termScreens[id]; screen != nil {
			screen.SetControlled(true)
		}
		return tea.Batch(nextInput, m.drainTerminalCommands())
	}
	if msg.command.Kind == shellterm.CommandReadRows && msg.command.Sequence == 0 {
		delete(m.terminalReads, id)
	}
	if msg.err != nil {
		if screen := m.termScreens[id]; screen != nil {
			if msg.command.Kind == shellterm.CommandResize {
				screen.ResizeFailed()
			}
			if msg.command.Sequence != 0 {
				screen.RowsFailed(msg.command.Sequence)
			}
			if errors.Is(msg.err, workspace.ErrTerminalControl) {
				screen.SetControlled(false)
				delete(m.terminalLeases, id)
				m.notifyTerminalControlElsewhere(id)
				return nextInput
			}
		}
		m.reportTerminalIOError("command", id, msg.err)
		return nextInput
	}
	if m.ws.TermMgr() == nil {
		return nextInput
	}
	if _, exists := m.ws.TermMgr().Get(id); !exists && m.termScreens[id] == nil {
		return nextInput
	}
	screen := m.ensureTermScreen(id, msg.projection.Info.Title)
	if msg.lease.Generation != 0 {
		if m.terminalLeases == nil {
			m.terminalLeases = make(map[string]workspace.TerminalLease)
		}
		m.terminalLeases[id] = msg.lease
		screen.SetControlled(true)
	}
	staleViewport := !screen.AcceptsViewport(msg.command.Viewport)
	if staleViewport {
		if msg.command.Sequence != 0 {
			screen.RowsFailed(msg.command.Sequence)
		}
	} else if msg.command.Sequence != 0 {
		screen.SetRows(msg.command.Sequence, msg.projection)
	} else {
		screen.SetProjection(msg.projection)
	}
	m.applyTerminalTitle(id, msg.projection.Info.DisplayName())
	if fw := m.overlayHost.Window(ui.TerminalOverlayID(id)); fw != nil {
		fw.MarkContentDirty()
	}
	var next tea.Cmd
	if m.terminalDirty[id] || staleViewport {
		delete(m.terminalDirty, id)
		next = m.terminalRefresh(id)
	}
	return tea.Batch(nextInput, next, m.drainTerminalCommands())
}

func (m *App) applyTerminalFrame(msg shellport.TerminalFrameMsg) tea.Cmd {
	result := context.Canceled
	defer func() {
		if msg.Applied != nil {
			select {
			case msg.Applied <- result:
			default:
			}
		}
	}()
	if m.ws == nil || msg.Service != m.ws.Terminals() || m.terminalClosed[msg.ID] {
		return nil
	}
	if stream, ok := msg.Service.(shellport.StreamingTerminals); ok {
		if err := stream.ApplyTerminalFrame(&msg); err != nil {
			result = err
			return nil
		}
	}
	if msg.Err != nil {
		if screen := m.termScreens[msg.ID]; screen != nil {
			screen.ResizeFailed()
		}
		// An input failure rolls provisional ink back to the latest authority,
		// resolved on Update rather than captured by a racing transport worker.
		if msg.Projection.Info.ID != "" {
			if screen := m.termScreens[msg.ID]; screen != nil {
				screen.SetImmutableProjection(msg.Projection)
				if fw := m.overlayHost.Window(ui.TerminalOverlayID(msg.ID)); fw != nil {
					fw.MarkContentDirty()
				}
			}
		}
		if errors.Is(msg.Err, workspace.ErrTerminalControl) {
			// Another window took input control. This shell keeps showing the
			// terminal as a viewer and says how to type here again.
			delete(m.terminalLeases, msg.ID)
			if screen := m.termScreens[msg.ID]; screen != nil {
				screen.SetControlled(false)
			}
			m.notifyTerminalControlElsewhere(msg.ID)
			result = msg.Err
			return nil
		}
		m.reportTerminalIOError("stream", msg.ID, msg.Err)
		result = msg.Err
		return nil
	}
	screen := m.ensureTermScreen(msg.ID, msg.Projection.Info.DisplayName())
	// SDK stream publications (including authoritative prediction rollbacks)
	// retain immutable rows for their lifetime; do not clone the full grid again.
	screen.SetImmutableProjection(msg.Projection)
	if msg.Prediction != nil {
		screen.SetPrediction(*msg.Prediction)
	}
	m.applyTerminalTitle(msg.ID, msg.Projection.Info.DisplayName())
	if fw := m.overlayHost.Window(ui.TerminalOverlayID(msg.ID)); fw != nil {
		fw.MarkContentDirty()
	}
	result = nil
	return m.drainTerminalCommands()
}

func (m *App) drainTerminalCommands() tea.Cmd {
	var commands []tea.Cmd
	for id, screen := range m.termScreens {
		if stream, ok := m.ws.Terminals().(shellport.StreamingTerminals); ok {
			viewport := screen.ViewportRead()
			stream.SetViewport(id, terminalvt.RowRequest{Start: viewport.Start, HistoryEpoch: viewport.HistoryEpoch, HistoryBase: viewport.HistoryBase})
		}
		for _, command := range screen.TakePendingCommands() {
			commands = append(commands, m.terminalCommand(command))
		}
		commands = append(commands, screen.TakePendingCopy())
	}
	return tea.Batch(commands...)
}

func (m *App) handleTerminalEffects(msg workspace.TerminalEffectsMsg) {
	if msg.Effects.Bell {
		m.markTerminalBell(msg.ID)
	}
	// A child cannot write every attached shell's system clipboard. The offer
	// remains inert unless the user explicitly copies it from this shell.
	if len(msg.Effects.Clipboard) > 0 && m.Feedback != nil {
		if m.terminalLeases[msg.ID].Generation == 0 {
			return
		}
		if m.terminalClipboard == nil {
			m.terminalClipboard = make(map[string]string)
		}
		m.terminalClipboard[msg.ID] = string(msg.Effects.Clipboard[len(msg.Effects.Clipboard)-1])
		m.Feedback.NotifyInfo("Terminal offered clipboard text; use Copy offered text in its menu")
	}
}

// currentTerminalID is the terminal the user works with: the focused window,
// the docked terminal, a terminal tab, else the last one used in this shell.
func (m *App) currentTerminalID() string {
	if m.windowManager != nil {
		if fw := m.windowManager.Focused(); fw != nil {
			if id, ok := ui.TerminalIDFromOverlay(ui.OverlayID(fw.ID)); ok && m.termScreens[id] != nil {
				return id
			}
		}
	}
	if m.termDock != nil && m.termDock.TerminalActive() {
		if id := m.termDock.ActiveID(); id != "" {
			return id
		}
	}
	if screen := m.mainTerminalOwnsKeys(); screen != nil {
		return screen.TermID()
	}
	if m.termScreens[m.lastTerminalID] != nil {
		return m.lastTerminalID
	}
	return ""
}

// openCurrentTerminalMenu is the keyboard path to the terminal's right-click
// menu (Command Palette > Terminal Actions).
func (m *App) openCurrentTerminalMenu() {
	id := m.currentTerminalID()
	if id == "" {
		if m.Feedback != nil {
			m.Feedback.NotifyInfo("No terminal is open. Open one with Alt+F12.")
		}
		return
	}
	m.openTerminalControlMenu(id)
}

func (m *App) openTerminalControlMenu(id string) {
	// Keyboard access anchors to the terminal, never the middle of the shell.
	mouse := tea.Mouse{}
	if s := m.termScreens[id]; s != nil && m.rootHost != nil {
		if r, ok := m.rootHost.ScreenRect(s.ID()); ok {
			mouse.X, mouse.Y = r.X, r.Y
		}
	}
	m.openTerminalContextMenu(id, mouse)
}

func (m *App) openTerminalContextMenu(id string, mouse tea.Mouse) {
	m.openContextMenuState(mouse, m.terminalContextMenu(id))
}

func (m *App) terminalContextMenu(id string) *ui.ContextMenuState {
	screen := m.termScreens[id]
	if screen == nil || m.terminalClosed[id] {
		return nil
	}
	owner, generation := m.ws, m.workspaceSwitchGen.Load()
	current := func() bool {
		return m.ws == owner && generation == m.workspaceSwitchGen.Load() && m.termScreens[id] == screen && !m.terminalClosed[id]
	}
	guard := func(fn func()) func() {
		return func() {
			if current() {
				fn()
			}
		}
	}
	items := []ui.ContextMenuItem{
		{Label: "Copy", Detail: "Ctrl+Shift+C", Disabled: !screen.HasSelection(), Action: guard(func() {
			m.runDetachedAsyncCmd(tea.Batch(screen.CopySelection(), m.drainTerminalCommands()))
		})},
		{Label: "Paste", Detail: "Ctrl+Shift+V", Disabled: !screen.PasteAllowed(), Action: guard(func() {
			m.runDetachedAsyncCmd(m.pasteTerminalClipboard(screen))
		})},
		{Label: "Select all", Detail: "Ctrl+Shift+A", Action: guard(func() { screen.SelectAll(); m.forceFrame(); m.runDetachedAsyncCmd(m.drainTerminalCommands()) })},
		{Label: "Clear terminal", Disabled: !screen.PasteActive(), Action: guard(func() {
			m.runDetachedAsyncCmd(m.terminalCommand(shellterm.Command{ID: id, Kind: shellterm.CommandClear}))
		})},
	}
	if screen.ReadOnly() {
		items = []ui.ContextMenuItem{items[0], items[2], {Label: "Close output", Action: guard(func() { m.closeTerminal(id) })}}
		return &ui.ContextMenuState{Title: screen.Title(), Sections: []ui.ContextMenuSection{{Items: items}}}
	}
	if !screen.Controlled() {
		items = append(items, ui.ContextMenuItem{Label: "Take input control", Action: guard(func() {
			m.runDetachedAsyncCmd(m.claimTerminalInput(id, claimTakeOver))
		})})
	}
	if text := m.terminalClipboard[id]; text != "" {
		items = append(items, ui.ContextMenuItem{Label: "Copy offered text", Action: guard(func() { m.copyToClipboard(text) })})
	}
	manage := []ui.ContextMenuItem{
		{Label: "New terminal", Action: guard(func() { m.runDetachedAsyncCmd(m.openNewTerminal()) })},
		{Label: "Rename terminal", Disabled: !screen.PasteActive(), Action: guard(func() {
			lease := m.terminalLeases[id]
			m.openTextPrompt(ui.TextPromptRename, "Rename terminal", "Terminal name", screen.Title(), func(name string) error {
				if !current() || m.terminalLeases[id] != lease {
					return errors.New("terminal changed; open Rename terminal again")
				}
				if err := workspace.ValidateTerminalName(name); err != nil {
					return err
				}
				m.runDetachedAsyncCmd(m.terminalCommand(shellterm.Command{ID: id, Kind: shellterm.CommandRename, Paste: []byte(name)}))
				return nil
			})
		})},
		{Label: "Open as tab", Action: guard(func() { m.openTerminalAsTab(id) })},
		{Label: "Open as window", Action: guard(func() { m.promoteTerminal(id) })},
		{Label: "Move to panel", Action: guard(func() { m.dockTerminal(id) })},
		{Label: "Kill terminal", Action: guard(func() { m.closeTerminal(id) })},
	}
	return &ui.ContextMenuState{Title: screen.Title(), Sections: []ui.ContextMenuSection{{Items: items}, {Items: manage}}}
}

type terminalClipboardTarget struct{ *shellterm.Screen }

func (t terminalClipboardTarget) PasteActive() bool { return t.PasteAllowed() }

func (m *App) pasteTerminalClipboard(screen *shellterm.Screen) tea.Cmd {
	id := screen.TermID()
	owner, generation, lease := m.ws, m.workspaceSwitchGen.Load(), m.terminalLeases[id]
	return m.beginNativeClipboardPaste(terminalClipboardTarget{screen}, nil, func(text string) tea.Cmd {
		if m.ws != owner || generation != m.workspaceSwitchGen.Load() || m.termScreens[id] != screen ||
			m.terminalLeases[id] != lease || !screen.PasteAllowed() {
			return nil
		}
		screen.PasteText(ui.PastedLineBreaks(text))
		if !screen.Controlled() {
			// A viewer keeps the paste and asks for free input control.
			return m.claimTerminalInput(id, claimInput)
		}
		return m.drainTerminalCommands()
	})
}
