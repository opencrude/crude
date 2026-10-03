package workspace

import (
	"context"
	"errors"

	"crude/board"
)

type CollaborationSummary struct {
	ID               string
	CoordinatorID    string
	Participants     int
	LatestSequence   int64
	HumanReadThrough int64
}

type BoardParticipant struct {
	board.Member
	AcceptedThrough int64
	NotifiedThrough int64
}

type BoardSnapshot struct {
	Boards       []CollaborationSummary
	Page         board.Page
	Participants []BoardParticipant
	Unread       int
}

func (workspace *Workspace) BoardSnapshot(ctx context.Context, id, query, thread string, since int64) (BoardSnapshot, error) {
	if err := workspace.ensureCapabilities(ctx); err != nil {
		return BoardSnapshot{}, err
	}
	var snapshot BoardSnapshot
	rows, err := workspace.WorkspaceDB.DB.QueryContext(ctx, `SELECT b.id, b.coordinator_id, b.last_sequence, b.human_read_sequence, COUNT(m.agent_id) FROM boards b LEFT JOIN board_members m ON m.board_id=b.id WHERE b.project_id=? GROUP BY b.id ORDER BY b.last_sequence DESC, b.created_at DESC LIMIT 100`, workspace.RootPath)
	if err != nil {
		return snapshot, err
	}
	for rows.Next() {
		var item CollaborationSummary
		if err := rows.Scan(&item.ID, &item.CoordinatorID, &item.LatestSequence, &item.HumanReadThrough, &item.Participants); err != nil {
			_ = rows.Close()
			return snapshot, err
		}
		snapshot.Boards = append(snapshot.Boards, item)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return snapshot, err
	}
	if id == "" && len(snapshot.Boards) > 0 {
		id = snapshot.Boards[0].ID
	}
	if id == "" {
		return snapshot, nil
	}
	request := board.ReadRequest{BoardID: id, ProjectID: workspace.RootPath, Thread: thread, Since: since, Limit: board.DefaultLimit}
	if query != "" {
		snapshot.Page, err = workspace.Board.InspectSearch(ctx, request, query)
	} else {
		snapshot.Page, err = workspace.Board.Inspect(ctx, request)
	}
	if err != nil {
		return snapshot, err
	}
	snapshot.Unread, err = workspace.Board.HumanUnread(ctx, id)
	if err != nil {
		return snapshot, err
	}
	rows, err = workspace.WorkspaceDB.DB.QueryContext(ctx, `SELECT agent_id,name,parent_id,availability,accepted_sequence,notice_sequence FROM board_members WHERE board_id=? ORDER BY agent_id`, id)
	if err != nil {
		return snapshot, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var participant BoardParticipant
		if err := rows.Scan(&participant.ID, &participant.Name, &participant.ParentID, &participant.Availability, &participant.AcceptedThrough, &participant.NotifiedThrough); err != nil {
			return snapshot, err
		}
		snapshot.Participants = append(snapshot.Participants, participant)
	}
	return snapshot, rows.Err()
}

func (workspace *Workspace) MarkBoardRead(ctx context.Context, id string, through int64) error {
	if err := workspace.ensureCapabilities(ctx); err != nil {
		return err
	}
	if through <= 0 {
		return errors.New("only a contiguous inspected page can be marked read")
	}
	return workspace.Board.AcceptHumanRead(ctx, id, through)
}
