package collaboration

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// UpdateClaims atomically repairs the nonexecuting claim configuration for a
// child after one exact terminal attempt. It never wakes or starts a run.
func (store *Store) UpdateClaims(ctx context.Context, request UpdateClaimsRequest) (UpdateClaimsResult, error) {
	var result UpdateClaimsResult
	if request.ParentID == "" || request.AgentID == "" || request.RuntimeEpoch == 0 || request.TerminalRunID == "" || request.ExpectedRunVersion == 0 || len(request.Paths) == 0 || len(request.Paths) > 256 {
		return result, fmt.Errorf("%w: claim update identity", ErrInvalid)
	}
	paths := make([]string, 0, len(request.Paths))
	for _, path := range request.Paths {
		normalized, err := normalizedPath(path)
		if err != nil {
			return result, err
		}
		paths = append(paths, normalized)
	}
	slices.Sort(paths)
	paths = slices.Compact(paths)
	err := store.transaction(ctx, func(tx *sql.Tx) error {
		agent, err := loadAgent(ctx, tx, request.AgentID)
		if err != nil {
			return err
		}
		if agent.ParentID != request.ParentID || agent.RuntimeEpoch != request.RuntimeEpoch || agent.CurrentRunID != "" {
			return ErrStale
		}
		run, err := loadRun(ctx, tx, request.TerminalRunID)
		if err != nil {
			return err
		}
		if run.AgentID != agent.ID || run.RuntimeEpoch != agent.RuntimeEpoch || run.Version != request.ExpectedRunVersion || !run.State.Terminal() {
			return ErrStale
		}
		if run.State != RunInterrupted {
			return fmt.Errorf("%w: claim repair requires an interrupted attempt", ErrConflict)
		}
		active, err := activeClaims(ctx, tx)
		if err != nil {
			return err
		}
		current := make([]Claim, 0)
		for _, claim := range active {
			if claim.AgentID == agent.ID && claim.RunID == "" {
				current = append(current, claim)
			}
		}
		if len(current) != len(request.ExpectedVersions) {
			return ErrStale
		}
		for _, claim := range current {
			if request.ExpectedVersions[claim.ID] != claim.Version {
				return ErrStale
			}
		}
		ceiling, err := claimRepairCeiling(ctx, tx, agent.ID, run.RequestID)
		if err != nil {
			return err
		}
		for _, path := range paths {
			if !containedByAny(path, ceiling) {
				return fmt.Errorf("%w: path %q exceeds original resource ceiling", ErrConflict, path)
			}
		}
		now := time.Now().UTC()
		for _, claim := range current {
			expected := request.ExpectedVersions[claim.ID]
			updated, err := tx.ExecContext(ctx, `UPDATE collaboration_claims SET version=version+1,released_at=? WHERE id=? AND version=? AND released_at=0`, now.UnixNano(), claim.ID, expected)
			if err != nil {
				return err
			}
			affected, err := updated.RowsAffected()
			if err != nil || affected != 1 {
				if err != nil {
					return err
				}
				return ErrStale
			}
			claim.Version++
			claim.ReleasedAt = now
			result.Released = append(result.Released, claim)
		}
		active, err = activeClaims(ctx, tx)
		if err != nil {
			return err
		}
		for _, path := range paths {
			for _, existing := range active {
				if existing.AgentID != agent.ID && pathsOverlap(path, existing.Path) {
					return fmt.Errorf("%w: path %q overlaps claim %s owned by agent %s", ErrConflict, path, existing.ID, existing.AgentID)
				}
			}
			claim := Claim{ID: newID("claim"), Path: path, AgentID: agent.ID, RequestID: run.RequestID, Version: 1, AcquiredAt: now}
			if _, err := tx.ExecContext(ctx, `INSERT INTO collaboration_claims(id,path,agent_id,run_id,request_id,version,acquired_at) VALUES(?,?,?,?,?,?,?)`, claim.ID, claim.Path, claim.AgentID, "", claim.RequestID, claim.Version, now.UnixNano()); err != nil {
				return err
			}
			result.Claims = append(result.Claims, claim)
		}
		// The body is read by the child and shown in its transcript; exact claim
		// identities stay in the typed result and the claim_repair reference.
		body := claimRepairBody(run.ID, result.Claims, len(result.Released))
		auditPreimage, _ := json.Marshal(map[string]any{"agent_id": agent.ID, "terminal_run_id": run.ID, "run_version": run.Version, "expected_versions": request.ExpectedVersions, "paths": paths})
		auditHash := sha256.Sum256(auditPreimage)
		published, err := store.publishTx(ctx, tx, PublishRequest{
			SenderID: request.ParentID, IdempotencyKey: fmt.Sprintf("claim-repair:%s:%d:%x", run.ID, run.Version, auditHash[:12]),
			RecipientIDs: []string{agent.ID}, Origin: OriginRuntime, Kind: EventPeerNote,
			Body: body, RunID: run.ID, RequestID: run.RequestID,
			References: []Reference{{Kind: "claim_repair", ID: run.ID, Revision: int(run.Version)}},
		})
		if err != nil {
			return err
		}
		result.Audit = published.Event
		return nil
	})
	if err == nil {
		store.signal()
	}
	return result, err
}

