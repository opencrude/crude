package dag

import (
	"fmt"
	"log"
	"sync"
	"time"
)

// ToolFunc is called by the executor to run a tool node.
type ToolFunc func(tool string, params map[string]any) (string, error)

// RollbackFunc is called to undo a completed operation.
type RollbackFunc func(node *Node) error

// ConfirmFunc is called when a destructive operation needs user confirmation.
// Returns true to proceed, false to skip.
type ConfirmFunc func(node *Node) (bool, error)

// AuditFunc is called after each node execution to record the audit trail.
type AuditFunc func(entry AuditEntry)

// Executor runs a DAG, dispatching nodes concurrently where independent.
type Executor struct {
	dag        *DAG
	toolFn     ToolFunc
	rollbackFn RollbackFunc
	confirmFn  ConfirmFunc
	auditFn    AuditFunc
	emit       func(Event)
	blockCh    <-chan string
	logger     *log.Logger
}

// New creates an Executor.
func New(
	dag *DAG,
	toolFn ToolFunc,
	emit func(Event),
	blockCh <-chan string,
) *Executor {
	return &Executor{
		dag:     dag,
		toolFn:  toolFn,
		emit:    emit,
		blockCh: blockCh,
		logger:  log.Default(),
	}
}

// SetRollbackFunc sets the rollback function for undo support.
func (e *Executor) SetRollbackFunc(fn RollbackFunc) { e.rollbackFn = fn }

// SetConfirmFunc sets the confirmation gate function.
func (e *Executor) SetConfirmFunc(fn ConfirmFunc) { e.confirmFn = fn }

// SetAuditFunc sets the audit trail function.
func (e *Executor) SetAuditFunc(fn AuditFunc) { e.auditFn = fn }

// SetLogger sets the logger.
func (e *Executor) SetLogger(l *log.Logger) { e.logger = l }

// Run executes the DAG. Independent branches run concurrently.
// Returns when all nodes are terminal.
func (e *Executor) Run() error {
	mu := &sync.Mutex{}
	ready := make(chan *Node, len(e.dag.Nodes))
	var wg sync.WaitGroup

	// inDeg tracks how many unfinished dependencies each node has.
	inDeg := make(map[string]int, len(e.dag.Nodes))
	for _, n := range e.dag.Nodes {
		inDeg[n.ID] = len(n.DependsOn)
	}

	enqueueDependents := func(completedID string) {
		mu.Lock()
		defer mu.Unlock()
		for _, depID := range e.dag.Dependents[completedID] {
			inDeg[depID]--
			if inDeg[depID] == 0 {
				dep := e.dag.Nodes[depID]
				if dep.Status == Pending {
					wg.Add(1)
					ready <- dep
				}
			}
		}
	}

	// Seed initial ready nodes (no dependencies).
	for _, n := range e.dag.Nodes {
		if inDeg[n.ID] == 0 {
			wg.Add(1)
			ready <- n
		}
	}

	// Drain ready channel with goroutines.
	done := make(chan struct{})
	go func() {
		for n := range ready {
			node := n
			go func() {
				defer wg.Done()
				e.runNode(node)
				if node.Status == Successful {
					enqueueDependents(node.ID)
				} else if node.Status == Err {
					e.dag.SkipDependents(node.ID)
				}
			}()
		}
		close(done)
	}()

	wg.Wait()
	close(ready)
	<-done

	// Rollback on failure if rollback function is set.
	if e.rollbackFn != nil {
		e.rollbackFailed()
	}

	e.emit(Event{Type: "complete"})

	// Return first error if any.
	for _, n := range e.dag.Nodes {
		if n.Status == Err && n.RunErr != nil {
			return n.RunErr
		}
	}
	return nil
}

