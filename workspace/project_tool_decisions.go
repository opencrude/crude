package workspace

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"crude/fileio"
	"crude/insight/catalog"
	"crude/types"
)

// ProjectToolDecisionResult is an audit result, not installation authority.
// Even a successful result requires a separate, current effect-admission fence.
type ProjectToolDecisionResult struct {
	ReceiptID string
}

type projectToolDecision struct {
	ReceiptID string                  `json:"receipt_id"`
	At        time.Time               `json:"at"`
	Review    types.ProjectToolReview `json:"review"`
	Selected  []string                `json:"selected"`
	Rejected  []string                `json:"rejected"`
	// Records are evidence of a choice, never a reusable effect grant. This
	// remains false even when the commit succeeded but revalidation failed.
	EffectAuthority bool `json:"effect_authority"`
}

// PrepareProjectToolDecision is restricted to the native decision owner.
// The caller must retain the exact preview, validate human action and consume
// its owner-bound, one-shot receipt before invoking this method. Neither a
// token nor this API establishes consent. Never expose it to agents or generic
// settings. Call on the workspace owner; run the returned function on a
// cancellable worker. It does not install, retry, or authorize installation.
// decisionHistory is the exact stored history retained by the native preview,
// including absence; save compares it atomically before appending a new choice.
func (w *Workspace) PrepareProjectToolDecision(review types.ProjectToolReview, selectedIDs []string, receiptID, decisionHistory string) (func(context.Context) (ProjectToolDecisionResult, error), error) {
	if w == nil || strings.TrimSpace(receiptID) == "" {
		return nil, errors.New("project decision owner is unavailable")
	}
	review = types.CloneProjectToolReview(review)
	selected := append([]string{}, selectedIDs...)
	sort.Strings(selected)
	rejected, err := decisionSelection(review, selected)
	if err != nil {
		return nil, err
	}
	// Preparation can run on the workspace actor. Do not wait for a project
	// switch or shutdown, which hold this lifetime lock while closing services.
	if !w.capabilityMu.TryRLock() {
		return nil, errors.New("the workspace is changing; review the tools again")
	}
	db, root, manager := w.WorkspaceDB, w.RootPath, w.LSPMgr
	if db == nil || manager == nil {
		w.capabilityMu.RUnlock()
		return nil, errors.New("project decision owner is unavailable")
	}
	defaults := w.AppDefaults()
	w.capabilityMu.RUnlock()
	return func(ctx context.Context) (ProjectToolDecisionResult, error) {
		return saveProjectToolDecision(ctx, w, db, root, manager, defaults, review, selected, rejected, receiptID, decisionHistory)
	}, nil
}

func decisionSelection(review types.ProjectToolReview, selected []string) ([]string, error) {
	preimage := types.CloneProjectToolReview(review)
	preimage.Token = ""
	if review.Token == "" || reviewHash(preimage) != review.Token {
		return nil, errors.New("the tool review changed; review it again")
	}
	items := make(map[string]types.ProjectToolCandidate, len(review.Items))
	for _, item := range review.Items {
		id := item.ID
		item.ID = ""
		if id == "" || reviewHash(item) != id {
			return nil, errors.New("the tool review contains an invalid item")
		}
		if _, ok := items[id]; ok {
			return nil, errors.New("the tool review contains duplicate items")
		}
		items[id] = item
	}
	chosen := make(map[string]bool, len(selected))
	for _, id := range selected {
		item, ok := items[id]
		if !ok || !item.Installable || chosen[id] {
			return nil, errors.New("the selected tools do not match the review")
		}
		chosen[id] = true
	}
	rejected := make([]string, 0, len(items)-len(chosen))
	for id := range items {
		if !chosen[id] {
			rejected = append(rejected, id)
		}
	}
	sort.Strings(rejected)
	return rejected, nil
}

