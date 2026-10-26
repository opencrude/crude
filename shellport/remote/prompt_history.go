package remote

import (
	"context"
	"errors"

	"crude/coreprotocol"
	"crude/history"
	"crude/sessiondb"
	"crude/shellport"
)

var _ shellport.PromptHistory = (*Workspace)(nil)
var _ shellport.PromptHistoryAttachments = (*Workspace)(nil)

func (w *Workspace) ImportPromptHistoryAttachment(ctx context.Context, sessionID, entryID string, index int) (sessiondb.Reference, error) {
	var result coreprotocol.ReferenceView
	err := w.call(ctx, coreprotocol.HistoryImportAttachment, &coreprotocol.HistoryCommand{SessionID: sessionID, EntryID: entryID, AttachmentIndex: index}, &result)
	return result.Reference, err
}

func historyEntries(result coreprotocol.HistoryListResult) []history.Entry {
	entries := make([]history.Entry, len(result.Entries))
	for i, e := range result.Entries {
		entries[i] = history.Entry{ID: e.ID, Text: e.Text, Count: e.Count, FirstSeen: e.FirstSeen, LastSeen: e.LastSeen}
		for _, a := range e.Attachments {
			entries[i].Attachments = append(entries[i].Attachments, history.Attachment{Start: a.Start, End: a.End, Reference: a.Reference.Reference, Unavailable: a.Unavailable})
		}
	}
	return entries
}

func (w *Workspace) ListPromptHistory(ctx context.Context, sessionID string) ([]history.Entry, error) {
	var result coreprotocol.HistoryListResult
	err := w.call(ctx, coreprotocol.HistoryList, &coreprotocol.HistoryCommand{SessionID: sessionID}, &result)
	return historyEntries(result), err
}

func (w *Workspace) RecordPromptHistory(ctx context.Context, sessionID, text string, attachments []history.Attachment) ([]history.Entry, error) {
	if sessionID == "" {
		return nil, errors.New("prompt history needs an exact open session")
	}
	in := coreprotocol.HistoryCommand{SessionID: sessionID, Text: text}
	if len(attachments) > 0 {
		// Resolve revisions through the typed core read, not a guessed hash or a
		// client-local file path. The sent reference snapshot must still match.
		var refs coreprotocol.ReferenceListResult
		if err := w.call(ctx, coreprotocol.ReferenceList, &coreprotocol.ReferenceCommand{SessionID: sessionID}, &refs); err != nil {
			return nil, err
		}
		for _, a := range attachments {
			var exact coreprotocol.ReferenceView
			for _, ref := range refs.References {
				if ref.Reference.ID == a.Reference.ID && ref.Reference == a.Reference && ref.Revision != "" {
					exact = ref
					break
				}
			}
			if exact.Revision == "" || a.Unavailable {
				return nil, errors.New("prompt history reference changed or is unavailable")
			}
			in.Attachments = append(in.Attachments, coreprotocol.HistoryAttachment{Start: a.Start, End: a.End, Reference: exact})
		}
	}
	var result coreprotocol.HistoryListResult
	err := w.call(ctx, coreprotocol.HistoryRecord, &in, &result)
	return historyEntries(result), err
}