func (e *Executor) runNode(n *Node) {
	n.Status = Executing
	n.Started = time.Now().UTC()
	e.emit(Event{Type: "node_start", Node: n})

	if n.Tool == "" {
		e.skipNode(n, "no tool specified")
		return
	}

	if n.Tool == "" {
		e.skipNode(n, "no tool specified")
		return
	}

	// Handle blocking nodes (require user input).
	if n.Tool == "blocking" {
		e.runBlockingNode(n)
		return
	}

	// Handle reply nodes (no tool execution needed).
	if n.Tool == "reply" {
		content, _ := n.Parameters["content"].(string)
		n.Result = content
		n.Status = Successful
		n.Finished = time.Now().UTC()
		e.emit(Event{Type: "node_done", Node: n, Data: content})
		e.auditNode(n)
		return
	}

	// Handle confirmation gate for destructive operations.
	if e.confirmFn != nil && n.Operation.IsDestructive() {
		proceed, err := e.confirmFn(n)
		if err != nil {
			e.failNode(n, fmt.Errorf("confirmation error: %w", err))
			return
		}
		if !proceed {
			e.skipNode(n, "user rejected confirmation")
			return
		}
	}

	result, err := e.toolFn(n.Tool, n.Parameters)
	if err != nil {
		e.failNode(n, err)
		return
	}

	n.Result = result
	n.Status = Successful
	n.Finished = time.Now().UTC()
	e.emit(Event{Type: "node_done", Node: n, Data: result})
	e.auditNode(n)
}

func (e *Executor) runBlockingNode(n *Node) {
	q := n.BlockQuestion
	if q == "" {
		q = n.Description
	}
	e.emit(Event{Type: "blocking", Node: n, Data: q})

	resolution, ok := <-e.blockCh
	if !ok {
		e.failNode(n, fmt.Errorf("block channel closed"))
		return
	}
	n.Result = resolution
	n.Status = Successful
	n.Finished = time.Now().UTC()
	e.emit(Event{Type: "node_done", Node: n, Data: resolution})
	e.auditNode(n)
}

func (e *Executor) failNode(n *Node, err error) {
	n.Status = Err
	n.RunErr = err
	n.Finished = time.Now().UTC()
	e.emit(Event{Type: "node_error", Node: n, Data: err.Error()})
	e.auditNode(n)
}

func (e *Executor) skipNode(n *Node, reason string) {
	n.Status = Skipped
	n.RunErr = fmt.Errorf("skipped: %s", reason)
	n.Finished = time.Now().UTC()
	e.emit(Event{Type: "node_skipped", Node: n, Data: reason})
	e.auditNode(n)
}

func (e *Executor) auditNode(n *Node) {
	if e.auditFn == nil {
		return
	}
	entry := AuditEntry{
		NodeID:      n.ID,
		Operation:   n.Operation,
		Tool:        n.Tool,
		Description: n.Description,
		Parameters:  n.Parameters,
		StartTime:   n.Started,
		EndTime:     n.Finished,
		Duration:    n.Duration().String(),
		Status:      n.Status.String(),
		Result:      n.Result,
	}
	if n.RunErr != nil {
		entry.Error = n.RunErr.Error()
	}
	e.auditFn(entry)
}

// rollbackFailed rolls back successful nodes in reverse topological order
// when any node in the DAG has failed.
func (e *Executor) rollbackFailed() {
	hasFailure := false
	for _, n := range e.dag.Nodes {
		if n.Status == Err {
			hasFailure = true
			break
		}
	}
	if !hasFailure {
		return
	}

	order, err := e.dag.TopologicalOrder()
	if err != nil {
		e.logger.Printf("[dag] rollback: cannot determine order: %v", err)
		return
	}

	// Rollback in reverse order (leaves first).
	for i := len(order) - 1; i >= 0; i-- {
		n := order[i]
		if n.Status != Successful || n.RollbackData == nil {
			continue
		}
		if err := e.rollbackFn(n); err != nil {
			e.logger.Printf("[dag] rollback %s failed: %v", n.ID, err)
		} else {
			e.logger.Printf("[dag] rolled back %s", n.ID)
		}
	}
}
