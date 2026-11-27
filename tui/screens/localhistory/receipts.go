package localhistory

import (
	"crude/tui/ui"
	"crude/workspace/localhistory"
	"fmt"
	"strings"
	"time"
)

// ActionReceipts requests another page or the exact historical diff.
const ActionReceipts = "history.receipts"

// ReceiptMsg is a read-only result for one open history view.
type ReceiptMsg struct {
	Query localhistory.AgentReceiptQuery
	Page  localhistory.RunChangePage
	Error string
}
type receiptView struct {
	items          []ui.ListItem
	coverage, path string
	detail         bool
	index          int
}

func (s *Screen) setReceipts(m ReceiptMsg) {
	s.loading = false
	s.errorText = m.Error
	if s.receipts == nil {
		s.receipts = &receiptView{}
	}
	v := s.receipts
	if m.Error != "" {
		return
	}
	if m.Query.OperationID != "" && len(m.Page.Changes) == 1 {
		c := m.Page.Changes[0]
		v.detail = true
		v.path = c.Path
		s.status = "Esc to return to the changes"
		var items []ui.ListItem
		for i, line := range strings.Split(strings.TrimSuffix(c.Diff, "\n"), "\n") {
			items = append(items, ui.ListItem{ID: fmt.Sprint(i), Label: line})
		}
		if c.DiffNote != "" {
			items = append(items, ui.ListItem{ID: "note", Label: c.DiffNote})
		} else if c.Diff == "" {
			items = []ui.ListItem{{ID: "metadata", Label: "The file metadata changed."}}
		}
		s.list.SetItems(items)
		s.list.SelectIndex(0)
		return
	}
	v.coverage = m.Page.Coverage
	s.status = v.coverage
	if m.Query.After == 0 {
		v.items = nil
	} else if len(v.items) > 0 && v.items[len(v.items)-1].ID == "more" {
		v.items = v.items[:len(v.items)-1]
	}
	first := len(v.items)
	for _, c := range m.Page.Changes {
		q := m.Query
		q.After = 0
		q.OperationID = c.OperationID
		q.Path = c.Path
		v.items = append(v.items, ui.ListItem{ID: c.OperationID + ":" + c.Path, Label: c.Path, Detail: snapshotTime(time.Unix(0, c.CreatedAt), time.Now()), Payload: q})
	}
	if m.Page.After > 0 {
		q := m.Query
		q.After = m.Page.After
		v.items = append(v.items, ui.ListItem{ID: "more", Label: "Show more changes", Payload: q, Command: true})
	}
	if len(v.items) == 0 {
		v.items = []ui.ListItem{{ID: "empty", Label: "No direct file edits recorded for this agent.", Disabled: true}}
	}
	s.list.SetItems(v.items)
	s.list.SelectIndex(first)
}
