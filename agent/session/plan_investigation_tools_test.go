package session

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"crude/agent/tools"
	"crude/agent/worktree"
	"crude/types/command"
	"crude/types/conversation"
	"crude/types/toolresult"
	"crude/workspace/fileio"
)

func investigationToolFixture(t *testing.T) (*Session, worktree.Context, context.Context, worktree.Owner) {
	t.Helper()
	s, _, source := executionAdmissionFixture(t)
	store := attachDelegationTestCollaboration(t, s)
	if err := s.SetPlanMode(true); err != nil {
		t.Fatal(err)
	}
	cycle, err := s.planInvestigationCycle(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	owner, _ := startInvestigationTestRun(t, s, store, s.sessionID+"_orch_1")
	return s, source, withPlanInvestigationCycle(t.Context(), cycle), owner
}

func TestPlanInvestigationToolsKeepSourceAndRejectAllOutsideOperands(t *testing.T) {
	s, source, ctx, owner := investigationToolFixture(t)
	call := func(tool string, params map[string]any) (tools.ToolResult, error) {
		return s.executePlanInvestigationTool(ctx, source.ID, source.Root, owner, 1, tool, params)
	}
	assertBytes := func(path, want string) {
		t.Helper()
		got, err := os.ReadFile(path)
		if err != nil || string(got) != want {
			t.Fatalf("%s = %q, want %q: %v", path, got, want, err)
		}
	}
	const file, original = "baseline.txt", "preserved baseline\n"
	edit := map[string]any{"path": file, "old_string": "preserved baseline", "new_string": "experiment"}
	if res, err := call("edit_file", edit); err == nil || res.Outcome.State != toolresult.Denied {
		t.Fatalf("write before open: %+v, %v", res, err)
	}
	if _, err := call("open_plan_investigation", nil); err != nil {
		t.Fatal(err)
	}
	checkout, err := s.activePlanInvestigation(ctx, source.ID, owner)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := call("edit_file", edit); err != nil {
		t.Fatal(err)
	}
	sourceFile := filepath.Join(source.Root, file)
	assertBytes(sourceFile, original)
	assertBytes(filepath.Join(checkout.Root, file), "experiment\n")
	read := s.buildToolFnAtRoot(false, nil, nil, source.Root)
	if got, err := read(ctx, "read_file", map[string]any{"path": file}); err != nil || !strings.Contains(got.Content, "preserved baseline") {
		t.Fatalf("default read changed roots: %+v, %v", got, err)
	}
	if got, err := read(ctx, "read_file", map[string]any{"path": filepath.Join(checkout.Root, file)}); err != nil || !strings.Contains(got.Content, "experiment") {
		t.Fatalf("explicit investigation read: %+v, %v", got, err)
	}
	const patch = "*** Begin Patch\n*** Update File: baseline.txt\n*** Move to: moved.txt\n@@\n-experiment\n+prototype\n*** End Patch"
	if _, err := call("apply_patch", map[string]any{"input": patch}); err != nil {
		t.Fatal(err)
	}
	assertBytes(filepath.Join(checkout.Root, "moved.txt"), "prototype\n")
	badPatch := fmt.Sprintf("*** Begin Patch\n*** Add File: first.txt\n+must not be added\n*** Update File: moved.txt\n*** Move to: %s\n@@\n-prototype\n+outside\n*** End Patch", sourceFile)
	for _, tc := range []struct {
		tool   string
		params map[string]any
	}{
		{"write_file", map[string]any{"path": sourceFile, "content": "source write"}},
		{"move_file", map[string]any{"source": "moved.txt", "destination": sourceFile}},
		{"copy_file", map[string]any{"source": sourceFile, "destination": "copied.txt"}},
		{"apply_patch", map[string]any{"input": badPatch}},
		{"apply_patch", map[string]any{"input": "*** Begin Patch\n*** Delete File: moved.txt\n*** End Patch"}},
		{"write_file", map[string]any{"path": ".git", "content": "not Git metadata"}},
	} {
		if res, err := call(tc.tool, tc.params); err == nil || res.Outcome.State != toolresult.Denied {
			t.Fatalf("unsafe %s was not denied: %+v, %v", tc.tool, res, err)
		}
	}
	if _, err := os.Stat(filepath.Join(checkout.Root, "first.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unsafe later patch operand allowed earlier mutation: %v", err)
	}
	assertBytes(sourceFile, original)
	assertBytes(filepath.Join(checkout.Root, "moved.txt"), "prototype\n")
	if err := os.Symlink(source.Root, filepath.Join(checkout.Root, "link")); err == nil {
		if _, err := call("write_file", map[string]any{"path": "link/baseline.txt", "content": "symlink escape"}); err == nil {
			t.Fatal("native write followed a checkout symlink")
		}
	}
	if _, err := read(ctx, "write_file", map[string]any{"path": filepath.Join(checkout.Root, "moved.txt"), "content": "ordinary write"}); err == nil {
		t.Fatal("ordinary native write bypassed the investigation checkout boundary")
	}
}

func TestPlanInvestigationCommandRoutesUseOneExactPermissionAndRealEvidence(t *testing.T) {
	t.Setenv("LC_ALL", "C")
	s, source, ctx, owner := investigationToolFixture(t)
	call := func(tool string, params map[string]any) (tools.ToolResult, error) {
		return s.executePlanInvestigationTool(ctx, source.ID, source.Root, owner, 1, tool, params)
	}
	commandCases := []struct {
		tool   string
		params map[string]any
	}{
		{"execute_command", map[string]any{"command": "echo plan-verified"}},
		{"exec_command", map[string]any{"cmd": "echo plan-verified"}},
		{"async_execute_command", map[string]any{"command": "echo plan-verified", "handle": "investigation-evidence"}},
	}
	if res, err := call("execute_command", map[string]any{"command": "git status"}); err != nil || !strings.Contains(res.Content, "nothing to commit") {
		t.Fatalf("ordinary Git inspection: %+v, %v", res, err)
	}
	for _, tc := range commandCases {
		if res, err := call(tc.tool, tc.params); err == nil || res.Outcome.State != toolresult.Denied {
			t.Fatalf("%s ran before open: %+v, %v", tc.tool, res, err)
		}
	}
	if _, err := call("open_plan_investigation", nil); err != nil {
		t.Fatal(err)
	}
	for _, tc := range commandCases {
		if res, err := call(tc.tool, tc.params); err == nil || res.Outcome.State != toolresult.Denied {
			t.Fatalf("%s ran before consent: %+v, %v", tc.tool, res, err)
		}
	}
	request, err := s.planInvestigationHostRequest(ctx, source.ID, source.Root, owner, commandCases[0].tool, commandCases[0].params)
	if err != nil || request == nil || sameToolPath(request.Directory, source.Root) {
		t.Fatalf("consent was not specific to investigation: %+v, %v", request, err)
	}
	if err := request.Decide("allow"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range commandCases {
		if request, err := s.planInvestigationHostRequest(ctx, source.ID, source.Root, owner, tc.tool, tc.params); err != nil || request != nil {
			t.Fatalf("%s did not reuse exact investigation consent: %+v, %v", tc.tool, request, err)
		}
		res, err := call(tc.tool, tc.params)
		if err != nil {
			t.Fatalf("%s: %+v, %v", tc.tool, res, err)
		}
		result, ok := tools.ParseCommandResult(tc.tool, res.Content)
		if !ok || !sameToolPath(result.CWD, request.Directory) {
			t.Fatalf("%s wrong execution directory: %+v", tc.tool, result)
		}
		if tc.tool != "async_execute_command" && (result.OutputDescriptor.Status != command.Completed || !strings.Contains(result.Output, "plan-verified")) {
			t.Fatalf("%s missing exact terminal result: %+v", tc.tool, result)
		}
	}
	info, ok := s.asyncMgr.Check("investigation-evidence")
	if !ok {
		t.Fatal("async experiment was not owned by the command manager")
	}
	terminal := waitInvestigationToolCommand(t, s, info.Ref())
	if terminal.Status != fileio.AsyncCompleted || terminal.ExitCode != 0 {
		t.Fatalf("async experiment did not settle: %+v", terminal)
	}
	if res, err := call("execute_command", map[string]any{"command": "echo not-allowed", "cwd": source.Root}); err == nil || res.Outcome.State != toolresult.Denied {
		t.Fatalf("approval enabled source execution: %+v, %v", res, err)
	}
	res, err := call("execute_command", map[string]any{"command": "echo changed > baseline.txt"})
	changed, parsed := tools.ParseCommandResult("execute_command", res.Content)
	if err == nil || !parsed || !changed.Uncertain || !strings.Contains(changed.Error, "measured files changed") {
		t.Fatalf("changed experiment files reported a clean pass: %+v, %v", res, err)
	}
	if err := s.recordProposedPlan(1, "<proposed_plan>\n# Experiment plan\n\nReview the evidence.\n</proposed_plan>", planInvestigationCycleFromContext(ctx), source.ID); err != nil {
		t.Fatal(err)
	}
	review, ok := s.PlanReview()
	if !ok || len(review.Revisions) != 1 || len(review.Revisions[0].Evidence) != 4 ||
		review.Revisions[0].Evidence[3].Status != conversation.PlanEvidenceInconclusive {
		t.Fatalf("exact command receipts did not reach the revision: %+v", review)
	}
}

func waitInvestigationToolCommand(t *testing.T, s *Session, ref fileio.AsyncCmdRef) fileio.AsyncCmdInfo {
	t.Helper()
	timeout := time.After(10 * time.Second)
	for {
		_, _, info, changed, err := s.asyncMgr.ObserveOutput(ref, 0, 1)
		if err != nil {
			t.Fatal(err)
		}
		if info.Status != fileio.AsyncRunning {
			return info
		}
		select {
		case <-changed:
		case <-timeout:
			t.Fatal("async experiment did not settle")
		}
	}
}

func TestPlanInvestigationAsyncHoldsCheckoutUntilCancellationEvidence(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX sleep as a cancellable child")
	}
	s, source, ctx, owner := investigationToolFixture(t)
	checkout, err := s.openPlanInvestigation(ctx, source.ID, owner)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.setInvestigationConsent(checkout.ID, "allow"); err != nil {
		t.Fatal(err)
	}
	params := map[string]any{"command": "sleep 30", "handle": "cancel-investigation"}
	if _, err := s.executePlanInvestigationTool(ctx, source.ID, source.Root, owner, 1, "async_execute_command", params); err != nil {
		t.Fatal(err)
	}
	info, ok := s.asyncMgr.Check("cancel-investigation")
	if !ok {
		t.Fatal("async experiment was not owned by the command manager")
	}
	edit := map[string]any{"path": "baseline.txt", "old_string": "preserved baseline", "new_string": "experiment"}
	if _, err := s.executePlanInvestigationTool(ctx, source.ID, source.Root, owner, 1, "edit_file", edit); err == nil || !strings.Contains(err.Error(), "still running") {
		t.Fatalf("edit entered the experiment measurement: %v", err)
	}
	if err := s.asyncMgr.KillRef(info.Ref()); err != nil {
		t.Fatal(err)
	}
	if settled := waitInvestigationToolCommand(t, s, info.Ref()); settled.Status != fileio.AsyncKilled {
		t.Fatalf("cancelled command published an incorrect terminal state: %+v", settled)
	}
	if _, err := s.executePlanInvestigationTool(ctx, source.ID, source.Root, owner, 1, "edit_file", edit); err != nil {
		t.Fatalf("the settled command retained the checkout lease: %v", err)
	}
	journal, err := s.loadInvestigationEvidenceJournal(ctx)
	if err != nil || len(journal.Entries) != 1 || journal.Entries[0].Ref.Status != conversation.PlanEvidenceInterrupted {
		t.Fatalf("the actual cancellation was not retained: %+v: %v", journal, err)
	}
}

func TestPlanSourceGitStatusNeverInvokesProjectHooksOrFilters(t *testing.T) {
	s, source, ctx, owner := investigationToolFixture(t)
	runGit := func(args ...string) {
		t.Helper()
		if output, err := exec.Command("git", append([]string{"-C", source.Root}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %s: %s: %v", args, output, err)
		}
	}
	runGit("config", "core.fsmonitor", "./project-monitor")
	runGit("config", "filter.marker.clean", "echo invoked > escaped; cat")
	runGit("config", "filter.marker.required", "true")
	for name, content := range map[string]string{
		".gitattributes":  "baseline.txt filter=marker\n",
		"baseline.txt":    "different tracked bytes\n",
		"project-monitor": "#!/bin/sh\necho invoked > escaped\n",
	} {
		if err := os.WriteFile(filepath.Join(source.Root, name), []byte(content), 0700); err != nil {
			t.Fatal(err)
		}
	}
	res, err := s.executePlanInvestigationTool(ctx, source.ID, source.Root, owner, 1, "execute_command", map[string]any{"command": "git status --short"})
	if err != nil || !strings.Contains(res.Content, "baseline.txt") {
		t.Fatalf("source Git status: %+v, %v", res, err)
	}
	if _, err := os.Stat(filepath.Join(source.Root, "escaped")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("status invoked project configuration: %v", err)
	}
	for _, text := range []string{"go test ./...", "npm test", "cargo test", "git -c core.fsmonitor=./project-monitor status", "rg --pre ./project-monitor x", "git status && ./project-monitor", "./project-monitor", "sed 'e ./project-monitor' baseline.txt"} {
		if request, err := s.planInvestigationHostRequest(ctx, source.ID, source.Root, owner, "execute_command", map[string]any{"command": text}); err == nil || request != nil {
			t.Fatalf("project execution was treated as source inspection: %q", text)
		}
	}
}
