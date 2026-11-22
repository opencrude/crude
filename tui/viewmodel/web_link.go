package viewmodel

import (
	"context"
	"os"

	tea "charm.land/bubbletea/v2"

	"crude/internal/hostenv"
	"crude/tui/cell"
	"crude/tui/ui"
	"crude/workspace/fileio"
)

// Web links open through Crude, not through the terminal's own link handling:
// terminals differ (Ghostty needs Shift while Crude reads the mouse), so a
// click asks once and fileio.OpenBrowserURL starts the browser.

const webLinkOverlay ui.OverlayID = "web-link"

type webLinkPrompt struct {
	target string

	activate func(int) tea.Cmd
}

type webLinkOpenedMsg struct {
	generation uint64
	err        error
}

// webLinkMaxShown bounds the address in the prompt; the copy is complete.
const webLinkMaxShown = 240

func (m *App) openWebLink(target string) tea.Cmd {
	return m.confirmWebLink(target, fileio.OpenConfirmedWebURL)
}

func (m *App) confirmWebLink(target string, launch func(context.Context, string) error) tea.Cmd {
	link := cell.NewLink(target, "")
	if link == nil || link.File() || m.overlayHost == nil {
		return nil
	}
	parsed, err := fileio.WebURL(target)
	// Never start a desktop app on the far side of an SSH login.
	remote := hostenv.SSHSession(os.Getenv)
	open := !remote && err == nil
	gen := m.workspaceSwitchGen.Load()
	p := &webLinkPrompt{target: target}
	shown := target
	if runes := []rune(shown); len(runes) > webLinkMaxShown {
		shown = string(runes[:webLinkMaxShown]) + "…"
	}
	spec := ui.ConfirmFrameSpec{Title: "Open Link", Width: 72, Severity: ui.ConfirmPromptWarning}
	switch {
	case open:
		spec.Question = "Open this link in your browser?"
		spec.Detail = "Site: " + parsed.Host + "\n" + shown + "\nOpen links only from sources you trust."
		if parsed.Scheme == "http" {
			spec.Detail += "\nThis connection is not encrypted."
		}
		spec.Buttons = []ui.ConfirmButtonSpec{{Label: "Open link", ActionID: ui.ConfirmActionYes},
			{Label: "Copy link", ActionID: ui.ConfirmActionSave}, {Label: "Cancel", ActionID: ui.ConfirmActionCancel}}
	case remote:
		spec.Question = "Copy this link?"
		spec.Detail = shown + "\nCrude runs over SSH here. Paste the link into a browser on your computer."
		spec.Buttons = []ui.ConfirmButtonSpec{{Label: "Copy link", ActionID: ui.ConfirmActionSave}, {Label: "Cancel", ActionID: ui.ConfirmActionCancel}}
	default:
		spec.Question = "Copy this link?"
		spec.Detail = shown + "\nCrude cannot open this address directly. Copy the link to use it yourself."
		spec.Buttons = []ui.ConfirmButtonSpec{{Label: "Copy link", ActionID: ui.ConfirmActionSave}, {Label: "Cancel", ActionID: ui.ConfirmActionCancel}}
	}
	spec.FocusedIndex = len(spec.Buttons) - 1
	buttons := spec.Buttons
	p.activate = func(index int) tea.Cmd {
		if index < 0 || index >= len(buttons) || m.webLink != p || gen != m.workspaceSwitchGen.Load() {
			return nil
		}
		m.overlayHost.Close(webLinkOverlay)
		switch buttons[index].ActionID {
		case ui.ConfirmActionYes:
			return m.clientIOCommand(context.Background(), func(ctx context.Context) tea.Msg {
				return webLinkOpenedMsg{generation: gen, err: launch(ctx, p.target)}
			})
		case ui.ConfirmActionSave:
			return m.clipboardCopyCommand(p.target)
		}
		return nil
	}
	m.webLink = p
	frame := ui.BuildConfirmFrame(m.Theme, spec)
	m.openConfirmModal(webLinkOverlay, frame, p.activate, func() {
		if m.webLink == p {
			m.webLink = nil
		}
	})
	return nil
}

func (m *App) applyWebLinkOpened(msg webLinkOpenedMsg) {
	if msg.generation == m.workspaceSwitchGen.Load() && msg.err != nil && m.Feedback != nil {
		m.Feedback.NotifyError("Could not open the link in a browser. Copy the link and open it yourself.")
	}
}
