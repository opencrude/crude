package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"sync"
	"time"

	"crude/buildinfo"
	"crude/corewire"
)

// RemoteInstallRequiredError is an immutable, single-use consent challenge for
// one observed SSH destination and exact executable. Error construction probes
// facts only; InstallRemote is called exclusively after explicit user approval.
// Its two methods form a presentation-neutral interface (no app import in UI).
type RemoteInstallRequiredError struct {
	options                          corewire.SSHOptions
	host                             corewire.SSHHost
	image, cohort, digest, namespace string
	cause                            error
	expires                          time.Time
	mu                               sync.Mutex
	used                             bool
}

func (e *RemoteInstallRequiredError) Error() string {
	return "Remote core needs the matching source cohort; review and confirm installation before connecting"
}
func (e *RemoteInstallRequiredError) Unwrap() error { return e.cause }

func (e *RemoteInstallRequiredError) RemoteInstallSummary() string {
	return fmt.Sprintf("Install source cohort %s on SSH host %s (%s/%s) in %s/remote-bin?\nNative image SHA-256: %s\nExisting images and workspace data are retained. A running older core is never stopped; if present, stop it safely on the host before reconnecting.", e.cohort, e.options.Host, e.host.OS, e.host.Arch, e.host.DataDir, e.digest)
}

func prepareRemoteInstall(ctx context.Context, options corewire.SSHOptions, cause error) error {
	host, err := corewire.ProbeSSHHost(ctx, options, buildinfo.DataDirName)
	if err != nil {
		return fmt.Errorf("remote core needs installation, but the verified host probe failed: %w", err)
	}
	if !matchingRemotePlatform(host, runtime.GOOS, runtime.GOARCH) {
		return fmt.Errorf("remote core needs source cohort %s for %s/%s; this shell is %s/%s. No verified release artifact source is configured. Install a native Crude image from that exact source cohort on SSH host %s and put it on PATH, then reconnect; this shell executable will not be uploaded", buildinfo.ProtocolBuild, host.OS, host.Arch, runtime.GOOS, runtime.GOARCH, options.Host)
	}
	image, err := os.Executable()
	if err != nil {
		return err
	}
	digest, err := remoteImageDigest(image)
	if err != nil {
		return err
	}
	imageID := buildinfo.ExecutableID()
	if len(imageID) != 16 || digest[:16] != imageID {
		return errors.New("running shell image changed; restart the shell before offering an exact matching install")
	}
	return &RemoteInstallRequiredError{options: options, host: host, image: image, cohort: buildinfo.ProtocolBuild, digest: digest, namespace: buildinfo.DataDirName, cause: cause, expires: time.Now().Add(10 * time.Minute)}
}

func matchingRemotePlatform(host corewire.SSHHost, goos, arch string) bool {
	osMatches := goos == "linux" && host.OS == "Linux" || goos == "darwin" && host.OS == "Darwin"
	archMatches := arch == "amd64" && host.Arch == "x86_64" || arch == "arm64" && (host.Arch == "aarch64" || host.Arch == "arm64")
	return osMatches && archMatches
}

func remoteImageDigest(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func (e *RemoteInstallRequiredError) InstallRemote(ctx context.Context) error {
	e.mu.Lock()
	if e.used || time.Now().After(e.expires) {
		e.mu.Unlock()
		return errors.New("install review expired or already used; test the connection and review again")
	}
	e.used = true
	e.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	f, err := os.Open(e.image)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil {
		return err
	}
	if hex.EncodeToString(h.Sum(nil)) != e.digest {
		return errors.New("reviewed executable changed; nothing installed")
	}
	if _, err = f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	return corewire.InstallSSHImage(ctx, e.options, e.namespace, e.cohort, e.digest, e.host, f)
}
