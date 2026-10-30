package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"crude/buildinfo"
	"crude/coreprotocol"
	"crude/corewire"
	"crude/retry"

	"github.com/gofrs/flock"
)

const expectedCoreOwnerEnv = "CRUDE_EXPECT_CORE_OWNER"

// Only the owned helper's decoded Accepted=false branch emits this status.
// Go runtime panics exit 2; that status must remain an uncertain one-attempt
// outcome, not evidence that a lifecycle request had no effect.
const coreHandoverRefusedExit = 75

// coreOwner is the current local endpoint owner's external installation record,
// not a protocol decoder. Only the exact owning executable speaks to its core.
type coreOwner struct {
	Generation string `json:"generation"`
	Executable string `json:"executable"`
	Digest     string `json:"digest"`
	Cohort     string `json:"cohort"`
	Schema     string `json:"schema"`
}

func coreOwnerPath(home string) string {
	return filepath.Join(DataDir(home), "corewire", "owner.json")
}

// The lifetime lock outlives all listeners and runtime data owners. Unlike an
// endpoint probe it proves crash-left ownership without confusing shutdown's
// listener-close/data-flush interval with absence.
func lockCoreLifetime(home string) (*flock.Flock, error) {
	dir := filepath.Dir(coreOwnerPath(home))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	if err := secureOwnerPath(dir, true); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "lifetime.lock")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err == nil {
		_ = f.Close()
		if err := secureOwnerPath(path, false); err != nil {
			return nil, err
		}
	} else if !errors.Is(err, os.ErrExist) {
		return nil, err
	}
	if err := validateOwnerPath(path, false); err != nil {
		return nil, err
	}
	lock := flock.New(path)
	locked, err := lock.TryLock()
	if err != nil || !locked {
		_ = lock.Close()
		if err == nil {
			err = errCoreOwned
		}
		return nil, err
	}
	return lock, nil
}

// Only call while holding the lifetime lock and after claiming the listener (or
// proving local carrier absence). No owner from this contract can still write.
func clearSettledCoreOwner(home string) error {
	owner, err := readCoreOwner(home)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return removeCoreOwner(home, owner.Generation)
}

