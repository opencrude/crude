package app

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"crude/buildinfo"
	"crude/coreprotocol"
	"crude/shellport"
	"crude/types/control"
	"crude/types/handover"

	"github.com/gofrs/flock"
)

const restartRecordLimit = 64 << 10

// CoreRestartIdentity is the bounded, read-only candidate executable handshake.
// A source cohort is not an image digest: both must be checked.
type CoreRestartIdentity struct {
	BuildID string `json:"build_id"`
	Cohort  string `json:"cohort"`
	Schema  string `json:"schema"`
	SHA256  string `json:"sha256"`
	GOOS    string `json:"goos"`
	GOARCH  string `json:"goarch"`
}

func CoreRestartIdentityForCurrent() (CoreRestartIdentity, error) {
	exe, err := os.Executable()
	if err != nil {
		return CoreRestartIdentity{}, errors.New("cannot identify running handover image")
	}
	digest, _, err := restartImageDigest(exe)
	if err != nil {
		return CoreRestartIdentity{}, errors.New("cannot verify running handover image")
	}
	return CoreRestartIdentity{BuildID: BuildID(), Cohort: buildinfo.ProtocolBuild,
		Schema: coreprotocol.Schema, SHA256: digest, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}, nil
}

type coreRestartManifest struct {
	Version   int                        `json:"version"`
	Home      string                     `json:"home"`
	Root      string                     `json:"root"`
	Profile   string                     `json:"profile"`
	ControlID string                     `json:"control_id"`
	Target    control.Target             `json:"target"`
	Input     shellport.CoreRestartInput `json:"input"`
	Caller    control.Caller             `json:"caller"`
	OldOwner  coreOwner                  `json:"old_owner"`
	Image     string                     `json:"image"`
	Identity  CoreRestartIdentity        `json:"identity"`
	Token     string                     `json:"token"`
	NotBefore time.Time                  `json:"not_before"`
	ExpiresAt time.Time                  `json:"expires_at"`
}

// Each receipt is an immutable snapshot, published without replacement. A crash
// after *_requested is deliberately uncertain: restoring only observes it.
type coreRestartReceipt struct {
	ManifestSHA256    string         `json:"manifest_sha256"`
	Sequence          int            `json:"sequence"`
	Event             string         `json:"event"`
	State             handover.State `json:"state"`
	Ready             bool           `json:"ready"`
	StopRequested     bool           `json:"stop_requested"`
	StopSettled       bool           `json:"stop_settled"`
	LaunchRequested   bool           `json:"launch_requested"`
	ContinueRequested bool           `json:"continue_requested"`
	CancelRequested   bool           `json:"cancel_requested"`
	Error             string         `json:"error,omitempty"`
}

// CoreRestartStartup contains only routing facts, never the private nonce.
type CoreRestartStartup struct {
	Root, Profile, SessionID, OperationID string
}

// ReadCoreRestartStartup is passive. It neither starts a core nor consumes a
// reservation. The caller must use precisely this profile/root/session and must
// call Continue on the exact resulting original-client attachment.
func ReadCoreRestartStartup(home, path string) (CoreRestartStartup, error) {
	m, _, _, err := readRestartManifest(home, path)
	if err != nil {
		return CoreRestartStartup{}, err
	}
	return CoreRestartStartup{Root: m.Root, Profile: m.Profile,
		SessionID: m.Target.SessionID, OperationID: m.ControlID}, nil
}

type coreRestartHost struct {
	mu                                sync.Mutex
	home, root, profile, dir, startup string
	target                            control.Target
	owner                             coreOwner
	port                              shellport.CoreRestartPort
	now                               func() time.Time
	identity                          func(context.Context, string) (CoreRestartIdentity, error)
	currentIdentity                   func() (CoreRestartIdentity, error)
	stop                              func(context.Context, string, coreOwner) error
	settle                            func(context.Context, string) error
	launch                            func(context.Context, string, []string, []string, string) error
}

var _ shellport.CoreRestartHost = (*coreRestartHost)(nil)

