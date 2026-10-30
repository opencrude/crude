package workspace

import (
	"testing"

	"crude/fileio"
	"crude/insight/governor"
	"crude/types"
)

// trustAnalysis is explicit test-fixture admission, never called by negative
// trust tests or production constructors.
func trustAnalysis(t *testing.T, manager *LSPManager, root string) {
	t.Helper()
	g, err := governor.New(governor.Limits{MaxProcesses: 16, MaxWaiting: 64})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.SetAnalysisGovernor(g, "test-workspace", "test-generation"); err != nil {
		t.Fatal(err)
	}
	bindAnalysisRoot(t, manager, root)
}

func bindAnalysisRoot(t *testing.T, manager *LSPManager, root string) string {
	t.Helper()
	identity, err := fileio.DirectoryIdentity(root)
	if err != nil {
		t.Fatal(err)
	}
	manager.SetWorkspaceRoot(root)
	manager.SetAnalysisPolicy(types.WorkspaceAnalysisSettings{Trust: types.AnalysisProjectExecutionAllowed}, identity)
	return identity
}
