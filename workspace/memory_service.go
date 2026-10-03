package workspace

import (
	"context"
	"errors"
	"strings"
	"time"

	"crude/memory"
)

const memoryServiceTimeout = 5 * time.Second

var ErrMemoryUnavailable = errors.New("workspace memory is unavailable; reopen the workspace to retry")

type MemoryDetail struct {
	Record    memory.Record
	Revisions []memory.Revision
	Feedback  []memory.Feedback
	Exposures []memory.Exposure
}

func (w *Workspace) MemoryAvailable() bool {
	return w != nil && w.WorkspaceDB != nil && w.WorkspaceDB.Memory != nil
}

func (w *Workspace) SearchMemories(ctx context.Context, query memory.ListQuery) ([]memory.ScoredRecord, error) {
	if !w.MemoryAvailable() {
		return nil, ErrMemoryUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, memoryServiceTimeout)
	defer cancel()
	return w.WorkspaceDB.Memory.List(ctx, query)
}

func (w *Workspace) SearchMemoryEpisodes(ctx context.Context, query string, limit int) ([]memory.Episode, error) {
	if !w.MemoryAvailable() {
		return nil, ErrMemoryUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, memoryServiceTimeout)
	defer cancel()
	episodes, err := w.WorkspaceDB.Memory.Episodes(ctx, "", limit)
	if err != nil {
		return nil, err
	}
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return episodes, nil
	}
	filtered := make([]memory.Episode, 0, len(episodes))
	for _, episode := range episodes {
		text := strings.Join([]string{episode.ID, episode.SessionID, episode.Situation, episode.Method, episode.Outcome, episode.Conditions}, " ")
		for _, evidence := range episode.Evidence {
			text += " " + evidence.Summary + " " + evidence.MessageID + " " + evidence.ToolCallID
		}
		if strings.Contains(strings.ToLower(text), query) {
			filtered = append(filtered, episode)
		}
	}
	return filtered, nil
}

func (w *Workspace) InspectMemory(ctx context.Context, id string) (MemoryDetail, error) {
	if !w.MemoryAvailable() {
		return MemoryDetail{}, ErrMemoryUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, memoryServiceTimeout)
	defer cancel()
	store := w.WorkspaceDB.Memory
	detail := MemoryDetail{}
	var err error
	if detail.Record, err = store.Get(ctx, id); err != nil {
		return detail, err
	}
	if detail.Revisions, err = store.Revisions(ctx, id); err != nil {
		return detail, err
	}
	if detail.Feedback, err = store.FeedbackForMemory(ctx, id, 100); err != nil {
		return detail, err
	}
	detail.Exposures, err = store.ExposuresForMemory(ctx, id, 100)
	return detail, err
}

func (w *Workspace) PinMemory(ctx context.Context, id string, expectedRevision int64, pinned bool) error {
	if !w.MemoryAvailable() {
		return ErrMemoryUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, memoryServiceTimeout)
	defer cancel()
	return w.WorkspaceDB.Memory.SetPinned(ctx, id, expectedRevision, pinned, memory.ActorUser)
}

func (w *Workspace) ForgetMemoryRecord(ctx context.Context, id string, expectedRevision int64) error {
	if !w.MemoryAvailable() {
		return ErrMemoryUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, memoryServiceTimeout)
	defer cancel()
	return w.WorkspaceDB.Memory.Supersede(ctx, id, expectedRevision, "", memory.ActorUser, "")
}

func (w *Workspace) CorrectMemory(ctx context.Context, replacement memory.Record) (memory.Record, error) {
	if !w.MemoryAvailable() {
		return memory.Record{}, ErrMemoryUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, memoryServiceTimeout)
	defer cancel()
	replacement.Source = memory.SourceUser
	return w.WorkspaceDB.Memory.Update(ctx, replacement.ID, replacement, memory.ActorUser, "")
}