func newCoreRestartHost(home, root, profile string, target control.Target, port shellport.CoreRestartPort) (*coreRestartHost, error) {
	if !filepath.IsAbs(home) || !filepath.IsAbs(root) || filepath.Clean(home) != home || filepath.Clean(root) != root ||
		profile == "" || strings.TrimSpace(profile) != profile || len(profile) > 4096 ||
		strings.ContainsAny(profile, "\x00\r\n\t") || target.WorkspaceID == "" ||
		target.Shell == nil || target.Shell.ClientID == "" || target.Shell.Epoch == 0 || port == nil {
		return nil, errors.New("restart requires an exact local profile and attached shell")
	}
	target.Shell = &control.ShellOwner{ClientID: target.Shell.ClientID, Epoch: target.Shell.Epoch}
	dir := restartProfileDir(home, profile)
	if err := restartPrivateDirs(home, dir); err != nil {
		return nil, err
	}
	owner, err := readCoreOwner(home)
	if err != nil {
		return nil, errors.New("restart host cannot verify its attached local core owner")
	}
	return &coreRestartHost{home: home, root: root, profile: profile, dir: dir, target: target, owner: owner, port: port,
		now: time.Now, identity: inspectRestartCandidate, currentIdentity: CoreRestartIdentityForCurrent,
		stop:   handoverOwnedCore,
		settle: settleRestartLifetime, launch: runRestartSuccessor}, nil
}

func newCoreRestartHostFromManifest(home, root, profile string, target control.Target, port shellport.CoreRestartPort, path string) (*coreRestartHost, error) {
	h, err := newCoreRestartHost(home, root, profile, target, port)
	if err != nil {
		return nil, err
	}
	m, _, _, err := readRestartManifest(home, path)
	if err != nil {
		return nil, err
	}
	if m.Root != root || m.Profile != profile || m.Target.WorkspaceID != target.WorkspaceID ||
		m.Target.Shell.ClientID != target.Shell.ClientID || (target.SessionID != "" && m.Target.SessionID != target.SessionID) {
		return nil, errors.New("restart successor is not the original profile attachment")
	}
	h.startup = path
	return h, nil
}

func (h *coreRestartHost) StartupOperationID() string {
	if h.startup == "" {
		return ""
	}
	m, _, _, err := readRestartManifest(h.home, h.startup)
	if err != nil {
		return ""
	}
	return m.ControlID
}

func (h *coreRestartHost) StartupSessionID() string {
	if h.startup == "" {
		return ""
	}
	m, _, _, err := readRestartManifest(h.home, h.startup)
	if err != nil {
		return ""
	}
	return m.Target.SessionID
}

func restartHash(s string) string { return fmt.Sprintf("%x", sha256.Sum256([]byte(s))) }
func restartProfileDir(home, profile string) string {
	return filepath.Join(DataDir(home), "core-restart", restartHash(profile))
}
func (h *coreRestartHost) manifestPath(id string) string {
	return filepath.Join(h.dir, restartHash(id), "manifest.json")
}

// withLock coordinates all shell instances of this exact private profile.
func (h *coreRestartHost) withLock(ctx context.Context, fn func() error) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := restartPrivateDirs(h.home, h.dir); err != nil {
		return err
	}
	path := filepath.Join(h.dir, "host.lock")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err == nil {
		err = errors.Join(f.Sync(), f.Close())
		if err == nil {
			err = secureOwnerPath(path, false)
		}
	} else if errors.Is(err, os.ErrExist) {
		err = nil
	}
	if err != nil {
		return err
	}
	if err := validateOwnerPath(path, false); err != nil {
		return err
	}
	before, err := os.Lstat(path)
	if err != nil {
		return err
	}
	lock := flock.New(path)
	ok, err := lock.TryLock()
	if err != nil {
		_ = lock.Close()
		return err
	}
	if !ok {
		_ = lock.Close()
		return errors.New("original profile restart operation is busy")
	}
	defer func() { _ = lock.Close() }()
	after, err := os.Lstat(path)
	if err != nil || !os.SameFile(before, after) {
		return errors.New("restart lock changed")
	}
	return fn()
}

