package collaboration

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"
)

func (store *Store) PublishReport(ctx context.Context, request PublishReportRequest) (PublishReportResult, error) {
	if err := validateIdentity("author_id", request.AuthorID, 256); err != nil {
		return PublishReportResult{}, err
	}
	if err := validateIdentity("idempotency_key", request.IdempotencyKey, 512); err != nil {
		return PublishReportResult{}, err
	}
	if !validateReportKind(request.Kind) {
		return PublishReportResult{}, fmt.Errorf("%w: report kind", ErrInvalid)
	}
	if err := validateBody(request.Summary, MaxBodyBytes); err != nil {
		return PublishReportResult{}, fmt.Errorf("report summary: %w", err)
	}
	if err := validateBody(request.Body, MaxArtifactBytes); err != nil {
		return PublishReportResult{}, fmt.Errorf("report body: %w", err)
	}
	if err := validateOptionalIdentity("run_id", request.RunID, 256); err != nil {
		return PublishReportResult{}, err
	}
	if err := validateOptionalIdentity("request_id", request.RequestID, 256); err != nil {
		return PublishReportResult{}, err
	}
	if err := validateReferences(request.References); err != nil {
		return PublishReportResult{}, err
	}
	request.RecipientIDs = normalizedRecipients(request.RecipientIDs)
	if len(request.RecipientIDs) == 0 {
		return PublishReportResult{}, fmt.Errorf("%w: report recipient required", ErrInvalid)
	}
	if request.ArtifactID == "" {
		identity, _ := json.Marshal([]string{store.workspaceID, request.AuthorID, request.IdempotencyKey})
		request.ArtifactID = "artifact_" + hashBytes(identity)
	}
	if err := validateIdentity("artifact_id", request.ArtifactID, 256); err != nil {
		return PublishReportResult{}, err
	}
	request.References = slices.Clone(request.References)
	request.Metadata = cloneMetadata(request.Metadata)
	fingerprint, _, err := hashJSON(request)
	if err != nil {
		return PublishReportResult{}, err
	}

	var result PublishReportResult
	err = store.transaction(ctx, func(tx *sql.Tx) error {
		var previousFingerprint, artifactID, eventID string
		var revision int
		err := tx.QueryRowContext(ctx, `SELECT fingerprint,artifact_id,revision,event_id
			FROM collaboration_report_dedupe WHERE author_id=? AND idempotency_key=?`,
			request.AuthorID, request.IdempotencyKey).
			Scan(&previousFingerprint, &artifactID, &revision, &eventID)
		if err == nil {
			if previousFingerprint != fingerprint {
				return ErrChangedRetry
			}
			artifact, err := loadArtifact(ctx, tx, artifactID, revision)
			if err != nil {
				return err
			}
			event, err := loadEvent(ctx, tx, eventID)
			if err != nil {
				return err
			}
			deliveries, err := deliveriesForEvent(ctx, tx, event.ID)
			if err != nil {
				return err
			}
			result = PublishReportResult{
				Artifact: artifact,
				Publish:  PublishResult{Event: event, Deliveries: deliveries, Duplicate: true},
			}
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}

		author, err := loadAgent(ctx, tx, request.AuthorID)
		if err != nil {
			return fmt.Errorf("collaboration: report author: %w", err)
		}
		// New publications must come from the current runtime. The dedupe check
		// above intentionally precedes this guard so an accepted call remains
		// replayable after its run finishes or its owner detaches.
		if author.State != AgentAvailable || author.Presence != RuntimeAttached {
			return fmt.Errorf("%w: report author is not attached and available", ErrUnavailable)
		}
		if request.RunID != "" {
			run, err := loadRun(ctx, tx, request.RunID)
			if err != nil {
				return fmt.Errorf("collaboration: report run: %w", err)
			}
			if run.AgentID != request.AuthorID {
				return fmt.Errorf("%w: report run belongs to another agent", ErrConflict)
			}
			if run.RuntimeEpoch != author.RuntimeEpoch || author.CurrentRunID != run.ID || run.State.Terminal() {
				return fmt.Errorf("%w: report run is not current", ErrStale)
			}
		}
		if request.Kind == ReportFinal && author.ParentID != "" {
			if request.RunID == "" {
				return fmt.Errorf("%w: child final report requires an active owned run", ErrInvalid)
			}
			if !slices.Contains(request.RecipientIDs, author.ParentID) {
				return fmt.Errorf("%w: child final report must address its parent", ErrInvalid)
			}
		}
		var latest sql.NullInt64
		if err := tx.QueryRowContext(ctx, `SELECT MAX(revision) FROM collaboration_artifacts WHERE id=?`, request.ArtifactID).Scan(&latest); err != nil {
			return err
		}
		if latest.Valid {
			if request.SupersedesRevision != int(latest.Int64) {
				return fmt.Errorf("%w: report revision %d is current", ErrStale, latest.Int64)
			}
			previous, err := loadArtifact(ctx, tx, request.ArtifactID, int(latest.Int64))
			if err != nil {
				return err
			}
			if previous.WorkspaceID != store.workspaceID || previous.AuthorID != request.AuthorID ||
				previous.RunID != request.RunID || previous.RequestID != request.RequestID {
				return fmt.Errorf("%w: report provenance cannot change", ErrConflict)
			}
			if previous.Kind == ReportFinal && request.Kind != ReportFinal {
				return fmt.Errorf("%w: final report cannot be downgraded", ErrConflict)
			}
			revision = int(latest.Int64) + 1
		} else {
			if request.SupersedesRevision != 0 {
				return fmt.Errorf("%w: report has no previous revision", ErrStale)
			}
			revision = 1
		}
		if err := validateReportRequest(ctx, tx, author, request.RunID, request.RequestID); err != nil {
			return err
		}
		if request.Kind == ReportFinal && author.ParentID != "" {
			var otherID string
			err := tx.QueryRowContext(ctx, `SELECT id FROM collaboration_artifacts
				WHERE workspace_id=? AND run_id=? AND author_id=? AND kind=?
				AND id<>? LIMIT 1`,
				store.workspaceID, request.RunID, request.AuthorID, ReportFinal, request.ArtifactID).Scan(&otherID)
			if err == nil {
				return fmt.Errorf("%w: child run already has final report %s", ErrConflict, otherID)
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
		}
		referencesJSON, err := json.Marshal(request.References)
		if err != nil {
			return err
		}
		metadataJSON, err := json.Marshal(request.Metadata)
		if err != nil {
			return err
		}
		digestInput, err := json.Marshal(struct {
			Summary    string
			Body       string
			References []Reference
			Metadata   map[string]any
		}{request.Summary, request.Body, request.References, request.Metadata})
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		artifact := Artifact{
			ID: request.ArtifactID, Revision: revision, WorkspaceID: store.workspaceID,
			AuthorID: request.AuthorID, RunID: request.RunID, RequestID: request.RequestID,
			Kind: request.Kind, Summary: request.Summary, Body: request.Body,
			Digest: hashBytes(digestInput), SupersedesRevision: request.SupersedesRevision,
			References: request.References, Metadata: request.Metadata, CreatedAt: now,
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO collaboration_artifacts
			(id,revision,workspace_id,author_id,run_id,request_id,kind,summary,body,digest,
			supersedes_revision,references_json,metadata_json,created_at)
			VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			artifact.ID, artifact.Revision, artifact.WorkspaceID, artifact.AuthorID,
			artifact.RunID, artifact.RequestID, artifact.Kind, artifact.Summary,
			artifact.Body, artifact.Digest, artifact.SupersedesRevision,
			string(referencesJSON), string(metadataJSON), now.UnixNano()); err != nil {
			return err
		}

		published, err := store.publishTx(ctx, tx, PublishRequest{
			SenderID: request.AuthorID, SenderName: author.Name,
			IdempotencyKey: "report:" + request.IdempotencyKey,
			RecipientIDs:   request.RecipientIDs, Origin: OriginPeer,
			Kind: EventReportPublished, Body: request.Summary, RequestID: request.RequestID,
			RunID: request.RunID, References: []Reference{{
				Kind: "report", ID: artifact.ID, Revision: artifact.Revision,
			}},
		})
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO collaboration_report_dedupe
			(author_id,idempotency_key,fingerprint,artifact_id,revision,event_id)
			VALUES(?,?,?,?,?,?)`,
			request.AuthorID, request.IdempotencyKey, fingerprint, artifact.ID,
			artifact.Revision, published.Event.ID); err != nil {
			return err
		}
		result = PublishReportResult{Artifact: artifact, Publish: published}
		return nil
	})
	if err == nil {
		store.signal()
	}
	return result, err
}

func (store *Store) Artifact(ctx context.Context, id string, revision int) (Artifact, error) {
	if err := validateIdentity("artifact_id", id, 256); err != nil {
		return Artifact{}, err
	}
	if revision < 0 {
		return Artifact{}, fmt.Errorf("%w: negative report revision", ErrInvalid)
	}
	var result Artifact
	err := store.readTransaction(ctx, func(tx *sql.Tx) error {
		var err error
		result, err = loadArtifact(ctx, tx, id, revision)
		return err
	})
	return result, err
}

func (store *Store) ListArtifacts(ctx context.Context, authorID, requestID, runID string, limit int) ([]Artifact, error) {
	if limit <= 0 {
		limit = DefaultReportLimit
	}
	if limit > MaxReportLimit {
		limit = MaxReportLimit
	}
	var result []Artifact
	err := store.readTransaction(ctx, func(tx *sql.Tx) error {
		statement := `SELECT id,revision,workspace_id,author_id,run_id,request_id,kind,
			summary,body,digest,supersedes_revision,references_json,metadata_json,created_at
			FROM collaboration_artifacts WHERE workspace_id=?`
		arguments := []any{store.workspaceID}
		if authorID != "" {
			statement += ` AND author_id=?`
			arguments = append(arguments, authorID)
		}
		if requestID != "" {
			statement += ` AND request_id=?`
			arguments = append(arguments, requestID)
		}
		if runID != "" {
			statement += ` AND run_id=?`
			arguments = append(arguments, runID)
		}
		statement += ` ORDER BY created_at DESC,id,revision DESC LIMIT ?`
		arguments = append(arguments, limit)
		rows, err := tx.QueryContext(ctx, statement, arguments...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			artifact, err := scanArtifact(rows)
			if err != nil {
				return err
			}
			result = append(result, artifact)
		}
		return rows.Err()
	})
	return result, err
}

func loadArtifact(ctx context.Context, tx *sql.Tx, id string, revision int) (Artifact, error) {
	statement := `SELECT id,revision,workspace_id,author_id,run_id,request_id,kind,
		summary,body,digest,supersedes_revision,references_json,metadata_json,created_at
		FROM collaboration_artifacts WHERE id=?`
	arguments := []any{id}
	if revision > 0 {
		statement += ` AND revision=?`
		arguments = append(arguments, revision)
	} else {
		statement += ` ORDER BY revision DESC LIMIT 1`
	}
	return scanArtifact(tx.QueryRowContext(ctx, statement, arguments...))
}

func scanArtifact(row scanner) (Artifact, error) {
	var artifact Artifact
	var kind, referencesJSON, metadataJSON string
	var createdAt int64
	err := row.Scan(&artifact.ID, &artifact.Revision, &artifact.WorkspaceID,
		&artifact.AuthorID, &artifact.RunID, &artifact.RequestID, &kind,
		&artifact.Summary, &artifact.Body, &artifact.Digest,
		&artifact.SupersedesRevision, &referencesJSON, &metadataJSON, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Artifact{}, ErrNotFound
	}
	if err != nil {
		return Artifact{}, err
	}
	artifact.Kind = ReportKind(kind)
	artifact.CreatedAt = timeFromUnixNano(createdAt)
	if err := json.Unmarshal([]byte(referencesJSON), &artifact.References); err != nil {
		return Artifact{}, fmt.Errorf("collaboration: decode report references: %w", err)
	}
	if err := json.Unmarshal([]byte(metadataJSON), &artifact.Metadata); err != nil {
		return Artifact{}, fmt.Errorf("collaboration: decode report metadata: %w", err)
	}
	return artifact, nil
}

func cloneMetadata(input map[string]any) map[string]any {
	if input == nil {
		return map[string]any{}
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		return map[string]any{}
	}
	var output map[string]any
	if json.Unmarshal(encoded, &output) != nil || output == nil {
		return map[string]any{}
	}
	return output
}
