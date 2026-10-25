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

	"github.com/tinylib/msgp/msgp"

	"crude/core"
	"crude/coreprotocol"
	"crude/corewire"
	"crude/provider"
	"crude/session"
	"crude/workspace"
)

func TestCoreHandoverMatchingHelperStopsOnlyIdleOwner(t *testing.T) {
	for _, busy := range []bool{false, true} {
		t.Run(map[bool]string{false: "idle", true: "attached"}[busy], func(t *testing.T) {
			home := coreTestHome(t)
			t.Setenv("CRUDE_HOME", home)
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			client, err := ConnectCore(ctx, home, core.NewClientID())
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = client.Close() }()
			owner, err := readCoreOwner(home)
			if err != nil {
				t.Fatal(err)
			}
			if !busy {
				_ = client.Close()
			}
			err = handoverCore(ctx, home)
			if busy {
				if err == nil {
					t.Fatal("automatic handover stopped an attached owner")
				}
				current, readErr := readCoreOwner(home)
				if readErr != nil || current != owner {
					t.Fatalf("busy owner changed: %+v %v", current, readErr)
				}
				if _, err := CoreStatus(ctx, home); err != nil {
					t.Fatal(err)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if _, err := os.Lstat(coreOwnerPath(home)); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("settled owner descriptor remains: %v", err)
				}
				if _, err := os.Stat(owner.Executable); err != nil {
					t.Fatalf("settled helper was pruned without lifecycle proof: %v", err)
				}
				if err := prepareCoreStart(ctx, home); err != nil {
					t.Fatalf("owner did not release lifetime: %v", err)
				}
			}
		})
	}
}

func TestCoreHandoverFencesIdleRetainedWorkspace(t *testing.T) {
	home := coreTestHome(t)
	t.Setenv("CRUDE_HOME", home)
	agents := session.DefaultAgentsConfig()
	profile := agents.Profiles["default"]
	profile.Categories["default"] = session.CategoryConfig{AccountID: "fixture", Model: "fixture-model"}
	agents.Profiles["default"] = profile
	if err := SaveFileConfig(FileConfig{Agents: &agents, Accounts: map[string]provider.Account{"fixture": {ID: "fixture", ProviderID: "openai", Connection: provider.Template{Name: "Fixture", ProviderURL: "http://127.0.0.1:1/v1", APIStandard: "openai", AuthType: provider.AuthNone}}}}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client, err := ConnectCore(ctx, home, core.NewClientID())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(home, "project")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	attached := attach(t, client, root)
	var created workspace.NewSessionResult
	if err := client.Call(ctx, attached.WorkspaceID, coreprotocol.NewSession, &coreprotocol.InvocationSessionRequest{}, &created); err != nil || created.Error != "" {
		t.Fatalf("create retained backend: %+v %v", created, err)
	}
	if created.SessionID == "" {
		t.Fatal("no backend session created")
	}
	_ = client.Close()
	owner, err := readCoreOwner(home)
	if err != nil {
		t.Fatal(err)
	}
	// Session creation legitimately schedules owned memory maintenance. Do not
	// pretend that detach alone proves the retained backend is idle.
	for {
		status, err := CoreStatus(ctx, home)
		if err != nil {
			t.Fatal(err)
		}
		if len(core.BusyWorkspaces(status)) == 0 {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("retained session never became idle")
		case <-time.After(20 * time.Millisecond):
		}
	}
	if err := handoverCore(ctx, home); err != nil {
		t.Fatal("idle retained workspace refused:", err)
	}
	if _, err := os.Stat(owner.Executable); err != nil {
		t.Fatal("retained owner image lost:", err)
	}
	if _, err := readCoreOwner(home); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old owner did not settle: %v", err)
	}
	client, err = ConnectCore(ctx, home, core.NewClientID())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()
	attach(t, client, root)
}

func TestCoreOwnerGenerationPreventsReplacementStop(t *testing.T) {
	home := coreTestHome(t)
	t.Setenv("CRUDE_HOME", home)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client, err := ConnectCore(ctx, home, core.NewClientID())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()
	var result coreprotocol.ShutdownResult
	err = client.Call(ctx, "", coreprotocol.CoreShutdownMethod, coreprotocol.ShutdownRequest{ExpectedOwner: strings.Repeat("0", 64)}, &result)
	if err != nil || result.Accepted {
		t.Fatalf("wrong incarnation stopped: %+v %v", result, err)
	}
	t.Setenv(expectedCoreOwnerEnv, strings.Repeat("0", 64))
	if _, err := StopCore(ctx, home, false); err == nil {
		t.Fatal("helper ignored its expected incarnation")
	}
	t.Setenv(expectedCoreOwnerEnv, "")
	if _, err := CoreStatus(ctx, home); err != nil {
		t.Fatal(err)
	}
}