func (h *coreRestartHost) Schedule(ctx context.Context, req control.Request) (state handover.State, err error) {
	defer func() {
		if err != nil {
			state = handover.State{}
		}
	}()
	err = h.withLock(ctx, func() error {
		var input shellport.CoreRestartInput
		// The attached shell identity has no selected execution. Preserve the
		// validated native request's selection in the immutable manifest; the
		// pending-control/session owner rechecks that actual source selection.
		if req.CommandID != "core.restart" || req.ID == "" || len(req.ID) > 4096 ||
			req.Target.SessionID == "" || req.Target.WorkspaceID != h.target.WorkspaceID ||
			req.Target.Shell == nil || *req.Target.Shell != *h.target.Shell ||
			(h.target.SessionID != "" && req.Target.SessionID != h.target.SessionID) ||
			(h.target.ExecutionID != "" && req.Target.ExecutionID != h.target.ExecutionID) ||
			req.Target.ResourceID != h.target.ResourceID ||
			req.Caller.Kind != control.AgentCaller || req.Caller.AgentID == "" || req.Caller.RunID == "" {
			return errors.New("restart request does not match the authorized original shell")
		}
		if err := restartDecode(req.Input, &input, 8<<10); err != nil {
			return errors.New("invalid restart input")
		}
		if !filepath.IsAbs(input.Executable) || filepath.Clean(input.Executable) != input.Executable ||
			!digestToken(input.SHA256) || strings.ToLower(input.SHA256) != input.SHA256 ||
			input.GoalID == "" || len(input.GoalID) > 4096 || input.DelaySeconds < 0 || input.DelaySeconds > 300 {
			return errors.New("restart requires an exact executable digest, goal, and delay of 0..300 seconds")
		}
		path := h.manifestPath(req.ID)
		if _, statErr := os.Lstat(filepath.Dir(path)); statErr == nil {
			m, receipt, digest, readErr := readRestartManifest(h.home, path)
			if readErr != nil {
				return readErr
			}
			if m.Input != input || m.Caller != req.Caller || !restartSameTarget(m.Target, req.Target) {
				return errors.New("restart request ID already binds different arguments")
			}
			state, readErr = h.inspect(ctx, path, m, receipt, digest)
			return readErr
		} else if !errors.Is(statErr, os.ErrNotExist) {
			return statErr
		}
		if err := h.checkNoPending(); err != nil {
			return err
		}
		old, err := readCoreOwner(h.home)
		if err != nil || old != h.owner {
			return errors.New("restart cannot verify original attached core owner")
		}
		before, err := os.Lstat(input.Executable)
		if err != nil {
			return errors.New("restart candidate unavailable")
		}
		actual, size, err := restartImageDigest(input.Executable)
		if err != nil || actual != input.SHA256 {
			return errors.New("restart candidate digest or file validation failed")
		}
		image, err := stageCoreImage(h.home, input.Executable, input.SHA256[:16])
		if err != nil {
			return errors.New("restart candidate staging failed")
		}
		if err := validateCoreImage(image, size); err != nil {
			return errors.New("restart staged image size or mode mismatch")
		}
		if err := verifyCoreImage(image, actual); err != nil {
			return errors.New("restart staged image digest mismatch")
		}
		identity, err := h.identity(ctx, image)
		if err != nil || !restartValidIdentity(identity, actual) {
			return errors.New("restart candidate self-identity failed")
		}
		after, afterSize, err := restartImageDigest(input.Executable)
		if err != nil || after != actual || afterSize != size {
			return errors.New("restart candidate changed during verification")
		}
		afterInfo, err := os.Lstat(input.Executable)
		if err != nil || !os.SameFile(before, afterInfo) {
			return errors.New("restart candidate inode changed during verification")
		}
		if err := verifyCoreImage(image, actual); err != nil {
			return errors.New("restart staged image changed during verification")
		}
		current, err := readCoreOwner(h.home)
		if err != nil || current != old {
			return errors.New("original core owner changed while scheduling")
		}
		var token [32]byte
		if _, err := rand.Read(token[:]); err != nil {
			return errors.New("restart nonce generation failed")
		}
		now := h.now().UTC()
		m := coreRestartManifest{Version: 1, Home: h.home, Root: h.root, Profile: h.profile,
			ControlID: req.ID, Target: req.Target, Input: input, Caller: req.Caller,
			OldOwner: old, Image: image, Identity: identity, Token: hex.EncodeToString(token[:]),
			NotBefore: now.Add(time.Duration(input.DelaySeconds) * time.Second), ExpiresAt: now.Add(10 * time.Minute)}
		if err := os.Mkdir(filepath.Dir(path), 0o700); err != nil {
			return errors.New("restart intent already exists or cannot be created")
		}
		if err := secureOwnerPath(filepath.Dir(path), true); err != nil {
			return err
		}
		if err := syncRestartDirectory(h.dir); err != nil {
			return err
		}
		data, err := json.Marshal(m)
		if err != nil {
			return err
		}
		if err := publishRestartFile(path, data); err != nil {
			return err
		}
		digest := restartHash(string(data))
		receipt := coreRestartReceipt{ManifestSHA256: digest, Event: "reserve_requested"}
		if err := writeRestartReceipt(path, &receipt); err != nil {
			return err
		}
		reservation := handover.Reservation{OperationID: req.ID, GoalID: input.GoalID,
			TokenSHA256: restartHash(m.Token), SuccessorCohort: identity.Cohort, SuccessorSchema: identity.Schema,
			ExpiresAt: m.ExpiresAt, SourceAgentID: req.Caller.AgentID, SourceRunID: req.Caller.RunID,
			SourceCoreOwner: old.Generation, SourceShell: *req.Target.Shell}
		state, err = h.port.ReserveCoreRestart(ctx, req.ID, req.Target.SessionID, reservation)
		if err != nil {
			return h.recordFailure(path, &receipt, "reservation outcome uncertain; inspect original operation")
		}
		if err := restartValidateState(m, state); err != nil {
			return err
		}
		state.Reason = ""
		receipt.State, receipt.Event = state, "reserve_observed"
		return writeRestartReceipt(path, &receipt)
	})
	return state, err
}

