package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"crude/buildinfo"
	"crude/coreprotocol"
	"crude/corewire"
)

func remoteInstallFixture(t *testing.T) corewire.SSHOptions {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX SSH fixture")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ssh"), []byte("#!/bin/sh\nfor arg do command=$arg; done\nexec sh -c \"$command\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("CRUDE_HOME", t.TempDir())
	return corewire.SSHOptions{Host: "test-host", User: "test-user", Port: 2222}
}

func TestRemoteInstallRequiresExactSingleUseReview(t *testing.T) {
	o := remoteInstallFixture(t)
	cause := &coreprotocol.Fault{Code: "build_mismatch", Message: "different image"}
	err := prepareRemoteInstall(t.Context(), o, cause)
	var offer *RemoteInstallRequiredError
	if !errors.As(err, &offer) {
		t.Fatalf("no install offer: %v", err)
	}
	if !errors.Is(err, cause) {
		t.Fatal("lost typed mismatch")
	}
	for _, fact := range []string{"test-host", buildinfo.ProtocolBuild, offer.digest, offer.host.OS + "/" + offer.host.Arch, offer.host.DataDir, "SHA-256"} {
		if !strings.Contains(offer.RemoteInstallSummary(), fact) {
			t.Fatalf("review lacks exact host/cohort/image fact %q", fact)
		}
	}
	if offer.cohort != buildinfo.ProtocolBuild || len(offer.digest) != 64 {
		t.Fatal("review did not separate source cohort and full image digest")
	}
	target := filepath.Join(offer.host.DataDir, "remote-bin", "crude-"+offer.cohort)
	if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("probe installed without consent")
	}
	if err := offer.InstallRemote(t.Context()); err != nil {
		t.Fatal(err)
	}
	got, err := remoteImageDigest(target)
	if err != nil || got != offer.digest {
		t.Fatalf("installed wrong image: %v", err)
	}
	if err := offer.InstallRemote(t.Context()); err == nil {
		t.Fatal("replayed install review")
	}
}

func TestRemoteInstallReviewRefusesChangedAndExpiredImage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "image")
	if err := os.WriteFile(path, []byte("before"), 0600); err != nil {
		t.Fatal(err)
	}
	digest, err := remoteImageDigest(path)
	if err != nil {
		t.Fatal(err)
	}
	offer := &RemoteInstallRequiredError{image: path, digest: digest, expires: time.Now().Add(time.Minute)}
	if err := os.WriteFile(path, []byte("after"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := offer.InstallRemote(t.Context()); err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("changed image: %v", err)
	}
	offer = &RemoteInstallRequiredError{expires: time.Now().Add(-time.Second)}
	if err := offer.InstallRemote(t.Context()); err == nil {
		t.Fatal("expired review accepted")
	}
	offer = &RemoteInstallRequiredError{expires: time.Now().Add(time.Minute)}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := offer.InstallRemote(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled offer: %v", err)
	}
}

func TestRemoteInstallNeverCopiesAcrossPlatforms(t *testing.T) {
	for _, test := range []struct {
		host     corewire.SSHHost
		os, arch string
		want     bool
	}{
		{corewire.SSHHost{OS: "Linux", Arch: "x86_64"}, "linux", "amd64", true},
		{corewire.SSHHost{OS: "Linux", Arch: "aarch64"}, "linux", "arm64", true},
		{corewire.SSHHost{OS: "Darwin", Arch: "arm64"}, "darwin", "arm64", true},
		{corewire.SSHHost{OS: "Darwin", Arch: "x86_64"}, "linux", "amd64", false},
		{corewire.SSHHost{OS: "Linux", Arch: "aarch64"}, "linux", "amd64", false},
		{corewire.SSHHost{OS: "Windows", Arch: "x86_64"}, "windows", "amd64", false},
	} {
		if got := matchingRemotePlatform(test.host, test.os, test.arch); got != test.want {
			t.Fatalf("platform %+v => %v", test, got)
		}
	}
}

func TestRemoteInstallForeignHostRequiresNativeMatchingCohort(t *testing.T) {
	o := remoteInstallFixture(t)
	dir := t.TempDir()
	// Simulate the authenticated host probe only; no foreign executable is run.
	if err := os.WriteFile(filepath.Join(dir, "uname"), []byte("#!/bin/sh\ncase \"$1\" in -s) echo ForeignOS;; -m) echo foreignarch;; *) exit 1;; esac\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	err := prepareRemoteInstall(t.Context(), o, &coreprotocol.Fault{Code: "build_mismatch"})
	var offer *RemoteInstallRequiredError
	if err == nil || errors.As(err, &offer) {
		t.Fatalf("foreign platform offered shell image: %v", err)
	}
	for _, fact := range []string{buildinfo.ProtocolBuild, "ForeignOS/foreignarch", "test-host", "native Crude image", "PATH", "will not be uploaded"} {
		if !strings.Contains(err.Error(), fact) {
			t.Fatalf("missing native install instruction %q: %v", fact, err)
		}
	}
	if _, err := os.Stat(filepath.Join(os.Getenv("CRUDE_HOME"), "."+buildinfo.DataDirName, "remote-bin")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("foreign platform wrote installation")
	}
}
