package dag

import (
	"fmt"
	"sync"
	"time"

	"crude/logger"
)

type ToolFunc func(tool string, params map[string]any) (string, error)

type RollbackFunc func(node *Node) error

type ConfirmFunc func(node *Node) (proceed bool, always bool, err error)

type AuditFunc func(entry AuditEntry)

type Executor struct {
	dag        *DAG
	toolFn     ToolFunc
	rollbackFn RollbackFunc
	confirmFn  ConfirmFunc
	auditFn    AuditFunc
	emit       func(Event)
	blockCh    <-chan string
	logger     *logger.Logger
}

func New(
	dag *DAG,
	toolFn ToolFunc,
	emit func(Event),
	blockCh <-chan string,
	lg *logger.Logger,
) *Executor {
	return &Executor{
		dag:     dag,
		toolFn:  toolFn,
		emit:    emit,
		blockCh: blockCh,
		logger:  lg,
	}
}

func (e *Executor) SetRollbackFunc(fn RollbackFunc) { e.rollbackFn = fn }

func (e *Executor) SetConfirmFunc(fn ConfirmFunc) { e.confirmFn = fn }

func (e *Executor) SetAuditFunc(fn AuditFunc) { e.auditFn = fn }

func (e *Executor) Run() error {
	mu := &sync.Mutex{}
	ready := make(chan *Node, len(e.dag.Nodes))
	var wg sync.WaitGroup

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

	for _, n := range e.dag.Nodes {
		if inDeg[n.ID] == 0 {
			wg.Add(1)
			ready <- n
		}
	}

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

	if e.rollbackFn != nil {
		e.rollbackFailed()
	}

	e.emit(Event{Type: "complete"})

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
	e.logger.Info("dag.node_start", "node", n.ID, "tool", n.Tool, "op", string(n.Operation))

	if n.Tool == "" {
		e.skipNode(n, "no tool specified")
		return
	}

	if n.Tool == "blocking" {
		e.runBlockingNode(n)
		return
	}

	if n.Tool == "reply" {
		content, _ := n.Parameters["content"].(string)
		n.Result = content
		n.Status = Successful
		n.Finished = time.Now().UTC()
		e.emit(Event{Type: "node_done", Node: n, Data: content})
		e.auditNode(n)
		e.logger.Info("dag.node_complete", "node", n.ID, "tool", n.Tool, "content", content, "latency_ms", n.Duration().Milliseconds())
		return
	}

	if e.confirmFn != nil && n.Operation.IsDestructive() {
		e.logger.Info("safety.confirm_req", "node", n.ID, "tool", n.Tool, "destructive", true)
		proceed, always, err := e.confirmFn(n)
		if err != nil {
			e.failNode(n, fmt.Errorf("confirmation error: %w", err))
			return
		}
		if !proceed {
			e.logger.Info("safety.confirm_result", "node", n.ID, "tool", n.Tool, "granted", false)
			e.skipNode(n, "user rejected confirmation")
			e.emit(Event{Type: "blocking_complete", Node: n, Data: "denied"})
			return
		}
		e.logger.Info("safety.confirm_result", "node", n.ID, "tool", n.Tool, "granted", true)
		if always {
			e.emit(Event{Type: "confirmation_always", Node: n, Data: nil})
		}
	}

	nodeStart := time.Now()
	result, err := e.toolFn(n.Tool, n.Parameters)
	nodeDur := time.Since(nodeStart).Milliseconds()
	if err != nil {
		e.failNode(n, err)
		return
	}

	n.Result = result
	n.Status = Successful
	n.Finished = time.Now().UTC()
	e.emit(Event{Type: "node_done", Node: n, Data: result})
	e.auditNode(n)
	e.logger.Info("dag.node_complete", "node", n.ID, "tool", n.Tool, "content", result, "latency_ms", nodeDur)
}

func (e *Executor) runBlockingNode(n *Node) {
	q := n.BlockQuestion
	if q == "" {
		q = n.Description
	}
	e.emit(Event{Type: "blocking", Node: n, Data: q})
	e.logger.Info("dag.blocking", "node", n.ID, "question", q)

	resolution, ok := <-e.blockCh
	if !ok {
		e.failNode(n, fmt.Errorf("block channel closed"))
		return
	}
	e.logger.Info("dag.resume", "node", n.ID, "response", resolution)
	n.Result = resolution
	n.Status = Successful
	n.Finished = time.Now().UTC()
	e.emit(Event{Type: "node_done", Node: n, Data: resolution})
	e.auditNode(n)
	e.logger.Info("dag.node_complete", "node", n.ID, "tool", n.Tool, "content", "", "latency_ms", n.Duration().Milliseconds())
}

func (e *Executor) failNode(n *Node, err error) {
	n.Status = Err
	n.RunErr = err
	n.Finished = time.Now().UTC()
	e.emit(Event{Type: "node_error", Node: n, Data: err.Error()})
	e.auditNode(n)
	e.logger.Error("dag.node_error", "node", n.ID, "error", err.Error(), "latency_ms", n.Duration().Milliseconds())
}

func (e *Executor) skipNode(n *Node, reason string) {
	n.Status = Skipped
	n.RunErr = fmt.Errorf("skipped: %s", reason)
	n.Finished = time.Now().UTC()
	e.emit(Event{Type: "node_skipped", Node: n, Data: reason})
	e.auditNode(n)
	e.logger.Info("dag.node_skipped", "node", n.ID, "tool", n.Tool, "reason", reason)
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
		e.logger.Warn("dag.rollback_error", "reason", "cannot determine order", "error", err.Error())
		return
	}

	for i := len(order) - 1; i >= 0; i-- {
		n := order[i]
		if n.Status != Successful || n.RollbackData == nil {
			continue
		}
		e.logger.Warn("safety.rollback", "node", n.ID, "tool", n.Tool, "reason", "executing rollback")
		if err := e.rollbackFn(n); err != nil {
			e.logger.Warn("dag.rollback_error", "reason", fmt.Sprintf("rollback %s failed", n.ID), "error", err.Error())
		} else {
			e.logger.Info("dag.rollback_complete", "node", n.ID)
			e.logger.Info("safety.rollback_node", "node", n.ID, "status", "rollback_complete")
		}
	}
}
