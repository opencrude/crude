package collaboration

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"time"
)

func (store *Store) AcquireClaims(ctx context.Context, request AcquireClaimsRequest) ([]Claim, error) {
	if err := validateIdentity("agent_id", request.AgentID, 256); err != nil {
		return nil, err
	}
	if err := validateOptionalIdentity("run_id", request.RunID, 256); err != nil {
		return nil, err
	}
	if err := validateOptionalIdentity("request_id", request.RequestID, 256); err != nil {
		return nil, err
	}
	if len(request.Paths) == 0 || len(request.Paths) > 256 {
		return nil, fmt.Errorf("%w: claim paths", ErrInvalid)
	}
	paths := make([]string, 0, len(request.Paths))
	for _, path := range request.Paths {
		normalized, err := normalizedPath(path)
		if err != nil {
			return nil, err
		}
		paths = append(paths, normalized)
	}
	slices.Sort(paths)
	paths = slices.Compact(paths)

	var result []Claim
	err := store.transaction(ctx, func(tx *sql.Tx) error {
		agent, err := loadAgent(ctx, tx, request.AgentID)
		if err != nil {
			return err
		}
		if agent.State == AgentClosed {
			return ErrUnavailable
		}
		if request.RunID != "" {
			run, err := loadRun(ctx, tx, request.RunID)
			if err != nil {
				return err
			}
			if run.AgentID != request.AgentID || run.State.Terminal() ||
				run.RuntimeEpoch != agent.RuntimeEpoch || agent.CurrentRunID != run.ID || agent.Presence != RuntimeAttached {
				return fmt.Errorf("%w: inactive or foreign claim run", ErrConflict)
			}
		}
		active, err := activeClaims(ctx, tx)
		if err != nil {
			return err
		}
		for _, path := range paths {
			reused := false
			for _, existing := range active {
				if !pathsOverlap(path, existing.Path) {
					continue
				}
				if existing.AgentID == request.AgentID && existing.RunID == request.RunID && existing.Path == path {
					result = append(result, existing)
					reused = true
					break
				}
				if existing.AgentID == request.AgentID && existing.RunID == request.RunID {
					continue
				}
				return fmt.Errorf("%w: path %q overlaps claim %s owned by agent %s",
					ErrConflict, path, existing.ID, existing.AgentID)
			}
			if reused {
				continue
			}
			now := time.Now().UTC()
			claim := Claim{
				ID: newID("claim"), Path: path, AgentID: request.AgentID,
				RunID: request.RunID, RequestID: request.RequestID, Version: 1,
				AcquiredAt: now,
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO collaboration_claims
				(id,path,agent_id,run_id,request_id,version,acquired_at)
				VALUES(?,?,?,?,?,?,?)`,
				claim.ID, claim.Path, claim.AgentID, claim.RunID, claim.RequestID,
				claim.Version, now.UnixNano()); err != nil {
				return err
			}
			active = append(active, claim)
			result = append(result, claim)
		}
		return nil
	})
	return result, err
}

func (store *Store) ReleaseClaims(ctx context.Context, request ReleaseClaimsRequest) ([]Claim, error) {
	if err := validateIdentity("agent_id", request.AgentID, 256); err != nil {
		return nil, err
	}
	if len(request.ClaimIDs) == 0 {
		return nil, fmt.Errorf("%w: claim ids", ErrInvalid)
	}
	ids := slices.Clone(request.ClaimIDs)
	slices.Sort(ids)
	ids = slices.Compact(ids)
	var released []Claim
	err := store.transaction(ctx, func(tx *sql.Tx) error {
		now := time.Now().UTC()
		for _, id := range ids {
			if err := validateIdentity("claim_id", id, 256); err != nil {
				return err
			}
			current, err := loadClaim(ctx, tx, id)
			if err != nil {
				return err
			}
			expected := request.ExpectedVersions[id]
			if current.AgentID != request.AgentID || !current.ReleasedAt.IsZero() ||
				expected == 0 || current.Version != expected {
				return ErrStale
			}
			if request.RunID != "" && current.RunID != request.RunID {
				return ErrConflict
			}
			update, err := tx.ExecContext(ctx, `UPDATE collaboration_claims
				SET version=version+1,released_at=?
				WHERE id=? AND agent_id=? AND version=? AND released_at=0`,
				now.UnixNano(), id, request.AgentID, expected)
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
			current.Version++
			current.ReleasedAt = now
			released = append(released, current)
		}
		return nil
	})
	return released, err
}

func (store *Store) Claims(ctx context.Context, agentID string, activeOnly bool) ([]Claim, error) {
	var result []Claim
	err := store.readTransaction(ctx, func(tx *sql.Tx) error {
		statement := `SELECT id,path,agent_id,run_id,request_id,version,acquired_at,released_at
			FROM collaboration_claims WHERE 1=1`
		var arguments []any
		if agentID != "" {
			statement += ` AND agent_id=?`
			arguments = append(arguments, agentID)
		}
		if activeOnly {
			statement += ` AND released_at=0`
		}
		statement += ` ORDER BY acquired_at,id`
		rows, err := tx.QueryContext(ctx, statement, arguments...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			claim, err := scanClaim(rows)
			if err != nil {
				return err
			}
			result = append(result, claim)
		}
		return rows.Err()
	})
	return result, err
}

func activeClaims(ctx context.Context, tx *sql.Tx) ([]Claim, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id,path,agent_id,run_id,request_id,version,acquired_at,released_at
		FROM collaboration_claims WHERE released_at=0 ORDER BY acquired_at,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []Claim
	for rows.Next() {
		claim, err := scanClaim(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, claim)
	}
	return result, rows.Err()
}

func loadClaim(ctx context.Context, tx *sql.Tx, id string) (Claim, error) {
	return scanClaim(tx.QueryRowContext(ctx, `SELECT id,path,agent_id,run_id,request_id,version,acquired_at,released_at
		FROM collaboration_claims WHERE id=?`, id))
}

func scanClaim(row scanner) (Claim, error) {
	var claim Claim
	var acquiredAt, releasedAt int64
	err := row.Scan(&claim.ID, &claim.Path, &claim.AgentID, &claim.RunID,
		&claim.RequestID, &claim.Version, &acquiredAt, &releasedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Claim{}, ErrNotFound
	}
	if err != nil {
		return Claim{}, err
	}
	claim.AcquiredAt = timeFromUnixNano(acquiredAt)
	claim.ReleasedAt = timeFromUnixNano(releasedAt)
	return claim, nil
}