func restartSameTarget(a, b control.Target) bool {
	return a.WorkspaceID == b.WorkspaceID && a.SessionID == b.SessionID && a.ExecutionID == b.ExecutionID &&
		a.ResourceID == b.ResourceID && a.Shell != nil && b.Shell != nil && *a.Shell == *b.Shell
}

func (h *coreRestartHost) checkNoPending() error {
	entries, err := os.ReadDir(h.dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Name() == "host.lock" {
			continue
		}
		if !entry.IsDir() || !digestToken(entry.Name()) {
			return errors.New("unrecognized restart intent; manual inspection required")
		}
		_, r, _, err := readRestartManifest(h.home, filepath.Join(h.dir, entry.Name(), "manifest.json"))
		if err != nil {
			return errors.New("incomplete restart intent retained; inspect original operation")
		}
		if !restartTerminal(r.State.Phase) {
			return errors.New("another restart intent is pending in this original profile")
		}
	}
	return nil
}

func restartTerminal(phase handover.Phase) bool {
	return phase == handover.Consumed || phase == handover.Cancelled || phase == handover.Invalidated || phase == handover.Expired
}

func (h *coreRestartHost) recordFailure(path string, r *coreRestartReceipt, message string) error {
	r.Error = message // Only fixed, nonsecret categories; never subprocess/port errors.
	return errors.Join(errors.New(message), writeRestartReceipt(path, r))
}

func (h *coreRestartHost) inspect(ctx context.Context, path string, m coreRestartManifest, r coreRestartReceipt, _ string) (handover.State, error) {
	state, err := h.port.InspectCoreRestart(ctx, m.Target.SessionID, m.ControlID)
	if err != nil {
		return r.State, errors.New("restart observation unavailable; original intent retained")
	}
	if err := restartValidateState(m, state); err != nil {
		return r.State, err
	}
	state.Reason = ""
	if state == r.State {
		// Readiness polling observes the same immutable evidence repeatedly.
		// Journal a transition, not every 250 ms presentation sample.
		return state, nil
	}
	r.State, r.Event = state, "state_observed"
	return state, writeRestartReceipt(path, &r)
}

func (h *coreRestartHost) Inspect(ctx context.Context, operationID string) (state handover.State, err error) {
	err = h.withLock(ctx, func() error {
		path := h.manifestPath(operationID)
		m, r, digest, err := readRestartManifest(h.home, path)
		if err != nil {
			return err
		}
		state, err = h.inspect(ctx, path, m, r, digest)
		return err
	})
	return state, err
}

func (h *coreRestartHost) Cancel(ctx context.Context, operationID string) (state handover.State, err error) {
	defer func() {
		if err != nil {
			state = handover.State{}
		}
	}()
	err = h.withLock(ctx, func() error {
		path := h.manifestPath(operationID)
		m, r, digest, err := readRestartManifest(h.home, path)
		if err != nil {
			return err
		}
		if r.StopRequested {
			return errors.New("restart stop outcome already committed or uncertain; inspect original operation")
		}
		if r.CancelRequested || restartTerminal(r.State.Phase) {
			state, err = h.inspect(ctx, path, m, r, digest)
			return err
		}
		r.CancelRequested, r.Event = true, "cancel_requested"
		if err := writeRestartReceipt(path, &r); err != nil {
			return err
		}
		state, err = h.port.CancelCoreRestart(ctx, m.Target.SessionID, m.ControlID, m.Token)
		if err != nil {
			return h.recordFailure(path, &r, "cancellation outcome uncertain; inspect original operation")
		}
		if err := restartValidateState(m, state); err != nil {
			return err
		}
		state.Reason = ""
		r.State, r.Event = state, "cancel_observed"
		return writeRestartReceipt(path, &r)
	})
	return state, err
}

func (h *coreRestartHost) validateReady(m coreRestartManifest, r coreRestartReceipt) error {
	if m.Home != h.home || m.Root != h.root || m.Profile != h.profile ||
		m.Target.WorkspaceID != h.target.WorkspaceID || m.Target.Shell == nil || *m.Target.Shell != *h.target.Shell {
		return errors.New("restart readiness belongs to a different original attachment")
	}
	if h.now().Before(m.NotBefore) {
		return shellport.ErrCoreRestartPending
	}
	if !h.now().Before(m.ExpiresAt) {
		return errors.New("restart authorization expired")
	}
	if r.StopRequested || r.CancelRequested || restartTerminal(r.State.Phase) {
		return errors.New("restart is no longer eligible for handover")
	}
	current, err := readCoreOwner(h.home)
	if err != nil || current != m.OldOwner {
		return errors.New("original core owner changed before handover")
	}
	if err := validateOwnerPath(CoreImageDir(h.home), true); err != nil {
		return err
	}
	if err := validateOwnerPath(m.Image, false); err != nil {
		return err
	}
	if err := verifyCoreImage(m.Image, m.Identity.SHA256); err != nil {
		return errors.New("restart staged image changed")
	}
	return nil
}

