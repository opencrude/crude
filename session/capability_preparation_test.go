package session

import (
	"os"
	"strings"
	"testing"
	"time"

	"crude/agent"
	"crude/fileio"
	captypes "crude/types/capability"
)

func preparationFixture(t *testing.T, mode string) (*Session, *agent.Agent, captypes.PreparationRequired) {
	t.Helper()
	t.Setenv("CRUDE_PROMOTION_HELPER", mode)
	manager, lg := promotionTestManager(t)
	request := captypes.PreparationRequired{ConnectionID: "registry_9a1d", Name: "Computer Use", Command: os.Args[0],
		Args: []string{"-test.run=^TestCommandPromotionHelperProcess$"}, Key: "key"}
	return &Session{logger: lg}, &agent.Agent{AsyncMgr: manager, WorkDir: t.TempDir()}, request
}

func TestQuickPackagePreparationContinuesInline(t *testing.T) {
	s, live, request := preparationFixture(t, "echo")
	prepared, _, err := s.prepareCapabilityPackage(t.Context(), live, request)
	if err != nil || !prepared {
		t.Fatalf("prepared=%v err=%v", prepared, err)
	}
}

func TestFailedPackagePreparationReportsOutputAndDoesNotLaunch(t *testing.T) {
	s, live, request := preparationFixture(t, "fail")
	prepared, outcome, err := s.prepareCapabilityPackage(t.Context(), live, request)
	if prepared || err == nil || !strings.Contains(outcome.Content, "helper exploded") || !strings.Contains(outcome.Content, "capability_activate") {
		t.Fatalf("prepared=%v err=%v outcome=%q", prepared, err, outcome.Content)
	}
}

// A slow download becomes an ordinary background command with a stable handle
// instead of blocking the agent loop or being killed by a deadline. Repeating
// the activation observes it rather than starting a duplicate download.
func TestSlowPackagePreparationBecomesTrackedBackgroundCommand(t *testing.T) {
	setPromotionThreshold(t, 100*time.Millisecond)
	s, live, request := preparationFixture(t, "sleep")
	prepared, outcome, err := s.prepareCapabilityPackage(t.Context(), live, request)
	handle := preparationHandle(request.ConnectionID)
	if prepared || err != nil || !strings.Contains(outcome.Content, handle) || !strings.Contains(outcome.Content, "command_outcome") {
		t.Fatalf("prepared=%v err=%v outcome=%q", prepared, err, outcome.Content)
	}
	info, tracked := live.AsyncMgr.Check(handle)
	if !tracked || info.Status != fileio.AsyncRunning {
		t.Fatalf("preparation is not a tracked background command: %+v", info)
	}
	prepared, outcome, err = s.prepareCapabilityPackage(t.Context(), live, request)
	if prepared || err != nil || !strings.Contains(outcome.Content, "still being prepared") {
		t.Fatalf("repeat activation: prepared=%v err=%v outcome=%q", prepared, err, outcome.Content)
	}
	if len(live.AsyncMgr.List()) != 1 {
		t.Fatalf("repeat activation started a duplicate download: %+v", live.AsyncMgr.List())
	}
}

func TestPreparationCommandLineQuotesExactArguments(t *testing.T) {
	line, err := preparationCommandLine("npx", []string{"--yes", "--package=computer-use-mcp@1.8.0", "--call", "exit 0"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(line, "--package=computer-use-mcp@1.8.0") || !strings.Contains(line, "'exit 0'") && !strings.Contains(line, `"exit 0"`) {
		t.Fatalf("command line = %s", line)
	}
	if _, err := preparationCommandLine("npx", []string{"bad\nline"}); err == nil {
		t.Fatal("control characters were accepted")
	}
}
