package workspace

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"time"

	"crude/document"
	"crude/fileio"
	"crude/insight/catalog"
	"crude/toolstore"
	"crude/types"
)

const projectToolPreviewLimit = 16
const projectToolPreviewTTL = 5 * time.Minute

type projectToolSetupStore interface {
	projectToolInstaller
	projectToolExecutableStore
	List(context.Context) ([]toolstore.Tool, int64, error)
	MarkUsedContext(context.Context, string, string, func(context.Context) error) error
}

type projectToolSetupLifetime struct {
	ctx    context.Context
	cancel context.CancelFunc
}

type projectToolSetupPreview struct {
	id               string
	owner            document.Owner
	lifetime         *projectToolSetupLifetime
	expires          time.Time
	quiet            bool
	decisionHistory  string
	full             types.ProjectToolReview
	offered          types.ProjectToolReview
	db               *WorkspaceDB
	manager          *LSPManager
	policyGeneration uint64
	catalog          *catalog.Catalog
	selected         map[string]bool
	ctx              context.Context
	cancel           context.CancelFunc
}

// ProjectToolSetupOwner is the native shell's one-shot decision owner. It does
// not authenticate callers: only the authenticated native core route may call
// Review and Decide. Generic controls and agents must not expose these methods.
// Blocking methods run on the core's joined cancellable workers. Close and
// Detach only revoke in-memory lifetimes; the core joins its workers separately.
type ProjectToolSetupOwner struct {
	mu       sync.Mutex
	w        *Workspace
	store    projectToolSetupStore
	closed   bool
	owners   map[document.Owner]*projectToolSetupLifetime
	pending  map[string]*projectToolSetupPreview
	active   map[string]*projectToolSetupPreview
	requests map[projectToolSetupRequest]time.Time
	retained []*projectToolEvaluationRetained
}

type projectToolSetupRequest struct {
	owner document.Owner
	id    string
}

func NewProjectToolSetupOwner(w *Workspace, store *toolstore.Store) *ProjectToolSetupOwner {
	o := &ProjectToolSetupOwner{
		w: w, owners: make(map[document.Owner]*projectToolSetupLifetime),
		pending: make(map[string]*projectToolSetupPreview), active: make(map[string]*projectToolSetupPreview),
		requests: make(map[projectToolSetupRequest]time.Time),
	}
	if store != nil {
		o.store = store
	}
	return o
}

func (o *ProjectToolSetupOwner) lifetimeLocked(owner document.Owner) (*projectToolSetupLifetime, error) {
	if o.closed || o.w == nil || owner.ClientID == "" || owner.Epoch == 0 {
		return nil, document.ErrOwner
	}
	lifetime := o.owners[owner]
	if lifetime == nil {
		ctx, cancel := context.WithCancel(context.Background())
		lifetime = &projectToolSetupLifetime{ctx: ctx, cancel: cancel}
		o.owners[owner] = lifetime
	}
	if lifetime.ctx.Err() != nil {
		return nil, document.ErrOwner
	}
	return lifetime, nil
}

func (o *ProjectToolSetupOwner) expireLocked(now time.Time) {
	for id, p := range o.pending {
		if !now.Before(p.expires) || p.lifetime.ctx.Err() != nil {
			delete(o.pending, id)
		}
	}
	for request, expires := range o.requests {
		if !now.Before(expires) {
			delete(o.requests, request)
		}
	}
}

func setupOperationContext(ctx context.Context, lifetime *projectToolSetupLifetime) (context.Context, func()) {
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(lifetime.ctx, cancel)
	return ctx, func() { stop(); cancel() }
}

func setupRandomID() (string, error) {
	var id [24]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(id[:]), nil
}