func (h *coreRestartHost) Ready(ctx context.Context, operationID string) error {
	return h.withLock(ctx, func() error {
		path := h.manifestPath(operationID)
		m, r, _, err := readRestartManifest(h.home, path)
		if err != nil {
			return err
		}
		if h.now().Before(m.NotBefore) {
			return shellport.ErrCoreRestartPending
		}
		if !h.now().Before(m.ExpiresAt) {
			return errors.New("restart authorization expired")
		}
		if r.StopRequested || r.CancelRequested || restartTerminal(r.State.Phase) {
			return errors.New("restart is no longer eligible for handover")
		}
		state, err := h.port.InspectCoreRestart(ctx, m.Target.SessionID, m.ControlID)
		if err != nil {
			return errors.New("restart readiness observation unavailable")
		}
		if err := restartValidateState(m, state); err != nil {
			return err
		}
		if state.Phase == handover.Reserved {
			return shellport.ErrCoreRestartPending
		}
		if state.Phase != handover.Ready {
			return errors.New("original session is no longer eligible for restart")
		}
		if err := h.validateReady(m, r); err != nil {
			return err
		}
		state.Reason = ""
		r.State, r.Ready, r.Event = state, true, "ready_checked"
		return writeRestartReceipt(path, &r)
	})
}

// RunReady is called by the original foreground application ONLY after Wait,
// draft flush, ws.Close, all client/manager closes, and profile.Close succeeded.
// It never uses the now-closed port. A persisted stop/launch request is not replayed.
func (h *coreRestartHost) RunReady(ctx context.Context, operationID string) error {
	var m coreRestartManifest
	err := h.withLock(ctx, func() error {
		path := h.manifestPath(operationID)
		var r coreRestartReceipt
		var err error
		m, r, _, err = readRestartManifest(h.home, path)
		if err != nil {
			return err
		}
		if !r.Ready || r.State.Phase != handover.Ready {
			return errors.New("restart has no verified ready checkpoint")
		}
		if err := h.validateReady(m, r); err != nil {
			return err
		}
		r.StopRequested, r.Event = true, "stop_requested"
		if err := writeRestartReceipt(path, &r); err != nil {
			return err
		}
		if err := h.stop(ctx, h.home, m.OldOwner); err != nil {
			return h.recordFailure(path, &r, "graceful handover refused or uncertain; no successor launched")
		}
		if err := h.settle(ctx, h.home); err != nil {
			return h.recordFailure(path, &r, "core lifetime settlement uncertain; no successor launched")
		}
		r.StopSettled, r.Event = true, "stop_settled"
		if err := writeRestartReceipt(path, &r); err != nil {
			return err
		}
		if !h.now().Before(m.ExpiresAt) {
			return h.recordFailure(path, &r, "restart authorization expired after core settlement")
		}
		if err := verifyCoreImage(m.Image, m.Identity.SHA256); err != nil {
			return h.recordFailure(path, &r, "successor image changed after core settlement")
		}
		r.LaunchRequested, r.Event = true, "successor_launch_requested"
		return writeRestartReceipt(path, &r)
	})
	if err != nil {
		return err
	}
	// Release the profile host lock before launching: the exact successor must
	// acquire it to consume this manifest. This is foreground, inherited terminal
	// I/O, and a joined Wait, not an escaped agent/background command.
	launchErr := h.launch(ctx, m.Image, []string{"--core-handover", h.manifestPath(operationID)},
		coreProfileEnv(h.home, ""), h.root)
	// Exit zero is not evidence of activation: an invalid or prematurely closed
	// candidate may never have consumed its exact reservation. Observe only local
	// committed evidence; the original port has already been closed.
	return h.withLock(context.WithoutCancel(ctx), func() error {
		path := h.manifestPath(operationID)
		_, r, _, err := readRestartManifest(h.home, path)
		if err != nil {
			return err
		}
		r.Event = "successor_exited"
		if launchErr != nil {
			return h.recordFailure(path, &r, "successor exited unsuccessfully; inspect original restart operation")
		}
		if !r.ContinueRequested || r.State.Phase != handover.Consumed {
			return h.recordFailure(path, &r, "successor exited without verified continuation; inspect original restart operation")
		}
		r.Error = ""
		return writeRestartReceipt(path, &r)
	})
}

