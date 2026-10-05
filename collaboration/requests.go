package collaboration

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
)

// WorkRequest is a projection of the immutable journal, not a second scheduler.
// A disposition records a participant's commitment; it never grants authority.
type WorkRequest struct {
	ID           string                        `json:"id"`
	Revision     uint64                        `json:"revision"`
	EventID      string                        `json:"event_id"`
	ThreadID     string                        `json:"thread_id"`
	SenderID     string                        `json:"sender_id"`
	RecipientIDs []string                      `json:"recipient_ids"`
	Dispositions map[string]RequestDisposition `json:"dispositions"`
}

func (store *Store) Request(ctx context.Context, id string) (WorkRequest, error) {
	if err := validateIdentity("request_id", id, 256); err != nil {
		return WorkRequest{}, err
	}
	var result WorkRequest
	err := store.readTransaction(ctx, func(tx *sql.Tx) error {
		var err error
		result, err = loadWorkRequest(ctx, tx, id)
		return err
	})
	return result, err
}

func loadWorkRequest(ctx context.Context, tx *sql.Tx, id string) (WorkRequest, error) {
	var encoded string
	err := tx.QueryRowContext(ctx, `SELECT event_json FROM collaboration_events
		WHERE request_id=? AND kind IN ('work_request','work_revision')
		ORDER BY sequence DESC LIMIT 1`, id).Scan(&encoded)
	if errors.Is(err, sql.ErrNoRows) {
		return WorkRequest{}, ErrNotFound
	}
	if err != nil {
		return WorkRequest{}, err
	}
	event, err := decodeEvent(encoded)
	if err != nil {
		return WorkRequest{}, err
	}
	result := WorkRequest{
		ID: id, Revision: event.RequestRevision, EventID: event.ID,
		ThreadID: event.ThreadID, SenderID: event.SenderID,
		Dispositions: make(map[string]RequestDisposition, len(event.Recipients)),
	}
	for _, recipient := range event.Recipients {
		result.RecipientIDs = append(result.RecipientIDs, recipient.ID)
		result.Dispositions[recipient.ID] = DispositionPending
	}
	rows, err := tx.QueryContext(ctx, `SELECT event_json FROM collaboration_events
		WHERE request_id=? AND request_revision=? AND kind='request_disposition'
		ORDER BY sequence`, id, result.Revision)
	if err != nil {
		return WorkRequest{}, err
	}
	defer rows.Close()
	for rows.Next() {
		if err := rows.Scan(&encoded); err != nil {
			return WorkRequest{}, err
		}
		disposition, err := decodeEvent(encoded)
		if err != nil {
			return WorkRequest{}, err
		}
		typed, err := requestDisposition(disposition.References)
		if err != nil {
			return WorkRequest{}, err
		}
		result.Dispositions[disposition.SenderID] = typed
	}
	return result, rows.Err()
}

// EventDisposition reads the typed reference, never the event's free-form body.
func EventDisposition(event Event) RequestDisposition {
	if event.Kind != EventRequestDisposition {
		return ""
	}
	disposition, _ := requestDisposition(event.References)
	return disposition
}

func requestDisposition(references []Reference) (RequestDisposition, error) {
	var result RequestDisposition
	for _, reference := range references {
		if reference.Kind != "disposition" {
			continue
		}
		if result != "" || reference.Revision != 0 {
			return "", fmt.Errorf("%w: ambiguous disposition", ErrInvalid)
		}
		switch RequestDisposition(reference.ID) {
		case DispositionAccepted, DispositionDeferred, DispositionDeclined:
			result = RequestDisposition(reference.ID)
		default:
			return "", fmt.Errorf("%w: unknown disposition", ErrInvalid)
		}
	}
	if result == "" {
		return "", fmt.Errorf("%w: typed disposition required", ErrInvalid)
	}
	return result, nil
}

func prepareRequestEvent(ctx context.Context, tx *sql.Tx, request PublishRequest, event *Event) error {
	switch event.Kind {
	case EventWorkRequest:
		if event.RequestRevision != 1 {
			return fmt.Errorf("%w: a new work request starts at revision 1", ErrInvalid)
		}
		if _, err := loadWorkRequest(ctx, tx, event.RequestID); !errors.Is(err, ErrNotFound) {
			if err != nil {
				return err
			}
			return fmt.Errorf("%w: request already exists; issue a revision", ErrConflict)
		}
		if event.Supersedes != "" {
			return fmt.Errorf("%w: new request cannot supersede another event", ErrInvalid)
		}
	case EventWorkRevision, EventRequestDisposition:
		current, err := loadWorkRequest(ctx, tx, event.RequestID)
		if err != nil {
			return fmt.Errorf("collaboration: exact work request required: %w", err)
		}
		if event.Kind == EventWorkRevision {
			if event.SenderID != current.SenderID ||
				!slices.Equal(normalizedRecipients(request.RecipientIDs), normalizedRecipients(current.RecipientIDs)) {
				return fmt.Errorf("%w: request author and recipients are immutable", ErrConflict)
			}
			if event.RequestRevision != current.Revision+1 ||
				event.Supersedes != "" && event.Supersedes != current.EventID {
				return fmt.Errorf("%w: current request revision is %d", ErrStale, current.Revision)
			}
			event.Supersedes = current.EventID
		} else {
			if event.RequestRevision != current.Revision {
				return fmt.Errorf("%w: current request revision is %d", ErrStale, current.Revision)
			}
			if !slices.Contains(current.RecipientIDs, event.SenderID) ||
				len(event.Recipients) != 1 || event.Recipients[0].ID != current.SenderID {
				return fmt.Errorf("%w: only the addressed participant may respond to the request owner", ErrConflict)
			}
			next, err := requestDisposition(event.References)
			if err != nil {
				return err
			}
			previous := current.Dispositions[event.SenderID]
			if previous == next || previous == DispositionDeclined {
				return fmt.Errorf("%w: disposition is %s; a declined request needs a new revision", ErrConflict, previous)
			}
		}
		if event.ReplyTo != "" {
			reply, err := loadEvent(ctx, tx, event.ReplyTo)
			if err != nil {
				return err
			}
			if reply.ThreadID != current.ThreadID {
				return fmt.Errorf("%w: reply belongs to another request thread", ErrConflict)
			}
		} else {
			event.ReplyTo = current.EventID
		}
		event.ThreadID = current.ThreadID
	}
	return nil
}

func validateReportRequest(ctx context.Context, tx *sql.Tx, author Agent, runID, requestID string) error {
	if requestID == "" {
		return nil
	}
	work, err := loadWorkRequest(ctx, tx, requestID)
	if errors.Is(err, ErrNotFound) && author.ParentID != "" && runID != "" {
		// A child's original launch is an actual delegated assignment, not a
		// peer work-request journal entry. Its exact launch identity is valid.
		run, runErr := loadRun(ctx, tx, runID)
		if runErr == nil && run.AgentID == author.ID && run.RequestID == requestID {
			return nil
		}
	}
	if err != nil {
		return fmt.Errorf("collaboration: report request identity: %w", err)
	}
	if work.SenderID != author.ID && !slices.Contains(work.RecipientIDs, author.ID) {
		return fmt.Errorf("%w: report author is not a work-request participant", ErrConflict)
	}
	return nil
}
