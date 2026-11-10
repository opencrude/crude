package core

import (
	"path/filepath"
	"testing"

	"crude/types"
	"crude/workspace"
)

func TestHandshakePublishesActualCoreNativeBuild(t *testing.T) {
	setCoreTestHome(t, t.TempDir())
	r, err := New(func(root string) (*workspace.Workspace, error) {
		return workspace.NewWorkspace(root, "", "", types.Settings{}, coreTestLogger{}), nil
	}, filepath.Join(t.TempDir(), "receipts.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	r.SetBuildID("server-native-different-from-shell")
	wire := newResourceWire(t, r, "native-build-reader")
	if got := wire.c.CoreBuildID(); got != "server-native-different-from-shell" {
		t.Fatal("handshake reported the shell's build instead of the core's", got)
	}
}