func (h *coreRestartHost) Continue(ctx context.Context) (state handover.State, err error) {
	defer func() {
		if err != nil {
			state = handover.State{}
		}
	}()
	err = h.withLock(ctx, func() error {
		if h.startup == "" {
			return errors.New("no exact startup restart manifest")
		}
		m, r, digest, err := readRestartManifest(h.home, h.startup)
		if err != nil {
			return err
		}
		if !r.StopSettled || !r.LaunchRequested {
			return errors.New("restart successor launch was not committed")
		}
		current, err := h.currentIdentity()
		if err != nil || current != m.Identity {
			return errors.New("running successor is not the authorized exact image")
		}
		owner, err := readCoreOwner(h.home)
		if err != nil || owner != h.owner || owner.Generation == m.OldOwner.Generation || owner.Digest != m.Identity.SHA256 ||
			owner.Cohort != m.Identity.Cohort || owner.Schema != m.Identity.Schema {
			return errors.New("successor core is not the authorized new image owner")
		}
		if err := verifyCoreImage(owner.Executable, m.Identity.SHA256); err != nil {
			return errors.New("successor core owner image failed verification")
		}
		if r.ContinueRequested || restartTerminal(r.State.Phase) {
			state, err = h.inspect(ctx, h.startup, m, r, digest)
			return err
		}
		if !h.now().Before(m.ExpiresAt) || h.now().Before(m.NotBefore) {
			return errors.New("restart successor authorization is outside its time window")
		}
		r.ContinueRequested, r.Event = true, "continue_requested"
		if err := writeRestartReceipt(h.startup, &r); err != nil {
			return err
		}
		state, err = h.port.ContinueCoreRestart(ctx, m.Target.SessionID, m.ControlID, m.Token)
		if err != nil {
			return h.recordFailure(h.startup, &r, "continuation outcome uncertain; inspect original operation")
		}
		if err := restartValidateState(m, state); err != nil {
			return err
		}
		state.Reason = ""
		r.State, r.Event = state, "continue_observed"
		return writeRestartReceipt(h.startup, &r)
	})
	return state, err
}

func restartValidateState(m coreRestartManifest, s handover.State) error {
	if s.OperationID != m.ControlID || s.GoalID != m.Input.GoalID || s.SuccessorCohort != m.Identity.Cohort ||
		s.SuccessorSchema != m.Identity.Schema || !s.ExpiresAt.Equal(m.ExpiresAt) ||
		s.SourceAgentID != m.Caller.AgentID || s.SourceRunID != m.Caller.RunID ||
		s.SourceCoreOwner != m.OldOwner.Generation || s.SourceShell != *m.Target.Shell ||
		(s.Phase != handover.Reserved && s.Phase != handover.Ready && !restartTerminal(s.Phase)) {
		return errors.New("restart session returned mismatched operation evidence")
	}
	return nil
}

func restartValidIdentity(i CoreRestartIdentity, digest string) bool {
	return i.SHA256 == digest && digestToken(i.SHA256) && i.BuildID == digest[:16] &&
		digestToken(i.Cohort) && i.Schema != "" && len(i.Schema) <= 256 &&
		i.GOOS == runtime.GOOS && i.GOARCH == runtime.GOARCH
}

func restartImageDigest(path string) (string, int64, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return "", 0, err
	}
	if !before.Mode().IsRegular() || before.Size() <= 0 || before.Size() > 1<<30 ||
		(runtime.GOOS != "windows" && before.Mode().Perm()&0o100 == 0) {
		return "", 0, errors.New("candidate is not a bounded regular executable")
	}
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = f.Close() }()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(before, opened) {
		return "", 0, errors.New("candidate changed while opening")
	}
	digest, err := coreImageDigest(f)
	if err != nil {
		return "", 0, err
	}
	after, err := os.Lstat(path)
	if err != nil || !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return "", 0, errors.New("candidate changed while hashing")
	}
	return digest, before.Size(), nil
}

func inspectRestartCandidate(ctx context.Context, path string) (CoreRestartIdentity, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "--core-handover-identity")
	// Identity has no need for accounts, home overrides, source-tree access or
	// other inherited process configuration. Authorization is for this exact
	// executable, not implicit credential forwarding to its metadata probe.
	cmd.Env = []string{}
	if runtime.GOOS == "windows" {
		cmd.Env = append(cmd.Env, "SystemRoot="+os.Getenv("SystemRoot"))
	}
	cmd.WaitDelay = time.Second
	out, stderr := &restartBoundedOutput{}, &restartBoundedOutput{}
	cmd.Stdout, cmd.Stderr = out, stderr
	if err := cmd.Run(); err != nil || out.overflow || stderr.overflow {
		return CoreRestartIdentity{}, errors.New("candidate identity process failed")
	}
	var identity CoreRestartIdentity
	if err := restartDecode(out.data, &identity, 8<<10); err != nil {
		return identity, errors.New("candidate identity JSON is invalid")
	}
	return identity, nil
}

