package collaboration

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// AgentInspection separates the active attempt from retained history. Reading
// it never attaches an agent, admits a delivery or changes lifecycle ownership.
type AgentInspection struct {
	Agent       Agent             `json:"agent"`
	Run         *Run              `json:"run"`
	LatestRun   *Run              `json:"latest_run"`
	Reports     []ArtifactSummary `json:"reports"`
	SelectedRun *Run              `json:"selected_run,omitempty"`
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
	return store.InspectAgentAttempt(ctx, id, "")
}

// InspectAgentAttempt binds reports to the selected attempt without redefining
// the agent's current or latest run. No historical session is opened.
func (store *Store) InspectAgentAttempt(ctx context.Context, id, runID string) (AgentInspection, error) {
	if runID != "" {
		if err := validateIdentity("run_id", runID, 256); err != nil {
			return AgentInspection{}, err
		}
	}
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
		selected := result.LatestRun
		if runID != "" {
			run, err := loadRun(ctx, tx, runID)
			if err != nil {
				return err
			}
			if run.AgentID != id {
				return fmt.Errorf("%w: run does not belong to this agent", ErrNotFound)
			}
			result.SelectedRun, selected = &run, &run
		}
		if selected == nil {
			return nil
		}
		rows, err := tx.QueryContext(ctx, `SELECT a.id,a.revision,a.author_id,a.run_id,a.kind,
			substr(a.summary,1,800),a.created_at FROM collaboration_artifacts a
			WHERE a.author_id=? AND a.run_id=?
			AND a.revision=(SELECT MAX(b.revision) FROM collaboration_artifacts b WHERE b.id=a.id)
			ORDER BY a.created_at DESC,a.id LIMIT 5`, id, selected.ID)
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

// RunPage is a chronological page from the run journal. Before identifies the
// last row of the next-newer page, not a floating SQL offset.
type RunPage struct {
	Runs   []Run  `json:"runs"`
	Before string `json:"before_run,omitempty"`
}

func (store *Store) ListRuns(ctx context.Context, agentID, before string, limit int) (RunPage, error) {
	if limit < 1 || limit > 128 {
		return RunPage{}, errors.New("run history limit must be 1 to 128")
	}
	if agentID != "" {
		if err := validateIdentity("agent_id", agentID, 256); err != nil {
			return RunPage{}, err
		}
	}
	if before != "" {
		if err := validateIdentity("before_run", before, 256); err != nil {
			return RunPage{}, err
		}
	}
	result := RunPage{Runs: []Run{}}
	err := store.readTransaction(ctx, func(tx *sql.Tx) error {
		query := `SELECT ` + runColumns + ` FROM collaboration_runs WHERE workspace_id=?`
		args := []any{store.workspaceID}
		if agentID != "" {
			query += ` AND agent_id=?`
			args = append(args, agentID)
		}
		if before != "" {
			anchor, err := loadRun(ctx, tx, before)
			if err != nil {
				return err
			}
			if agentID != "" && anchor.AgentID != agentID {
				return fmt.Errorf("%w: run history cursor belongs to another agent", ErrNotFound)
			}
			query += ` AND (started_at,id)<(?,?)`
			args = append(args, anchor.StartedAt.UnixNano(), anchor.ID)
		}
		rows, err := tx.QueryContext(ctx, query+` ORDER BY started_at DESC,id DESC LIMIT ?`, append(args, limit+1)...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			run, err := scanRun(rows)
			if err != nil {
				return err
			}
			if len(result.Runs) == limit {
				result.Before = result.Runs[limit-1].ID
				break
			}
			result.Runs = append(result.Runs, run)
		}
		return rows.Err()
	})
	return result, err
}

// HistoryParticipants pages the durable workspace registry by stable identity.
// Presence and state changes cannot reorder an already visited participant.
func (store *Store) HistoryParticipants(ctx context.Context, after, category string, limit int) ([]Agent, error) {
	if len(after) > 256 || len(category) > 128 || limit < 1 || limit > 64 {
		return nil, ErrInvalid
	}
	var agents []Agent
	err := store.readTransaction(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT `+agentColumns+` FROM collaboration_agents
			WHERE workspace_id=? AND id>? AND (?='' OR category=?) ORDER BY id LIMIT ?`, store.workspaceID, after, category, category, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			a, err := scanAgent(rows)
			if err != nil {
				return err
			}
			agents = append(agents, a)
		}
		return rows.Err()
	})
	return agents, err
}

// InputRun returns a run link only when the journal has one unambiguous owner
// for this exact input. Retries can legitimately share an original input.
func (store *Store) InputRun(ctx context.Context, agentID, inputID string) (string, error) {
	var runID string
	err := store.readTransaction(ctx, func(tx *sql.Tx) error {
		var count int
		err := tx.QueryRowContext(ctx, `SELECT COALESCE(MIN(id),''),COUNT(*) FROM (
			SELECT id FROM collaboration_runs WHERE agent_id=? AND context_commit_id=? AND context_commit_id!='' LIMIT 2
		)`, agentID, inputID).Scan(&runID, &count)
		if count != 1 {
			runID = ""
		}
		return err
	})
	return runID, err
}
