package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"crude/shellport"
	"crude/types/control"
	"crude/types/handover"
)

type restartTestPort struct {
	state                                                  handover.State
	reservation                                            handover.Reservation
	reserves, inspects, cancels, continues                 int
	reserveError, inspectError, cancelError, continueError error
	beforeReserve                                          func()
}

func (p *restartTestPort) ReserveCoreRestart(_ context.Context, _, _ string, r handover.Reservation) (handover.State, error) {
	p.reserves++
	if p.beforeReserve != nil {
		p.beforeReserve()
	}
	p.reservation = r
	p.state = handover.State{OperationID: r.OperationID, GoalID: r.GoalID, SuccessorCohort: r.SuccessorCohort,
		SuccessorSchema: r.SuccessorSchema, ExpiresAt: r.ExpiresAt, SourceAgentID: r.SourceAgentID, SourceRunID: r.SourceRunID,
		SourceRuntimeEpoch: 9, SourceTurnGeneration: 7, SourceCoreOwner: r.SourceCoreOwner, SourceShell: r.SourceShell, Phase: handover.Reserved}
	return p.state, p.reserveError
}
func (p *restartTestPort) InspectCoreRestart(context.Context, string, string) (handover.State, error) {
	p.inspects++
	return p.state, p.inspectError
}
func (p *restartTestPort) CancelCoreRestart(_ context.Context, _, _, token string) (handover.State, error) {
	p.cancels++
	if restartHash(token) != p.reservation.TokenSHA256 {
		return handover.State{}, errors.New("bad token")
	}
	p.state.Phase = handover.Cancelled
	return p.state, p.cancelError
}
func (p *restartTestPort) ContinueCoreRestart(_ context.Context, _, _, token string) (handover.State, error) {
	p.continues++
	if restartHash(token) != p.reservation.TokenSHA256 {
		return handover.State{}, errors.New("bad token")
	}
	p.state.Phase = handover.Consumed
	return p.state, p.continueError
}

type restartFixture struct {
	host     *coreRestartHost
	port     *restartTestPort
	request  control.Request
	input    shellport.CoreRestartInput
	identity CoreRestartIdentity
	old      coreOwner
	now      time.Time
}

func newRestartFixture(t *testing.T) *restartFixture {
	t.Helper()
	home := t.TempDir()
	root := filepath.Join(home, "project")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(coreOwnerPath(home)), 0700); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{DataDir(home), filepath.Dir(coreOwnerPath(home))} {
		if err := secureOwnerPath(dir, true); err != nil {
			t.Fatal(err)
		}
	}
	oldSource := filepath.Join(home, "old")
	if err := os.WriteFile(oldSource, []byte("old harmless fixture image"), 0700); err != nil {
		t.Fatal(err)
	}
	oldDigest, _, err := restartImageDigest(oldSource)
	if err != nil {
		t.Fatal(err)
	}
	oldImage, err := stageCoreImage(home, oldSource, oldDigest[:16])
	if err != nil {
		t.Fatal(err)
	}
	old := coreOwner{Generation: strings.Repeat("1", 64), Executable: oldImage, Digest: oldDigest, Cohort: strings.Repeat("2", 64), Schema: "old-schema"}
	if err := publishCoreOwner(coreOwnerPath(home), old); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(home, "candidate")
	if err := os.WriteFile(source, []byte("new harmless fixture image"), 0700); err != nil {
		t.Fatal(err)
	}
	digest, _, err := restartImageDigest(source)
	if err != nil {
		t.Fatal(err)
	}
	identity := CoreRestartIdentity{BuildID: digest[:16], SHA256: digest, Cohort: strings.Repeat("3", 64), Schema: "new-schema", GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}
	target := control.Target{WorkspaceID: "workspace-1", Shell: &control.ShellOwner{ClientID: "original-shell-client", Epoch: 42}}
	port := &restartTestPort{}
	host, err := newCoreRestartHost(home, root, "original-profile", target, port)
	if err != nil {
		t.Fatal(err)
	}
	f := &restartFixture{host: host, port: port, identity: identity, old: old, now: time.Date(2026, 10, 29, 15, 0, 0, 0, time.UTC)}
	host.now = func() time.Time { return f.now }
	host.identity = func(context.Context, string) (CoreRestartIdentity, error) { return identity, nil }
	host.stop = func(context.Context, string, coreOwner) error { t.Fatal("unexpected core stop"); return nil }
	host.settle = func(context.Context, string) error { t.Fatal("unexpected lifetime settlement"); return nil }
	host.launch = func(context.Context, string, []string, []string, string) error {
		t.Fatal("unexpected successor launch")
		return nil
	}
	f.input = shellport.CoreRestartInput{Executable: source, SHA256: digest, GoalID: "authorized-goal", DelaySeconds: 5}
	target.SessionID = "session-1"
	f.request = control.Request{ID: "restart-operation-1", CommandID: "core.restart", Target: target,
		Caller: control.Caller{Kind: control.AgentCaller, AgentID: "root-agent", RunID: "original-run"}}
	f.setInput(t)
	return f
}