type restartBoundedOutput struct {
	data     []byte
	overflow bool
}

func (b *restartBoundedOutput) Write(p []byte) (int, error) {
	n := len(p)
	if n > (8<<10)-len(b.data) {
		b.overflow = true
	}
	b.data = append(b.data, p[:min(n, (8<<10)-len(b.data))]...)
	return n, nil
}

func runRestartSuccessor(ctx context.Context, image string, args, env []string, dir string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// Context cancellation must not kill an activated successor/core. The owner
	// waits for the foreground child just as a terminal's normal parent does.
	cmd := exec.Command(image, args...)
	cmd.Env, cmd.Dir = env, dir
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}

func settleRestartLifetime(ctx context.Context, home string) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		lock, err := lockCoreLifetime(home)
		if err == nil {
			defer func() { _ = lock.Close() }()
			_, ownerErr := readCoreOwner(home)
			if !errors.Is(ownerErr, os.ErrNotExist) {
				return errors.New("core owner replaced or not settled")
			}
			return nil
		}
		if !errors.Is(err, errCoreOwned) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func restartDecode(data []byte, value any, maxBytes int) error {
	if len(data) == 0 || len(data) > maxBytes {
		return errors.New("restart JSON exceeds bound")
	}
	if err := restartUniqueJSON(json.NewDecoder(bytes.NewReader(data)), 0); err != nil {
		return err
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(value); err != nil {
		return err
	}
	var extra any
	if err := d.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("restart JSON has trailing value")
	}
	return nil
}

// encoding/json otherwise accepts duplicate object keys with last-value wins.
// The executable handshake and immutable bindings must have one interpretation.
func restartUniqueJSON(d *json.Decoder, depth int) error {
	if depth > 32 {
		return errors.New("restart JSON nesting exceeds bound")
	}
	token, err := d.Token()
	if err != nil {
		return err
	}
	switch token {
	case json.Delim('{'):
		keys := map[string]bool{}
		for d.More() {
			key, err := d.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			if !ok || keys[name] {
				return errors.New("restart JSON has duplicate or invalid keys")
			}
			keys[name] = true
			if err := restartUniqueJSON(d, depth+1); err != nil {
				return err
			}
		}
		_, err = d.Token()
		return err
	case json.Delim('['):
		for d.More() {
			if err := restartUniqueJSON(d, depth+1); err != nil {
				return err
			}
		}
		_, err = d.Token()
		return err
	}
	return nil
}

// Validate each private directory component, not just the final path. Existing
// paths are never chmod-repaired and symlinks are never accepted.
func restartPrivateDirs(home, path string) error {
	base := DataDir(home)
	rel, err := filepath.Rel(base, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return errors.New("restart path escapes profile")
	}
	parts := []string{base}
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		if part != "." {
			parts = append(parts, filepath.Join(parts[len(parts)-1], part))
		}
	}
	for _, dir := range parts {
		if err := os.Mkdir(dir, 0o700); err == nil {
			if err := secureOwnerPath(dir, true); err != nil {
				return err
			}
			if err := syncRestartDirectory(filepath.Dir(dir)); err != nil {
				return err
			}
		} else if !errors.Is(err, os.ErrExist) {
			return err
		}
		if err := validateOwnerPath(dir, true); err != nil {
			return err
		}
	}
	return nil
}

func publishRestartFile(path string, data []byte) error {
	if len(data) > restartRecordLimit {
		return errors.New("restart record exceeds bound")
	}
	dir := filepath.Dir(path)
	if err := validateOwnerPath(dir, true); err != nil {
		return err
	}
	before, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".record-")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(f.Name()) }()
	if err := secureOwnerPath(f.Name(), false); err != nil {
		_ = f.Close()
		return err
	}
	_, writeErr := f.Write(data)
	if err := errors.Join(writeErr, f.Sync(), f.Close()); err != nil {
		return err
	}
	after, err := os.Lstat(dir)
	if err != nil || !os.SameFile(before, after) {
		return errors.New("restart directory changed before publication")
	}
	if err := os.Link(f.Name(), path); err != nil {
		return err
	}
	// File data is fsynced before publication. Unix directory fsync persists the
	// link as well; Windows directories cannot be opened with os.Open.
	return syncRestartDirectory(dir)
}

