package board

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const humanReaderID = ""

func (store *Store) Read(ctx context.Context, request ReadRequest) (Page, error) {
	return store.read(ctx, request, false, false, "")
}

func (store *Store) Inspect(ctx context.Context, request ReadRequest) (Page, error) {
	return store.read(ctx, request, true, false, "")
}

func (store *Store) Search(ctx context.Context, boardID, projectID, agentID, query string, limit int) (Page, error) {
	return store.SearchPage(ctx, ReadRequest{BoardID: boardID, ProjectID: projectID, AgentID: agentID, Limit: limit}, query)
}

func (store *Store) SearchPage(ctx context.Context, request ReadRequest, query string) (Page, error) {
	return store.search(ctx, request, query, false)
}

func (store *Store) InspectSearch(ctx context.Context, request ReadRequest, query string) (Page, error) {
	return store.search(ctx, request, query, true)
}

func (store *Store) search(ctx context.Context, request ReadRequest, query string, human bool) (Page, error) {
	if len(query) > 256 {
		return Page{}, ErrTooLarge
	}
	if err := validateIdentity("query", query, 256); err != nil {
		return Page{}, err
	}
	return store.read(ctx, request, human, true, query)
}

func (store *Store) read(ctx context.Context, request ReadRequest, human, search bool, query string) (Page, error) {
	if request.ProjectID != store.projectID {
		return Page{}, ErrProjectMismatch
	}
	if request.Since < 0 || request.Limit < 0 {
		return Page{}, ErrInvalidCursor
	}
	if request.Limit == 0 {
		request.Limit = DefaultLimit
	}
	if request.Limit > MaxLimit {
		request.Limit = MaxLimit
	}
	if !human {
		if err := validateMemberID(request.AgentID); err != nil {
			return Page{}, err
		}
	}
	var page Page
	err := store.transaction(ctx, !search, func(tx *sql.Tx) error {
		state, err := store.state(ctx, tx, request.BoardID, request.ProjectID)
		if err != nil {
			return err
		}
		if request.Since > state.lastSequence {
			return ErrInvalidCursor
		}
		accepted := state.humanRead
		readerID := humanReaderID
		if !human {
			_, accepted, err = findMember(ctx, tx, request.BoardID, request.AgentID)
			if err != nil {
				return err
			}
			readerID = request.AgentID
		}
		members, err := roster(ctx, tx, request.BoardID)
		if err != nil {
			return err
		}
		page = Page{
			BoardID:          request.BoardID,
			ProjectID:        request.ProjectID,
			Roster:           members,
			Messages:         make([]Message, 0),
			Cursor:           request.Since,
			LatestSequence:   state.lastSequence,
			AcceptedThrough:  accepted,
			HumanReadThrough: state.humanRead,
		}
		contiguous := !search && request.Thread == "" && request.Since <= accepted
		statement := `SELECT message_json, archived FROM board_messages WHERE board_id = ? AND sequence > ? AND sequence <= ?`
		arguments := []any{request.BoardID, request.Since, state.lastSequence}
		if request.Thread != "" {
			thread, err := messageByID(ctx, tx, request.BoardID, request.Thread)
			if err != nil {
				return fmt.Errorf("board: thread: %w", err)
			}
			statement += ` AND thread_id = ?`
			arguments = append(arguments, thread.ThreadID)
		}
		if search {
			statement += ` AND (instr(lower(body), lower(?)) > 0 OR id = ? OR instr(lower(message_json), lower(?)) > 0)`
			arguments = append(arguments, query, query, query)
		}
		statement += ` ORDER BY sequence LIMIT ?`
		arguments = append(arguments, request.Limit+1)
		rows, err := tx.QueryContext(ctx, statement, arguments...)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			if len(page.Messages) >= request.Limit {
				page.HasMore = true
				break
			}
			var encoded string
			var archived bool
			if err := rows.Scan(&encoded, &archived); err != nil {
				return err
			}
			message, err := decodeMessage(encoded, archived)
			if err != nil {
				return err
			}
			previousCursor, previousAccept := page.Cursor, page.AcceptThrough
			page.Messages = append(page.Messages, message)
			page.Cursor = message.Sequence
			if contiguous && message.Sequence > accepted {
				page.AcceptThrough = message.Sequence
			}
			size, err := pageBytes(&page)
			if err != nil {
				return err
			}
			if size > MaxPageBytes {
				page.Messages = page.Messages[:len(page.Messages)-1]
				page.Cursor, page.AcceptThrough = previousCursor, previousAccept
				page.HasMore = true
				break
			}
		}
		if err := rows.Err(); err != nil {
			return err
		}
		if err := rows.Close(); err != nil {
			return err
		}
		if len(page.Messages) == 0 && page.HasMore {
			return ErrTooLarge
		}
		if _, err := pageBytes(&page); err != nil {
			return err
		}
		if page.Bytes > MaxPageBytes {
			return ErrTooLarge
		}
		if page.AcceptThrough > accepted {
			return offerPage(ctx, tx, request.BoardID, readerID, request.Since, page.AcceptThrough)
		}
		return nil
	})
	if err != nil {
		return Page{}, err
	}
	return page, nil
}

