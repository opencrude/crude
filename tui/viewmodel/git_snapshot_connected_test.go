package viewmodel

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"crude/shellport"
	"crude/tui/screens/gitpanel"
	"crude/tui/ui"
	"crude/types"
	"crude/workspace"
)

func TestConnectedGitExternalCommitRefreshAndEmptyGeneration(t *testing.T) {
	for _, size := range [][2]int{{160, 40}, {80, 24}} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			var modelRequests atomic.Int32
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				modelRequests.Add(1)
				http.Error(w, "a clean repository must not request a model", http.StatusInternalServerError)
			}))
			defer provider.Close()
			root := newGitRepoFixture(t)
			runtime := remoteJourneyRuntime(t, provider.URL)
			shell := newRemoteJourneyShell(t, runtime, root)
			peer := newRemoteJourneyShell(t, runtime, root)
			app := shell.app
			app.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			remoteJourneyUntil(t, []remoteJourneyShell{shell, peer}, func() bool {
				return len(app.gitPanel.Snapshot().Changes) == 1 && len(peer.app.gitPanel.Snapshot().Changes) == 1
			})
			runAppCommand(t, app, app.openGitPanel(gitpanel.ModeChanges))
			stale := app.gitPanel.Snapshot()
			app.gitPanel.SetMessage("My private commit draft")
			runGitFixture(t, root, "add", "--", "tracked.txt")
			runGitFixture(t, root, "commit", "-m", "external editor commit")

			// Generate entered while the panel still painted its old diff.
			// A typed empty result must restore the draft, refresh all Git
			// surfaces and never turn into an apparent commit/model failure.
			command, handled := app.handleGitPanelAction(ui.Action{ID: gitpanel.ActionGenerate, Payload: gitpanel.GenerateRequest{StageAll: true}})
			if !handled || command == nil {
				t.Fatal("Generate did not reach the connected native action")
			}
			runAppCommand(t, app, command)
			if app.gitPanel.Writing() || app.gitPanel.Message() != "My private commit draft" || len(app.gitPanel.Snapshot().Changes) != 0 {
				t.Fatalf("empty generation lost the draft or retained phantom files: %+v draft=%q", app.gitPanel.Snapshot(), app.gitPanel.Message())
			}
			paint := ansi.Strip(app.View().RenderContent())
			if strings.Contains(paint, "generation_failed") || strings.Contains(paint, "there are no changes to describe") ||
				strings.Contains(headerGitSegment(t, app), "✎") || modelRequests.Load() != 0 {
				t.Fatalf("clean repository still paints a failure/diff or called a model (%d):\n%s", modelRequests.Load(), paint)
			}
			current := app.gitPanel.Snapshot()
			if current.Head == stale.Head || current.SourceID == "" || current.Sequence <= stale.Sequence {
				t.Fatalf("fresh core read identity was not carried through RPC: stale=%+v fresh=%+v", stale, current)
			}
			if app.presentGitSnapshot(stale) || app.gitPanel.Snapshot().Head != current.Head {
				t.Fatal("a delayed pre-commit publication resurrected the old change list")
			}

			// The real Refresh shortcut must also re-read working bytes, not
			// depend on another watch publication getting through first.
			if err := os.WriteFile(filepath.Join(root, "tracked.txt"), []byte("three\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			app.paintShellFrame()
			app.rootHost.Focus(gitpanel.ListID)
			_, refresh := app.Update(tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl})
			if refresh == nil {
				t.Fatal("Ctrl+R did not dispatch Refresh from the mounted Git panel")
			}
			runAppCommand(t, app, refresh)
			if len(app.gitPanel.Snapshot().Changes) != 1 || app.gitPanel.Message() != "My private commit draft" {
				t.Fatal("Refresh read stale state or rewrote the private draft")
			}
			remoteJourneyUntil(t, []remoteJourneyShell{shell, peer}, func() bool {
				return len(app.gitPanel.Snapshot().Changes) == 1 && len(peer.app.gitPanel.Snapshot().Changes) == 1
			})
			runGitFixture(t, root, "add", "--", "tracked.txt")
			remoteJourneyUntil(t, []remoteJourneyShell{shell, peer}, func() bool {
				return len(app.gitPanel.Snapshot().StagedPaths()) == 1 && len(peer.app.gitPanel.Snapshot().StagedPaths()) == 1
			})
			runGitFixture(t, root, "commit", "-m", "agent later commit")
			runGitFixture(t, root, "switch", "-c", "agent-checkout")
			remoteJourneyUntil(t, []remoteJourneyShell{shell, peer}, func() bool {
				a, b := app.gitPanel.Snapshot(), peer.app.gitPanel.Snapshot()
				return a.Branch == "agent-checkout" && b.Branch == "agent-checkout" &&
					len(a.Changes) == 0 && len(b.Changes) == 0
			})
			if app.gitPanel.Message() != "My private commit draft" {
				t.Fatal("external agent Git operations consumed the shell's draft")
			}
		})
	}
}

