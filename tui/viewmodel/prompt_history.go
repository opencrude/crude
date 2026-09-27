package viewmodel

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/history"
	"crude/tui/input"
	"crude/tui/screens/statusoverview"
	"crude/tui/ui"
	"crude/tui/wm"
	"crude/workspace"
)

type promptHistorySavedMsg struct {
	store *history.Store
	err   error
}

type promptHistoryRestoredMsg struct {
	session    *workspace.Session
	store      *workspace.ReferenceStore
	generation uint64
	draft      string
	text       string
	refIDs     []string
	err        error
}

func (m *App) recallPrompt(event input.HistoryRecallEvent) tea.Cmd {
	if len(event.Entry.Attachments) == 0 {
		m.Input.FinishHistoryRecall(event.Generation, event.Entry.Text, nil, true)
		m.persistComposerDraftIfChanged()
		return nil
	}
	owner, store, draft := m.ActiveSession, m.RefStore(), m.Input.Value()
	if store == nil {
		m.Input.FinishHistoryRecall(event.Generation, "", nil, false)
		m.Feedback.NotifyError("Open an agent session to restore attachments")
		return nil
	}
	return func() tea.Msg {
		text, ids, err := store.RestorePrompt(event.Entry)
		return promptHistoryRestoredMsg{session: owner, store: store, generation: event.Generation, draft: draft, text: text, refIDs: ids, err: err}
	}
}

func (m *App) applyPromptHistory(msg promptHistoryRestoredMsg) {
	if msg.session != m.ActiveSession || msg.store != m.RefStore() || !m.Input.HistoryRecallCurrent(msg.generation) {
		return
	}
	if msg.draft != m.Input.Value() {
		m.Input.FinishHistoryRecall(msg.generation, "", nil, false)
		return
	}
	if !m.Input.FinishHistoryRecall(msg.generation, msg.text, msg.refIDs, msg.err == nil) {
		return
	}
	if msg.err != nil {
		m.Feedback.NotifyError("Could not restore prompt: " + msg.err.Error())
		return
	}
	m.persistComposerDraftIfChanged()
}

func (m *App) openPromptHistory() {
	if m.overlayHost == nil || m.Prompts == nil {
		return
	}
	entries := m.Prompts.Entries()
	items := make([]statusoverview.Item, 0, len(entries))
	for i := len(entries) - 1; i >= 0; i-- {
		entry := entries[i]
		attachmentIDs := make(map[string]bool)
		unavailable := false
		lines := []statusoverview.DetailLine{{Label: "Prompt", Value: entry.Text}}
		for index, attachment := range entry.Attachments {
			ref := attachment.Reference
			if attachmentIDs[ref.ID] {
				continue
			}
			attachmentIDs[ref.ID] = true
			value, role := ref.DisplayName()+" · "+ref.Title+" · "+ref.Mime, ui.ColorMuted
			if attachment.Unavailable {
				unavailable = true
				value += " (unavailable)"
				role = ui.ColorError
			}
			action := ""
			if !attachment.Unavailable && ref.FilePath != "" {
				action = "prompt:attachment:" + entry.ID + ":" + strconv.Itoa(index)
			}
			lines = append(lines, statusoverview.DetailLine{Label: "Attachment", Value: value, Role: role, Action: action})
		}
		kind := "Text"
		if len(attachmentIDs) > 0 {
			kind = fmt.Sprintf("%d attached", len(attachmentIDs))
		}
		if unavailable {
			kind = "Missing media"
		}
		items = append(items, statusoverview.Item{
			ID: entry.ID, Label: time.Unix(entry.LastSeen, 0).Local().Format("Jan 02 15:04"),
			Context: strings.Join(strings.Fields(entry.Text), " "), Summary: kind,
			Lines: lines, PrimaryAction: "prompt:restore:" + entry.ID,
			PrimaryLabel: "Use in composer", PrimaryEnabled: true,
		})
	}
	if len(items) == 0 {
		items = append(items, statusoverview.Item{ID: "empty", Label: "No prompt history yet. Sent prompts and attachments appear here.", Header: true})
	}
	screen := statusoverview.NewStats(m.Theme, statusoverview.Projection{Active: statusoverview.ViewTabs, Views: map[statusoverview.ViewID][]statusoverview.Item{statusoverview.ViewTabs: items}})
	screen.SetSearchPlaceholder("Search prompts and attachments")
	w, h := fitFloatingSize(m.width, m.height, 112, 24, 40, 10)
	frame := ui.NewWindowFrameWidget(m.Theme, "prompt-history-frame", ui.FrameWindow, "Prompt History", screen, nil, true, w, h)
	screen.SetFocusHandler(frame.Focus)
	frame.Focus(statusoverview.SearchID)
	win := wm.NewFloatingWindow(string(ui.OverlayPromptHistory), "Prompt History", frame, w, h,
		wm.WithDecoration(true), wm.WithMovable(true), wm.WithResizable(true), wm.WithClosable(true), wm.WithFocusable(true), wm.WithModal(true))
	wm.CenterOnScreen(win, m.width, m.height)
	m.overlayHost.Open(ui.OverlayPromptHistory, win, ui.ModalDialogPolicy(), nil)
}
