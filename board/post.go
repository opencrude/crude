package board

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

func (store *Store) Post(ctx context.Context, request PostRequest) (PostResult, error) {
	if request.ProjectID != store.projectID {
		return PostResult{}, ErrProjectMismatch
	}
	if err := validatePost(request); err != nil {
		return PostResult{}, err
	}
	request.References = append([]Reference{}, request.References...)
	encodedRequest, err := json.Marshal(request)
	if err != nil {
		return PostResult{}, err
	}
	if len(encodedRequest) > MaxPostBytes {
		return PostResult{}, ErrTooLarge
	}
	fingerprint := hashBytes(encodedRequest)
	identity, err := json.Marshal([]string{request.ProjectID, request.SenderID, request.ProviderMessageID, request.ToolCallID})
	if err != nil {
		return PostResult{}, err
	}
	messageID := "board_" + hashBytes(identity)
	var result PostResult
	err = store.transaction(ctx, true, func(tx *sql.Tx) error {
		state, err := store.state(ctx, tx, request.BoardID, request.ProjectID)
		if err != nil {
			return err
		}
		sender, _, err := findMember(ctx, tx, request.BoardID, request.SenderID)
		if err != nil {
			return err
		}
		var previousFingerprint, previousJSON string
		var archived bool
		err = tx.QueryRowContext(ctx, `SELECT fingerprint, message_json, archived FROM board_messages
			WHERE project_id = ? AND sender_id = ? AND provider_message_id = ? AND tool_call_id = ?`,
			request.ProjectID, request.SenderID, request.ProviderMessageID, request.ToolCallID).
			Scan(&previousFingerprint, &previousJSON, &archived)
		if err == nil {
			if previousFingerprint != fingerprint {
				return ErrChangedRetry
			}
			message, err := decodeMessage(previousJSON, archived)
			if err != nil {
				return err
			}
			result = PostResult{Message: message, Duplicate: true}
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		recipientID, availability, recipients, err := resolveRecipients(ctx, tx, request.BoardID, request.To, state.coordinatorID)
		if err != nil {
			return err
		}
		message := Message{
			ID:                    messageID,
			BoardID:               request.BoardID,
			ProjectID:             request.ProjectID,
			Sequence:              state.lastSequence + 1,
			SenderID:              sender.ID,
			SenderName:            sender.Name,
			To:                    request.To,
			RecipientID:           recipientID,
			RecipientAvailability: availability,
			Recipients:            recipients,
			Type:                  request.Type,
			Body:                  request.Body,
			ReplyTo:               request.ReplyTo,
			ThreadID:              messageID,
			References:            request.References,
			CreatedAt:             time.Now().UTC(),
		}
		if request.ReplyTo != "" {
			parent, err := messageByID(ctx, tx, request.BoardID, request.ReplyTo)
			if err != nil {
				return fmt.Errorf("board: reply target: %w", err)
			}
			message.ThreadID = parent.ThreadID
			message.Archived = parent.Archived
		}
		encodedMessage, err := json.Marshal(message)
		if err != nil {
			return err
		}
		encodedResult, err := json.Marshal(PostResult{Message: message})
		if err != nil {
			return err
		}
		if len(encodedResult) > MaxPostBytes {
			return ErrTooLarge
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO board_messages
			(id, board_id, project_id, sequence, sender_id, provider_message_id, tool_call_id, fingerprint, recipient_id, thread_id, body, message_json, payload_bytes, archived)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			message.ID, message.BoardID, message.ProjectID, message.Sequence, message.SenderID, request.ProviderMessageID,
			request.ToolCallID, fingerprint, message.RecipientID, message.ThreadID, message.Body, string(encodedMessage), len(encodedMessage), message.Archived); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE boards SET last_sequence = ? WHERE id = ?`, message.Sequence, message.BoardID); err != nil {
			return err
		}
		if err := archiveOverflow(ctx, tx, request.BoardID); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, `SELECT archived FROM board_messages WHERE id = ?`, message.ID).Scan(&message.Archived); err != nil {
			return err
		}
		result = PostResult{Message: message}
		return nil
	})
	if err != nil {
		return PostResult{}, err
	}
	return result, nil
}

func hashBytes(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}

func validatePost(request PostRequest) error {
	for _, field := range []struct {
		name  string
		value string
		limit int
	}{
		{"board_id", request.BoardID, 256},
		{"sender_id", request.SenderID, 128},
		{"provider_message_id", request.ProviderMessageID, 256},
		{"tool_call_id", request.ToolCallID, 256},
		{"to", request.To, 128},
	} {
		if err := validateIdentity(field.name, field.value, field.limit); err != nil {
			return err
		}
	}
	switch request.Type {
	case TypeInfo, TypeAsk, TypeResult, TypeHold, TypeVeto:
	default:
		return fmt.Errorf("%w: type must be INFO, ASK, RESULT, HOLD, or VETO", ErrInvalid)
	}
	if strings.TrimSpace(request.Body) == "" || !utf8.ValidString(request.Body) {
		return fmt.Errorf("%w: body must be nonempty UTF-8", ErrInvalid)
	}
	if len(request.Body) > MaxPostBytes || len(request.References) > 32 {
		return ErrTooLarge
	}
	if request.ReplyTo != "" {
		if err := validateIdentity("reply_to", request.ReplyTo, 128); err != nil {
			return err
		}
	}
	for _, reference := range request.References {
		if err := validateIdentity("reference kind", reference.Kind, 32); err != nil {
			return err
		}
		if strings.IndexFunc(reference.Kind, func(character rune) bool {
			return (character < 'a' || character > 'z') && (character < 'A' || character > 'Z') && (character < '0' || character > '9') && character != '_' && character != '-'
		}) >= 0 {
			return fmt.Errorf("%w: reference kind is an identity, not content", ErrInvalid)
		}
		if err := validateIdentity("reference id", reference.ID, 256); err != nil {
			return err
		}
	}
	return nil
}

func resolveRecipients(ctx context.Context, tx *sql.Tx, boardID, to, coordinatorID string) (string, string, []Recipient, error) {
	var members []Member
	resolved := to
	if to == "ALL" {
		var err error
		members, err = roster(ctx, tx, boardID)
		if err != nil {
			return "", "", nil, err
		}
	} else {
		if to == "main" {
			if coordinatorID == "" {
				return "", "", nil, fmt.Errorf("%w: board has no coordinator", ErrNotMember)
			}
			resolved = coordinatorID
		}
		member, _, err := findMember(ctx, tx, boardID, resolved)
		if err != nil {
			return "", "", nil, fmt.Errorf("board: recipient: %w", err)
		}
		members = []Member{member}
	}
	recipients := make([]Recipient, 0, len(members))
	availability := AvailabilityUnknown
	for index, member := range members {
		recipients = append(recipients, Recipient{ID: member.ID, Name: member.Name, Availability: member.Availability})
		if index == 0 {
			availability = member.Availability
		} else if availability != member.Availability {
			availability = AvailabilityUnknown
		}
	}
	return resolved, availability, recipients, nil
}

func decodeMessage(encoded string, archived bool) (Message, error) {
	var message Message
	if err := json.Unmarshal([]byte(encoded), &message); err != nil {
		return Message{}, fmt.Errorf("board: decode message: %w", err)
	}
	message.Archived = archived
	return message, nil
}

func messageByID(ctx context.Context, tx *sql.Tx, boardID, id string) (Message, error) {
	var encoded string
	var archived bool
	err := tx.QueryRowContext(ctx, `SELECT message_json, archived FROM board_messages WHERE board_id = ? AND id = ?`, boardID, id).Scan(&encoded, &archived)
	if errors.Is(err, sql.ErrNoRows) {
		return Message{}, ErrNotFound
	}
	if err != nil {
		return Message{}, err
	}
	return decodeMessage(encoded, archived)
}
