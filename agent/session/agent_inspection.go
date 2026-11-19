package session

import (
	"context"
	"crude/internal/fileuri"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	"crude/agent/collaboration"
	"crude/agent/sessiondb"
)

type agentInspection struct {
	collaboration.AgentInspection
	Recovery   *agentRecoveryView            `json:"recovery,omitempty"`
	Claims     []collaboration.Claim         `json:"claims,omitempty"`
	Trigger    agentInspectionTrigger        `json:"trigger"`
	RecentWork sessiondb.AgentWorkInspection `json:"recent_work"`
}

type agentInspectionTrigger struct {
	Available  bool   `json:"available"`
	InputID    string `json:"input_id,omitempty"`
	Origin     string `json:"origin,omitempty"`
	EventID    string `json:"event_id,omitempty"`
	Kind       string `json:"kind,omitempty"`
	SenderID   string `json:"sender_id,omitempty"`
	SenderName string `json:"sender_name,omitempty"`
	ThreadID   string `json:"thread_id,omitempty"`
	Preview    string `json:"preview,omitempty"`
	Note       string `json:"note,omitempty"`
}

func (s *Session) inspectAgent(ctx context.Context, store *collaboration.Store, agentID string) (agentInspection, error) {
	base, err := store.InspectAgent(ctx, agentID)
	if err != nil {
		return agentInspection{}, err
	}
	result := agentInspection{AgentInspection: base, Recovery: s.agentRecovery(agentID)}
	result.Claims, err = store.Claims(ctx, agentID, true)
	if err != nil {
		return result, err
	}
	result.RecentWork = sessiondb.AgentWorkInspection{Scope: "latest_persisted_turn", Messages: []sessiondb.InspectionMessage{}}
	db, closeDB, err := s.agentInspectionDB(ctx, store, base.Agent)
	if err != nil {
		result.RecentWork.Note = "Persisted public work unavailable: " + err.Error()
	} else {
		defer closeDB()
		// Root output belongs to the session transcript, while root admission
		// rows use its runtime agent ID. Children own their assignment transcript.
		transcriptOwnerID := agentID
		if base.Agent.ParentID == "" {
			transcriptOwnerID = base.Agent.SessionID
		}
		work, readErr := sessiondb.InspectAgentWork(ctx, db, agentID, transcriptOwnerID)
		if readErr != nil {
			result.RecentWork.Note = "Persisted public work unavailable: " + readErr.Error()
		} else {
			result.RecentWork = work
			if base.Agent.ParentID != "" {
				// Child transcripts span continuation attempts under one assignment.
				// Their historical display generation is not a run attribution.
				result.RecentWork.Scope = "child_assignment_transcript"
				result.RecentWork.Generation = 0
				result.RecentWork.Note = "Persisted child assignment evidence may span attempts; per-attempt attribution is unavailable."
			}
		}
		if result.Recovery == nil {
			incident, readErr := sessiondb.InspectAgentRecovery(ctx, db, base.Agent.SessionID, agentID)
			if readErr == nil && incident != nil {
				result.Recovery = &agentRecoveryView{ID: incident.ID, Generation: incident.Generation, State: incident.State, Summary: incident.Summary, Action: incident.Action}
			}
		}
	}
	result.Trigger = agentInspectionTrigger{Note: "No recorded input link for this attempt."}
	if base.LatestRun == nil {
		result.Trigger.Note = "This agent has no recorded run."
		return result, nil
	}
	latest := base.LatestRun
	result.Trigger.Origin, result.Trigger.InputID = string(latest.Origin), latest.ContextCommitID
	input := result.RecentWork.Input
	if input != nil && base.Agent.ParentID == "" {
		result.RecentWork.RunID = input.RunID
		if input.MessageID == latest.ContextCommitID {
			result.RecentWork.RunID = latest.ID
		}
	}
	if result.Trigger.InputID == "" && input != nil && input.RunID == latest.ID {
		result.Trigger.InputID = input.MessageID
	}
	event, err := store.InspectionEvent(ctx, agentID, result.Trigger.InputID)
	if err != nil {
		return result, err
	}
	if event != nil {
		result.Trigger = inspectionEventTrigger(event)
	} else if input != nil && input.MessageID == latest.ContextCommitID {
		result.Trigger = agentInspectionTrigger{Available: true, InputID: input.MessageID, Origin: input.Origin, Preview: input.Preview}
	}
	return result, nil
}

func inspectionEventTrigger(event *collaboration.Event) agentInspectionTrigger {
	return agentInspectionTrigger{Available: true, InputID: event.ID, EventID: event.ID, Origin: string(event.Origin), Kind: string(event.Kind),
		SenderID: event.SenderID, SenderName: event.SenderName, ThreadID: event.ThreadID, Preview: event.Body}
}

// Cross-session inspection opens existing storage in SQLite read-only mode,
// never through ResumeSession, OpenAndMigrate or runtime attachment.
func (s *Session) agentInspectionDB(ctx context.Context, store *collaboration.Store, participant collaboration.Agent) (*sql.DB, func(), error) {
	if participant.SessionID == s.sessionID && s.db != nil {
		return s.db, func() {}, nil
	}
	if s.dbPath == "" {
		return nil, nil, fmt.Errorf("session storage is not available")
	}
	return OpenAgentTranscript(ctx, store, filepath.Dir(s.dbPath), participant)
}

