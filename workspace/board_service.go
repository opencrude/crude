package workspace

import (
	"context"
	"errors"

	"crude/collaboration"
)

type BoardParticipant struct {
	collaboration.Agent
}

type BoardSnapshot struct {
	WorkspaceID  string
	Page         collaboration.Page
	Participants []BoardParticipant
	Unread       int
}

func (workspace *Workspace) BoardSnapshot(
	ctx context.Context,
	query, thread string,
	since, snapshot int64,
) (BoardSnapshot, error) {
	if err := workspace.ensureWorkspacePersistence(); err != nil {
		return BoardSnapshot{}, err
	}
	if workspace.Collaboration == nil {
		return BoardSnapshot{}, errors.New("workspace collaboration runtime is unavailable")
	}
	page, err := workspace.Collaboration.Page(ctx, collaboration.PageQuery{
		Since: since, Snapshot: snapshot, ThreadID: thread, Search: query,
		Limit: collaboration.DefaultPageLimit,
	})
	if err != nil {
		return BoardSnapshot{}, err
	}
	agents, err := workspace.Collaboration.ListAgents(ctx, collaboration.AgentQuery{
		IncludeClosed: true, Limit: collaboration.MaxAgentLimit,
	})
	if err != nil {
		return BoardSnapshot{}, err
	}
	participants := make([]BoardParticipant, 0, len(agents))
	for _, agent := range agents {
		participants = append(participants, BoardParticipant{Agent: agent})
	}
	unread, err := workspace.Collaboration.HumanUnread(ctx)
	if err != nil {
		return BoardSnapshot{}, err
	}
	return BoardSnapshot{
		WorkspaceID: workspace.Collaboration.WorkspaceID(),
		Page:        page, Participants: participants, Unread: unread,
	}, nil
}

func (workspace *Workspace) MarkBoardRead(ctx context.Context, through int64) error {
	if err := workspace.ensureWorkspacePersistence(); err != nil {
		return err
	}
	if workspace.Collaboration == nil {
		return errors.New("workspace collaboration runtime is unavailable")
	}
	if through <= 0 {
		return errors.New("only a stable inspected snapshot can be marked read")
	}
	return workspace.Collaboration.MarkHumanRead(ctx, through)
}