// Review suppresses remembered choices during discovery. An explicit native
// Set up tools action may review still-missing tools again, but never restores
// effect authority from an earlier choice.
func (o *ProjectToolSetupOwner) Review(ctx context.Context, owner document.Owner, explicit bool) (types.ProjectToolSetupReview, error) {
	o.mu.Lock()
	lifetime, err := o.lifetimeLocked(owner)
	o.mu.Unlock()
	if err != nil {
		return types.ProjectToolSetupReview{}, err
	}
	ctx, done := setupOperationContext(ctx, lifetime)
	defer done()
	p, err := o.snapshot(ctx)
	if err != nil {
		return types.ProjectToolSetupReview{}, err
	}
	records, history, err := readProjectToolDecisions(ctx, p.db)
	if err != nil {
		return types.ProjectToolSetupReview{}, err
	}
	known := decidedProjectToolCandidates(records, p.full)
	p.decisionHistory = history
	p.quiet = len(known) != 0
	var installed []toolstore.Tool
	if o.store != nil {
		installed, _, err = o.store.List(ctx)
		if err != nil {
			return types.ProjectToolSetupReview{}, err
		}
	}
	p.offered = types.CloneProjectToolReview(p.full)
	p.offered.Items = nil
	for _, item := range p.full.Items {
		if (explicit || !known[item.ID]) && !setupCandidateInstalled(item, installed) {
			p.offered.Items = append(p.offered.Items, item)
		}
	}
	p.offered.Token = ""
	p.offered.Token = reviewHash(p.offered)
	if err := currentDecisionReview(ctx, o.w, p.db, p.full.Project, p.manager, p.full); err != nil {
		return types.ProjectToolSetupReview{}, err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed || lifetime.ctx.Err() != nil || ctx.Err() != nil {
		return types.ProjectToolSetupReview{}, document.ErrOwner
	}
	o.expireLocked(time.Now())
	// An active decision owns these needs until it settles.
	for _, active := range o.active {
		if active.full.Project == p.full.Project && active.full.RootIdentity == p.full.RootIdentity {
			return types.ProjectToolSetupReview{Message: "Crude is setting up the project tools."}, nil
		}
	}
	if len(p.offered.Items) == 0 {
		return types.ProjectToolSetupReview{Review: p.offered, Message: "There are no new tools to review."}, nil
	}
	for id, old := range o.pending {
		if old.owner != owner {
			continue
		}
		if old.offered.Token == p.offered.Token && old.catalog == p.catalog &&
			old.policyGeneration == p.policyGeneration && old.db == p.db && old.manager == p.manager &&
			old.decisionHistory == p.decisionHistory {
			return setupPreviewView(old), nil
		}
		delete(o.pending, id)
	}
	if len(o.pending) >= projectToolPreviewLimit {
		return types.ProjectToolSetupReview{}, document.ErrLimit
	}
	id, err := setupRandomID()
	if err != nil {
		return types.ProjectToolSetupReview{}, err
	}
	p.id, p.owner, p.lifetime, p.expires = id, owner, lifetime, time.Now().Add(projectToolPreviewTTL)
	o.pending[id] = p
	return setupPreviewView(p), nil
}

func setupPreviewView(p *projectToolSetupPreview) types.ProjectToolSetupReview {
	return types.ProjectToolSetupReview{
		ID: p.id, Review: types.CloneProjectToolReview(p.offered), NeedsDecision: true,
		Quiet:   p.quiet,
		Message: "Review the tools for this project.",
	}
}

func (o *ProjectToolSetupOwner) snapshot(ctx context.Context) (*projectToolSetupPreview, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	w := o.w
	if !w.capabilityMu.TryRLock() {
		return nil, errors.New("the workspace is changing; review the tools again")
	}
	db, root, m := w.WorkspaceDB, w.RootPath, w.LSPMgr
	w.capabilityMu.RUnlock()
	if db == nil || m == nil {
		return nil, errors.New("project tools are not available")
	}
	identity, err := fileio.DirectoryIdentity(root)
	if err != nil {
		return nil, err
	}
	c := catalog.Current()
	m.mu.Lock()
	plan, policyGeneration := types.CloneAssemblyPlan(m.assembly), m.settingsGeneration
	m.mu.Unlock()
	review, err := buildProjectToolReview(root, identity, plan, c, catalog.CurrentTargets())
	if err != nil {
		return nil, err
	}
	if err := currentDecisionReview(ctx, w, db, root, m, review); err != nil {
		return nil, err
	}
	return &projectToolSetupPreview{db: db, manager: m, policyGeneration: policyGeneration, catalog: c, full: review}, nil
}

func decidedProjectToolCandidates(records []projectToolDecision, review types.ProjectToolReview) map[string]bool {
	known := make(map[string]bool)
	for _, record := range records {
		if record.Review.Project == review.Project && record.Review.RootIdentity == review.RootIdentity {
			for _, item := range record.Review.Items {
				known[item.ID] = true
			}
		}
	}
	return known
}

func setupCandidateInstalled(item types.ProjectToolCandidate, tools []toolstore.Tool) bool {
	if !item.Installable {
		return false
	}
	for _, tool := range tools {
		if tool.Package == item.Package && tool.Version == item.Version && tool.Source == item.Source &&
			tool.URL == item.URL && tool.Archive == item.Archive && tool.Bin == item.Bin && tool.Command == item.Command {
			return true
		}
	}
	return false
}

func (o *ProjectToolSetupOwner) Decide(ctx context.Context, owner document.Owner, requestID, previewID string, selectedIDs []string) (types.ProjectToolSetupResult, error) {
	if err := ctx.Err(); err != nil {
		return types.ProjectToolSetupResult{}, err
	}
	selected := append([]string(nil), selectedIDs...)
	o.mu.Lock()
	o.expireLocked(time.Now())
	p := o.pending[previewID]
	request := projectToolSetupRequest{owner, requestID}
	if o.closed || p == nil || p.owner != owner || p.lifetime.ctx.Err() != nil ||
		strings.TrimSpace(requestID) == "" || len(requestID) > 256 {
		o.mu.Unlock()
		return types.ProjectToolSetupResult{}, document.ErrOwner
	}
	if _, used := o.requests[request]; used {
		o.mu.Unlock()
		return types.ProjectToolSetupResult{}, document.ErrRequest
	}
	if _, err := decisionSelection(p.offered, selected); err != nil {
		o.mu.Unlock()
		return types.ProjectToolSetupResult{}, err
	}
	if len(o.requests) >= 128 {
		o.mu.Unlock()
		return types.ProjectToolSetupResult{}, document.ErrLimit
	}
	for _, active := range o.active {
		if active.full.Project == p.full.Project && active.full.RootIdentity == p.full.RootIdentity {
			o.mu.Unlock()
			return types.ProjectToolSetupResult{}, errors.New("project tools are already being set up")
		}
	}
	// Consume before the first effect. No audit row or hash can restore this
	// live authority after a restart, cancellation, detach or failed operation.
	delete(o.pending, previewID)
	o.requests[request] = time.Now().Add(projectToolPreviewTTL)
	p.ctx, p.cancel = context.WithCancel(ctx)
	p.selected = make(map[string]bool, len(selected))
	for _, id := range selected {
		p.selected[id] = true
	}
	o.active[p.id] = p
	o.mu.Unlock()
	stop := context.AfterFunc(p.lifetime.ctx, p.cancel)
	defer func() {
		stop()
		p.cancel()
		o.mu.Lock()
		delete(o.active, p.id)
		o.mu.Unlock()
	}()
	authority := &projectToolSetupAuthority{o: o, p: p}
	watching := make(chan struct{})
	go func() {
		defer close(watching)
		for {
			_, changed := p.manager.Changed()
			if err := authority.gate(p.ctx, "", nil); err != nil {
				p.cancel()
				return
			}
			select {
			case <-p.ctx.Done():
				return
			case <-changed:
			}
		}
	}()
	defer func() { p.cancel(); <-watching }()
	if err := authority.validateReview(p.ctx); err != nil {
		return types.ProjectToolSetupResult{}, err
	}
	_, history, err := readProjectToolDecisions(p.ctx, p.db)
	if err != nil {
		return types.ProjectToolSetupResult{}, err
	}
	if history != p.decisionHistory {
		return types.ProjectToolSetupResult{}, errors.New("the project tool choices changed; review them again")
	}
	work, err := o.w.PrepareProjectToolDecision(p.offered, selected, p.id, p.decisionHistory)
	if err != nil {
		return types.ProjectToolSetupResult{}, err
	}
	saved, err := work(p.ctx)
	result := types.ProjectToolSetupResult{DecisionID: saved.ReceiptID}
	for _, id := range selected {
		result.Items = append(result.Items, types.ProjectToolSetupItem{ItemID: id, Message: "Crude did not try to install this tool.", Evaluation: toolEvaluationNotAttempted})
	}
	if err != nil {
		return result, err
	}
	if err := authority.validateReview(p.ctx); err != nil {
		return result, err
	}
	if len(selected) == 0 {
		result.Message = "Crude saved your choice. You can continue editing without these tools."
		return result, nil
	}
	if o.store == nil {
		result.Message = "Crude could not install the tools. The tool store is not available."
		return result, errors.New("the tool store is not available")
	}
	installer := &setupEvaluatingInstaller{o: o, authority: authority, status: make(map[string]string)}
	results, installErr := installReviewedProjectTools(p.ctx, installer, authority, p.offered, selected, p.id, p.full.Project,
		projectToolInstallLimits{MaxDownloadBytes: 512 << 20, MaxExtractedBytes: 2 << 30, MaxEntries: 65536})
	result.Items = nil
	used := false
	for _, item := range results {
		view := types.ProjectToolSetupItem{ItemID: item.ItemID, ToolID: item.Tool.ID, Message: item.Message, Attempted: item.Attempted, Evaluation: toolEvaluationNotAttempted}
		var uncertain *toolstore.PublicationUnconfirmedError
		view.Installed = item.Tool.ID != "" && !errors.As(item.Err, &uncertain)
		if status := installer.status[item.ItemID]; status != "" {
			view.Evaluation = status
			if status != toolEvaluationPassed {
				view.EvaluationMessage = "Crude could not finish the tool check. Check the tool details."
			}
		}
		if item.Err == nil && item.Tool.ID != "" {
			var err error
			if view.Evaluation != toolEvaluationPassed {
				view.Evaluation, err = o.evaluate(p.ctx, authority, item.ItemID, item.Tool, o.store)
			}
			if err != nil {
				view.EvaluationMessage = "Crude could not finish the tool check. Check the tool details."
				view.Message = view.EvaluationMessage
				installErr = errors.Join(installErr, err)
				result.Items = append(result.Items, view)
				continue
			}
			view.EvaluationMessage = "Crude started the tool in a test folder. Its language features have not been checked."
			err = authority.Validate(p.ctx, item.ItemID)
			if err == nil {
				err = o.store.MarkUsedContext(p.ctx, item.Tool.ID, p.full.Project, func(ctx context.Context) error {
					return authority.AdmitPublication(ctx, item.ItemID)
				})
			}
			if err != nil {
				installErr = errors.Join(installErr, err)
				view.Message = "Crude installed the tool but could not use it for this project. Review the tools again."
			} else {
				used = true
			}
		}
		result.Items = append(result.Items, view)
	}
	if used {
		if err := authority.refresh(p.ctx); err != nil {
			installErr = errors.Join(installErr, err)
		}
	}
	if installErr != nil {
		result.Message = "Crude could not finish setting up the project tools. Check the tool details."
	} else {
		result.Message = "Crude installed the selected tools. It will check the project again."
	}
	return result, installErr
}

type projectToolSetupAuthority struct {
	o *ProjectToolSetupOwner
	p *projectToolSetupPreview
}

func (a *projectToolSetupAuthority) validateReview(ctx context.Context) error {
	if err := a.gate(ctx, "", nil); err != nil {
		return err
	}
	p := a.p
	if err := currentDecisionReview(ctx, a.o.w, p.db, p.full.Project, p.manager, p.full); err != nil {
		return err
	}
	current, err := buildProjectToolReview(p.full.Project, p.full.RootIdentity, p.manager.Assembly(), p.catalog, catalog.CurrentTargets())
	if err != nil || !reflect.DeepEqual(current, p.full) {
		return errors.New("the project tools changed; review them again")
	}
	var raw string
	if err := p.db.DB.QueryRowContext(ctx, `SELECT value FROM workspace_metadata WHERE key=?`, workspaceSettingsKey).Scan(&raw); err != nil {
		return err
	}
	var overrides types.WorkspaceOverrides
	if err := validateUniqueJSON([]byte(raw)); err != nil {
		return err
	}
	if err := json.Unmarshal([]byte(raw), &overrides); err != nil {
		return err
	}
	if !boundWorkspaceSettings(a.o.w.AppDefaults(), overrides, p.full.RootIdentity).Analysis.Trusted() {
		return errors.New("the project is not trusted")
	}
	return a.gate(ctx, "", nil)
}

func (a *projectToolSetupAuthority) Validate(ctx context.Context, id string) error {
	if err := a.gate(ctx, id, nil); err != nil {
		return err
	}
	return a.validateReview(ctx)
}

func (a *projectToolSetupAuthority) AdmitPublication(ctx context.Context, id string) error {
	return a.gate(ctx, id, nil)
}

// gate performs no filesystem, database or store calls. Both publication and
// usage serialize here against Detach/Close and the manager's trust generation.
func (a *projectToolSetupAuthority) gate(ctx context.Context, id string, apply func(*LSPManager)) error {
	o, p := a.o, a.p
	o.mu.Lock()
	defer o.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if o.closed || o.active[p.id] != p || p.ctx.Err() != nil || p.lifetime.ctx.Err() != nil ||
		(id != "" && !p.selected[id]) || catalog.Current() != p.catalog {
		return errors.New("the project tools changed; review them again")
	}
	w := o.w
	if !w.capabilityMu.TryRLock() {
		return errors.New("the workspace is changing; review the tools again")
	}
	defer w.capabilityMu.RUnlock()
	if w.WorkspaceDB != p.db || w.RootPath != p.full.Project || w.LSPMgr != p.manager {
		return errors.New("the project changed; review the tools again")
	}
	m := p.manager
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || m.workspaceRoot != p.full.Project || !m.analysisTrustedLocked() ||
		m.settingsGeneration != p.policyGeneration ||
		m.analysisRootIdentity != p.full.RootIdentity || m.assemblyRequested != p.full.AssemblyGeneration ||
		m.assembly.Generation != p.full.AssemblyGeneration {
		return errors.New("the project setup changed; review the tools again")
	}
	if apply != nil {
		apply(m)
	}
	return nil
}

func (a *projectToolSetupAuthority) refresh(ctx context.Context) error {
	if err := a.validateReview(ctx); err != nil {
		return err
	}
	return a.gate(ctx, "", func(m *LSPManager) {
		m.discovery = nil
		m.assemblyActive = true
		m.requestAssemblyLocked()
	})
}

func (o *ProjectToolSetupOwner) Detach(owner document.Owner) {
	o.mu.Lock()
	defer o.mu.Unlock()
	lifetime := o.owners[owner]
	if lifetime == nil {
		ctx, cancel := context.WithCancel(context.Background())
		lifetime = &projectToolSetupLifetime{ctx: ctx, cancel: cancel}
		o.owners[owner] = lifetime
	}
	lifetime.cancel()
	for id, p := range o.pending {
		if p.owner == owner {
			delete(o.pending, id)
		}
	}
	for _, p := range o.active {
		if p.owner == owner {
			p.cancel()
		}
	}
}

func (o *ProjectToolSetupOwner) Close() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.closed = true
	for _, lifetime := range o.owners {
		lifetime.cancel()
	}
	clear(o.pending)
	for _, p := range o.active {
		p.cancel()
	}
}
