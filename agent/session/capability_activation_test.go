package session

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"crude/agent/capability"
	"crude/agent/collaboration"
	"crude/agent/skills"
	"crude/agent/tools"
	"crude/agent/turnloop"
	captypes "crude/types/capability"
	"crude/workspace/fileio"
)

// The package is already local. Its actual executable bootstraps before it can
// speak MCP. Delaying a package-manager helper would not reproduce this case.
func TestActivationBootstrapHelper(t *testing.T) {
	path := os.Getenv("CRUDE_ACTIVATION_RELEASE")
	if path == "" {
		return
	}
	for {
		if _, err := os.Stat(path); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	TestSessionAutonomyStdioHelper(t)
}

// Activation is a job from its first instant: the handle returns before the
// server answers, with no command promotion window to wait out.
func TestSlowActivationSettlesWithDiscoveredToolsWithoutReplay(t *testing.T) {
	setPromotionThreshold(t, time.Hour)
	s := newTestSessionForPlan(t)
	root := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(root, "capabilities.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	service, err := capability.New(root, t.TempDir(), db)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	release := filepath.Join(root, "release")
	config := captypes.ConnectionConfig{ID: "slow", Name: "Slow fixture", Transport: "stdio", LaunchApproved: true,
		Command: os.Args[0], Args: []string{"-test.run=^TestActivationBootstrapHelper$"},
		Environment: map[string]string{"CRUDE_ACTIVATION_RELEASE": release, "CRUDE_SESSION_AUTONOMY_HELPER": "1"}}
	if err := service.Configure(t.Context(), captypes.Settings{Connections: map[string]captypes.ConnectionConfig{"slow": config}}); err != nil {
		t.Fatal(err)
	}
	s.SetCapabilities(service, turnloop.NewFileOwnership())
	live := sharingAgentFor(t, s, captypes.RuntimeActor{ID: "root"}, "Root")
	manager, _ := promotionTestManager(t)
	live.AsyncMgr, live.WorkDir = manager, root
	settled := make(chan fileio.AsyncCmdInfo, 2)
	manager.SetSettleObserver(func(info fileio.AsyncCmdInfo) { settled <- info })
	ctx, stop := context.WithCancel(t.Context())
	defer stop()
	result := make(chan string, 1)
	go func() {
		out, err := live.ToolFn(ctx, "capability_activate", map[string]any{"id": "slow"})
		if err != nil {
			result <- err.Error()
			return
		}
		result <- out.Content
	}()
	var handle string
	select {
	case content := <-result:
		job, ok := tools.ParseCommandResult("capability_activate", content)
		if !ok || job.OutputDescriptor.Status != fileio.AsyncRunning || job.ExecutionID == "" {
			t.Fatalf("activation did not return an exact running job: %s", content)
		}
		handle = job.OutputDescriptor.Handle
	case <-time.After(2 * time.Second):
		stop()
		<-result
		t.Fatal("actual MCP startup held the turn instead of returning a background handle")
	}
	stop() // A normally finished tool/turn no longer owns this admitted launch.
	info, ok := manager.Check(handle)
	if !ok || info.PID != 0 || !manager.Running() {
		t.Fatalf("job inventory: %+v", info)
	}
	ref := info.Ref()
	ref.StartedAt = ref.StartedAt.Add(time.Second)
	if err := manager.KillRef(ref); !errors.Is(err, fileio.ErrAsyncCommandReplaced) {
		t.Fatalf("stale control was accepted: %v", err)
	}
	if os.Getenv("CRUDE_LONG_ACTIVATION_PROOF") == "1" {
		time.Sleep(31 * time.Second)
		if info, ok := manager.Check(handle); !ok || info.Status != fileio.AsyncRunning {
			t.Fatalf("default handshake deadline killed activation: %+v", info)
		}
	}
	if err := os.WriteFile(release, nil, 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case final := <-settled:
		if final.Handle != handle || final.Status != fileio.AsyncCompleted || final.SettledOutput == nil || !strings.Contains(final.SettledOutput.Output, "lookup") || final.SettledOutput.ExitCode != nil {
			t.Fatalf("settlement is not activation evidence: %+v", final)
		}
	case <-time.After(6 * time.Second):
		t.Fatal("no automatic activation settlement")
	}
	connected := false
	for _, state := range service.Snapshot().Connections {
		connected = connected || state.ID == "slow" && state.State == "connected" && len(state.Tools) == 1
	}
	if !connected {
		t.Fatal("settling activation stopped the connected server")
	}
	// A running server has nothing left to start: its tools return in the
	// result itself, and no second job appears.
	again, err := live.ToolFn(t.Context(), "capability_activate", map[string]any{"id": "slow"})
	if err != nil || !strings.Contains(again.Content, `"connection_id":"slow"`) || strings.Contains(again.Content, "background job") {
		t.Fatalf("connected activation was not answered inline: %v %s", err, again.Content)
	}
	if len(manager.List()) != 1 {
		t.Fatalf("connected activation started another job: %+v", manager.List())
	}
}

func TestActivationOutcomeInterruptsBothWaitsWithoutConsumingDelivery(t *testing.T) {
	s, store, owner, other := collaborationContractFixture(t)
	manager, _ := promotionTestManager(t)
	live := &turnloop.Agent{AsyncMgr: manager, WorkDir: t.TempDir()}
	s.observeCommandOutcomes(manager, owner.ID)
	release := make(chan struct{})
	ctx, cancel := context.WithTimeout(t.Context(), 6*time.Second)
	defer cancel()
	out, err := s.runActivation(ctx, live, owner.ID, "skill", "Activate skill", func(job context.Context) (captypes.Result, error) {
		select {
		case <-job.Done():
			return captypes.Result{}, job.Err()
		case <-release:
			return captypes.Result{Content: []captypes.Content{{Type: "text", Text: "verified skill instructions"}}}, nil
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	handle := promotedHandle(t, out.Content)
	waitCtx := context.WithValue(ctx, tools.ExecutionIdentityKey{}, tools.ExecutionIdentity{AgentID: owner.ID})
	timed, eventWait := make(chan string, 1), make(chan bool, 1)
	go func() {
		result, _ := s.buildToolFn(false, nil, nil)(waitCtx, "wait", map[string]any{"seconds": 20.0})
		timed <- result.Content
	}()
	go func() {
		value, err := s.waitForCollaboration(waitCtx, store, map[string]any{"timeout_seconds": 20})
		if err != nil {
			eventWait <- false
			return
		}
		v := value.(collaborationWaitObservation)
		eventWait <- v.Event != nil && v.Event.Kind == collaboration.EventCommandOutcome
	}()
	close(release)
	if got := <-timed; got != "An event is ready." {
		t.Fatalf("timed wait: %q", got)
	}
	if !<-eventWait {
		t.Fatal("agent_wait missed activation")
	}
	page, err := store.Page(ctx, collaboration.PageQuery{RecipientID: owner.ID, Unadmitted: true})
	if err != nil || len(page.Events) != 1 || page.Events[0].Command == nil || page.Events[0].Command.Handle != handle || !strings.Contains(page.Events[0].Body, "verified skill instructions") {
		t.Fatalf("journal: %+v %v", page, err)
	}
	peer, err := store.Page(ctx, collaboration.PageQuery{RecipientID: other.ID})
	if err != nil || len(peer.Events) != 0 {
		t.Fatalf("completion delivered to unrelated agent: %+v %v", peer, err)
	}
}

// Installed instructions are local files: there is nothing to wait for, so
// the agent reads them in the activation result and no job appears.
func TestInstalledSkillAnswersInlineWithoutAJob(t *testing.T) {
	setPromotionThreshold(t, time.Hour)
	s := newTestSessionForPlan(t)
	root := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(root, "capabilities.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	service, err := capability.New(root, t.TempDir(), db)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	skill, err := skills.Create(filepath.Join(root, "review"), "review", "Review exact evidence before changing a file")
	if err != nil {
		t.Fatal(err)
	}
	settings := captypes.Normalize(captypes.Settings{Skills: map[string]captypes.SkillConfig{"local:review": {ID: "local:review", Name: "review", Path: skill.Path, Mode: "automatic"}}})
	if err := service.Configure(t.Context(), settings); err != nil {
		t.Fatal(err)
	}
	s.SetCapabilities(service, turnloop.NewFileOwnership())
	live := sharingAgentFor(t, s, captypes.RuntimeActor{ID: "root"}, "Root")
	manager, _ := promotionTestManager(t)
	live.AsyncMgr, live.WorkDir = manager, root
	out, err := live.ToolFn(t.Context(), "capability_activate", map[string]any{"id": "local:review"})
	if err != nil || !strings.HasPrefix(out.Content, "<skill ") {
		t.Fatalf("installed skill was not answered inline: %v %s", err, out.Content)
	}
	if jobs := manager.List(); len(jobs) != 0 {
		t.Fatalf("loading installed instructions started a job: %+v", jobs)
	}
}
