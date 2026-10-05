package collaboration

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

func (store *Store) Publish(ctx context.Context, request PublishRequest) (PublishResult, error) {
	if err := validatePublishRequest(request); err != nil {
		return PublishResult{}, err
	}
	request.RecipientIDs = normalizedRecipients(request.RecipientIDs)
	request.References = append([]Reference(nil), request.References...)
	var result PublishResult
	err := store.transaction(ctx, func(tx *sql.Tx) error {
		var err error
		result, err = store.publishTx(ctx, tx, request)
		return err
	})
	if err != nil {
		return PublishResult{}, err
	}
	store.signal()
	return result, nil
}

func (store *Store) publishTx(ctx context.Context, tx *sql.Tx, request PublishRequest) (PublishResult, error) {
	if request.Kind == EventWorkRequest {
		if request.RequestID == "" {
			identity, _ := json.Marshal([]string{store.workspaceID, request.SenderID, request.IdempotencyKey})
			request.RequestID = "request_" + hashBytes(identity)
		}
		if request.RequestRevision == 0 {
			request.RequestRevision = 1
		}
	}
	fingerprint, _, err := hashJSON(request)
	if err != nil {
		return PublishResult{}, err
	}
	identity, _ := json.Marshal([]string{store.workspaceID, request.SenderID, request.IdempotencyKey})
	eventID := "event_" + hashBytes(identity)
	var previousFingerprint, previousJSON string
	err = tx.QueryRowContext(ctx, `SELECT fingerprint,event_json FROM collaboration_events
		WHERE workspace_id=? AND sender_id=? AND idempotency_key=?`,
		store.workspaceID, request.SenderID, request.IdempotencyKey).
		Scan(&previousFingerprint, &previousJSON)
	if err == nil {
		if previousFingerprint != fingerprint {
			return PublishResult{}, ErrChangedRetry
		}
		event, err := decodeEvent(previousJSON)
		if err != nil {
			return PublishResult{}, err
		}
		deliveries, err := deliveriesForEvent(ctx, tx, event.ID)
		if err != nil {
			return PublishResult{}, err
		}
		return PublishResult{Event: event, Deliveries: deliveries, Duplicate: true}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return PublishResult{}, err
	}

	senderName := request.SenderName
	if request.Origin == OriginPeer {
		sender, err := loadAgent(ctx, tx, request.SenderID)
		if err != nil {
			return PublishResult{}, fmt.Errorf("collaboration: sender: %w", err)
		}
		senderName = sender.Name
	} else if strings.TrimSpace(senderName) == "" {
		senderName = request.SenderID
	}
	recipients, err := resolveRecipients(ctx, tx, request.SenderID, request.RecipientIDs, request.Broadcast)
	if err != nil {
		return PublishResult{}, err
	}
	if len(recipients) == 0 && !request.AuditOnly {
		if request.Broadcast {
			return PublishResult{}, fmt.Errorf("%w: no other agents have active attached runs", ErrUnavailable)
		}
		return PublishResult{}, fmt.Errorf("%w: no recipients", ErrInvalid)
	}

	var last int64
	if err := tx.QueryRowContext(ctx, `SELECT last_sequence FROM collaboration_state WHERE singleton=1`).Scan(&last); err != nil {
		return PublishResult{}, err
	}
	now := time.Now().UTC()
	event := Event{
		ID: eventID, Sequence: last + 1, WorkspaceID: store.workspaceID,
		SenderID: request.SenderID, SenderName: senderName, Recipients: recipients,
		Broadcast: request.Broadcast,
		Origin:    request.Origin, Kind: request.Kind, Body: request.Body,
		ReplyTo: request.ReplyTo, ThreadID: eventID, RequestID: request.RequestID,
		RequestRevision: request.RequestRevision, RunID: request.RunID,
		Supersedes: request.Supersedes, References: request.References, CreatedAt: now,
	}
	if request.ReplyTo != "" {
		parent, err := loadEvent(ctx, tx, request.ReplyTo)
		if err != nil {
			return PublishResult{}, fmt.Errorf("collaboration: reply target: %w", err)
		}
		event.ThreadID = parent.ThreadID
		event.Archived = parent.Archived
	}
	if request.Supersedes != "" {
		previous, err := loadEvent(ctx, tx, request.Supersedes)
		if err != nil {
			return PublishResult{}, fmt.Errorf("collaboration: superseded event: %w", err)
		}
		if previous.SenderID != request.SenderID {
			return PublishResult{}, fmt.Errorf("%w: cannot supersede another sender", ErrConflict)
		}
		if request.ReplyTo == "" {
			event.ThreadID = previous.ThreadID
		}
	}
	if err := prepareRequestEvent(ctx, tx, request, &event); err != nil {
		return PublishResult{}, err
	}
	encoded, err := json.Marshal(event)
	if err != nil {
		return PublishResult{}, err
	}
	if len(encoded) > MaxBodyBytes*2 {
		return PublishResult{}, ErrTooLarge
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO collaboration_events
		(id,workspace_id,sequence,sender_id,idempotency_key,fingerprint,origin,kind,body,
		reply_to,thread_id,request_id,request_revision,run_id,supersedes,event_json,payload_bytes,archived,created_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		event.ID, event.WorkspaceID, event.Sequence, event.SenderID, request.IdempotencyKey,
		fingerprint, event.Origin, event.Kind, event.Body, event.ReplyTo, event.ThreadID,
		event.RequestID, event.RequestRevision, event.RunID, event.Supersedes,
		string(encoded), len(encoded), event.Archived, now.UnixNano()); err != nil {
		return PublishResult{}, err
	}
	deliveries := make([]Delivery, 0, len(recipients))
	for _, recipient := range recipients {
		delivery := Delivery{
			ID: newID("delivery"), EventID: event.ID, RecipientID: recipient.ID,
			State: DeliveryPending, Version: 1, CreatedAt: now, UpdatedAt: now,
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO collaboration_deliveries
			(id,event_id,recipient_id,state,version,created_at,updated_at)
			VALUES(?,?,?,?,?,?,?)`,
			delivery.ID, delivery.EventID, delivery.RecipientID, delivery.State,
			delivery.Version, now.UnixNano(), now.UnixNano()); err != nil {
			return PublishResult{}, err
		}
		deliveries = append(deliveries, delivery)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE collaboration_state SET last_sequence=? WHERE singleton=1`, event.Sequence); err != nil {
		return PublishResult{}, err
	}
	if event.Supersedes != "" {
		if _, err := tx.ExecContext(ctx, `UPDATE collaboration_deliveries SET
			state='superseded',version=version+1,reason=?,updated_at=?
			WHERE event_id=? AND state='pending'`,
			"superseded by "+event.ID, now.UnixNano(), event.Supersedes); err != nil {
			return PublishResult{}, err
		}
	}
	return PublishResult{Event: event, Deliveries: deliveries}, nil
}

func (store *Store) ReservePending(ctx context.Context, request ReserveRequest) ([]PendingDelivery, error) {
	if err := validateIdentity("recipient_id", request.RecipientID, 256); err != nil {
		return nil, err
	}
	if request.RuntimeEpoch == 0 {
		return nil, fmt.Errorf("%w: runtime epoch", ErrInvalid)
	}
	if request.ThroughSequence < 0 {
		return nil, fmt.Errorf("%w: negative admission sequence", ErrInvalid)
	}
	if request.Limit <= 0 {
		request.Limit = 20
	}
	if request.Limit > MaxPageLimit {
		request.Limit = MaxPageLimit
	}
	request.OnlyKinds = normalizedEventKinds(request.OnlyKinds)
	request.ExcludeKinds = normalizedEventKinds(request.ExcludeKinds)
	for _, kind := range append(append([]EventKind(nil), request.OnlyKinds...), request.ExcludeKinds...) {
		if !validateEventKind(kind) {
			return nil, fmt.Errorf("%w: invalid reserved event kind", ErrInvalid)
		}
	}
	if len(request.OnlyKinds) > 0 && len(request.ExcludeKinds) > 0 {
		return nil, fmt.Errorf("%w: reserve kind filters conflict", ErrInvalid)
	}
	var result []PendingDelivery
	err := store.transaction(ctx, func(tx *sql.Tx) error {
		recipient, err := loadAgent(ctx, tx, request.RecipientID)
		if err != nil {
			return err
		}
		allowSuspendedControl := len(request.OnlyKinds) == 1 &&
			request.OnlyKinds[0] == EventLifecycleControl &&
			recipient.State == AgentSuspended
		if (recipient.State != AgentAvailable && !allowSuspendedControl) ||
			recipient.Presence != RuntimeAttached || recipient.RuntimeEpoch != request.RuntimeEpoch {
			return ErrUnavailable
		}
		statement := `SELECT d.id,d.event_id,d.recipient_id,d.state,d.version,
			d.reserved_epoch,d.context_commit_id,d.reason,d.created_at,d.updated_at,d.admitted_at,e.event_json
			FROM collaboration_deliveries d
			JOIN collaboration_events e ON e.id=d.event_id
			WHERE d.recipient_id=? AND d.state='pending'
		`
		arguments := []any{request.RecipientID}
		if request.ThroughSequence > 0 {
			statement += ` AND e.sequence<=?`
			arguments = append(arguments, request.ThroughSequence)
		}
		if len(request.OnlyKinds) > 0 {
			statement += ` AND e.kind IN (` + strings.TrimRight(strings.Repeat("?,", len(request.OnlyKinds)), ",") + `)`
			for _, kind := range request.OnlyKinds {
				arguments = append(arguments, kind)
			}
		}
		if len(request.ExcludeKinds) > 0 {
			statement += ` AND e.kind NOT IN (` + strings.TrimRight(strings.Repeat("?,", len(request.ExcludeKinds)), ",") + `)`
			for _, kind := range request.ExcludeKinds {
				arguments = append(arguments, kind)
			}
		}
		statement += ` ORDER BY e.sequence,d.id LIMIT ?`
		arguments = append(arguments, request.Limit)
		rows, err := tx.QueryContext(ctx, statement, arguments...)
		if err != nil {
			return err
		}
		type candidate struct {
			delivery Delivery
			event    Event
		}
		var candidates []candidate
		for rows.Next() {
			delivery, event, err := scanPending(rows)
			if err != nil {
				_ = rows.Close()
				return err
			}
			candidates = append(candidates, candidate{delivery: delivery, event: event})
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return err
		}
		if err := rows.Close(); err != nil {
			return err
		}
		now := time.Now().UTC()
		for _, candidate := range candidates {
			update, err := tx.ExecContext(ctx, `UPDATE collaboration_deliveries SET
				state='admitting',version=version+1,reserved_epoch=?,updated_at=?
				WHERE id=? AND recipient_id=? AND state='pending' AND version=?`,
				request.RuntimeEpoch, now.UnixNano(), candidate.delivery.ID,
				request.RecipientID, candidate.delivery.Version)
			if err != nil {
				return err
			}
			affected, err := update.RowsAffected()
			if err != nil {
				return err
			}
			if affected != 1 {
				continue
			}
			candidate.delivery.State = DeliveryAdmitting
			candidate.delivery.Version++
			candidate.delivery.ReservedEpoch = request.RuntimeEpoch
			candidate.delivery.UpdatedAt = now
			result = append(result, PendingDelivery{Event: candidate.event, Delivery: candidate.delivery})
		}
		return nil
	})
	return result, err
}

func (store *Store) HasUnsettledAdmission(ctx context.Context, recipientID string) (bool, error) {
	var found bool
	err := store.readTransaction(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT EXISTS (
			SELECT 1 FROM collaboration_deliveries WHERE recipient_id=? AND state='admitting'
		)`, recipientID).Scan(&found)
	})
	return found, err
}

func normalizedEventKinds(kinds []EventKind) []EventKind {
	result := append([]EventKind(nil), kinds...)
	slices.Sort(result)
	return slices.Compact(result)
}

func (store *Store) Admit(ctx context.Context, request AdmitRequest) (Delivery, error) {
	if err := validateIdentity("delivery_id", request.DeliveryID, 256); err != nil {
		return Delivery{}, err
	}
	if err := validateIdentity("recipient_id", request.RecipientID, 256); err != nil {
		return Delivery{}, err
	}
	if err := validateIdentity("context_commit_id", request.ContextCommitID, 512); err != nil {
		return Delivery{}, err
	}
	if request.RuntimeEpoch == 0 || request.ExpectedVersion == 0 {
		return Delivery{}, fmt.Errorf("%w: delivery epoch/version", ErrInvalid)
	}
	var result Delivery
	err := store.transaction(ctx, func(tx *sql.Tx) error {
		recipient, err := loadAgent(ctx, tx, request.RecipientID)
		if err != nil {
			return err
		}
		if recipient.RuntimeEpoch != request.RuntimeEpoch || recipient.Presence != RuntimeAttached {
			return ErrStale
		}
		current, err := loadDelivery(ctx, tx, request.DeliveryID)
		if err != nil {
			return err
		}
		if current.RecipientID == request.RecipientID && current.State == DeliveryAdmitted &&
			current.ReservedEpoch == request.RuntimeEpoch && current.Version == request.ExpectedVersion+1 &&
			current.ContextCommitID == request.ContextCommitID {
			result = current
			return nil
		}
		now := time.Now().UTC()
		update, err := tx.ExecContext(ctx, `UPDATE collaboration_deliveries SET
			state='admitted',version=version+1,context_commit_id=?,updated_at=?,admitted_at=?
			WHERE id=? AND recipient_id=? AND state='admitting' AND reserved_epoch=? AND version=?`,
			request.ContextCommitID, now.UnixNano(), now.UnixNano(), request.DeliveryID,
			request.RecipientID, request.RuntimeEpoch, request.ExpectedVersion)
		if err != nil {
			return err
		}
		affected, err := update.RowsAffected()
		if err != nil {
			return err
		}
		if affected != 1 {
			return ErrStale
		}
		result, err = loadDelivery(ctx, tx, request.DeliveryID)
		return err
	})
	return result, err
}

func (store *Store) ReleaseReservation(ctx context.Context, request ReleaseRequest) (Delivery, error) {
	if err := validateIdentity("delivery_id", request.DeliveryID, 256); err != nil {
		return Delivery{}, err
	}
	if request.RuntimeEpoch == 0 || request.ExpectedVersion == 0 {
		return Delivery{}, fmt.Errorf("%w: delivery epoch/version", ErrInvalid)
	}
	next := DeliveryPending
	reservedEpoch := uint64(0)
	if request.Reject {
		next = DeliveryRejected
	}
	var result Delivery
	err := store.transaction(ctx, func(tx *sql.Tx) error {
		recipient, err := loadAgent(ctx, tx, request.RecipientID)
		if err != nil {
			return err
		}
		if recipient.RuntimeEpoch != request.RuntimeEpoch || recipient.Presence != RuntimeAttached {
			return ErrStale
		}
		now := time.Now().UTC()
		update, err := tx.ExecContext(ctx, `UPDATE collaboration_deliveries SET
			state=?,version=version+1,reserved_epoch=?,reason=?,updated_at=?
			WHERE id=? AND recipient_id=? AND state='admitting' AND reserved_epoch=? AND version=?`,
			next, reservedEpoch, request.Reason, now.UnixNano(), request.DeliveryID,
			request.RecipientID, request.RuntimeEpoch, request.ExpectedVersion)
		if err != nil {
			return err
		}
		affected, err := update.RowsAffected()
		if err != nil {
			return err
		}
		if affected != 1 {
			return ErrStale
		}
		result, err = loadDelivery(ctx, tx, request.DeliveryID)
		return err
	})
	return result, err
}

func (store *Store) Withdraw(ctx context.Context, request WithdrawRequest) ([]Delivery, error) {
	if err := validateIdentity("event_id", request.EventID, 256); err != nil {
		return nil, err
	}
	if err := validateIdentity("sender_id", request.SenderID, 256); err != nil {
		return nil, err
	}
	if err := validateIdentity("idempotency_key", request.IdempotencyKey, 512); err != nil {
		return nil, err
	}
	var result []Delivery
	err := store.transaction(ctx, func(tx *sql.Tx) error {
		event, err := loadEvent(ctx, tx, request.EventID)
		if err != nil {
			return err
		}
		if event.SenderID != request.SenderID {
			return ErrConflict
		}
		var key string
		if err := tx.QueryRowContext(ctx, `SELECT idempotency_key FROM collaboration_events WHERE id=?`, event.ID).Scan(&key); err != nil {
			return err
		}
		if key != request.IdempotencyKey {
			return ErrStale
		}
		var blocked int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM collaboration_deliveries
			WHERE event_id=? AND state!='pending'`, event.ID).Scan(&blocked); err != nil {
			return err
		}
		if blocked > 0 {
			return ErrAlreadyAdmitting
		}
		now := time.Now().UTC().UnixNano()
		if _, err := tx.ExecContext(ctx, `UPDATE collaboration_deliveries SET
			state='withdrawn',version=version+1,reason=?,updated_at=?
			WHERE event_id=? AND state='pending'`, request.Reason, now, event.ID); err != nil {
			return err
		}
		result, err = deliveriesForEvent(ctx, tx, event.ID)
		return err
	})
	return result, err
}

