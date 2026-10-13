package workspace

import (
	"context"
	"crude/fileio"
	"crude/session"
	"crude/worktree"
	"encoding/json"
	"errors"
	"path/filepath"
)

// ExecutionService borrows the logical workspace's document/history owners.
// Managed checkouts are artifacts, not additional workspaces or settings owners.
type ExecutionService struct {
	*worktree.Service
	documents *DocumentService
	history   *LocalHistoryService
}

var _ session.ExecutionService = (*ExecutionService)(nil)

func (w *Workspace) ensureExecutions() error {
	if w.Executions != nil {
		return nil
	}
	service, err := worktree.Open(w.RootPath, filepath.Join(w.WorkspaceDir, "executions"), worktree.Options{})
	if err != nil {
		return err
	}
	w.Executions = &ExecutionService{Service: service, documents: w.LiveDocuments, history: w.HistoryService}
	return nil
}

func (s *ExecutionService) Create(ctx context.Context, request worktree.Request) (worktree.Context, error) {
	if s.documents == nil {
		return worktree.Context{}, errors.New("accepted document authority unavailable")
	}
	// Capture while holding the authoritative lock through materialization: a
	// newer accepted edit cannot disappear between snapshot and checkout capture.
	s.documents.mu.Lock()
	defer s.documents.mu.Unlock()
	if s.documents.closed || s.documents.stateErr != nil {
		return worktree.Context{}, errors.New("accepted document authority unavailable")
	}
	if err := s.documents.integrationTable(); err != nil {
		return worktree.Context{}, err
	}
	var pending int
	if err := s.documents.stateDB.QueryRow(`SELECT count(*) FROM workspace_integrations WHERE json_extract(payload,'$.state')='prepared'`).Scan(&pending); err != nil {
		return worktree.Context{}, err
	}
	if pending != 0 {
		return worktree.Context{}, errors.New("unfinished integration requires explicit recovery before execution creation")
	}
	if request.SourceContextID == "" {
		request.Documents = s.documents.executionDocumentsLocked()
	} else {
		request.Documents = nil
	}
	return s.Service.Create(ctx, request)
}

func (s *ExecutionService) Apply(ctx context.Context, plan worktree.PlanDelta) (worktree.Receipt, error) {
	if s.documents == nil || s.history == nil || s.history.store == nil {
		return worktree.Receipt{}, errors.New("execution integration requires document authority and Local History")
	}
	return s.documents.applyExecution(ctx, s.Service, s.history, plan)
}

// Review binds the immutable source delta to current accepted document/disk
// state. The returned digest covers actual merged results, not a guessed patch.
func (s *ExecutionService) Review(ctx context.Context, id string) (worktree.PlanDelta, error) {
	if s.documents == nil {
		return worktree.PlanDelta{}, errors.New("document authority unavailable")
	}
	s.documents.mu.Lock()
	defer s.documents.mu.Unlock()
	unlock, err := fileio.LockMutationContext(ctx)
	if err != nil {
		return worktree.PlanDelta{}, err
	}
	defer unlock()
	return s.Resolve(ctx, id, s.documents.executionDocumentsLocked())
}
func (s *ExecutionService) RecoverIntegration(ctx context.Context, operationID string) (worktree.Receipt, error) {
	if s.documents == nil {
		return worktree.Receipt{}, errors.New("document authority unavailable")
	}
	return s.documents.recoverExecution(ctx, s.Service, operationID)
}
func (s *ExecutionService) Undo(ctx context.Context, operationID string) (worktree.Receipt, error) {
	if s.documents == nil || s.history == nil || s.history.store == nil {
		return worktree.Receipt{}, errors.New("integration authority unavailable")
	}
	return s.documents.undoExecution(ctx, s.Service, s.history, operationID)
}
func (s *ExecutionService) Integration(operationID string) (IntegrationStatus, error) {
	if s.documents == nil {
		return IntegrationStatus{}, errors.New("document authority unavailable")
	}
	s.documents.mu.Lock()
	defer s.documents.mu.Unlock()
	return s.documents.loadIntegration(operationID)
}

// Cleanup requires the authoritative durable transaction receipt, not caller
// supplied evidence. The artifact owner additionally verifies source archives
// and refuses pins/Keep/newer unintegrated changes before ordinary Git removal.
func (s *ExecutionService) Cleanup(ctx context.Context, id string, owner worktree.Owner) error {
	c, err := s.Inspect(id)
	if err != nil {
		return err
	}
	if c.Integration != nil {
		j, err := s.Integration(c.Integration.OperationID)
		if err != nil {
			return err
		}
		if (j.State != "committed" && j.State != "undone") || j.Receipt != *c.Integration || j.Plan.Owner != owner {
			return errors.New("integration receipt unavailable for cleanup")
		}
		if s.history == nil || s.history.store == nil {
			return errors.New("integration backup authority unavailable")
		}
		if _, err = s.history.store.LoadManifest(j.Receipt.SafetySnapshotID); err != nil {
			return err
		}
	}
	return s.Service.Cleanup(ctx, id, owner)
}

// Integrations is passive recovery discovery; callers must not replay prepared
// entries. Payload backup bytes are available only through exact Integration.
func (s *ExecutionService) Integrations() ([]IntegrationStatus, error) {
	if s.documents == nil {
		return nil, errors.New("document authority unavailable")
	}
	s.documents.mu.Lock()
	defer s.documents.mu.Unlock()
	if err := s.documents.integrationTable(); err != nil {
		return nil, err
	}
	rows, err := s.documents.stateDB.Query(`SELECT payload FROM workspace_integrations ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	result := []IntegrationStatus{}
	for rows.Next() {
		var data []byte
		if err = rows.Scan(&data); err != nil {
			return nil, err
		}
		var j IntegrationStatus
		if err = json.Unmarshal(data, &j); err != nil {
			return nil, err
		}
		j.Documents = nil
		j.Disk = nil
		result = append(result, j)
	}
	return result, rows.Err()
}