func (f *restartFixture) setInput(t *testing.T) {
	t.Helper()
	data, err := json.Marshal(f.input)
	if err != nil {
		t.Fatal(err)
	}
	f.request.Input = data
}
func (f *restartFixture) schedule(t *testing.T) {
	t.Helper()
	if _, err := f.host.Schedule(t.Context(), f.request); err != nil {
		t.Fatal(err)
	}
}
func (f *restartFixture) ready(t *testing.T) {
	t.Helper()
	f.schedule(t)
	f.now = f.now.Add(5 * time.Second)
	f.port.state.Phase = handover.Ready
	if err := f.host.Ready(t.Context(), f.request.ID); err != nil {
		t.Fatal(err)
	}
}

func TestCoreRestartScheduleDurablePrivateAndIdempotent(t *testing.T) {
	f := newRestartFixture(t)
	path := f.host.manifestPath(f.request.ID)
	f.port.beforeReserve = func() {
		m, r, _, err := readRestartManifest(f.host.home, path)
		if err != nil || r.Event != "reserve_requested" || m.ControlID != f.request.ID {
			t.Fatalf("reservation preceded durable intent: %+v %v", r, err)
		}
	}
	state, err := f.host.Schedule(t.Context(), f.request)
	if err != nil {
		t.Fatal(err)
	}
	m, r, _, err := readRestartManifest(f.host.home, path)
	if err != nil {
		t.Fatal(err)
	}
	if m.OldOwner != f.old || m.Identity != f.identity || m.Target.Shell.ClientID != f.request.Target.Shell.ClientID ||
		!m.NotBefore.Equal(f.now.Add(5*time.Second)) || !m.ExpiresAt.Equal(f.now.Add(10*time.Minute)) ||
		f.port.reservation.TokenSHA256 != restartHash(m.Token) || f.port.reservation.TokenSHA256 == m.Token {
		t.Fatal("intent omitted immutable original/new owner or nonce binding")
	}
	published, _ := json.Marshal(state)
	if strings.Contains(string(published), m.Token) || strings.Contains(string(published), f.port.reservation.TokenSHA256) {
		t.Fatal("private nonce escaped in state")
	}
	if r.Event != "reserve_observed" {
		t.Fatalf("receipt %+v", r)
	}
	for _, name := range []string{path, filepath.Join(filepath.Dir(path), "receipt-000001.json")} {
		if err := validateOwnerPath(name, false); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.host.Schedule(t.Context(), f.request); err != nil {
		t.Fatal(err)
	}
	if f.port.reserves != 1 || f.port.inspects != 1 {
		t.Fatalf("duplicate schedule replayed effect: %+v", f.port)
	}
	f.input.DelaySeconds++
	f.setInput(t)
	if _, err := f.host.Schedule(t.Context(), f.request); err == nil {
		t.Fatal("changed duplicate input accepted")
	}
	if f.port.reserves != 1 {
		t.Fatal("changed duplicate repeated reservation")
	}
}

func TestCoreRestartPreservesValidatedSourceExecutionSelection(t *testing.T) {
	f := newRestartFixture(t)
	f.request.Target.ExecutionID = "original-managed-execution"
	f.schedule(t)
	m, _, _, err := readRestartManifest(f.host.home, f.host.manifestPath(f.request.ID))
	if err != nil || m.Target.ExecutionID != "original-managed-execution" {
		t.Fatalf("original validated execution selection lost: %+v %v", m.Target, err)
	}
	changed := f.request
	changed.Target.ExecutionID = "another-execution"
	if _, err := f.host.Schedule(t.Context(), changed); err == nil {
		t.Fatal("changed source execution accepted under original operation ID")
	}
	if f.port.reserves != 1 {
		t.Fatal("changed execution repeated a reservation")
	}
}

func TestCoreRestartUncertainReserveRestoreOnlyObserves(t *testing.T) {
	f := newRestartFixture(t)
	f.port.reserveError = errors.New("secret-token must never be returned")
	state, err := f.host.Schedule(t.Context(), f.request)
	if err == nil || strings.Contains(err.Error(), "secret-token") || state.OperationID != "" {
		t.Fatalf("unsafe error/result: %+v %v", state, err)
	}
	restored, err := newCoreRestartHost(f.host.home, f.host.root, f.host.profile, f.host.target, f.port)
	if err != nil {
		t.Fatal(err)
	}
	if f.port.reserves != 1 || f.port.inspects != 0 {
		t.Fatal("constructor replayed an effect")
	}
	if _, err := restored.Schedule(t.Context(), f.request); err != nil {
		t.Fatal(err)
	}
	if f.port.reserves != 1 || f.port.inspects != 1 {
		t.Fatal("uncertain reservation replayed rather than inspected")
	}
}

func TestCoreRestartScheduleRejectsCompetingAndUnboundRequests(t *testing.T) {
	for _, test := range []string{"shell", "workspace", "goal", "digest", "delay", "symlink", "mode", "source-change", "identity", "owner-change"} {
		t.Run(test, func(t *testing.T) {
			f := newRestartFixture(t)
			switch test {
			case "shell":
				f.request.Target.Shell = &control.ShellOwner{ClientID: "another-terminal", Epoch: 42}
			case "workspace":
				f.request.Target.WorkspaceID = "another-workspace"
			case "goal":
				f.input.GoalID = ""
			case "digest":
				f.input.SHA256 = strings.Repeat("f", 64)
			case "delay":
				f.input.DelaySeconds = 301
			case "symlink":
				link := filepath.Join(f.host.home, "candidate-link")
				if err := os.Symlink(f.input.Executable, link); err != nil {
					t.Skipf("symlinks unavailable: %v", err)
				}
				f.input.Executable = link
			case "mode":
				if runtime.GOOS == "windows" {
					t.Skip("Windows executable mode is ACL-owned")
				}
				if err := os.Chmod(f.input.Executable, 0600); err != nil {
					t.Fatal(err)
				}
			case "source-change":
				f.host.identity = func(context.Context, string) (CoreRestartIdentity, error) {
					if err := os.WriteFile(f.input.Executable, []byte("changed source"), 0700); err != nil {
						t.Fatal(err)
					}
					return f.identity, nil
				}
			case "identity":
				f.host.identity = func(context.Context, string) (CoreRestartIdentity, error) {
					i := f.identity
					i.GOARCH = "wrong"
					return i, nil
				}
			case "owner-change":
				replaceRestartOwner(t, f.host.home, coreOwner{Generation: strings.Repeat("a", 64), Executable: f.old.Executable, Digest: f.old.Digest, Cohort: f.old.Cohort, Schema: f.old.Schema})
			}
			f.setInput(t)
			if _, err := f.host.Schedule(t.Context(), f.request); err == nil {
				t.Fatal("unbound/invalid request accepted")
			}
			if f.port.reserves != 0 {
				t.Fatal("invalid request reached session admission")
			}
		})
	}
	f := newRestartFixture(t)
	f.schedule(t)
	second := f.request
	second.ID = "competing-operation"
	if _, err := f.host.Schedule(t.Context(), second); err == nil {
		t.Fatal("competing pending schedule accepted")
	}
	if _, err := f.host.Cancel(t.Context(), f.request.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.host.Schedule(t.Context(), second); err != nil {
		t.Fatalf("completed terminal record blocked new operation: %v", err)
	}
	if _, err := os.Stat(f.host.manifestPath(f.request.ID)); err != nil {
		t.Fatal("terminal intent was removed")
	}
}

func TestCoreRestartReadyRequiresNaturalCheckpointAndExactOwner(t *testing.T) {
	f := newRestartFixture(t)
	f.schedule(t)
	if err := f.host.Ready(t.Context(), f.request.ID); !errors.Is(err, shellport.ErrCoreRestartPending) {
		t.Fatalf("early readiness: %v", err)
	}
	f.now = f.now.Add(5 * time.Second)
	if err := f.host.Ready(t.Context(), f.request.ID); !errors.Is(err, shellport.ErrCoreRestartPending) {
		t.Fatalf("uncommitted checkpoint: %v", err)
	}
	f.port.state.Phase = handover.Ready
	if err := f.host.Ready(t.Context(), f.request.ID); err != nil {
		t.Fatal(err)
	}
	replaceRestartOwner(t, f.host.home, coreOwner{Generation: strings.Repeat("b", 64), Executable: f.old.Executable, Digest: f.old.Digest, Cohort: f.old.Cohort, Schema: f.old.Schema})
	if err := f.host.Ready(t.Context(), f.request.ID); err == nil || errors.Is(err, shellport.ErrCoreRestartPending) {
		t.Fatal("replacement owner was pending/successful")
	}
}

func TestCoreRestartReadyRejectsExpiredTamperedAndRetargeted(t *testing.T) {
	for _, test := range []string{"expired", "image", "profile-epoch", "receipt", "manifest", "symlink-directory"} {
		t.Run(test, func(t *testing.T) {
			f := newRestartFixture(t)
			f.schedule(t)
			f.now = f.now.Add(5 * time.Second)
			f.port.state.Phase = handover.Ready
			path := f.host.manifestPath(f.request.ID)
			m, _, _, err := readRestartManifest(f.host.home, path)
			if err != nil {
				t.Fatal(err)
			}
			switch test {
			case "expired":
				f.now = f.now.Add(10 * time.Minute)
			case "image":
				if err := os.WriteFile(m.Image, []byte("changed"), 0700); err != nil {
					t.Fatal(err)
				}
			case "profile-epoch":
				f.host.target.Shell.Epoch++
			case "receipt":
				if err := os.WriteFile(filepath.Join(filepath.Dir(path), "receipt-000002.json"), []byte("{}"), 0600); err != nil {
					t.Fatal(err)
				}
			case "manifest":
				if err := os.WriteFile(path, []byte("{}"), 0600); err != nil {
					t.Fatal(err)
				}
			case "symlink-directory":
				dir := filepath.Dir(path)
				if err := os.Rename(dir, dir+"-original"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(dir+"-original", dir); err != nil {
					t.Skipf("symlinks unavailable: %v", err)
				}
			}
			if err := f.host.Ready(t.Context(), f.request.ID); err == nil || errors.Is(err, shellport.ErrCoreRestartPending) {
				t.Fatalf("unsafe readiness: %v", err)
			}
		})
	}
}

func replaceRestartOwner(t *testing.T, home string, owner coreOwner) {
	t.Helper()
	if err := os.Remove(coreOwnerPath(home)); err != nil {
		t.Fatal(err)
	}
	if err := publishCoreOwner(coreOwnerPath(home), owner); err != nil {
		t.Fatal(err)
	}
}

func TestCoreRestartRunReadyExactForegroundOnceWithoutClosedPort(t *testing.T) {
	f := newRestartFixture(t)
	f.ready(t)
	var calls []string
	f.port.inspectError = errors.New("port closed after UI shutdown")
	f.host.stop = func(_ context.Context, home string, old coreOwner) error {
		calls = append(calls, "stop")
		if home != f.host.home || old != f.old {
			t.Fatal("wrong owner selected")
		}
		return os.Remove(coreOwnerPath(home))
	}
	f.host.settle = func(_ context.Context, home string) error {
		calls = append(calls, "settle")
		return settleRestartLifetime(t.Context(), home)
	}
	f.host.launch = func(_ context.Context, image string, args, env []string, dir string) error {
		calls = append(calls, "foreground-launch-and-wait")
		m, r, _, err := readRestartManifest(f.host.home, f.host.manifestPath(f.request.ID))
		if err != nil || !r.StopSettled || !r.LaunchRequested {
			t.Fatalf("launch preceded receipt: %+v %v", r, err)
		}
		if image != m.Image || dir != f.host.root || len(args) != 2 || args[0] != "--core-handover" || args[1] != f.host.manifestPath(f.request.ID) {
			t.Fatal("successor was retargeted")
		}
		found := false
		for _, e := range env {
			if e == "CRUDE_HOME="+f.host.home {
				found = true
			}
		}
		if !found {
			t.Fatal("successor lost original home")
		}
		r.ContinueRequested = true
		r.State.Phase = handover.Consumed
		return writeRestartReceipt(f.host.manifestPath(f.request.ID), &r)
	}
	if err := f.host.RunReady(t.Context(), f.request.ID); err != nil {
		t.Fatal(err)
	}
	if strings.Join(calls, ",") != "stop,settle,foreground-launch-and-wait" {
		t.Fatalf("wrong lifecycle %v", calls)
	}
	if err := f.host.RunReady(t.Context(), f.request.ID); err == nil {
		t.Fatal("replayed committed/uncertain handover")
	}
	if len(calls) != 3 {
		t.Fatal("second handover repeated an effect")
	}
}

func TestCoreRestartStopFailurePreservesIntentNeverLaunchesOrRetries(t *testing.T) {
	f := newRestartFixture(t)
	f.ready(t)
	stops := 0
	f.host.stop = func(context.Context, string, coreOwner) error {
		stops++
		return errors.New("private helper error secret")
	}
	if err := f.host.RunReady(t.Context(), f.request.ID); err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatalf("unsafe stop error: %v", err)
	}
	if err := f.host.RunReady(t.Context(), f.request.ID); err == nil {
		t.Fatal("replayed failed/uncertain stop")
	}
	if stops != 1 {
		t.Fatal("repeated stop")
	}
	owner, err := readCoreOwner(f.host.home)
	if err != nil || owner != f.old {
		t.Fatalf("failed stop displaced original owner: %+v %v", owner, err)
	}
	_, r, _, err := readRestartManifest(f.host.home, f.host.manifestPath(f.request.ID))
	if err != nil || !r.StopRequested || r.LaunchRequested || r.Error == "" {
		t.Fatalf("uncertain receipt missing: %+v %v", r, err)
	}
}

func prepareRestartSuccessor(t *testing.T, f *restartFixture) *coreRestartHost {
	t.Helper()
	f.ready(t)
	// Independent successor fixture: the original foreground owner has already
	// persisted its launch request, but the successor has not activated anything.
	path := f.host.manifestPath(f.request.ID)
	m, r, _, err := readRestartManifest(f.host.home, path)
	if err != nil {
		t.Fatal(err)
	}
	r.StopRequested, r.StopSettled, r.LaunchRequested = true, true, true
	r.Event = "successor_launch_requested"
	if err := writeRestartReceipt(path, &r); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(coreOwnerPath(f.host.home)); err != nil {
		t.Fatal(err)
	}
	if err := publishCoreOwner(coreOwnerPath(f.host.home), coreOwner{Generation: strings.Repeat("c", 64), Executable: m.Image, Digest: m.Identity.SHA256, Cohort: m.Identity.Cohort, Schema: m.Identity.Schema}); err != nil {
		t.Fatal(err)
	}
	target := f.host.target
	target.Shell = &control.ShellOwner{ClientID: target.Shell.ClientID, Epoch: 1}
	next, err := newCoreRestartHostFromManifest(f.host.home, f.host.root, f.host.profile, target, f.port, f.host.manifestPath(f.request.ID))
	if err != nil {
		t.Fatal(err)
	}
	next.now = func() time.Time { return f.now }
	next.currentIdentity = func() (CoreRestartIdentity, error) { return f.identity, nil }
	return next
}

func TestCoreRestartSuccessorExitIsNotContinuationEvidence(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(map[bool]string{false: "zero-exit-without-activation", true: "failed-child"}[failed], func(t *testing.T) {
			f := newRestartFixture(t)
			f.ready(t)
			f.host.stop = func(context.Context, string, coreOwner) error { return os.Remove(coreOwnerPath(f.host.home)) }
			f.host.settle = settleRestartLifetime
			launches := 0
			f.host.launch = func(context.Context, string, []string, []string, string) error {
				launches++
				if failed {
					return errors.New("private candidate stderr")
				}
				return nil
			}
			if err := f.host.RunReady(t.Context(), f.request.ID); err == nil || strings.Contains(err.Error(), "stderr") {
				t.Fatalf("candidate exit was falsely reported as continuation or leaked an error: %v", err)
			}
			_, r, _, err := readRestartManifest(f.host.home, f.host.manifestPath(f.request.ID))
			if err != nil || r.Event != "successor_exited" || r.Error == "" || !r.LaunchRequested || r.ContinueRequested {
				t.Fatalf("candidate exit receipt missing: %+v %v", r, err)
			}
			if err := f.host.RunReady(t.Context(), f.request.ID); err == nil || launches != 1 {
				t.Fatal("failed/unverified successor was automatically relaunched")
			}
		})
	}
}

func TestCoreRestartContinueExactNewOwnerOnceAndUncertainInspect(t *testing.T) {
	for _, uncertain := range []bool{false, true} {
		t.Run(map[bool]string{false: "acknowledged", true: "lost-result"}[uncertain], func(t *testing.T) {
			f := newRestartFixture(t)
			next := prepareRestartSuccessor(t, f)
			if uncertain {
				f.port.continueError = errors.New("secret continuation error")
			}
			state, err := next.Continue(t.Context())
			if uncertain {
				if err == nil || strings.Contains(err.Error(), "secret") || state.OperationID != "" {
					t.Fatalf("unsafe uncertain continuation: %+v %v", state, err)
				}
			} else if err != nil || state.Phase != handover.Consumed {
				t.Fatalf("continuation: %+v %v", state, err)
			}
			f.now = f.now.Add(20 * time.Minute)
			state, err = next.Continue(t.Context())
			if err != nil || state.Phase != handover.Consumed || f.port.continues != 1 {
				t.Fatalf("repeat must inspect, not resume: %+v %v calls=%d", state, err, f.port.continues)
			}
			if next.StartupSessionID() != "session-1" || next.StartupOperationID() != f.request.ID {
				t.Fatal("startup lost original operation/session")
			}
		})
	}
}

func TestCoreRestartContinueRejectsOldOrDifferentImageOwner(t *testing.T) {
	for _, test := range []string{"old-owner", "owner-image", "running-image", "other-client", "other-profile", "no-launch", "expired"} {
		t.Run(test, func(t *testing.T) {
			f := newRestartFixture(t)
			next := prepareRestartSuccessor(t, f)
			switch test {
			case "old-owner":
				replaceRestartOwner(t, f.host.home, f.old)
			case "owner-image":
				owner := f.old
				owner.Generation = strings.Repeat("d", 64)
				owner.Cohort = f.identity.Cohort
				owner.Schema = f.identity.Schema
				replaceRestartOwner(t, f.host.home, owner)
			case "running-image":
				next.currentIdentity = func() (CoreRestartIdentity, error) { i := f.identity; i.SHA256 = f.old.Digest; return i, nil }
			case "other-client", "other-profile":
				target := next.target
				profile := next.profile
				if test == "other-client" {
					target.Shell = &control.ShellOwner{ClientID: "other-shell", Epoch: 1}
				} else {
					profile = "other-profile"
				}
				_, err := newCoreRestartHostFromManifest(f.host.home, f.host.root, profile, target, f.port, f.host.manifestPath(f.request.ID))
				if err == nil {
					t.Fatal("successor manifest retargeted")
				}
				return
			case "no-launch":
				path := next.startup
				_, r, _, err := readRestartManifest(f.host.home, path)
				if err != nil {
					t.Fatal(err)
				}
				r.LaunchRequested = false
				if err := writeRestartReceipt(path, &r); err != nil {
					t.Fatal(err)
				}
			case "expired":
				f.now = f.now.Add(11 * time.Minute)
			}
			if _, err := next.Continue(t.Context()); err == nil {
				t.Fatal("unauthorized continuation")
			}
			if f.port.continues != 0 {
				t.Fatal("invalid successor resumed original goal")
			}
		})
	}
}

func TestCoreRestartCancelUncertainIsNotReplayed(t *testing.T) {
	f := newRestartFixture(t)
	f.schedule(t)
	f.port.cancelError = errors.New("private failure")
	if _, err := f.host.Cancel(t.Context(), f.request.ID); err == nil {
		t.Fatal("expected uncertain cancel")
	}
	state, err := f.host.Cancel(t.Context(), f.request.ID)
	if err != nil || state.Phase != handover.Cancelled || f.port.cancels != 1 {
		t.Fatalf("cancel was replayed: %+v %v calls=%d", state, err, f.port.cancels)
	}
}

func TestCoreRestartPrivatePublicationNeverReplaces(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "private")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := secureOwnerPath(dir, true); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "record.json")
	if err := publishRestartFile(path, []byte("first")); err != nil {
		t.Fatal(err)
	}
	if err := publishRestartFile(path, []byte("second")); !errors.Is(err, os.ErrExist) {
		t.Fatalf("overwrote immutable record: %v", err)
	}
	data, err := readRestartFile(path)
	if err != nil || string(data) != "first" {
		t.Fatalf("record changed: %q %v", data, err)
	}
	link := filepath.Join(dir, "symlink.json")
	if err := os.Symlink(path, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := readRestartFile(link); err == nil {
		t.Fatal("followed symlink for private record")
	}
}
