package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"crude/agent/worktree"
	"crude/types/control"
	"crude/workspace"
)

// Execution control outputs stay inside control.MaxValueBytes however large an
// execution, its inventory or its integration history grows. A review returns
// the compact plan token, counts and one page of change metadata; inventories
// page by identity. File bytes never pass the control boundary: apply and
// cleanup reload the exact reviewed plan from its owner and recheck it.

// executionPageReserve leaves room for the page envelope (plan token, summary,
// cursor) beside its records.
const executionPageReserve = 4 << 10

var errExecutionCursor = errors.New("invalid cursor; start again without one")

var errExecutionReviewChanged = errors.New("the execution changed after this review; review it again without a cursor")

// reviewExecution reviews an execution on the first page and serves later pages
// from that same review. A cursor whose review is no longer the latest is
// checked against a fresh review and refused when the reviewed state changed.
func (a *actor) reviewExecution(ctx context.Context, service *workspace.ExecutionService, id string, discard bool, page executionPage) (executionReview, error) {
	key := executionReviewKey(id, discard)
	offset := 0
	if page.Cursor != "" {
		identity, at, err := parseReviewCursor(page.Cursor)
		if err != nil {
			return executionReview{}, err
		}
		offset = at
		if record, ok := a.executionReviews.load(key); ok && record.identity == identity {
			return record.page(offset, page)
		}
		record, err := reviewExecutionRecord(ctx, service, id, discard)
		if err != nil {
			return executionReview{}, err
		}
		if record.identity != identity {
			return executionReview{}, errExecutionReviewChanged
		}
		a.executionReviews.store(key, record)
		return record.page(offset, page)
	}
	record, err := reviewExecutionRecord(ctx, service, id, discard)
	if err != nil {
		return executionReview{}, err
	}
	a.executionReviews.store(key, record)
	return record.page(offset, page)
}

func reviewExecutionRecord(ctx context.Context, service *workspace.ExecutionService, id string, discard bool) (executionReviewRecord, error) {
	var plan worktree.PlanDelta
	var err error
	if discard {
		plan, err = service.ReviewDiscard(ctx, id)
	} else {
		plan, err = service.Review(ctx, id)
	}
	if err != nil {
		return executionReviewRecord{}, err
	}
	return newExecutionReviewRecord(plan, discard), nil
}