// OpenAgentTranscript borrows the workspace registry's exact participant and
// opens only its existing transcript. It never resumes the agent or migrates data.
func OpenAgentTranscript(ctx context.Context, store *collaboration.Store, directory string, participant collaboration.Agent) (*sql.DB, func(), error) {
	if directory == "" || !singlePathComponent(participant.SessionID) {
		return nil, nil, fmt.Errorf("session storage is not available")
	}
	if participant.ParentID != "" {
		parent, err := store.Agent(ctx, participant.ParentID)
		if err != nil {
			return nil, nil, err
		}
		if parent.ParentID != "" || !singlePathComponent(parent.SessionID) {
			return nil, nil, fmt.Errorf("child session owner is unavailable")
		}
		for _, segment := range []string{parent.SessionID, "subagents"} {
			directory = filepath.Join(directory, segment)
			if info, err := os.Lstat(directory); err != nil || !info.IsDir() {
				return nil, nil, fmt.Errorf("child transcript directory is unavailable")
			}
		}
	}
	path := filepath.Join(directory, participant.SessionID+".db")
	if info, err := os.Lstat(path); err != nil || !info.Mode().IsRegular() {
		return nil, nil, fmt.Errorf("no existing session database")
	}
	db, err := sql.Open("sqlite", fileuri.SQLiteDSN(path, "mode=ro&_pragma=query_only(1)&_pragma=busy_timeout(1000)"))
	if err != nil {
		return nil, nil, err
	}
	db.SetMaxOpenConns(1)
	var sessionID string
	if err := db.QueryRowContext(ctx, `SELECT id FROM sessions LIMIT 1`).Scan(&sessionID); err != nil || sessionID != participant.SessionID {
		_ = db.Close()
		return nil, nil, fmt.Errorf("session identity is unavailable")
	}
	return db, func() { _ = db.Close() }, nil
}

func singlePathComponent(path string) bool {
	return path != "" && path != "." && filepath.IsLocal(path) && filepath.Base(path) == path
}

type exactAgentRunInspection struct {
	AgentID        string                          `json:"agent_id"`
	SessionID      string                          `json:"session_id"`
	Run            *collaboration.Run              `json:"run"`
	Reports        []collaboration.ArtifactSummary `json:"reports"`
	Trigger        agentInspectionTrigger          `json:"trigger"`
	PublicWork     sessiondb.RunWork               `json:"public_work"`
	ChangesCommand string                          `json:"changes_command"`
}

func (s *Session) inspectExactAgentRun(ctx context.Context, store *collaboration.Store, agentID, runID, cursor string) (exactAgentRunInspection, error) {
	attempt, err := store.InspectAgentAttempt(ctx, agentID, runID)
	if err != nil {
		return exactAgentRunInspection{}, err
	}
	result := exactAgentRunInspection{AgentID: agentID, SessionID: attempt.Agent.SessionID, Run: attempt.SelectedRun, Reports: attempt.Reports, ChangesCommand: "history.run_changes"}
	result.Trigger = agentInspectionTrigger{Origin: string(attempt.SelectedRun.Origin), InputID: attempt.SelectedRun.ContextCommitID, Note: "No recorded public input link for this attempt."}
	event, err := store.InspectionEvent(ctx, agentID, result.Trigger.InputID)
	if err != nil {
		return result, err
	}
	if event != nil {
		result.Trigger = inspectionEventTrigger(event)
	}
	db, closeDB, err := s.agentInspectionDB(ctx, store, attempt.Agent)
	if err != nil {
		result.PublicWork = sessiondb.RunWork{RunID: runID, Activity: []sessiondb.RunWorkEntry{}, Coverage: "The session transcript is unavailable."}
		return result, ctx.Err()
	}
	defer closeDB()
	transcriptOwner := agentID
	if attempt.Agent.ParentID == "" {
		transcriptOwner = attempt.Agent.SessionID
	}
	if !result.Trigger.Available && result.Trigger.InputID != "" && attempt.SelectedRun.Origin == collaboration.OriginHuman {
		input, readErr := sessiondb.InspectHistoricalPrompt(ctx, db, attempt.Agent.SessionID, agentID, transcriptOwner, sessiondb.HistoricalPromptQuery{PromptID: result.Trigger.InputID, MaxBytes: 1024})
		if readErr != nil {
			result.Trigger.Note = "The linked human input could not be read."
		} else if input != nil {
			result.Trigger = agentInspectionTrigger{Available: input.TextAvailable, InputID: input.ID, Origin: input.Origin, Preview: input.Text,
				Note: "Use history.prompts with this agent_id and prompt_id for exact text and recorded models."}
		}
	}
	result.PublicWork, err = sessiondb.InspectRunWork(ctx, db, runID, agentID, transcriptOwner, cursor)
	return result, err
}