// NarrowActiveClaims atomically replaces one active actor's complete run-bound
// claim set with a strict subset of its exact observed scope. The immutable
// released and replacement claim rows are the transaction's audit trail. It
// never changes agent availability, permissions, runtime identity, or run state.
func (store *Store) NarrowActiveClaims(ctx context.Context, request NarrowActiveClaimsRequest) (NarrowActiveClaimsResult, error) {
	var result NarrowActiveClaimsResult
	if request.AgentID == "" || request.RuntimeEpoch == 0 || request.RunID == "" || request.ExpectedRunVersion == 0 || len(request.Paths) > 256 {
		return result, fmt.Errorf("%w: active claim identity", ErrInvalid)
	}
	paths := make([]string, 0, len(request.Paths))
	for _, path := range request.Paths {
		normalized, err := normalizedPath(path)
		if err != nil {
			return result, err
		}
		paths = append(paths, normalized)
	}
	slices.Sort(paths)
	paths = slices.Compact(paths)
	err := store.transaction(ctx, func(tx *sql.Tx) error {
		actor, err := loadAgent(ctx, tx, request.AgentID)
		if err != nil {
			return err
		}
		if actor.RuntimeEpoch != request.RuntimeEpoch || actor.CurrentRunID != request.RunID || actor.Presence != RuntimeAttached {
			return ErrStale
		}
		run, err := loadRun(ctx, tx, request.RunID)
		if err != nil {
			return err
		}
		if run.AgentID != actor.ID || run.RuntimeEpoch != actor.RuntimeEpoch || run.Version != request.ExpectedRunVersion || run.State.Terminal() {
			return ErrStale
		}
		active, err := activeClaims(ctx, tx)
		if err != nil {
			return err
		}
		current := make([]Claim, 0)
		for _, claim := range active {
			if claim.AgentID == actor.ID && claim.RunID == run.ID {
				current = append(current, claim)
			}
		}
		if len(current) == 0 || len(current) != len(request.ExpectedVersions) {
			return ErrStale
		}
		ceiling := make([]string, 0, len(current))
		currentPaths := make([]string, 0, len(current))
		for _, claim := range current {
			if request.ExpectedVersions[claim.ID] != claim.Version {
				return ErrStale
			}
			ceiling = append(ceiling, claim.Path)
			currentPaths = append(currentPaths, claim.Path)
		}
		for _, path := range paths {
			if !containedByAny(path, ceiling) {
				return fmt.Errorf("%w: path %q exceeds active claim ceiling", ErrConflict, path)
			}
		}
		slices.Sort(currentPaths)
		currentPaths = slices.Compact(currentPaths)
		if slices.Equal(paths, currentPaths) {
			return fmt.Errorf("%w: replacement must strictly narrow or relinquish active claims", ErrConflict)
		}
		now := time.Now().UTC()
		for _, claim := range current {
			expected := request.ExpectedVersions[claim.ID]
			updated, err := tx.ExecContext(ctx, `UPDATE collaboration_claims SET version=version+1,released_at=? WHERE id=? AND agent_id=? AND run_id=? AND version=? AND released_at=0`, now.UnixNano(), claim.ID, actor.ID, run.ID, expected)
			if err != nil {
				return err
			}
			affected, err := updated.RowsAffected()
			if err != nil || affected != 1 {
				if err != nil {
					return err
				}
				return ErrStale
			}
			claim.Version++
			claim.ReleasedAt = now
			result.Released = append(result.Released, claim)
		}
		active, err = activeClaims(ctx, tx)
		if err != nil {
			return err
		}
		for _, path := range paths {
			for _, existing := range active {
				if existing.AgentID != actor.ID && pathsOverlap(path, existing.Path) {
					return fmt.Errorf("%w: path %q overlaps claim %s owned by agent %s", ErrConflict, path, existing.ID, existing.AgentID)
				}
			}
			claim := Claim{ID: newID("claim"), Path: path, AgentID: actor.ID, RunID: run.ID, RequestID: run.RequestID, Version: 1, AcquiredAt: now}
			if _, err := tx.ExecContext(ctx, `INSERT INTO collaboration_claims(id,path,agent_id,run_id,request_id,version,acquired_at) VALUES(?,?,?,?,?,?,?)`, claim.ID, claim.Path, claim.AgentID, claim.RunID, claim.RequestID, claim.Version, now.UnixNano()); err != nil {
				return err
			}
			result.Claims = append(result.Claims, claim)
		}
		return nil
	})
	return result, err
}

func claimRepairCeiling(ctx context.Context, tx *sql.Tx, agentID, requestID string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT path FROM collaboration_claims WHERE agent_id=? AND request_id=? ORDER BY acquired_at,id`, agentID, requestID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var paths []string
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			return nil, err
		}
		paths = append(paths, path)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("%w: original claim ceiling is unavailable", ErrConflict)
	}
	// Keep only the broadest original paths. Later repairs may add narrower rows,
	// but can never turn those rows into a new authority ceiling.
	var ceiling []string
	for _, candidate := range paths {
		covered := false
		for _, other := range paths {
			if candidate != other && pathsOverlap(candidate, other) && pathContains(other, candidate) {
				covered = true
				break
			}
		}
		if !covered {
			ceiling = append(ceiling, candidate)
		}
	}
	slices.Sort(ceiling)
	return slices.Compact(ceiling), nil
}

func containedByAny(path string, ceilings []string) bool {
	for _, ceiling := range ceilings {
		if pathContains(ceiling, path) {
			return true
		}
	}
	return false
}

func pathContains(parent, path string) bool {
	if parent == path {
		return true
	}
	relative, err := filepath.Rel(parent, path)
	return err == nil && relative != "." && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func claimRepairBody(runID string, claims []Claim, released int) string {
	paths := make([]string, 0, len(claims))
	for _, claim := range claims {
		paths = append(paths, claim.Path)
	}
	return fmt.Sprintf("File scope repaired after run %s: released %d claim(s); now owns %s. Continue explicitly to resume work.",
		runID, released, strings.Join(paths, ", "))
}
