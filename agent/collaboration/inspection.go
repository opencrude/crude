package collaboration

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// AgentInspection separates the active attempt from retained history. Reading
// it never attaches an agent, admits a delivery or changes lifecycle ownership.
type AgentInspection struct {
	Agent     Agent             `json:"agent"`
	Run       *Run              `json:"run"`
	LatestRun *Run              `json:"latest_run"`
	Reports   []ArtifactSummary `json:"reports"`
}

// ArtifactSummary deliberately excludes the potentially large body and metadata.
// Artifact retains the full immutable evidence for an explicit artifact_read.
type ArtifactSummary struct {
	ID        string     `json:"id"`
	Revision  int        `json:"revision"`
	AuthorID  string     `json:"author_id"`
	RunID     string     `json:"run_id"`
	Kind      ReportKind `json:"kind"`
	Summary   string     `json:"summary"`
	CreatedAt time.Time  `json:"created_at"`
}

func (store *Store) InspectAgent(ctx context.Context, id string) (AgentInspection, error) {
	if err := validateIdentity("agent_id", id, 256); err != nil {
		return AgentInspection{}, err
	}
	result := AgentInspection{Reports: []ArtifactSummary{}}
	err := store.readTransaction(ctx, func(tx *sql.Tx) error {
		var err error
		result.Agent, err = loadAgent(ctx, tx, id)
		if err != nil {
			return err
		}
		if result.Agent.CurrentRunID != "" {
			run, err := loadRun(ctx, tx, result.Agent.CurrentRunID)
			if err != nil {
				return err
			}
			result.Run, result.LatestRun = &run, &run
		} else {
			var runID string
			err := tx.QueryRowContext(ctx, `SELECT id FROM collaboration_runs
				WHERE agent_id=? ORDER BY started_at DESC,id DESC LIMIT 1`, id).Scan(&runID)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			if err == nil {
				run, err := loadRun(ctx, tx, runID)
				if err != nil {
					return err
				}
				result.LatestRun = &run
			}
		}
		if result.LatestRun == nil {
			return nil
		}
		rows, err := tx.QueryContext(ctx, `SELECT a.id,a.revision,a.author_id,a.run_id,a.kind,
			substr(a.summary,1,800),a.created_at FROM collaboration_artifacts a
			WHERE a.author_id=? AND a.run_id=?
			AND a.revision=(SELECT MAX(b.revision) FROM collaboration_artifacts b WHERE b.id=a.id)
			ORDER BY a.created_at DESC,a.id LIMIT 5`, id, result.LatestRun.ID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var report ArtifactSummary
			var created int64
			if err := rows.Scan(&report.ID, &report.Revision, &report.AuthorID, &report.RunID, &report.Kind, &report.Summary, &created); err != nil {
				return err
			}
			report.CreatedAt = timeFromUnixNano(created)
			result.Reports = append(result.Reports, report)
		}
		return rows.Err()
	})
	return result, err
}

// InspectionEvent looks up exact triggering evidence only, never the nearest
// event by timestamp or an unrelated notice received during the same run.
func (store *Store) InspectionEvent(ctx context.Context, agentID, eventID string) (*Event, error) {
	if eventID == "" {
		return nil, nil
	}
	var result *Event
	err := store.readTransaction(ctx, func(tx *sql.Tx) error {
		var event Event
		var created int64
		err := tx.QueryRowContext(ctx, `SELECT e.id,e.sequence,e.sender_id,COALESCE(json_extract(e.event_json,'$.sender_name'),''),e.origin,e.kind,
			substr(e.body,1,1000),e.thread_id,e.created_at FROM collaboration_events e
			JOIN collaboration_deliveries d ON d.event_id=e.id
			WHERE e.id=? AND d.recipient_id=?`, eventID, agentID).
			Scan(&event.ID, &event.Sequence, &event.SenderID, &event.SenderName, &event.Origin, &event.Kind, &event.Body, &event.ThreadID, &created)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		event.CreatedAt = timeFromUnixNano(created)
		result = &event
		return nil
	})
	return result, err
}
