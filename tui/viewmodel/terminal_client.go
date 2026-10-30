package viewmodel

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"

	tea "charm.land/bubbletea/v2"

	"crude/shellport"
	"crude/terminalvt"
	"crude/tui/rendering"
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
		if command.Kind != shellterm.CommandReadRows && command.Kind != shellterm.CommandTakeControl {
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
			}
			for i := 0; i < max(1, command.Repeat); i++ {
				if err := stream.SendInput(input); err != nil {
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
			case shellterm.CommandReadRows:
				result.projection, result.err = service.Rows(command.ID, terminalvt.RowRequest{Start: command.Start, Count: command.Count, ViewportStart: command.ViewportStart, HistoryEpoch: command.HistoryEpoch, HistoryBase: command.HistoryBase})
			case shellterm.CommandTakeControl:
				result.lease, result.err = service.Acquire(command.ID, owner)
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
	if m.terminalClosed[id] {
		return nextInput
	}
	if _, streaming := msg.service.(shellport.StreamingTerminals); streaming && msg.command.Kind == shellterm.CommandTakeControl && msg.err == nil {
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
			if msg.command.Sequence != 0 {
				screen.RowsFailed(msg.command.Sequence)
			}
			if errors.Is(msg.err, workspace.ErrTerminalControl) {
				screen.SetControlled(false)
				delete(m.terminalLeases, id)
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
	m.applyTerminalTitle(id, msg.projection.Info.Title)
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
			delete(m.terminalLeases, msg.ID)
			if screen := m.termScreens[msg.ID]; screen != nil {
				screen.SetControlled(false)
			}
		}
		m.reportTerminalIOError("stream", msg.ID, msg.Err)
		result = msg.Err
		return nil
	}
	screen := m.ensureTermScreen(msg.ID, msg.Projection.Info.Title)
	// SDK stream publications (including authoritative prediction rollbacks)
	// retain immutable rows for their lifetime; do not clone the full grid again.
	screen.SetImmutableProjection(msg.Projection)
	m.applyTerminalTitle(msg.ID, msg.Projection.Info.Title)
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
	if msg.Effects.TitleChanged {
		m.applyTerminalTitle(msg.ID, msg.Effects.Title)
	}
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

func (m *App) openTerminalControlMenu(id string) {
	items := []ui.ContextMenuItem{{Label: "Take input control", Action: func() {
		m.runDetachedAsyncCmd(m.terminalCommand(shellterm.Command{ID: id, Kind: shellterm.CommandTakeControl}))
	}}}
	if text := m.terminalClipboard[id]; text != "" {
		items = append(items, ui.ContextMenuItem{Label: "Copy offered text", Action: func() { m.runDetachedAsyncCmd(rendering.CopyToClipboardCmd(text)) }})
	}
	m.openContextMenuState(tea.Mouse{X: max(0, m.width/2), Y: max(0, m.height/2)}, &ui.ContextMenuState{Title: "Terminal control", Sections: []ui.ContextMenuSection{{Items: items}}})
}