func (store *Store) Page(ctx context.Context, query PageQuery) (Page, error) {
	if query.Since < 0 || query.Snapshot < 0 {
		return Page{}, fmt.Errorf("%w: negative cursor", ErrInvalid)
	}
	if query.Limit <= 0 {
		query.Limit = DefaultPageLimit
	}
	if query.Limit > MaxPageLimit {
		query.Limit = MaxPageLimit
	}
	if len(query.Search) > 512 {
		return Page{}, ErrTooLarge
	}
	if query.Kind != "" && !validateEventKind(query.Kind) {
		return Page{}, fmt.Errorf("%w: invalid event kind", ErrInvalid)
	}
	var page Page
	err := store.readTransaction(ctx, func(tx *sql.Tx) error {
		if err := tx.QueryRowContext(ctx, `SELECT last_sequence,human_read_sequence FROM collaboration_state WHERE singleton=1`).
			Scan(&page.LatestSequence, &page.HumanReadThrough); err != nil {
			return err
		}
		if query.Snapshot == 0 {
			query.Snapshot = page.LatestSequence
		}
		if query.Snapshot > page.LatestSequence || query.Since > query.Snapshot {
			return fmt.Errorf("%w: cursor snapshot", ErrInvalid)
		}
		page.Snapshot = query.Snapshot
		page.Cursor = query.Since
		statement := `SELECT e.event_json FROM collaboration_events e`
		arguments := []any{}
		if query.RecipientID != "" {
			statement += ` JOIN collaboration_deliveries d ON d.event_id=e.id`
		}
		statement += ` WHERE e.sequence>? AND e.sequence<=?`
		arguments = append(arguments, query.Since, query.Snapshot)
		if query.ThreadID != "" {
			thread, err := loadEvent(ctx, tx, query.ThreadID)
			if err != nil {
				return err
			}
			statement += ` AND e.thread_id=?`
			arguments = append(arguments, thread.ThreadID)
		}
		if query.Search != "" {
			statement += ` AND (instr(lower(e.body),lower(?))>0 OR instr(lower(e.event_json),lower(?))>0)`
			arguments = append(arguments, query.Search, query.Search)
		}
		if query.SenderID != "" {
			statement += ` AND e.sender_id=?`
			arguments = append(arguments, query.SenderID)
		}
		if query.RecipientID != "" {
			statement += ` AND d.recipient_id=?`
			arguments = append(arguments, query.RecipientID)
		}
		if query.Kind != "" {
			statement += ` AND e.kind=?`
			arguments = append(arguments, query.Kind)
		}
		statement += ` ORDER BY e.sequence LIMIT ?`
		arguments = append(arguments, query.Limit+1)
		rows, err := tx.QueryContext(ctx, statement, arguments...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			if len(page.Events) >= query.Limit {
				page.HasMore = true
				break
			}
			var encoded string
			if err := rows.Scan(&encoded); err != nil {
				return err
			}
			event, err := decodeEvent(encoded)
			if err != nil {
				return err
			}
			previousCursor := page.Cursor
			page.Events = append(page.Events, event)
			page.Cursor = event.Sequence
			size, err := pageBytes(&page)
			if err != nil {
				return err
			}
			if size > MaxPageBytes {
				page.Events = page.Events[:len(page.Events)-1]
				page.Cursor = previousCursor
				page.HasMore = true
				break
			}
		}
		if err := rows.Err(); err != nil {
			return err
		}
		_, err = pageBytes(&page)
		return err
	})
	return page, err
}