func lockDecisionMutex(ctx context.Context, mu *sync.Mutex) error {
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if mu.TryLock() {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// currentDecisionReview observes each owner without holding its lock across
// filesystem, catalog or database I/O. The caller repeats this after commit.
func currentDecisionReview(ctx context.Context, w *Workspace, db *WorkspaceDB, root string, m *LSPManager, review types.ProjectToolReview) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// Never wait on the lifetime lock while holding the settings writer:
	// registration takes those locks in the opposite order.
	if !w.capabilityMu.TryRLock() {
		return errors.New("the workspace is changing; review the tools again")
	}
	matches := w.WorkspaceDB == db && w.RootPath == root && w.LSPMgr == m
	w.capabilityMu.RUnlock()
	if !matches || review.Project != root {
		return errors.New("the project changed; review the tools again")
	}
	identity, err := fileio.DirectoryIdentity(root)
	if err != nil || identity != review.RootIdentity {
		return errors.New("the project folder changed; review the tools again")
	}
	m.mu.Lock()
	valid := !m.closed && m.workspaceRoot == root && m.analysisTrustedLocked() &&
		m.analysisRootIdentity == identity && m.assemblyRequested == review.AssemblyGeneration &&
		m.assembly.Generation == review.AssemblyGeneration
	plan := types.CloneAssemblyPlan(m.assembly)
	m.mu.Unlock()
	if !valid {
		return errors.New("the project setup changed; review the tools again")
	}
	c := catalog.Current()
	current, err := buildProjectToolReview(root, identity, plan, c, catalog.CurrentTargets())
	if err != nil || !currentToolReviewContains(current, review) || catalog.Current() != c {
		return errors.New("the tool catalog or project setup changed; review the tools again")
	}
	m.mu.Lock()
	valid = !m.closed && m.workspaceRoot == root && m.analysisTrustedLocked() &&
		m.analysisRootIdentity == identity && m.assemblyRequested == review.AssemblyGeneration &&
		m.assembly.Generation == review.AssemblyGeneration
	m.mu.Unlock()
	if !valid {
		return errors.New("the project setup changed; review the tools again")
	}
	return ctx.Err()
}

// A native preview can omit candidates already decided or installed. It must
// still describe exact current candidates in the same assembly, not invent a
// download. The setup owner retains the complete snapshot as its effect fence.
func currentToolReviewContains(current, offered types.ProjectToolReview) bool {
	if current.Project != offered.Project || current.RootIdentity != offered.RootIdentity ||
		current.AssemblyGeneration != offered.AssemblyGeneration {
		return false
	}
	if _, err := decisionSelection(offered, nil); err != nil {
		return false
	}
	items := make(map[string]types.ProjectToolCandidate, len(current.Items))
	for _, item := range current.Items {
		items[item.ID] = item
	}
	for _, item := range offered.Items {
		if !reflect.DeepEqual(items[item.ID], item) {
			return false
		}
	}
	return true
}

// readProjectToolDecisions reads evidence only. In particular, a selected ID
// suppresses another automatic question; it never authorizes a new install.
func readProjectToolDecisions(ctx context.Context, db *WorkspaceDB) ([]projectToolDecision, string, error) {
	var raw string
	if err := db.DB.QueryRowContext(ctx, `SELECT value FROM workspace_metadata WHERE key=?`, workspaceSettingsKey).Scan(&raw); err != nil {
		return nil, "", err
	}
	if err := validateUniqueJSON([]byte(raw)); err != nil {
		return nil, "", err
	}
	var row struct {
		Capabilities map[string]json.RawMessage `json:"capabilities"`
	}
	if err := json.Unmarshal([]byte(raw), &row); err != nil {
		return nil, "", err
	}
	history := row.Capabilities["project_tool_decisions"]
	records, err := decodeProjectToolDecisions(history)
	return records, string(history), err
}

func decodeProjectToolDecisions(data []byte) ([]projectToolDecision, error) {
	if len(data) == 0 {
		return nil, nil
	}
	var records []projectToolDecision
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) || decoder.Decode(&records) != nil || records == nil {
		return nil, errors.New("invalid stored project tool decisions")
	}
	seen := make(map[string]bool)
	for _, record := range records {
		rej, err := decisionSelection(record.Review, record.Selected)
		if err != nil || record.ReceiptID == "" || record.At.IsZero() || record.EffectAuthority ||
			seen[record.ReceiptID] || !reflect.DeepEqual(rej, record.Rejected) {
			return nil, errors.New("invalid stored project tool decisions")
		}
		seen[record.ReceiptID] = true
	}
	return records, nil
}

