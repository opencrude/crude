package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"crude/coreclient"
	"crude/corewire"
	"crude/shellport/remote"
)

func validateCoreRestartInvocation(flags CLIFlags) error {
	if flags.CoreHandover == "" && !flags.CoreHandoverIdentity {
		return nil
	}
	if flags.CoreHandover != "" && flags.CoreHandoverIdentity {
		return errors.New("handover launch and identity inspection are separate routes")
	}
	if flags.Core || flags.CoreStdio || flags.CoreStatus || flags.CoreStop || flags.Server || flags.Force ||
		flags.MCP || flags.ListSessions || flags.DeleteSession != "" || flags.Resume != "" ||
		flags.ClientProfile != "" || flags.Workspace != "" || flags.SSHHost != "" || flags.SSHUser != "" ||
		flags.SSHPort != 0 || flags.SSHIdentity != "" || flags.APIKey != "" || flags.Account != "" ||
		flags.Model != "" || flags.Provider != "" || flags.SystemPrompt != "" ||
		flags.MCPEffects != "" || flags.MCPShell != "" || flags.MCPShellEpoch != 0 {
		return errors.New("core handover routes cannot select credentials, sessions, profiles or other lifecycle operations")
	}
	if flags.CoreHandover != "" && (!filepath.IsAbs(flags.CoreHandover) || filepath.Clean(flags.CoreHandover) != flags.CoreHandover) {
		return errors.New("--core-handover requires the exact absolute private manifest path")
	}
	return nil
}

type coreRestartDialer struct {
	mu             sync.Mutex
	home, manifest string
	owner          coreOwner
	startRequested bool
}

func newCoreRestartDialer(home, manifest string) remote.Dialer {
	d := &coreRestartDialer{home: home, manifest: manifest}
	return d.connect
}

// The private dialer is deliberately separate from ordinary startup. A
// successor may start the selected image after proven absence, but it may never
// auto-handover an unrelated/replacement owner, even for a protocol mismatch.
// One owner pin survives navigation, workspace attach and later carrier redials.
func (d *coreRestartDialer) connect(ctx context.Context, clientID string) (*coreclient.Client, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	home := d.home
	manifest, receipt, _, err := readRestartManifest(home, d.manifest)
	if err != nil {
		return nil, err
	}
	identity, err := CoreRestartIdentityForCurrent()
	if err != nil || identity != manifest.Identity || !receipt.StopSettled || !receipt.LaunchRequested {
		return nil, errors.New("handover startup does not match the verified admitted successor")
	}
	if now := time.Now(); now.Before(manifest.NotBefore) || !now.Before(manifest.ExpiresAt) {
		return nil, errors.New("handover startup is outside its admitted time window")
	}
	check := func() (coreOwner, error) {
		owner, err := readCoreOwner(home)
		if err != nil {
			return coreOwner{}, err
		}
		if owner.Generation == manifest.OldOwner.Generation || owner.Digest != manifest.Identity.SHA256 ||
			owner.Cohort != manifest.Identity.Cohort || owner.Schema != manifest.Identity.Schema {
			return coreOwner{}, errors.New("handover core owner was replaced or does not match the intended image")
		}
		if d.owner.Generation != "" && owner != d.owner {
			return coreOwner{}, errors.New("the admitted handover successor was replaced")
		}
		return owner, nil
	}
	if _, err := check(); errors.Is(err, os.ErrNotExist) {
		if d.owner.Generation != "" || d.startRequested {
			return nil, errors.New("the handover successor is absent; its launch is never replayed")
		}
		start, cancel := context.WithTimeout(ctx, 15*time.Second)
		absent := prepareCoreStart(start, home)
		cancel()
		if absent == nil {
			d.startRequested = true
			if err := spawnCore(home); err != nil {
				return nil, fmt.Errorf("start exact handover core: %w", err)
			}
		} else if !errors.Is(absent, errCoreOwned) {
			return nil, fmt.Errorf("confirm handover absence: %w", absent)
		}
	} else if err != nil {
		return nil, err
	}
	wait, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	for {
		expected, err := check()
		if err == nil {
			// Persist before dialing. A failed/uncertain carrier negotiation
			// cannot silently admit a different generation on the next call.
			d.owner = expected
			// Retain this exact successor generation across carrier reconnects.
			// A same-cohort, different image must not be accepted by negotiation.
			carrier := func(ctx context.Context) (corewire.Conn, error) {
				before, err := readCoreOwner(home)
				if err != nil || before != expected {
					return nil, errors.New("handover successor ownership changed before connection")
				}
				conn, err := localCarrier(DataDir(home))(ctx)
				if err != nil {
					return nil, err
				}
				after, err := readCoreOwner(home)
				if err != nil || after != before {
					_ = conn.Close()
					return nil, errors.New("handover successor ownership changed during connection")
				}
				return conn, nil
			}
			client, dialErr := coreclient.Dial(wait, carrier, clientID)
			if dialErr == nil {
				return client, nil
			}
			if !errors.Is(dialErr, coreclient.ErrNoCarrier) {
				return nil, dialErr
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		select {
		case <-wait.Done():
			return nil, fmt.Errorf("intended handover core did not become ready: %w", wait.Err())
		case <-time.After(25 * time.Millisecond):
		}
	}
}