// executionPage selects one page of records. A request without a limit takes as
// many records as fit; at least one record is always returned so a page always
// advances.
type executionPage struct {
	Cursor string `json:"cursor,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}

func (p executionPage) limit() (int, error) {
	if p.Limit == 0 {
		return control.MaxPageLimit, nil
	}
	if p.Limit < 1 || p.Limit > control.MaxPageLimit {
		return 0, fmt.Errorf("limit must be between 1 and %d", control.MaxPageLimit)
	}
	return p.Limit, nil
}

// fitRecords returns how many records from the start of records fit in budget
// encoded bytes, between 1 and limit.
func fitRecords[T any](records []T, limit, budget int) (int, error) {
	used := 0
	for i := 0; i < len(records) && i < limit; i++ {
		encoded, err := json.Marshal(records[i])
		if err != nil {
			return 0, err
		}
		used += len(encoded) + 1
		if i > 0 && used > budget {
			return i, nil
		}
	}
	return min(len(records), limit), nil
}

func pageBudget(envelope any) (int, error) {
	encoded, err := json.Marshal(envelope)
	if err != nil {
		return 0, err
	}
	return control.MaxValueBytes - executionPageReserve - len(encoded), nil
}

// pageAfter returns the records after the cursor identity in an inventory
// sorted by identity. Keyset cursors stay correct when records are added or
// removed between pages.
func pageAfter[T any](records []T, identity func(T) string, page executionPage, envelope any) ([]T, string, error) {
	limit, err := page.limit()
	if err != nil {
		return nil, "", err
	}
	start := 0
	if page.Cursor != "" {
		for start < len(records) && identity(records[start]) <= page.Cursor {
			start++
		}
	}
	budget, err := pageBudget(envelope)
	if err != nil {
		return nil, "", err
	}
	n, err := fitRecords(records[start:], limit, budget)
	if err != nil {
		return nil, "", err
	}
	selected := records[start : start+n]
	next := ""
	if start+n < len(records) {
		next = identity(selected[len(selected)-1])
	}
	return selected, next, nil
}

type executionInventory struct {
	Executions []worktree.Context `json:"executions"`
	NextCursor string             `json:"next_cursor,omitempty"`
}

func executionInventoryPage(entries []worktree.Context, page executionPage) (executionInventory, error) {
	active := make([]worktree.Context, 0, len(entries))
	for _, entry := range entries {
		if !entry.Removed {
			active = append(active, entry)
		}
	}
	// The owner already sorts by ID; keyset paging depends on it.
	longest := ""
	for _, entry := range active {
		if len(entry.ID) > len(longest) {
			longest = entry.ID
		}
	}
	selected, next, err := pageAfter(active, func(c worktree.Context) string { return c.ID }, page, executionInventory{Executions: []worktree.Context{}, NextCursor: longest})
	return executionInventory{Executions: selected, NextCursor: next}, err
}

// executionIntegration is one integration journal entry without its file
// bytes. Exact recovery and undo read the full journal from its owner.
type executionIntegration struct {
	State   string             `json:"state"`
	Receipt worktree.Receipt   `json:"receipt"`
	Plan    worktree.PlanDelta `json:"plan"`
	Changes int                `json:"changes"`
}

type executionIntegrations struct {
	Integrations []executionIntegration `json:"integrations"`
	NextCursor   string                 `json:"next_cursor,omitempty"`
}

func executionIntegrationsPage(statuses []workspace.IntegrationStatus, page executionPage) (executionIntegrations, error) {
	records := make([]executionIntegration, 0, len(statuses))
	longest := ""
	for _, status := range statuses {
		records = append(records, executionIntegration{State: status.State, Receipt: status.Receipt, Plan: executionReviewToken(status.Plan), Changes: len(status.Plan.Changes)})
		if len(status.Receipt.OperationID) > len(longest) {
			longest = status.Receipt.OperationID
		}
	}
	// The journal owner returns entries sorted by operation ID.
	selected, next, err := pageAfter(records, func(r executionIntegration) string { return r.Receipt.OperationID }, page, executionIntegrations{Integrations: []executionIntegration{}, NextCursor: longest})
	return executionIntegrations{Integrations: selected, NextCursor: next}, err
}

// executionReviewFile describes one side of a change without its bytes.
type executionReviewFile struct {
	Type string `json:"type"`
	Mode uint32 `json:"mode"`
	Hash string `json:"hash"`
	Size int    `json:"size"`
}

// executionReviewChange is one reviewed path. Change is added, modified,
// removed, or ignored (ignored output that a discard archives with the result).
type executionReviewChange struct {
	Path   string               `json:"path"`
	Change string               `json:"change"`
	Before *executionReviewFile `json:"before,omitempty"`
	After  *executionReviewFile `json:"after,omitempty"`
}

type executionReviewSummary struct {
	Changes  int `json:"changes"`
	Added    int `json:"added"`
	Modified int `json:"modified"`
	Removed  int `json:"removed"`
	Ignored  int `json:"ignored,omitempty"`
	// Bytes is the size of every added and modified file after the change.
	Bytes int64 `json:"bytes"`
}

// executionReview is the bounded review result. Plan is the exact token that
// execution.apply takes (integration review) or whose discard_digest
// execution.cleanup takes (discard review). NextCursor continues the change
// list of this same review.
type executionReview struct {
	Plan       worktree.PlanDelta      `json:"plan"`
	Summary    executionReviewSummary  `json:"summary"`
	Changes    []executionReviewChange `json:"changes"`
	NextCursor string                  `json:"next_cursor,omitempty"`
}

// executionReviewRecord is one immutable review, kept so its later pages come
// from the reviewed state, not from a checkout that changed since.
type executionReviewRecord struct {
	identity string // the digest a cursor binds
	plan     worktree.PlanDelta
	summary  executionReviewSummary
	changes  []executionReviewChange
}

func reviewFile(f *worktree.File) *executionReviewFile {
	if f == nil {
		return nil
	}
	return &executionReviewFile{Type: f.Type, Mode: f.Mode, Hash: f.Hash, Size: len(f.Data)}
}

func newExecutionReviewRecord(plan worktree.PlanDelta, discard bool) executionReviewRecord {
	record := executionReviewRecord{identity: plan.Digest, plan: executionReviewToken(plan), changes: make([]executionReviewChange, 0, len(plan.Changes)+len(plan.IgnoredPaths))}
	if discard {
		record.identity = plan.DiscardDigest
		record.plan.SourceDigest = ""
		record.plan.DiscardDigest, record.plan.HeadCommit = plan.DiscardDigest, plan.HeadCommit
	}
	for _, change := range plan.Changes {
		entry := executionReviewChange{Path: change.Path, Before: reviewFile(change.Before), After: reviewFile(change.After)}
		switch {
		case change.Before == nil:
			entry.Change = "added"
			record.summary.Added++
		case change.After == nil:
			entry.Change = "removed"
			record.summary.Removed++
		default:
			entry.Change = "modified"
			record.summary.Modified++
		}
		if change.After != nil {
			record.summary.Bytes += int64(len(change.After.Data))
		}
		record.changes = append(record.changes, entry)
	}
	for _, path := range plan.IgnoredPaths {
		record.changes = append(record.changes, executionReviewChange{Path: path, Change: "ignored"})
		record.summary.Ignored++
	}
	record.summary.Changes = len(plan.Changes)
	return record
}

func (r executionReviewRecord) page(offset int, page executionPage) (executionReview, error) {
	limit, err := page.limit()
	if err != nil {
		return executionReview{}, err
	}
	if offset < 0 || offset > len(r.changes) {
		return executionReview{}, errExecutionCursor
	}
	out := executionReview{Plan: r.plan, Summary: r.summary, Changes: []executionReviewChange{}}
	budget, err := pageBudget(executionReview{Plan: r.plan, Summary: r.summary, Changes: []executionReviewChange{}, NextCursor: reviewCursor(r.identity, len(r.changes))})
	if err != nil {
		return executionReview{}, err
	}
	n, err := fitRecords(r.changes[offset:], limit, budget)
	if err != nil {
		return executionReview{}, err
	}
	out.Changes = r.changes[offset : offset+n]
	if offset+n < len(r.changes) {
		out.NextCursor = reviewCursor(r.identity, offset+n)
	}
	return out, nil
}

func reviewCursor(identity string, offset int) string {
	return identity + "." + strconv.Itoa(offset)
}

func parseReviewCursor(cursor string) (string, int, error) {
	identity, offset, ok := strings.Cut(cursor, ".")
	if !ok || identity == "" {
		return "", 0, errExecutionCursor
	}
	n, err := strconv.Atoi(offset)
	if err != nil || n < 1 {
		return "", 0, errExecutionCursor
	}
	return identity, n, nil
}

// executionReviewCache keeps the latest review of each execution and mode.
// It holds metadata only; a new first page replaces it.
type executionReviewCache struct {
	mu      sync.Mutex
	records map[string]executionReviewRecord
}

func executionReviewKey(id string, discard bool) string {
	if discard {
		return id + "\x00discard"
	}
	return id
}

func (c *executionReviewCache) load(key string) (executionReviewRecord, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	record, ok := c.records[key]
	return record, ok
}

func (c *executionReviewCache) store(key string, record executionReviewRecord) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.records == nil {
		c.records = make(map[string]executionReviewRecord)
	}
	c.records[key] = record
}

// forget drops both reviews of an execution whose result was applied or removed.
func (c *executionReviewCache) forget(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.records, executionReviewKey(id, false))
	delete(c.records, executionReviewKey(id, true))
}
