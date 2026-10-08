package session

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	"crude/plan"
	"crude/sessiondb"
	"crude/types/activity"
)

// ChildPlanActivity is a read-only, I/O-free projection for one exact child.
// A child without a plan never borrows its parent's plan. Stores survive child
// settlement and database closure so retained review surfaces remain readable.
func (s *Session) ChildPlanActivity(agentID string) *activity.Plan {
	if store, ok := s.childPlans.Load(agentID); ok {
		return store.(*plan.Store).ActivitySnapshot()
	}
	return nil
}

// bindChildPlanStore installs a fresh database authority at creation/recovery,
// before the child starts. Restoration never happens on a presentation read.
func (s *Session) bindChildPlanStore(agentID, sessionID string, db *sql.DB) error {
	store := plan.NewStore(db, sessionID)
	if err := store.RestoreActivity(context.Background()); err != nil {
		return err
	}
	s.childPlans.Store(agentID, store)
	return nil
}

func (s *Session) childPlanStore(agentID, sessionID string, db *sql.DB) (*plan.Store, error) {
	if agentID == "" || sessionID == "" || db == nil {
		return nil, fmt.Errorf("planning unavailable for sub-agent")
	}
	if store, ok := s.childPlans.Load(agentID); ok {
		return store.(*plan.Store), nil
	}
	store := plan.NewStore(db, sessionID)
	if err := store.RestoreActivity(context.Background()); err != nil {
		return nil, err
	}
	actual, _ := s.childPlans.LoadOrStore(agentID, store)
	return actual.(*plan.Store), nil
}

// restoreChildPlanActivity runs only during passive session resume. The closed
// database is not used again by presentation; explicit recovery replaces it.
func (s *Session) restoreChildPlanActivity(agentID, sessionID string) error {
	if agentID == "" || sessionID == "" || filepath.Base(sessionID) != sessionID {
		return fmt.Errorf("invalid child plan identity")
	}
	path := filepath.Join(s.SessionDir(), "subagents", sessionID+".db")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	db, err := sessiondb.OpenAndMigrate(path)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	return s.bindChildPlanStore(agentID, sessionID, db)
}