func (store *Store) MarkHumanRead(ctx context.Context, through int64) error {
	if through < 0 {
		return fmt.Errorf("%w: negative human cursor", ErrInvalid)
	}
	return store.transaction(ctx, func(tx *sql.Tx) error {
		var latest, current int64
		if err := tx.QueryRowContext(ctx, `SELECT last_sequence,human_read_sequence FROM collaboration_state WHERE singleton=1`).
			Scan(&latest, &current); err != nil {
			return err
		}
		if through > latest {
			return ErrStale
		}
		if through <= current {
			return nil
		}
		_, err := tx.ExecContext(ctx, `UPDATE collaboration_state SET human_read_sequence=? WHERE singleton=1`, through)
		return err
	})
}

func (store *Store) HumanUnread(ctx context.Context) (int, error) {
	var unread int
	err := store.readTransaction(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM collaboration_events e
			JOIN collaboration_state s ON s.singleton=1
			WHERE e.sequence>s.human_read_sequence`).Scan(&unread)
	})
	return unread, err
}

func validatePublishRequest(request PublishRequest) error {
	if err := validateIdentity("sender_id", request.SenderID, 256); err != nil {
		return err
	}
	if err := validateIdentity("idempotency_key", request.IdempotencyKey, 512); err != nil {
		return err
	}
	if !validateOrigin(request.Origin) || !validateEventKind(request.Kind) {
		return fmt.Errorf("%w: origin/kind", ErrInvalid)
	}
	if err := validateBody(request.Body, MaxBodyBytes); err != nil {
		return err
	}
	if err := validateOptionalIdentity("reply_to", request.ReplyTo, 256); err != nil {
		return err
	}
	if err := validateOptionalIdentity("request_id", request.RequestID, 256); err != nil {
		return err
	}
	if err := validateOptionalIdentity("run_id", request.RunID, 256); err != nil {
		return err
	}
	if err := validateOptionalIdentity("supersedes", request.Supersedes, 256); err != nil {
		return err
	}
	if err := validateReferences(request.References); err != nil {
		return err
	}
	if request.Broadcast && len(request.RecipientIDs) > 0 {
		return fmt.Errorf("%w: broadcast cannot also list recipients", ErrInvalid)
	}
	if request.Broadcast && (request.Origin != OriginPeer || request.Kind != EventPeerNote) {
		return fmt.Errorf("%w: broadcast is only available for peer notes", ErrInvalid)
	}
	if request.AuditOnly && (request.Origin != OriginRuntime || request.Kind != EventControlOutcome ||
		request.Broadcast || len(request.RecipientIDs) != 0) {
		return fmt.Errorf("%w: audit-only publication is reserved for runtime control outcomes", ErrInvalid)
	}
	if !request.Broadcast && len(request.RecipientIDs) == 0 && !request.AuditOnly {
		return fmt.Errorf("%w: recipient required", ErrInvalid)
	}
	if (request.Kind == EventWorkRevision || request.Kind == EventRequestDisposition) &&
		(request.RequestID == "" || request.RequestRevision == 0) {
		return fmt.Errorf("%w: exact request_id and request_revision required", ErrInvalid)
	}
	for _, recipient := range request.RecipientIDs {
		if err := validateIdentity("recipient_id", recipient, 256); err != nil {
			return err
		}
	}
	return nil
}

func normalizedRecipients(ids []string) []string {
	result := slices.Clone(ids)
	slices.Sort(result)
	return slices.Compact(result)
}

func resolveRecipients(ctx context.Context, tx *sql.Tx, senderID string, ids []string, broadcast bool) ([]Recipient, error) {
	var result []Recipient
	if broadcast {
		rows, err := tx.QueryContext(ctx, `SELECT a.id,a.name,a.state,a.presence
			FROM collaboration_agents a
			JOIN collaboration_runs r ON r.id=a.current_run_id
			WHERE a.id!=?
			  AND a.state='available'
			  AND a.presence='attached'
			  AND a.current_run_id!=''
			  AND r.agent_id=a.id
			  AND r.runtime_epoch=a.runtime_epoch
			  AND r.state NOT IN ('succeeded','failed','cancelled','interrupted')
			ORDER BY a.id`, senderID)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var recipient Recipient
			if err := rows.Scan(&recipient.ID, &recipient.Name, &recipient.State, &recipient.Presence); err != nil {
				return nil, err
			}
			result = append(result, recipient)
		}
		return result, rows.Err()
	}
	for _, id := range ids {
		agent, err := loadAgent(ctx, tx, id)
		if err != nil {
			return nil, fmt.Errorf("collaboration: recipient %q: %w", id, err)
		}
		result = append(result, Recipient{ID: agent.ID, Name: agent.Name, State: agent.State, Presence: agent.Presence})
	}
	return result, nil
}

func loadEvent(ctx context.Context, tx *sql.Tx, id string) (Event, error) {
	var encoded string
	err := tx.QueryRowContext(ctx, `SELECT event_json FROM collaboration_events WHERE id=?`, id).Scan(&encoded)
	if errors.Is(err, sql.ErrNoRows) {
		return Event{}, ErrNotFound
	}
	if err != nil {
		return Event{}, err
	}
	return decodeEvent(encoded)
}

func decodeEvent(encoded string) (Event, error) {
	var event Event
	if err := json.Unmarshal([]byte(encoded), &event); err != nil {
		return Event{}, fmt.Errorf("collaboration: decode event: %w", err)
	}
	return event, nil
}

func loadDelivery(ctx context.Context, tx *sql.Tx, id string) (Delivery, error) {
	row := tx.QueryRowContext(ctx, `SELECT id,event_id,recipient_id,state,version,reserved_epoch,
		context_commit_id,reason,created_at,updated_at,admitted_at
		FROM collaboration_deliveries WHERE id=?`, id)
	return scanDelivery(row)
}

func scanDelivery(row scanner) (Delivery, error) {
	var delivery Delivery
	var state string
	var createdAt, updatedAt, admittedAt int64
	err := row.Scan(&delivery.ID, &delivery.EventID, &delivery.RecipientID, &state,
		&delivery.Version, &delivery.ReservedEpoch, &delivery.ContextCommitID,
		&delivery.Reason, &createdAt, &updatedAt, &admittedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Delivery{}, ErrNotFound
	}
	if err != nil {
		return Delivery{}, err
	}
	delivery.State = DeliveryState(state)
	delivery.CreatedAt = timeFromUnixNano(createdAt)
	delivery.UpdatedAt = timeFromUnixNano(updatedAt)
	delivery.AdmittedAt = timeFromUnixNano(admittedAt)
	return delivery, nil
}

func scanPending(row scanner) (Delivery, Event, error) {
	var delivery Delivery
	var state, encoded string
	var createdAt, updatedAt, admittedAt int64
	err := row.Scan(&delivery.ID, &delivery.EventID, &delivery.RecipientID, &state,
		&delivery.Version, &delivery.ReservedEpoch, &delivery.ContextCommitID,
		&delivery.Reason, &createdAt, &updatedAt, &admittedAt, &encoded)
	if err != nil {
		return Delivery{}, Event{}, err
	}
	delivery.State = DeliveryState(state)
	delivery.CreatedAt = timeFromUnixNano(createdAt)
	delivery.UpdatedAt = timeFromUnixNano(updatedAt)
	delivery.AdmittedAt = timeFromUnixNano(admittedAt)
	event, err := decodeEvent(encoded)
	return delivery, event, err
}

func deliveriesForEvent(ctx context.Context, tx *sql.Tx, eventID string) ([]Delivery, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id,event_id,recipient_id,state,version,reserved_epoch,
		context_commit_id,reason,created_at,updated_at,admitted_at
		FROM collaboration_deliveries WHERE event_id=? ORDER BY recipient_id`, eventID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []Delivery
	for rows.Next() {
		delivery, err := scanDelivery(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, delivery)
	}
	return result, rows.Err()
}

func pageBytes(page *Page) (int, error) {
	for range 4 {
		encoded, err := json.Marshal(page)
		if err != nil {
			return 0, err
		}
		if page.Bytes == len(encoded) {
			return len(encoded), nil
		}
		page.Bytes = len(encoded)
	}
	return 0, errors.New("collaboration: page byte accounting did not converge")
}
