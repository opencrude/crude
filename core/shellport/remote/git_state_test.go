package remote

import (
	"context"
	"testing"

	"crude/core/coreprotocol"
	"crude/core/shellport"
	"crude/workspace"
)

func TestGitAndBoardTopicFaultsDoNotReportOperationFailures(t *testing.T) {
	for _, test := range []struct {
		topic string
		good  coreprotocol.WorkspaceState
		code  string
	}{
		{coreprotocol.TopicGit, coreprotocol.WorkspaceState{Git: &coreprotocol.GitSnapshotResult{Snapshot: workspace.GitSnapshot{Branch: "main"}}}, "git_failed"},
		{coreprotocol.TopicBoard, coreprotocol.WorkspaceState{Board: &coreprotocol.BoardSnapshotResult{Snapshot: workspace.BoardSnapshot{WorkspaceID: "retained"}}}, "board_failed"},
	} {
		t.Run(test.topic, func(t *testing.T) {
			w := &Workspace{}
			apply := func(state coreprotocol.WorkspaceState) shellport.ReplicaApplied {
				t.Helper()
				result, err := w.applyState(publicationBatch{topic: test.topic, state: state})
				if err != nil {
					t.Fatal(err)
				}
				return result
			}
			apply(test.good)
			failed := apply(coreprotocol.WorkspaceState{Fault: &coreprotocol.Fault{Code: test.code, Message: context.DeadlineExceeded.Error()}})
			for _, follow := range failed.Follow {
				if _, modal := follow.(shellport.ReplicaFailureMsg); modal {
					t.Fatalf("%s refresh sent a follow-up that opens an operation dialog", test.topic)
				}
			}
			if len(failed.Follow) != 1 {
				t.Fatalf("%s refresh did not publish inline status", test.topic)
			}
			if test.topic == coreprotocol.TopicGit && (w.gitState != test.good.Git || w.gitError == nil) {
				t.Fatal("Git refresh did not keep good evidence and record the fault")
			}
			if test.topic == coreprotocol.TopicBoard {
				snapshot, err := w.BoardSnapshot(t.Context(), "", "", 0, 0)
				if err != nil || snapshot.WorkspaceID != "retained" || w.boardError == nil {
					t.Fatalf("Board refresh hid its good evidence: %+v, %v", snapshot, err)
				}
			}
			recovered := apply(test.good)
			if w.gitError != nil || w.boardError != nil || len(recovered.Follow) != 1 {
				t.Fatalf("a good %s publication did not clear the fault and notify the panel", test.topic)
			}
		})
	}
}