func syncRestartDirectory(dir string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	return errors.Join(d.Sync(), d.Close())
}

func readRestartFile(path string) ([]byte, error) {
	if err := validateOwnerPath(path, false); err != nil {
		return nil, err
	}
	before, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if before.Size() > restartRecordLimit {
		return nil, errors.New("restart record exceeds bound")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(before, opened) {
		return nil, errors.New("restart record changed while opening")
	}
	data, err := io.ReadAll(io.LimitReader(f, restartRecordLimit+1))
	if err != nil || len(data) > restartRecordLimit {
		return nil, errors.New("restart record read failed")
	}
	after, err := os.Lstat(path)
	if err != nil || !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return nil, errors.New("restart record changed while reading")
	}
	return data, nil
}

func readRestartManifest(home, path string) (m coreRestartManifest, r coreRestartReceipt, digest string, err error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || filepath.Base(path) != "manifest.json" {
		return m, r, "", errors.New("invalid exact restart manifest path")
	}
	base := filepath.Join(DataDir(home), "core-restart")
	rel, relErr := filepath.Rel(base, filepath.Dir(path))
	if relErr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) ||
		len(strings.Split(rel, string(filepath.Separator))) != 2 {
		return m, r, "", errors.New("restart manifest escapes private intent directory")
	}
	// Check ancestor types before opening any nonce-bearing file.
	for dir := filepath.Dir(path); ; dir = filepath.Dir(dir) {
		if err := validateOwnerPath(dir, true); err != nil {
			return m, r, "", err
		}
		if dir == DataDir(home) {
			break
		}
	}
	// Never create a missing manifest or intent while restoring.
	data, err := readRestartFile(path)
	if err != nil {
		return m, r, "", errors.New("private restart manifest unavailable")
	}
	if err := restartDecode(data, &m, restartRecordLimit); err != nil {
		return m, r, "", errors.New("invalid private restart manifest")
	}
	expected := filepath.Join(restartProfileDir(home, m.Profile), restartHash(m.ControlID), "manifest.json")
	if path != expected || m.Version != 1 || m.Home != home || m.ControlID == "" ||
		!filepath.IsAbs(m.Root) || m.Profile == "" || m.Target.SessionID == "" || m.Target.WorkspaceID == "" ||
		m.Target.Shell == nil || m.Target.Shell.ClientID == "" || m.Target.Shell.Epoch == 0 ||
		!digestToken(m.Token) || !digestToken(m.OldOwner.Generation) ||
		m.Image != filepath.Join(CoreImageDir(home), coreImagePrefix+m.Identity.BuildID+filepath.Ext(m.Input.Executable)) ||
		!restartValidIdentity(m.Identity, m.Input.SHA256) || m.Input.GoalID == "" ||
		m.Input.DelaySeconds < 0 || m.Input.DelaySeconds > 300 ||
		m.NotBefore.IsZero() || m.ExpiresAt.IsZero() || !m.ExpiresAt.After(m.NotBefore) ||
		m.ExpiresAt.Sub(m.NotBefore) != 10*time.Minute-time.Duration(m.Input.DelaySeconds)*time.Second {
		return m, r, "", errors.New("restart manifest binding is invalid")
	}
	digest = restartHash(string(data))
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		return m, r, "", err
	}
	names := []string{}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "receipt-") && strings.HasSuffix(entry.Name(), ".json") {
			names = append(names, entry.Name())
		}
	}
	if len(names) == 0 || len(names) > 4096 {
		return m, r, "", errors.New("restart receipt missing or exceeds bound")
	}
	slices.Sort(names)
	last := names[len(names)-1]
	data, err = readRestartFile(filepath.Join(filepath.Dir(path), last))
	if err != nil {
		return m, r, "", err
	}
	if err := restartDecode(data, &r, restartRecordLimit); err != nil {
		return m, r, "", errors.New("invalid restart receipt")
	}
	if r.ManifestSHA256 != digest || last != fmt.Sprintf("receipt-%06d.json", r.Sequence) || r.Sequence != len(names) {
		return m, r, "", errors.New("restart receipt binding changed")
	}
	if r.State.OperationID != "" {
		if err := restartValidateState(m, r.State); err != nil {
			return m, r, "", err
		}
	}
	return m, r, digest, nil
}

func writeRestartReceipt(path string, r *coreRestartReceipt) error {
	if r.Sequence >= 4096 {
		return errors.New("restart receipt history limit reached")
	}
	r.Sequence++
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	return publishRestartFile(filepath.Join(filepath.Dir(path), fmt.Sprintf("receipt-%06d.json", r.Sequence)), data)
}