func prepareCoreStart(ctx context.Context, home string) error {
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		lock, err := lockCoreLifetime(home)
		if err == nil {
			defer func() { _ = lock.Close() }()
			conn, dialErr := corewire.DialLocal(ctx, DataDir(home))
			if dialErr == nil {
				_ = conn.Close()
				return errCoreOwned
			}
			if !coreAbsent(dialErr) {
				return dialErr
			}
			return clearSettledCoreOwner(home)
		}
		if !errors.Is(err, errCoreOwned) {
			return err
		}
		// A concurrently starting owner may already be ready. Return to strict
		// admission rather than waiting for that owner's entire lifetime.
		if conn, dialErr := corewire.DialLocal(ctx, DataDir(home)); dialErr == nil {
			_ = conn.Close()
			return errCoreOwned
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// CoreHandoverError never implies that the owner is absent or may be killed.
type CoreHandoverError struct{ Cause error }

func (e *CoreHandoverError) Error() string {
	return fmt.Sprintf("cannot safely hand over the existing core: %v; finish or close the named work and retry --core-stop, or select a separate profile with %s", e.Cause, buildinfo.HomeEnv)
}
func (e *CoreHandoverError) Unwrap() error { return e.Cause }

func installCoreOwner(home string) (coreOwner, error) {
	var owner coreOwner
	exe, err := os.Executable()
	if err != nil {
		return owner, err
	}
	// Direct --core invocations also retain an immutable helper, independent of
	// the original installation being rebuilt or removed.
	image, err := stageCoreImage(home, exe, BuildID())
	if err != nil {
		return owner, err
	}
	f, err := os.Open(image)
	if err != nil {
		return owner, err
	}
	digest, err := coreImageDigest(f)
	_ = f.Close()
	if err != nil {
		return owner, err
	}
	if !strings.HasPrefix(digest, BuildID()) {
		return owner, errors.New("running executable changed before owner image publication")
	}
	var nonce [32]byte
	if _, err = rand.Read(nonce[:]); err != nil {
		return owner, err
	}
	owner = coreOwner{Generation: hex.EncodeToString(nonce[:]), Executable: image, Digest: digest, Cohort: buildinfo.ProtocolBuild, Schema: coreprotocol.Schema}
	path := coreOwnerPath(home)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return owner, err
	}
	if err := secureOwnerPath(filepath.Dir(path), true); err != nil {
		return owner, err
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		return owner, fmt.Errorf("previous endpoint owner has not settled: %s", path)
	}
	if err := publishCoreOwner(path, owner); err != nil {
		return owner, err
	}
	return owner, nil
}

func publishCoreOwner(path string, owner coreOwner) error {
	data, err := json.Marshal(owner)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".owner-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(f.Name()) }()
	_, writeErr := f.Write(data)
	err = errors.Join(writeErr, f.Sync(), f.Close())
	if err != nil {
		return err
	}
	if err := secureOwnerPath(f.Name(), false); err != nil {
		return err
	}
	// No replacement: a prior descriptor is never silently displaced.
	return os.Link(f.Name(), path)
}

func readCoreOwner(home string) (coreOwner, error) {
	var owner coreOwner
	path := coreOwnerPath(home)
	if err := validateOwnerPath(filepath.Dir(path), true); err != nil {
		return owner, err
	}
	if err := validateOwnerPath(path, false); err != nil {
		return owner, err
	}
	before, err := os.Lstat(path)
	if err != nil {
		return owner, err
	}
	f, err := os.Open(path)
	if err != nil {
		return owner, err
	}
	defer func() { _ = f.Close() }()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(before, opened) {
		return owner, errors.New("endpoint owner changed while reading")
	}
	decoder := json.NewDecoder(io.LimitReader(f, 16<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&owner); err != nil {
		return owner, err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return owner, errors.New("invalid endpoint owner record")
	}
	if !digestToken(owner.Generation) || !digestToken(owner.Digest) || owner.Cohort == "" || owner.Schema == "" || !filepath.IsAbs(owner.Executable) {
		return owner, errors.New("incomplete endpoint owner record")
	}
	if filepath.Dir(owner.Executable) != CoreImageDir(home) || !strings.HasPrefix(filepath.Base(owner.Executable), coreImagePrefix) {
		return owner, errors.New("endpoint owner executable is not a retained core image")
	}
	return owner, nil
}

func digestToken(s string) bool {
	b, err := hex.DecodeString(s)
	return err == nil && len(b) == 32
}

func removeCoreOwner(home, generation string) error {
	owner, err := readCoreOwner(home)
	if err != nil {
		return err
	}
	if owner.Generation != generation {
		return errors.New("endpoint owner changed before release")
	}
	return os.Remove(coreOwnerPath(home))
}

func coreProfileEnv(home, generation string) []string {
	env := make([]string, 0, len(os.Environ())+2)
	for _, value := range os.Environ() {
		key, _, _ := strings.Cut(value, "=")
		if strings.EqualFold(key, buildinfo.HomeEnv) || strings.EqualFold(key, expectedCoreOwnerEnv) {
			continue
		}
		env = append(env, value)
	}
	return append(env, buildinfo.HomeEnv+"="+home, expectedCoreOwnerEnv+"="+generation)
}

func handoverCore(ctx context.Context, home string) error {
	owner, err := readCoreOwner(home)
	if err != nil {
		return &CoreHandoverError{err}
	}
	return stopOwnedCore(ctx, home, owner)
}

// Scheduled operations retain a specific predecessor. Its generation and image
// are rechecked before selecting the helper, so a replacement is never stopped.
func handoverOwnedCore(ctx context.Context, home string, owner coreOwner) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	// Closing a shell can leave an exact, already-fenced resource release in its
	// joined lane. Retry only the helper's explicit refused/no-effect exit, never
	// an unknown reply or accepted shutdown. Each attempt rechecks the same owner.
	for attempt := 1; ; attempt++ {
		err := stopOwnedCore(ctx, home, owner)
		var refused *coreHandoverRefusal
		if !errors.As(err, &refused) || attempt >= 8 {
			return err
		}
		if err := retry.Wait(ctx, retry.Exponential(attempt, 25*time.Millisecond, 250*time.Millisecond)); err != nil {
			return &CoreHandoverError{err}
		}
	}
}

type coreHandoverRefusal struct{ reason error }

func (e *coreHandoverRefusal) Error() string { return e.reason.Error() }
func (e *coreHandoverRefusal) Unwrap() error { return e.reason }

func stopOwnedCore(ctx context.Context, home string, owner coreOwner) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	current, err := readCoreOwner(home)
	if err != nil {
		return &CoreHandoverError{err}
	}
	if current != owner {
		return &CoreHandoverError{errors.New("endpoint owner changed before the scheduled handover")}
	}
	if err := validateOwnerPath(CoreImageDir(home), true); err != nil {
		return &CoreHandoverError{err}
	}
	if err := validateOwnerPath(owner.Executable, false); err != nil {
		return &CoreHandoverError{err}
	}
	if err := verifyCoreImage(owner.Executable, owner.Digest); err != nil {
		return &CoreHandoverError{err}
	}
	if err := ctx.Err(); err != nil {
		return &CoreHandoverError{err}
	}
	// Only this retained helper is stopped on context cancellation, never the
	// core process. No force, shell parsing, PID lookup, or foreign wire reader.
	cmd := exec.CommandContext(ctx, owner.Executable, "--core-stop")
	cmd.Env = coreProfileEnv(home, owner.Generation)
	cmd.Dir = home
	cmd.WaitDelay = time.Second
	output := &coreHelperOutput{}
	cmd.Stdout, cmd.Stderr = output, output
	if err := cmd.Run(); err != nil {
		cause := fmt.Errorf("matching owner helper failed: %w: %s", errors.Join(err, ctx.Err()), strings.TrimSpace(string(output.data)))
		var exit *exec.ExitError
		if ctx.Err() == nil && errors.As(err, &exit) && exit.ExitCode() == coreHandoverRefusedExit {
			return &CoreHandoverError{&coreHandoverRefusal{cause}}
		}
		return &CoreHandoverError{cause}
	}
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		current, err := readCoreOwner(home)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return &CoreHandoverError{err}
		}
		if current.Generation != owner.Generation {
			return &CoreHandoverError{errors.New("endpoint owner changed during handover")}
		}
		select {
		case <-ctx.Done():
			return &CoreHandoverError{ctx.Err()}
		case <-ticker.C:
		}
	}
}

// os/exec serializes writes when Stdout and Stderr are the same comparable
// writer. Retain bounded diagnostics even if an invalid helper is unexpectedly
// verbose; never buffer a subprocess's unbounded output.
type coreHelperOutput struct{ data []byte }

func (b *coreHelperOutput) Write(p []byte) (int, error) {
	n := len(p)
	b.data = append(b.data, p[:min(len(p), (8<<10)-len(b.data))]...)
	return n, nil
}