func saveProjectToolDecision(ctx context.Context, w *Workspace, db *WorkspaceDB, root string, m *LSPManager, defaults types.AppSettings, review types.ProjectToolReview, selected, rejected []string, receipt, decisionHistory string) (ProjectToolDecisionResult, error) {
	if err := currentDecisionReview(ctx, w, db, root, m, review); err != nil {
		return ProjectToolDecisionResult{}, err
	}
	if err := lockDecisionMutex(ctx, &db.settingsWriteMu); err != nil {
		return ProjectToolDecisionResult{}, err
	}
	defer db.settingsWriteMu.Unlock()
	tx, err := db.DB.BeginTx(ctx, nil)
	if err != nil {
		return ProjectToolDecisionResult{}, err
	}
	defer func() { _ = tx.Rollback() }()
	raw, err := settingValueTx(ctx, tx, workspaceSettingsKey)
	if err != nil {
		return ProjectToolDecisionResult{}, err
	}
	if raw == "" {
		raw = "{}"
	}
	if err := validateUniqueJSON([]byte(raw)); err != nil {
		return ProjectToolDecisionResult{}, fmt.Errorf("invalid stored workspace settings: %w", err)
	}
	var fields map[string]json.RawMessage
	var overrides types.WorkspaceOverrides
	if err := json.Unmarshal([]byte(raw), &fields); err != nil || fields == nil {
		return ProjectToolDecisionResult{}, errors.New("invalid stored workspace settings")
	}
	if err := json.Unmarshal([]byte(raw), &overrides); err != nil {
		return ProjectToolDecisionResult{}, err
	}
	if !boundWorkspaceSettings(defaults, overrides, review.RootIdentity).Analysis.Trusted() {
		return ProjectToolDecisionResult{}, errors.New("the project is not trusted")
	}
	capabilities := map[string]json.RawMessage{}
	if data := fields["capabilities"]; len(data) != 0 {
		if err := json.Unmarshal(data, &capabilities); err != nil || capabilities == nil {
			return ProjectToolDecisionResult{}, errors.New("invalid stored capability settings")
		}
	}
	records, err := decodeProjectToolDecisions(capabilities["project_tool_decisions"])
	if err != nil {
		return ProjectToolDecisionResult{}, err
	}
	// Compare under the settings writer and transaction. The earlier preview
	// check alone cannot exclude another shell's decision before this save.
	if string(capabilities["project_tool_decisions"]) != decisionHistory {
		return ProjectToolDecisionResult{}, errors.New("the project tool choices changed; review them again")
	}
	for _, record := range records {
		if record.ReceiptID == receipt {
			return ProjectToolDecisionResult{}, errors.New("the project decision was already recorded")
		}
	}
	records = append(records, projectToolDecision{ReceiptID: receipt, At: time.Now().UTC(), Review: review, Selected: selected, Rejected: rejected})
	capabilities["project_tool_decisions"], err = json.Marshal(records)
	if err != nil {
		return ProjectToolDecisionResult{}, err
	}
	fields["capabilities"], err = json.Marshal(capabilities)
	if err != nil {
		return ProjectToolDecisionResult{}, err
	}
	encoded, err := json.Marshal(fields)
	if err != nil {
		return ProjectToolDecisionResult{}, err
	}
	if err := currentDecisionReview(ctx, w, db, root, m, review); err != nil {
		return ProjectToolDecisionResult{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO workspace_metadata(key,value,updated_at) VALUES(?,?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value,updated_at=excluded.updated_at`, workspaceSettingsKey, string(encoded), time.Now().UnixNano()); err != nil {
		return ProjectToolDecisionResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return ProjectToolDecisionResult{}, err
	}
	db.publishSettingsRow(string(encoded))
	if err := currentDecisionReview(ctx, w, db, root, m, review); err != nil {
		return ProjectToolDecisionResult{ReceiptID: receipt}, fmt.Errorf("the decision was recorded but is not usable: %w", err)
	}
	return ProjectToolDecisionResult{ReceiptID: receipt}, nil
}

// Reject duplicate keys rather than interpreting ambiguous stored authority.
func validateUniqueJSON(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	var value func() error
	value = func() error {
		token, err := dec.Token()
		if err != nil {
			return err
		}
		d, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch d {
		case '{':
			seen := map[string]bool{}
			for dec.More() {
				key, err := dec.Token()
				if err != nil {
					return err
				}
				name, ok := key.(string)
				if !ok || seen[name] {
					return errors.New("duplicate or invalid object key")
				}
				seen[name] = true
				if err := value(); err != nil {
					return err
				}
			}
		case '[':
			for dec.More() {
				if err := value(); err != nil {
					return err
				}
			}
		default:
			return errors.New("invalid JSON value")
		}
		_, err = dec.Token()
		return err
	}
	if err := value(); err != nil {
		return err
	}
	if _, err := dec.Token(); err != io.EOF {
		return errors.New("extra JSON content")
	}
	return nil
}
