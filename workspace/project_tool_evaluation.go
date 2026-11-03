package workspace

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"crude/insight/governor"
	"crude/internal/fileuri"
	"crude/toolstore"
	"crude/types"
)

const (
	toolEvaluationNotAttempted       = "not_attempted"
	toolEvaluationPassed             = "passed"
	toolEvaluationFailed             = "failed"
	toolEvaluationCancelled          = "cancelled"
	toolEvaluationTeardownUnverified = "teardown_unverified"
)

type projectToolExecutableStore interface {
	AcquireExecutable(context.Context, string) (func(), bool, error)
}

// The install worker exclusively owns these sealed bytes until Evaluate returns.
// This scoped lease must never be used for an indexed executable.
type stagedProjectToolExecutable struct{ tool toolstore.Tool }

func (s stagedProjectToolExecutable) AcquireExecutable(ctx context.Context, executable string) (func(), bool, error) {
	if executable != s.tool.Executable {
		return nil, true, errors.New("the staged executable changed")
	}
	return func() {}, true, ctx.Err()
}

// An uncertain Stop retains the exact client, independent root and both leases.
// Cancellation or owner shutdown must not turn uncertainty into released capacity.
type projectToolEvaluationRetained struct {
	client LSPClient
	root   string
	lease  *governor.Lease
}

// evaluateProjectTool checks the standard initialize/initialized contract at an
// independent inert root. It reads no project source and supplies no invented
// semantic expectations. Start owns the actual framed protocol handshake.
func evaluateProjectTool(ctx context.Context, store projectToolExecutableStore, runtime LSPRuntime,
	spec types.LSPServerSpec, g *governor.Governor, owner governor.Owner, limit int,
	admit func(context.Context) error) (status string, retained *projectToolEvaluationRetained, err error) {
	status = toolEvaluationFailed
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	defer func() {
		if retained == nil && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) {
			status = toolEvaluationCancelled
		}
	}()
	if err = admit(ctx); err != nil {
		return
	}
	if store == nil || runtime == nil || g == nil {
		err = errors.New("the tool check service is not available")
		return
	}
	ticket, e := g.Submit(governor.Request{Owner: owner, WorkspaceLimit: limit})
	if e != nil {
		err = e
		return
	}
	defer ticket.Cancel()
	for {
		observation, changed := ticket.Observe()
		if observation.State == governor.Granted {
			break
		}
		if observation.State != governor.Queued {
			err = errors.New("tool check process admission was cancelled")
			return
		}
		select {
		case <-ctx.Done():
			err = ctx.Err()
			return
		case <-changed:
		}
	}
	release, managed, e := store.AcquireExecutable(ctx, spec.Command)
	if e != nil {
		err = e
		return
	}
	if !managed || release == nil {
		err = errors.New("the acquired executable could not be verified")
		return
	}
	// Before client construction there is no process to retain.
	defer func() {
		if release != nil {
			release()
		}
	}()
	root, e := os.MkdirTemp("", "crude-tool-check-")
	if e != nil {
		err = e
		return
	}
	defer func() {
		if retained == nil {
			err = errors.Join(err, os.RemoveAll(root))
			if err != nil && status == toolEvaluationPassed {
				status = toolEvaluationFailed
			}
		}
	}()
	if err = os.WriteFile(filepath.Join(root, "probe.txt"), []byte("Independent protocol check.\n"), 0600); err != nil {
		return
	}
	// Never pass project-bound configuration scopes into the independent root.
	spec = types.CloneLSPServerSpec(spec)
	spec.ConfigurationScopes = nil
	if err = admit(ctx); err != nil {
		return
	}
	lease, e := ticket.Begin()
	if e != nil {
		err = e
		return
	}
	client := runtime.NewClient(spec, fileuri.FromPath(root), func() error { return admit(ctx) })
	if client == nil {
		lease.Release()
		err = errors.New("the tool check transport is not available")
		return
	}
	client = &leasedLSPClient{LSPClient: client, release: release}
	release = nil // the exact client now owns the executable lease
	err = client.Start(ctx)
	if err == nil {
		err = admit(ctx)
	}
	if err == nil {
		select {
		case <-client.Done():
			err = errors.New("the tool stopped during its protocol check")
		default:
		}
	}
	if stopErr := client.Stop(); stopErr != nil {
		lease.Retain("The tool check process could not be stopped.")
		retained = &projectToolEvaluationRetained{client: client, root: root, lease: lease}
		status = toolEvaluationTeardownUnverified
		err = errors.Join(err, fmt.Errorf("tool check teardown is unverified: %w", stopErr))
		return
	}
	lease.Release()
	if err == nil {
		err = admit(ctx)
	}
	if err == nil {
		status = toolEvaluationPassed
	}
	return
}

func (o *ProjectToolSetupOwner) evaluate(ctx context.Context, authority *projectToolSetupAuthority, id string, tool toolstore.Tool, executableStore projectToolExecutableStore) (string, error) {
	p, m := authority.p, authority.p.manager
	m.mu.Lock()
	settings := types.CloneLSPSettings(m.settings)
	runtime, g := m.runtime, m.analysisGovernor
	owner := governor.Owner{Workspace: m.analysisWorkspace, Generation: m.analysisGeneration}
	limit := m.settings.MaxServers
	m.analysisRetained = true
	m.mu.Unlock()
	settings.Enabled = true
	checked := false
	for _, language := range p.manager.Assembly().Languages {
		var matches bool
		for _, item := range p.offered.Items {
			if item.ID != id {
				continue
			}
			for _, languageID := range item.Languages {
				matches = matches || language.ID == languageID
			}
		}
		if !matches {
			continue
		}
		spec, ok := settings.ResolveServer(language.Sample)
		if !ok || spec.Command != tool.Command || spec.ServerID != language.ServerID {
			return toolEvaluationFailed, errors.New("the tool launch settings changed; review the tools again")
		}
		spec.Command = tool.Executable
		owner.Producer = "check-" + rand.Text()
		status, retained, err := evaluateProjectTool(ctx, executableStore, runtime, spec, g, owner, limit,
			func(ctx context.Context) error { return authority.Validate(ctx, id) })
		if retained != nil {
			o.mu.Lock()
			o.retained = append(o.retained, retained)
			o.mu.Unlock()
		}
		if err != nil {
			if retained != nil {
				err = &toolstore.EvaluationUnverifiedError{Err: err}
			}
			return status, err
		}
		checked = true
	}
	if !checked {
		return toolEvaluationFailed, errors.New("there are no current launch settings to check")
	}
	return toolEvaluationPassed, nil
}

type setupEvaluatingInstaller struct {
	o         *ProjectToolSetupOwner
	authority *projectToolSetupAuthority
	status    map[string]string
}

func (s *setupEvaluatingInstaller) Install(ctx context.Context, request toolstore.Request) (toolstore.Tool, error) {
	var id string
	for _, item := range s.authority.p.offered.Items {
		if item.Package == request.Package && item.Command == request.Command {
			id = item.ID
			break
		}
	}
	if id == "" {
		return toolstore.Tool{}, errors.New("the staged tool does not match the review")
	}
	request.Evaluate = func(ctx context.Context, tool toolstore.Tool) error {
		status, err := s.o.evaluate(ctx, s.authority, id, tool, stagedProjectToolExecutable{tool})
		s.status[id] = status
		return err
	}
	return s.o.store.Install(ctx, request)
}