func TestGitFreshReadBoundarySurvivesEquivalentAndLateSnapshots(t *testing.T) {
	app := newFullTestApp(t)
	first := workspace.GitSnapshot{SourceID: "owner", Sequence: 10, Branch: "work", Head: "current", RepositoryRoot: app.ws.RootPath()}
	app.presentGitSnapshot(first)
	app.gitPanel.SetMessage("retain this draft")
	same := first
	same.Sequence = 12
	app.gitRefreshGen.Store(1)
	app.applySourceControlResult(sourceControlResultMsg{gen: 1, silent: true, snapshot: same})
	old := first
	old.Sequence, old.Head = 11, "old"
	old.Changes = []workspace.GitChange{{Path: "already-committed.txt", Area: workspace.GitAreaWorking, Code: "M"}}
	if app.presentGitSnapshot(old) || app.gitPanel.Snapshot().Sequence != 12 ||
		strings.Contains(headerGitSegment(t, app), "✎") || app.gitPanel.Message() != "retain this draft" {
		t.Fatal("equivalent fresh read did not fence an older, visibly different snapshot")
	}
	restarted := first
	restarted.SourceID, restarted.Sequence, restarted.Head = "new-core-owner", 1, "recovered"
	if !app.presentGitSnapshot(restarted) || app.gitPanel.Snapshot().Head != "recovered" {
		t.Fatal("fresh core lifetime was rejected by an old sequence domain")
	}
	// Receiving status evidence does not reset the fallback clock forever.
	app.gitSnapshotAt = time.Now().Add(-gitSnapshotFallback)
	if !app.gitSnapshotReady(time.Now()) {
		t.Fatal("connected read ordering disabled the event-loss fallback")
	}
}

type gitFailingStatusRead struct {
	shellport.GitService
}

func (g gitFailingStatusRead) Snapshot(context.Context) (workspace.GitSnapshot, error) {
	return workspace.GitSnapshot{}, errors.New("status read unavailable")
}

type gitReadFailurePort struct {
	shellport.Workspace
	service shellport.GitService
}

func (p gitReadFailurePort) Git() shellport.GitService { return p.service }

func TestGitCompletedCommitAndPushSurviveStatusReadFailure(t *testing.T) {
	app := newFullTestApp(t)
	root := newGitRepoFixture(t)
	runGitFixture(t, root, "config", "user.name", "Crude Test")
	runGitFixture(t, root, "config", "user.email", "crude@example.test")
	runGitFixture(t, root, "add", "--", "tracked.txt")
	service := workspace.NewGitService(root)
	snapshot, err := service.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	wsOf(app).RootPath, wsOf(app).Git = root, service
	center := wsOf(app).Status
	original := app.ws
	app.ws = gitReadFailurePort{Workspace: original, service: gitFailingStatusRead{GitService: original.Git()}}
	app.gitPanel.SetSnapshot(snapshot)
	app.gitPanel.SetMessage("Commit despite a later status read failure")
	request := gitpanel.CommitRequest{Paths: snapshot.StagedPaths(), Message: app.gitPanel.Message()}
	command := app.commitGit(request)
	if command == nil {
		t.Fatal("commit action did not start")
	}
	message := command().(sourceControlResultMsg)
	// A refresh that the user starts meanwhile must not erase the commit's
	// outcome or leave its draft looking uncommitted.
	app.gitRefreshGen.Add(1)
	app.applySourceControlResult(message)
	actual, err := service.Snapshot(t.Context())
	if err != nil || actual.Head == snapshot.Head || len(actual.Changes) != 0 {
		t.Fatalf("the real commit was not created: %+v %v", actual, err)
	}
	if app.gitPanel.Message() != "" {
		t.Fatal("successful commit left its committed draft in the field")
	}
	operations := center.Operations()
	if len(operations) != 1 || operations[0].State != types.OperationSucceeded {
		t.Fatalf("status read error changed the actual commit outcome: %+v", operations)
	}
	issues := app.ws.Status().Issues()
	if len(issues) != 1 || issues[0].Severity != types.SeverityWarning ||
		strings.Contains(issues[0].Summary, "Source control failed") ||
		!strings.Contains(issues[0].Detail, "Committed 1 file") {
		t.Fatalf("post-commit read failure has no truthful next step: %+v", issues)
	}

	remote := filepath.Join(t.TempDir(), "remote.git")
	runGitFixture(t, root, "init", "--bare", remote)
	runGitFixture(t, root, "remote", "add", "origin", remote)
	app.gitPanel.SetMessage("Keep this next draft")
	push, handled := app.handleGitPanelAction(ui.Action{ID: gitpanel.ActionPush})
	if !handled || push == nil {
		t.Fatal("Push did not reach its native action")
	}
	app.applySourceControlResult(push().(sourceControlResultMsg))
	published := filepath.Join(t.TempDir(), "published")
	runGitFixture(t, root, "clone", "--branch", actual.Branch, remote, published)
	received, err := workspace.NewGitService(published).Snapshot(t.Context())
	if err != nil || received.Head != actual.Head {
		t.Fatalf("the commit did not reach the real remote: %+v %v", received, err)
	}
	for _, operation := range center.Operations() {
		if operation.State != types.OperationSucceeded {
			t.Fatalf("a post-push read error changed the actual effect outcome: %+v", operation)
		}
	}
	if app.gitPanel.RemoteBusy() != "" || app.gitPanel.Message() != "Keep this next draft" {
		t.Fatal("the post-push read failure stranded the action or consumed the next draft")
	}
	if issues := app.ws.Status().Issues(); len(issues) != 1 || issues[0].Severity != types.SeverityWarning ||
		!strings.Contains(issues[0].Detail, "Pushed") {
		t.Fatalf("post-push evidence failure was not distinguished from a failed push: %+v", issues)
	}
}
