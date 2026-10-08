package viewmodel

import (
	"context"
	"errors"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/tui/screens/gitpanel"
)

// gitCommitWriteTimeout bounds one Aeron draft, including slow first tokens.
const gitCommitWriteTimeout = 2 * time.Minute

// gitCommitDeltaMsg carries streamed draft text; gitCommitWrittenMsg ends a run.
type gitCommitDeltaMsg struct {
	sequence uint64
	text     string
}

type gitCommitWrittenMsg struct {
	sequence uint64
	message  string
	err      error
}

var errGitCommitWriteStopped = errors.New("generation stopped · your previous draft is restored")

// startCommitMessageWriter asks Aeron for a message describing exactly what
// Commit would record. The model and account are resolved here, on the update
// goroutine; only repository reads and the request run in the background.
func (m *App) startCommitMessageWriter(request gitpanel.GenerateRequest) tea.Cmd {
	if m.gitPanel == nil || m.gitPanel.Writing() || m.Workspace == nil {
		return nil
	}
	writer, err := m.PrepareCommitMessageWriter()
	if err != nil {
		m.gitPanel.SetError(err)
		return nil
	}
	m.gitCommitWriteSequence++
	sequence := m.gitCommitWriteSequence
	ctx, cancel := context.WithTimeout(context.Background(), gitCommitWriteTimeout)
	m.gitCommitWriteCancel = cancel
	m.gitPanel.BeginWriting()
	bus := m.eventBus
	return func() tea.Msg {
		defer cancel()
		message, err := writer.Write(ctx, request.StageAll, func(token string) {
			if bus != nil && token != "" {
				bus.Send(gitCommitDeltaMsg{sequence: sequence, text: token})
			}
		})
		if errors.Is(err, context.Canceled) {
			err = errGitCommitWriteStopped
		}
		return gitCommitWrittenMsg{sequence: sequence, message: message, err: err}
	}
}

func (m *App) stopCommitMessageWriter() {
	if m.gitCommitWriteCancel != nil {
		m.gitCommitWriteCancel()
		m.gitCommitWriteCancel = nil
	}
}

func (m *App) applyCommitMessageDelta(msg gitCommitDeltaMsg) {
	if msg.sequence == m.gitCommitWriteSequence && m.gitPanel != nil {
		m.gitPanel.AppendWriting(msg.text)
	}
}

func (m *App) applyCommitMessageWritten(msg gitCommitWrittenMsg) {
	if msg.sequence != m.gitCommitWriteSequence || m.gitPanel == nil {
		return
	}
	m.gitCommitWriteCancel = nil
	m.gitPanel.FinishWriting(msg.message, msg.err)
	if msg.err == nil && m.Feedback != nil {
		m.Feedback.NotifyInfo("Aeron drafted the commit message · review it, then Commit")
	}
}