func pageBytes(page *Page) (int, error) {
	for attempt := 0; attempt < 4; attempt++ {
		encoded, err := json.Marshal(page)
		if err != nil {
			return 0, err
		}
		if page.Bytes == len(encoded) {
			return len(encoded), nil
		}
		page.Bytes = len(encoded)
	}
	return 0, fmt.Errorf("board: page byte accounting did not converge")
}

func offerPage(ctx context.Context, tx *sql.Tx, boardID, readerID string, since, through int64) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO board_read_offers(board_id, reader_id, since_sequence, through_sequence) VALUES (?, ?, ?, ?)
		ON CONFLICT(board_id, reader_id, through_sequence) DO UPDATE SET since_sequence = MIN(since_sequence, excluded.since_sequence)`,
		boardID, readerID, since, through)
	return err
}

func (store *Store) AcceptRead(ctx context.Context, boardID, agentID string, through int64) error {
	if err := validateMemberID(agentID); err != nil {
		return err
	}
	return store.accept(ctx, boardID, agentID, through, false)
}

func (store *Store) AcceptHumanRead(ctx context.Context, boardID string, through int64) error {
	return store.accept(ctx, boardID, humanReaderID, through, true)
}

func (store *Store) accept(ctx context.Context, boardID, agentID string, through int64, human bool) error {
	if through < 0 {
		return ErrInvalidCursor
	}
	return store.transaction(ctx, true, func(tx *sql.Tx) error {
		state, err := store.state(ctx, tx, boardID, store.projectID)
		if err != nil {
			return err
		}
		accepted := state.humanRead
		if !human {
			_, accepted, err = findMember(ctx, tx, boardID, agentID)
			if err != nil {
				return err
			}
		}
		if through <= accepted {
			return nil
		}
		if through > state.lastSequence {
			return ErrInvalidCursor
		}
		var since int64
		err = tx.QueryRowContext(ctx, `SELECT since_sequence FROM board_read_offers WHERE board_id = ? AND reader_id = ? AND through_sequence = ?`, boardID, agentID, through).Scan(&since)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrInvalidCursor
		}
		if err != nil {
			return err
		}
		if since > accepted {
			return ErrInvalidCursor
		}
		if human {
			_, err = tx.ExecContext(ctx, `UPDATE boards SET human_read_sequence = ? WHERE id = ?`, through, boardID)
		} else {
			_, err = tx.ExecContext(ctx, `UPDATE board_members SET accepted_sequence = ? WHERE board_id = ? AND agent_id = ?`, through, boardID, agentID)
		}
		return err
	})
}

func (store *Store) PendingNotice(ctx context.Context, boardID, agentID string) (string, error) {
	var notice string
	err := store.transaction(ctx, true, func(tx *sql.Tx) error {
		if _, err := store.state(ctx, tx, boardID, store.projectID); err != nil {
			return err
		}
		_, accepted, err := findMember(ctx, tx, boardID, agentID)
		if err != nil {
			return err
		}
		var latest sql.NullInt64
		if err := tx.QueryRowContext(ctx, `SELECT MAX(sequence) FROM board_messages WHERE board_id = ? AND sequence > ? AND sender_id != ? AND (recipient_id = 'ALL' OR recipient_id = ?)`,
			boardID, accepted, agentID, agentID).Scan(&latest); err != nil {
			return err
		}
		if !latest.Valid {
			return nil
		}
		notice = PendingNoticeText
		_, err = tx.ExecContext(ctx, `UPDATE board_members SET notice_sequence = MAX(notice_sequence, ?) WHERE board_id = ? AND agent_id = ?`, latest.Int64, boardID, agentID)
		return err
	})
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(notice), nil
}
