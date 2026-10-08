package input

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"crude/history"
	"crude/tui/theme"
)

func TestHistoryDownReachesNewestAndThenOriginalDraft(t *testing.T) {
	m := NewModel(theme.DefaultDark())
	m.SetHistory([]history.Entry{{Text: "prefix older"}, {Text: "prefix newest"}})
	m.SetInput("prefix")
	for _, step := range []struct {
		key  rune
		want string
	}{{tea.KeyUp, "prefix newest"}, {tea.KeyUp, "prefix older"}, {tea.KeyDown, "prefix newest"}, {tea.KeyDown, "prefix"}} {
		var event Event
		m, _, event = m.Update(tea.KeyPressMsg{Code: step.key}, false, false)
		if recall, ok := event.(HistoryRecallEvent); ok {
			m.FinishHistoryRecall(recall.Generation, recall.Entry.Text, nil, true)
		}
		if m.Value() != step.want {
			t.Fatalf("key=%d got=%q want=%q", step.key, m.Value(), step.want)
		}
	}
}

func TestHistoryWheelDoesNotDiscardRecallOrKeepSendBlocked(t *testing.T) {
	m := NewModel(theme.DefaultDark())
	m.SetHistory([]history.Entry{{Text: "history"}})
	m.SetInput("")
	m, _, event := m.Update(tea.MouseClickMsg{Button: tea.MouseWheelUp}, false, false)
	recall, ok := event.(HistoryRecallEvent)
	if !ok {
		t.Fatal("history wheel discarded its required restore event")
	}
	m.FinishHistoryRecall(recall.Generation, recall.Entry.Text, nil, true)
	m, _, event = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter}, false, false)
	if _, ok := event.(SubmitEvent); !ok {
		t.Fatal("history wheel left Send blocked")
	}
}