func TestCoreHandoverFailurePreservesOwner(t *testing.T) {
	for _, failure := range []string{"missing", "changed", "cancelled", "timeout"} {
		t.Run(failure, func(t *testing.T) {
			home := coreTestHome(t)
			t.Setenv("CRUDE_HOME", home)
			lock, err := lockCoreLifetime(home)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = lock.Close() }()
			owner, err := installCoreOwner(home)
			if err != nil {
				t.Fatal(err)
			}
			// No process is executing this test-owned image.
			switch failure {
			case "missing":
				if err := os.Remove(owner.Executable); err != nil {
					t.Fatal(err)
				}
			case "changed":
				if err := os.WriteFile(owner.Executable, []byte("changed image"), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithCancel(context.Background())
			if failure == "cancelled" {
				cancel()
			}
			if failure == "timeout" {
				cancel()
				ctx, cancel = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
			}
			defer cancel()
			if err := handoverCore(ctx, home); err == nil {
				t.Fatal("unsafe handover succeeded")
			}
			current, err := readCoreOwner(home)
			if err != nil || current != owner {
				t.Fatalf("failed handover mutated owner: %+v %v", current, err)
			}
		})
	}
}

func TestCoreLifetimeLockProtectsShutdownAndRecoversCrashDescriptor(t *testing.T) {
	home := coreTestHome(t)
	lock, err := lockCoreLifetime(home)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := installCoreOwner(home)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()
	if err := prepareCoreStart(ctx, home); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("live shutdown lifetime was treated as absent: %v", err)
	}
	if _, err := readCoreOwner(home); err != nil {
		t.Fatal(err)
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	if err := prepareCoreStart(context.Background(), home); err != nil {
		t.Fatalf("crash-left descriptor not recovered: %v", err)
	}
	if _, err := os.Lstat(coreOwnerPath(home)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale owner remains: %v", err)
	}
	if _, err := os.Stat(owner.Executable); err != nil {
		t.Fatal(err)
	}
}

func TestCoreOwnerRejectsUnsafeDescriptor(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix mode/symlink assertions; Windows ACL validated by native gate")
	}
	home := coreTestHome(t)
	lock, err := lockCoreLifetime(home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Close() }()
	owner, err := installCoreOwner(home)
	if err != nil {
		t.Fatal(err)
	}
	path := coreOwnerPath(home)
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readCoreOwner(home); err == nil {
		t.Fatal("public descriptor accepted")
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path, path+".saved"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(path+".saved", path); err != nil {
		t.Fatal(err)
	}
	if _, err := readCoreOwner(home); err == nil {
		t.Fatal("symlink descriptor accepted")
	}
	if err := removeCoreOwner(home, owner.Generation); err == nil {
		t.Fatal("unsafe descriptor removed")
	}
}

func TestConnectCoreTransportFailureNeverStagesOrSpawns(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix invalid-endpoint fixture")
	}
	home := coreTestHome(t)
	if err := os.MkdirAll(filepath.Join(DataDir(home), "corewire"), 0o700); err != nil {
		t.Fatal(err)
	}
	addr, err := corewire.LocalAddress(DataDir(home))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(addr, []byte("not a socket"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ConnectCore(context.Background(), home, core.NewClientID()); err == nil {
		t.Fatal("invalid endpoint accepted")
	}
	if _, err := os.Lstat(CoreImageDir(home)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("transport failure staged executable: %v", err)
	}
	if raw, err := os.ReadFile(addr); err != nil || string(raw) != "not a socket" {
		t.Fatalf("endpoint modified: %q %v", raw, err)
	}
}

func TestConnectCoreMismatchWithoutOwnerNeverStagesOrSpawns(t *testing.T) {
	sameLayout := func(b []byte) ([]byte, error) {
		welcome := coreprotocol.Welcome{Schema: coreprotocol.Schema, Build: "other-cohort", Fault: &coreprotocol.Fault{Code: "build_mismatch", Message: "other owner"}}
		return welcome.MarshalMsg(b)
	}
	// Another build's Welcome may have a different tuple length; only its
	// frozen [Schema, Build] prefix is shared. It must still be a mismatch.
	foreignLayout := func(b []byte) ([]byte, error) {
		b = msgp.AppendArrayHeader(b, 4)
		b = msgp.AppendString(b, "older-protocol")
		b = msgp.AppendString(b, "older-cohort")
		b = msgp.AppendNil(b)
		return msgp.AppendString(b, "fields this build does not know"), nil
	}
	for name, encode := range map[string]func([]byte) ([]byte, error){"same layout": sameLayout, "foreign layout": foreignLayout} {
		t.Run(name, func(t *testing.T) { connectCoreMismatchWithoutOwner(t, encode) })
	}
}

func connectCoreMismatchWithoutOwner(t *testing.T, encode func([]byte) ([]byte, error)) {
	home := coreTestHome(t)
	if err := os.MkdirAll(DataDir(home), 0o700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	listener, err := corewire.ListenLocal(ctx, DataDir(home))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	done := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			done <- err
			return
		}
		carrier := corewire.NewMux(conn, corewire.CarrierLocal, false)
		defer func() { _ = carrier.Close() }()
		stream, err := carrier.AcceptStream(ctx)
		if err != nil {
			done <- err
			return
		}
		if _, err := corewire.NewReader(stream, 64<<10).Next(); err != nil {
			done <- err
			return
		}
		message := corewire.NewMessage()
		defer message.Release()
		message.B, err = encode(message.B)
		if err == nil {
			err = message.Send(stream)
		}
		if err == nil {
			// Wait for the rejected client to close before releasing the carrier.
			_, _ = corewire.NewReader(stream, 1).Next()
		}
		done <- err
	}()
	_, err = ConnectCore(ctx, home, core.NewClientID())
	var handover *CoreHandoverError
	if !errors.As(err, &handover) {
		t.Fatalf("mismatch did not expose safe handover failure: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(CoreImageDir(home)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("mismatch staged an executable: %v", err)
	}
}

func TestConnectCoreSameCohortDifferentImageKeepsOwner(t *testing.T) {
	home := coreTestHome(t)
	if err := os.MkdirAll(DataDir(home), 0o700); err != nil {
		t.Fatal(err)
	}
	r, err := core.New(func(string) (*workspace.Workspace, error) { return nil, errors.New("unexpected workspace") }, filepath.Join(DataDir(home), "test-receipts.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	r.SetBuildID("different-native-image")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	listener, err := corewire.ListenLocal(ctx, DataDir(home))
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- core.Serve(ctx, r, listener) }()
	defer func() { cancel(); _ = listener.Close(); <-done }()
	client, err := ConnectCore(ctx, home, core.NewClientID())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()
	var status coreprotocol.CoreStatus
	if err := client.Call(ctx, "", coreprotocol.CoreStatusMethod, &coreprotocol.Empty{}, &status); err != nil || status.BuildID != "different-native-image" {
		t.Fatalf("same-cohort owner was not retained: %+v %v", status, err)
	}
	if _, err := os.Lstat(CoreImageDir(home)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("matching cohort staged a replacement image: %v", err)
	}
}

func TestCoreHandoverRunningHelperTimeoutPreservesDescriptor(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix exact-process shell fixture")
	}
	home := coreTestHome(t)
	lock, err := lockCoreLifetime(home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Close() }()
	if err := os.MkdirAll(CoreImageDir(home), 0o700); err != nil {
		t.Fatal(err)
	}
	image := filepath.Join(CoreImageDir(home), "crude-core-timeout")
	// exec avoids an orphan descendant when CommandContext stops this helper.
	if err := os.WriteFile(image, []byte("#!/bin/sh\nexec sleep 30\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(image)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := coreImageDigest(f)
	_ = f.Close()
	if err != nil {
		t.Fatal(err)
	}
	owner := coreOwner{Generation: strings.Repeat("1", 64), Executable: image, Digest: digest, Cohort: "test", Schema: "test"}
	if err := publishCoreOwner(coreOwnerPath(home), owner); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := handoverCore(ctx, home); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("helper deadline not surfaced: %v", err)
	}
	current, err := readCoreOwner(home)
	if err != nil || current != owner {
		t.Fatalf("helper timeout mutated owner: %+v %v", current, err)
	}
}
