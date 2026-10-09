package core

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"crude/coreprotocol"
	"crude/history"
	"crude/workspace"
)

func historyView(e history.Entry) coreprotocol.HistoryEntry {
	out := coreprotocol.HistoryEntry{ID: e.ID, Text: e.Text, Count: e.Count, FirstSeen: e.FirstSeen, LastSeen: e.LastSeen}
	for _, item := range e.Attachments {
		out.Attachments = append(out.Attachments, coreprotocol.HistoryAttachment{Start: item.Start, End: item.End, Reference: referenceView(item.Reference), Unavailable: item.Unavailable})
	}
	return out
}

func (a *actor) prepareHistory(request coreprotocol.Request) func(context.Context) coreprotocol.Response {
	if a.runtime == nil || a.runtime.history == nil {
		return func(context.Context) coreprotocol.Response {
			return fault(request, "history_unavailable", "application-owned prompt history has not been injected into the core")
		}
	}
	var in coreprotocol.HistoryCommand
	if err := decode(request.Payload, &in); err != nil {
		return func(context.Context) coreprotocol.Response { return fault(request, "invalid_request", err.Error()) }
	}
	var store *workspace.ReferenceStore
	for _, owner := range a.ws.Sessions {
		if owner.SessionID == in.SessionID {
			store = owner.RefStore
			break
		}
	}
	h, path := a.runtime.history, a.runtime.historyPath
	return func(ctx context.Context) coreprotocol.Response {
		if err := ctx.Err(); err != nil {
			return fault(request, "cancelled", err.Error())
		}
		value, err := runHistoryCommand(h, path, store, request.Method, in)
		if err != nil {
			return fault(request, "history_failed", err.Error())
		}
		return response(request, value)
	}
}

func runHistoryCommand(h *history.Store, path string, store *workspace.ReferenceStore, method string, in coreprotocol.HistoryCommand) (any, error) {
	referenceCommandMu.Lock()
	defer referenceCommandMu.Unlock()
	list := func() coreprotocol.HistoryListResult {
		out := coreprotocol.HistoryListResult{Entries: []coreprotocol.HistoryEntry{}}
		for _, entry := range h.Entries() {
			out.Entries = append(out.Entries, historyView(entry))
		}
		return out
	}
	if method == coreprotocol.HistoryList {
		return list(), nil
	}
	if method == coreprotocol.HistoryRecord {
		if store == nil || in.SessionID == "" {
			return nil, errors.New("exact reference session is not open")
		}
		if !utf8.ValidString(in.Text) || strings.TrimSpace(in.Text) == "" || len(strings.TrimSpace(in.Text)) > 32<<10 {
			return nil, errors.New("invalid history prompt")
		}
		runes, end := []rune(in.Text), 0
		var attachments []history.Attachment
		for _, item := range in.Attachments {
			ref, ok := store.GetReference(item.Reference.Reference.ID)
			if !ok || !ref.Active() || ref.SessionID != in.SessionID || item.Reference.Revision == "" || item.Reference.Revision != entityRevision(ref) || item.Unavailable {
				return nil, errors.New("history attachment is stale or belongs to another session")
			}
			if item.Start < end || item.End <= item.Start || item.End > len(runes) || string(runes[item.Start:item.End]) != ref.DisplayName() {
				return nil, errors.New("invalid attachment rune span")
			}
			end = item.End
			if ref.FilePath != "" {
				if _, err := history.ReadAttachment(ref.FilePath); err != nil {
					return nil, err
				}
			}
			attachments = append(attachments, history.Attachment{Start: item.Start, End: item.End, Reference: ref})
		}
		h.Record(in.Text, time.Now(), attachments...)
		if err := h.Save(path); err != nil {
			return nil, err
		}
		return list(), nil
	}
	var entry history.Entry
	for _, e := range h.Entries() {
		if e.ID == in.EntryID {
			entry = e
			break
		}
	}
	if entry.ID == "" {
		return nil, errors.New("exact history entry is missing")
	}
	if method == coreprotocol.HistoryRestore {
		if store == nil {
			return nil, errors.New("exact destination session is not open")
		}
		text, ids, err := store.RestorePrompt(entry)
		if err != nil {
			return nil, err
		}
		out := coreprotocol.HistoryRestoreResult{Text: text, ReferenceIDs: ids}
		for _, id := range ids {
			ref, _ := store.GetReference(id)
			out.References = append(out.References, referenceView(ref))
		}
		return out, nil
	}
	if method == coreprotocol.HistoryReadMedia {
		if in.AttachmentIndex < 0 || in.AttachmentIndex >= len(entry.Attachments) {
			return nil, errors.New("invalid attachment index")
		}
		attachment := entry.Attachments[in.AttachmentIndex]
		if attachment.Unavailable || attachment.Reference.FilePath == "" {
			return nil, errors.New("history media is unavailable")
		}
		data, err := history.ReadAttachment(attachment.Reference.FilePath)
		if err != nil {
			return nil, err
		}
		return referenceChunk(referenceView(attachment.Reference), data, true, in.Offset, in.Digest)
	}
	return nil, errors.New("unknown history operation")
}
